package logger

import (
	"context"
	"log/slog"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

const requestIDKey = "request_id"

// requestIDHandler adds the request id stored in the context to every record
// logged with that context, which correlates log lines of one request across
// services (ARCHITECTURE.md section 7.1).
type requestIDHandler struct {
	next slog.Handler
}

// NewRequestIDHandler wraps next so that records logged with a context
// carrying a request id get a request_id attribute, unless they set one
// themselves.
func NewRequestIDHandler(next slog.Handler) slog.Handler {
	return requestIDHandler{next: next}
}

func (h requestIDHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h requestIDHandler) Handle(ctx context.Context, record slog.Record) error {
	if id := requestid.FromContext(ctx); id != "" && !hasAttr(record, requestIDKey) {
		record = record.Clone()
		record.AddAttrs(slog.String(requestIDKey, id))
	}
	return h.next.Handle(ctx, record)
}

func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{next: h.next.WithAttrs(attrs)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{next: h.next.WithGroup(name)}
}

func hasAttr(record slog.Record, key string) bool {
	found := false
	record.Attrs(func(attr slog.Attr) bool {
		found = attr.Key == key
		return !found
	})
	return found
}
