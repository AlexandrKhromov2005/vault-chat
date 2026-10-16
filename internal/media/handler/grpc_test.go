package handler

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/validator"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
)

type testAuth struct{ actor string }

func (a testAuth) ValidateToken(_ context.Context, r *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
	return &authv1.ValidateTokenResponse{Valid: r.Token == "good", UserId: a.actor}, nil
}

type testFiles struct{ uploaded chan string }

func (f *testFiles) CheckUpload(context.Context, string, string) error { return nil }
func (f *testFiles) Upload(_ context.Context, owner, room, name string, data []byte) (*domain.File, error) {
	f.uploaded <- string(data)
	return &domain.File{ID: uuid.NewString(), OwnerID: owner, RoomID: room, Name: name, Size: int64(len(data)), CreatedAt: time.Now()}, nil
}
func (f *testFiles) Get(context.Context, string) (*domain.File, error) {
	return nil, domain.ErrNotFound
}
func (f *testFiles) Download(context.Context, string) (*domain.File, io.ReadCloser, error) {
	return nil, nil, domain.ErrNotFound
}

func TestUploadProtocol(t *testing.T) {
	files := &testFiles{uploaded: make(chan string, 10)}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	mediav1.RegisterMediaServiceServer(server, New(files, testAuth{uuid.NewString()}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := mediav1.NewMediaServiceClient(conn)
	for _, tc := range []struct {
		name, token string
		frames      []*mediav1.UploadRequest
		code        codes.Code
	}{
		{"success", "good", []*mediav1.UploadRequest{{RoomId: uuid.NewString(), Filename: "a.txt"}, {Data: []byte("hello")}}, codes.OK},
		{"unauthenticated", "bad", []*mediav1.UploadRequest{{RoomId: uuid.NewString(), Filename: "a.txt"}}, codes.Unauthenticated},
		{"header with data", "good", []*mediav1.UploadRequest{{RoomId: uuid.NewString(), Filename: "a.txt", Data: []byte("hello")}}, codes.InvalidArgument},
		{"second header", "good", []*mediav1.UploadRequest{{RoomId: uuid.NewString(), Filename: "a.txt"}, {Filename: "b.txt", Data: []byte("hello")}}, codes.InvalidArgument},
		{"empty", "good", nil, codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
			defer cancel()
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+tc.token))
			stream, err := client.Upload(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, frame := range tc.frames {
				if err := stream.Send(frame); err != nil {
					break
				}
			}
			_, err = stream.CloseAndRecv()
			if status.Code(err) != tc.code {
				t.Fatalf("code=%v err=%v", status.Code(err), err)
			}
		})
	}
	if len(files.uploaded) != 1 {
		t.Fatalf("invalid uploads committed: %d", len(files.uploaded))
	}
}

type readableFiles struct {
	testFiles
	fail error
}

func (f *readableFiles) Get(context.Context, string) (*domain.File, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	return &domain.File{ID: uuid.NewString(), Name: "a.txt", Size: 5, CreatedAt: time.Now()}, nil
}
func (f *readableFiles) Download(ctx context.Context, id string) (*domain.File, io.ReadCloser, error) {
	file, err := f.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return file, io.NopCloser(strings.NewReader("hello")), nil
}
func TestReadRPCsAndErrorMapping(t *testing.T) {
	files := &readableFiles{}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	mediav1.RegisterMediaServiceServer(server, New(files, testAuth{uuid.NewString()}))
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := mediav1.NewMediaServiceClient(conn)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer good"))
	req := &mediav1.GetFileRequest{FileId: uuid.NewString()}
	file, err := client.GetFile(ctx, req)
	if err != nil || file.GetFile().GetFilename() != "a.txt" {
		t.Fatalf("file=%v %v", file, err)
	}
	stream, err := client.Download(ctx, &mediav1.DownloadRequest{FileId: req.FileId})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetFile() == nil {
		t.Fatalf("metadata=%v %v", first, err)
	}
	chunk, err := stream.Recv()
	if err != nil || string(chunk.Data) != "hello" {
		t.Fatalf("data=%v %v", chunk, err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		err  error
		code codes.Code
	}{{validator.ErrTooLarge, codes.ResourceExhausted}, {validator.ErrInvalidFile, codes.InvalidArgument}, {validator.ErrUnsupported, codes.InvalidArgument}, {domain.ErrNotFound, codes.NotFound}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("database password"), codes.Internal}, {status.Error(codes.PermissionDenied, "private detail"), codes.PermissionDenied}} {
		got := safeError(tc.err)
		if status.Code(got) != tc.code || strings.Contains(got.Error(), "password") || strings.Contains(got.Error(), "private detail") {
			t.Fatalf("mapping=%v", got)
		}
	}
	for _, auth := range []metadata.MD{nil, metadata.Pairs("authorization", "Basic token"), metadata.Pairs("authorization", "Bearer a b"), metadata.Pairs("authorization", "Bearer good", "authorization", "Bearer good")} {
		_, err := client.GetFile(metadata.NewOutgoingContext(context.Background(), auth), req)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatal(err)
		}
	}
}

func TestMissingAndForbiddenRoomHideFileExistence(t *testing.T) {
	missing := safeError(domain.ErrNotFound)
	hidden := safeError(status.Error(codes.NotFound, "room not found"))
	if missing.Error() != hidden.Error() {
		t.Fatalf("existence leaked: missing=%v hidden=%v", missing, hidden)
	}
}
