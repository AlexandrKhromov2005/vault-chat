package preview

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	previewv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/preview/v1"
)

const (
	// MaxRPCRequest includes protobuf overhead above the original image limit.
	MaxRPCRequest = MaxInput + 64<<10
	// MaxRPCResponse includes protobuf overhead above the maximum RGB response.
	MaxRPCResponse = 300<<10 + 4<<10
)

// Decoder is the one-task process supervisor.
type Decoder interface {
	Decode(context.Context, []byte) (Result, error)
}

// RPCHandler admits one decode at a time to its decoder process.
type RPCHandler struct {
	previewv1.UnimplementedPreviewServiceServer
	decoder Decoder
	slot    chan struct{}
}

// NewRPCHandler binds a decoder with single-task capacity.
func NewRPCHandler(decoder Decoder) *RPCHandler {
	return &RPCHandler{decoder: decoder, slot: make(chan struct{}, 1)}
}

// Decode returns only bounded RGB pixels; errors never include image data.
func (h *RPCHandler) Decode(ctx context.Context, request *previewv1.DecodeRequest) (*previewv1.DecodeResponse, error) {
	if len(request.GetImage()) > MaxInput {
		return nil, status.Error(codes.OutOfRange, "preview budget exceeded")
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		return nil, status.Error(codes.Unavailable, "decoder capacity reached")
	}
	result, err := h.decoder.Decode(ctx, request.GetImage())
	if err != nil {
		return nil, rpcStatus(err)
	}
	if err := validateRPCResult(result); err != nil {
		return nil, status.Error(codes.Internal, "invalid decoder response")
	}
	return &previewv1.DecodeResponse{Width: uint32(result.Width), Height: uint32(result.Height), Rgb: result.RGB}, nil
}

func rpcStatus(err error) error {
	switch {
	case errors.Is(err, ErrInvalid):
		return status.Error(codes.InvalidArgument, "invalid image")
	case errors.Is(err, ErrSkipped):
		return status.Error(codes.OutOfRange, "preview budget exceeded")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "decoder timed out")
	}
	return status.Error(codes.Internal, "decoder failed")
}

// NewRPCServer applies the same wire budgets to plaintext tests and mTLS deployment.
func NewRPCServer(options ...grpc.ServerOption) *grpc.Server {
	options = append(options, grpc.MaxRecvMsgSize(MaxRPCRequest), grpc.MaxSendMsgSize(MaxRPCResponse))
	return grpc.NewServer(options...)
}

// RPCClient calls a preview worker with bounded request and response messages.
type RPCClient struct {
	client previewv1.PreviewServiceClient
}

// NewRPCClient binds the generated client to an existing authenticated connection.
func NewRPCClient(conn *grpc.ClientConn) *RPCClient {
	return &RPCClient{client: previewv1.NewPreviewServiceClient(conn)}
}

// Decode validates the response independently of the remote handler.
func (c *RPCClient) Decode(ctx context.Context, data []byte) (Result, error) {
	if len(data) > MaxInput {
		return Result{}, ErrSkipped
	}
	response, err := c.client.Decode(ctx, &previewv1.DecodeRequest{Image: data}, grpc.MaxCallSendMsgSize(MaxRPCRequest), grpc.MaxCallRecvMsgSize(MaxRPCResponse))
	if err != nil {
		switch status.Code(err) {
		case codes.InvalidArgument:
			return Result{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		case codes.OutOfRange:
			return Result{}, fmt.Errorf("%w: %w", ErrSkipped, err)
		case codes.Canceled:
			return Result{}, err
		default:
			return Result{}, fmt.Errorf("%w: %w", ErrWorker, err)
		}
	}
	result := Result{Width: int(response.GetWidth()), Height: int(response.GetHeight()), RGB: response.GetRgb()}
	if err := validateRPCResult(result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func validateRPCResult(result Result) error {
	if result.Width < 1 || result.Height < 1 || result.Width > MaxSide || result.Height > MaxSide || len(result.RGB) != result.Width*result.Height*3 {
		return fmt.Errorf("%w: invalid RPC response", ErrWorker)
	}
	return nil
}
