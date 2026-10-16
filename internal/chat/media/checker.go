// Package media verifies Chat's attachment references through private Media RPCs.
package media

import (
	"context"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Client is Media's authenticated metadata API.
type Client interface {
	GetFile(context.Context, *mediav1.GetFileRequest, ...grpc.CallOption) (*mediav1.GetFileResponse, error)
}

// Checker requires files to belong to both the sender and the target room.
type Checker struct{ Client Client }

// CheckAttachment passes the request bearer token in outgoing context metadata.
func (c Checker) CheckAttachment(ctx context.Context, actor, room, id string) error {
	reply, err := c.Client.GetFile(ctx, &mediav1.GetFileRequest{FileId: id})
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound, codes.PermissionDenied, codes.InvalidArgument:
			return service.ErrInvalidAttachment
		default:
			return service.ErrMediaUnavailable
		}
	}
	f := reply.GetFile()
	if f.GetId() != id || f.GetRoomId() != room || f.GetOwnerId() != actor {
		return service.ErrInvalidAttachment
	}
	return nil
}
