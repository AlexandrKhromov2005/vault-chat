package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"

	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
)

type mediaServer struct {
	mediav1.UnimplementedMediaServiceServer
	uploads chan []byte
}

func (s *mediaServer) Upload(stream grpc.ClientStreamingServer[mediav1.UploadRequest, mediav1.UploadResponse]) error {
	if ids := metadata.ValueFromIncomingContext(stream.Context(), requestid.MetadataKey); len(ids) != 1 || ids[0] != "media-test-id" {
		return fmt.Errorf("missing correlation ID")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	var data []byte
	for {
		f, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		data = append(data, f.Data...)
	}
	s.uploads <- data
	return stream.SendAndClose(&mediav1.UploadResponse{File: &mediav1.File{Id: "file", RoomId: first.RoomId, Filename: first.Filename, Size: int64(len(data)), CreatedAt: timestamppb.Now()}})
}
func (s *mediaServer) Download(_ *mediav1.DownloadRequest, stream grpc.ServerStreamingServer[mediav1.DownloadResponse]) error {
	if err := stream.Send(&mediav1.DownloadResponse{File: &mediav1.File{Id: "file", Filename: "a.txt", ContentType: "text/plain", Size: 5}}); err != nil {
		return err
	}
	return stream.Send(&mediav1.DownloadResponse{Data: []byte("hello")})
}
func TestMediaHTTP(t *testing.T) {
	backend := &mediaServer{uploads: make(chan []byte, 5)}
	l := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	mediav1.RegisterMediaServiceServer(server, backend)
	go func() { _ = server.Serve(l) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///media", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return l.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	h := NewMediaHandler(mediav1.NewMediaServiceClient(conn), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, parts := range []int{1, 2} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for i := 0; i < parts; i++ {
			part, err := writer.CreateFormFile("file", "a.txt")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write([]byte("hello"))
		}
		_ = writer.Close()
		r := httptest.NewRequest(http.MethodPost, "/", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.SetPathValue("room_id", "room")
		r = r.WithContext(middleware.WithIdentity(requestid.NewContext(r.Context(), "media-test-id"), middleware.Identity{Token: "good", UserID: "actor"}))
		w := httptest.NewRecorder()
		h.Upload(w, r)
		want := http.StatusCreated
		if parts == 2 {
			want = http.StatusBadRequest
		}
		if w.Code != want {
			t.Fatalf("parts=%d status=%d body=%s", parts, w.Code, w.Body.String())
		}
	}
	select {
	case data := <-backend.uploads:
		if string(data) != "hello" {
			t.Fatal(string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("no upload")
	}
	if len(backend.uploads) != 0 {
		t.Fatal("invalid multipart committed")
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(middleware.WithIdentity(requestid.NewContext(r.Context(), "media-test-id"), middleware.Identity{Token: "good", UserID: "actor"}))
	w := httptest.NewRecorder()
	h.Download(w, r)
	if w.Code != 200 || w.Body.String() != "hello" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Disposition") != "attachment; filename=a.txt" {
		t.Fatalf("download: %+v %s", w.Result().Header, w.Body.String())
	}
}
