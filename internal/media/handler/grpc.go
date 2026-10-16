// Package handler exposes authenticated, bounded Media streaming RPCs.
package handler

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/validator"
)

// Files is the room-authorized Media application API.
type Files interface {
	CheckUpload(context.Context, string, string) error
	Upload(context.Context, string, string, string, []byte) (*domain.File, error)
	Get(context.Context, string) (*domain.File, error)
	Download(context.Context, string) (*domain.File, io.ReadCloser, error)
}

// Auth validates bearer tokens without trusting caller-supplied actor IDs.
type Auth interface {
	ValidateToken(context.Context, *authv1.ValidateTokenRequest, ...grpc.CallOption) (*authv1.ValidateTokenResponse, error)
}

// Rooms checks membership through Chat's authenticated API.
type Rooms interface {
	GetRoom(context.Context, *chatv1.GetRoomRequest, ...grpc.CallOption) (*chatv1.GetRoomResponse, error)
}

// Gate forwards the original credential to Chat for each membership check.
type Gate struct{ Client Rooms }

// CheckRoom uses the current request's outgoing authorization metadata.
func (g Gate) CheckRoom(ctx context.Context, id string) error {
	_, err := g.Client.GetRoom(ctx, &chatv1.GetRoomRequest{RoomId: id})
	return err
}

// Handler serves Media RPCs with bounded upload concurrency.
type Handler struct {
	mediav1.UnimplementedMediaServiceServer
	files Files
	auth  Auth
	slots chan struct{}
}

// New binds file operations and independent Auth verification.
func New(files Files, auth Auth) *Handler {
	return &Handler{files: files, auth: auth, slots: make(chan struct{}, 16)}
}
func (h *Handler) identity(ctx context.Context) (context.Context, string, error) {
	ids := metadata.ValueFromIncomingContext(ctx, requestid.MetadataKey)
	if len(ids) == 1 && requestid.Valid(ids[0]) {
		ctx = requestid.NewContext(ctx, ids[0])
	}
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 {
		return ctx, "", status.Error(codes.Unauthenticated, "authentication required")
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return ctx, "", status.Error(codes.Unauthenticated, "invalid credential")
	}
	for _, c := range token {
		if c <= ' ' || c >= 127 {
			return ctx, "", status.Error(codes.Unauthenticated, "invalid credential")
		}
	}
	reply, err := h.auth.ValidateToken(ctx, &authv1.ValidateTokenRequest{Token: token})
	if err != nil {
		return ctx, "", safeError(err)
	}
	actor, err := uuid.Parse(reply.GetUserId())
	if !reply.GetValid() || err != nil || actor == uuid.Nil {
		return ctx, "", status.Error(codes.Unauthenticated, "invalid access token")
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), actor.String(), nil
}

// Upload accepts one metadata frame followed by bounded data frames.
func (h *Handler) Upload(stream grpc.ClientStreamingServer[mediav1.UploadRequest, mediav1.UploadResponse]) error {
	ctx, cancel := context.WithTimeout(stream.Context(), 30*time.Second)
	defer cancel()
	ctx, actor, err := h.identity(ctx)
	if err != nil {
		return err
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		return status.Error(codes.ResourceExhausted, "upload capacity reached")
	}
	first, err := receive(ctx, stream)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument, "metadata required")
		}
		return safeError(err)
	}
	if first.GetRoomId() == "" || first.GetFilename() == "" || len(first.GetData()) != 0 {
		return status.Error(codes.InvalidArgument, "metadata frame required")
	}
	if err := h.files.CheckUpload(ctx, first.RoomId, first.Filename); err != nil {
		return safeError(err)
	}
	var data []byte
	for {
		frame, err := receive(ctx, stream)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return safeError(err)
		}
		if frame.RoomId != "" || frame.Filename != "" || len(frame.Data) == 0 || len(frame.Data) > 64<<10 {
			return status.Error(codes.InvalidArgument, "invalid data frame")
		}
		if len(data)+len(frame.Data) > validator.MaxBytes {
			return safeError(validator.ErrTooLarge)
		}
		data = append(data, frame.Data...)
	}
	if len(data) == 0 {
		return safeError(validator.ErrInvalidFile)
	}
	file, err := h.files.Upload(ctx, actor, first.RoomId, first.Filename, data)
	if err != nil {
		return safeError(err)
	}
	return stream.SendAndClose(&mediav1.UploadResponse{File: fileProto(file)})
}
func receive(ctx context.Context, s grpc.ClientStreamingServer[mediav1.UploadRequest, mediav1.UploadResponse]) (*mediav1.UploadRequest, error) {
	type result struct {
		frame *mediav1.UploadRequest
		err   error
	}
	ch := make(chan result, 1)
	go func() { frame, err := s.Recv(); ch <- result{frame, err} }()
	select {
	case r := <-ch:
		return r.frame, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetFile checks current membership before returning metadata.
func (h *Handler) GetFile(ctx context.Context, r *mediav1.GetFileRequest) (*mediav1.GetFileResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx, _, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	f, err := h.files.Get(ctx, r.GetFileId())
	if err != nil {
		return nil, safeError(err)
	}
	return &mediav1.GetFileResponse{File: fileProto(f)}, nil
}

// Download sends metadata before bytes so the gateway can set safe headers.
func (h *Handler) Download(r *mediav1.DownloadRequest, s grpc.ServerStreamingServer[mediav1.DownloadResponse]) error {
	ctx, cancel := context.WithTimeout(s.Context(), 30*time.Second)
	defer cancel()
	ctx, _, err := h.identity(ctx)
	if err != nil {
		return err
	}
	f, body, err := h.files.Download(ctx, r.GetFileId())
	if err != nil {
		return safeError(err)
	}
	defer body.Close()
	if err := s.Send(&mediav1.DownloadResponse{File: fileProto(f)}); err != nil {
		return err
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if err := s.Send(&mediav1.DownloadResponse{Data: buf[:n]}); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return safeError(err)
		}
		if ctx.Err() != nil {
			return safeError(ctx.Err())
		}
	}
}
func fileProto(f *domain.File) *mediav1.File {
	return &mediav1.File{Id: f.ID, RoomId: f.RoomID, OwnerId: f.OwnerID, Filename: f.Name, ContentType: f.ContentType, Size: f.Size, CreatedAt: timestamppb.New(f.CreatedAt)}
}
func safeError(err error) error {
	switch {
	case errors.Is(err, validator.ErrTooLarge):
		return status.Error(codes.ResourceExhausted, "file too large")
	case errors.Is(err, validator.ErrInvalidFile), errors.Is(err, validator.ErrUnsupported):
		return status.Error(codes.InvalidArgument, "invalid or unsupported file")
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, "file not found")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "request timed out")
	}
	switch status.Code(err) {
	case codes.NotFound:
		return status.Error(codes.NotFound, "file not found")
	case codes.Unauthenticated, codes.PermissionDenied, codes.Unavailable, codes.DeadlineExceeded:
		return status.Error(status.Code(err), "file access unavailable")
	}
	return status.Error(codes.Internal, "file operation failed")
}
