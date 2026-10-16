package handler

import (
	"context"
	"errors"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/validator"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"time"
)

// MediaClient is the file API consumed by Gateway.
type MediaClient interface {
	Upload(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[mediav1.UploadRequest, mediav1.UploadResponse], error)
	GetFile(context.Context, *mediav1.GetFileRequest, ...grpc.CallOption) (*mediav1.GetFileResponse, error)
	Download(context.Context, *mediav1.DownloadRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[mediav1.DownloadResponse], error)
}

// MediaHandler translates bounded file requests without publishing object URLs.
type MediaHandler struct {
	client MediaClient
	logger *slog.Logger
	slots  chan struct{}
}

// NewMediaHandler binds private Media RPCs.
func NewMediaHandler(client MediaClient, logger *slog.Logger) *MediaHandler {
	return &MediaHandler{client: client, logger: logger, slots: make(chan struct{}, 8)}
}

type fileResponse struct {
	ID          string    `json:"file_id"`
	RoomID      string    `json:"room_id"`
	OwnerID     string    `json:"owner_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
}

func fileJSON(f *mediav1.File) fileResponse {
	return fileResponse{f.GetId(), f.GetRoomId(), f.GetOwnerId(), f.GetFilename(), f.GetContentType(), f.GetSize(), f.GetCreatedAt().AsTime()}
}

// Upload accepts exactly one multipart field named file, up to ten MiB.
func (h *MediaHandler) Upload(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		response.Error(w, 503, "unavailable", "upload capacity reached")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, validator.MaxBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		response.Error(w, 400, "invalid_argument", "multipart file required")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		response.Error(w, 400, "invalid_argument", "one file required")
		return
	}
	stream, err := h.client.Upload(ctx)
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	if err := stream.Send(&mediav1.UploadRequest{RoomId: r.PathValue("room_id"), Filename: part.FileName()}); err != nil {
		h.uploadError(w, r, stream, err)
		return
	}
	buf := make([]byte, 64<<10)
	total := 0
	for {
		n, readErr := part.Read(buf)
		if n > 0 {
			total += n
			if total > validator.MaxBytes {
				response.Error(w, 413, "too_large", "file too large")
				return
			}
			if err := stream.Send(&mediav1.UploadRequest{Data: buf[:n]}); err != nil {
				h.uploadError(w, r, stream, err)
				return
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			var large *http.MaxBytesError
			if errors.As(readErr, &large) {
				response.Error(w, 413, "too_large", "file too large")
			} else {
				response.Error(w, 400, "invalid_argument", "invalid multipart body")
			}
			return
		}
	}
	if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
		response.Error(w, 400, "invalid_argument", "exactly one file required")
		return
	}
	file, err := stream.CloseAndRecv()
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, fileJSON(file.GetFile()))
}
func (h *MediaHandler) uploadError(w http.ResponseWriter, r *http.Request, stream grpc.ClientStreamingClient[mediav1.UploadRequest, mediav1.UploadResponse], err error) {
	if errors.Is(err, io.EOF) {
		_, err = stream.CloseAndRecv()
	}
	h.backendError(w, r, err)
}

// File returns metadata only after Media checks current room membership.
func (h *MediaHandler) File(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	f, err := h.client.GetFile(ctx, &mediav1.GetFileRequest{FileId: r.PathValue("file_id")})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, 200, fileJSON(f.GetFile()))
}

// Download forces attachment handling and never serves active content inline.
func (h *MediaHandler) Download(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	stream, err := h.client.Download(ctx, &mediav1.DownloadRequest{FileId: r.PathValue("file_id")})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	first, err := stream.Recv()
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	f := first.GetFile()
	if f == nil || len(first.Data) != 0 {
		h.backendError(w, r, status.Error(codes.Internal, "invalid download"))
		return
	}
	w.Header().Set("Content-Type", f.GetContentType())
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.GetFilename()}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(f.GetSize(), 10))
	w.WriteHeader(200)
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			h.logger.WarnContext(r.Context(), "file stream failed", "code", status.Code(err))
			panic(http.ErrAbortHandler)
		}
		if _, err := w.Write(frame.GetData()); err != nil {
			return
		}
	}
}
func (h *MediaHandler) backendError(w http.ResponseWriter, r *http.Request, err error) {
	if status.Code(err) == codes.ResourceExhausted && status.Convert(err).Message() == "file too large" {
		response.Error(w, 413, "too_large", "file too large")
		return
	}
	h.logger.WarnContext(r.Context(), "media request failed", "code", status.Code(err))
	response.GRPCError(w, err)
}
