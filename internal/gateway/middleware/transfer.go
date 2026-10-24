package middleware

import (
	"context"
	"io"
	"net/http"
	"time"
)

// FileTransfer gives file routes a separate total budget and refreshes each
// connection deadline on progress. Server-wide JSON deadlines are replaced.
func FileTransfer(budget, idle time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), budget)
			defer cancel()
			rc := http.NewResponseController(w)
			deadline, _ := ctx.Deadline()
			progress := func() time.Time { return minTime(time.Now().Add(idle), deadline) }
			_ = rc.SetReadDeadline(progress())
			_ = rc.SetWriteDeadline(deadline)
			defer func() {
				_ = rc.SetReadDeadline(time.Time{})
				_ = rc.SetWriteDeadline(time.Time{})
			}()
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = &transferBody{ReadCloser: r.Body, rc: rc, deadline: progress}
			}
			next.ServeHTTP(&transferWriter{ResponseWriter: w, rc: rc, deadline: progress}, r.WithContext(ctx))
		})
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

type transferBody struct {
	io.ReadCloser
	rc       *http.ResponseController
	deadline func() time.Time
}

func (b *transferBody) Read(p []byte) (int, error) {
	_ = b.rc.SetReadDeadline(b.deadline())
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

type transferWriter struct {
	http.ResponseWriter
	rc       *http.ResponseController
	deadline func() time.Time
}

func (w *transferWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *transferWriter) Write(p []byte) (int, error) {
	_ = w.rc.SetWriteDeadline(w.deadline())
	return w.ResponseWriter.Write(p)
}
