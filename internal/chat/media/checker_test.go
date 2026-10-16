package media

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
)

type metadataClient struct {
	file *mediav1.File
	err  error
}

func (c metadataClient) GetFile(context.Context, *mediav1.GetFileRequest, ...grpc.CallOption) (*mediav1.GetFileResponse, error) {
	return &mediav1.GetFileResponse{File: c.file}, c.err
}
func TestAttachmentScope(t *testing.T) {
	for _, tc := range []struct {
		file           *mediav1.File
		upstream, want error
	}{
		{&mediav1.File{Id: "file", OwnerId: "actor", RoomId: "room"}, nil, nil},
		{&mediav1.File{Id: "file", OwnerId: "peer", RoomId: "room"}, nil, service.ErrInvalidAttachment},
		{&mediav1.File{Id: "file", OwnerId: "actor", RoomId: "other"}, nil, service.ErrInvalidAttachment},
		{nil, status.Error(codes.NotFound, "missing"), service.ErrInvalidAttachment},
		{nil, status.Error(codes.Unavailable, "down"), service.ErrMediaUnavailable},
	} {
		err := (Checker{metadataClient{tc.file, tc.upstream}}).CheckAttachment(context.Background(), "actor", "room", "file")
		if err != tc.want {
			t.Fatalf("got=%v want=%v", err, tc.want)
		}
	}
}
