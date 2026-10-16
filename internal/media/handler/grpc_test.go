package handler

import (
	"context"
	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"io"
	"net"
	"testing"
	"time"
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
