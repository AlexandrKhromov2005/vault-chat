package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
)

// Close codes of the WebSocket endpoint in the range RFC 6455 leaves to
// applications.
const (
	// closeUnauthorized: authentication is missing, invalid or expired; the
	// client refreshes its access token before reconnecting.
	closeUnauthorized websocket.StatusCode = 4001
)

// maxClientFrameBytes bounds client frames; an auth frame needs about 1 KiB.
const maxClientFrameBytes = 4096

// RealtimeOptions are the limits of WebSocket connections.
type RealtimeOptions struct {
	// AuthTimeout bounds waiting for the first auth frame and each token check.
	AuthTimeout time.Duration
	// ReauthInterval is the shortest time between auth frames of a connection,
	// so a client cannot turn its connection into a stream of Auth calls.
	ReauthInterval time.Duration
	// WriteTimeout bounds every write to a client.
	WriteTimeout time.Duration
}

// Realtime serves GET /api/v1/ws: connections that authenticate in-band and
// stay open while their access token is valid.
type Realtime struct {
	validator middleware.TokenValidator
	options   RealtimeOptions
	logger    *slog.Logger
}

// NewRealtime binds the Auth token validator and the limits.
func NewRealtime(validator middleware.TokenValidator, options RealtimeOptions, logger *slog.Logger) *Realtime {
	return &Realtime{validator: validator, options: options, logger: logger}
}

// closeError ends a connection with a close code and a fixed reason.
type closeError struct {
	code   websocket.StatusCode
	reason string
}

// session is the identity a connection authenticated as.
type session struct {
	userID    string
	expiresAt time.Time
}

// connection is the state of one accepted WebSocket connection.
type connection struct {
	*Realtime
	conn *websocket.Conn
	// renewed carries the expiry of a token sent by the client in-band.
	renewed chan time.Time
	// ended asks the writer to close; nil means the connection is already gone.
	ended   chan *closeError
	workers sync.WaitGroup
	// closers counts closes scheduled from timers, each until it has run to
	// completion or been canceled.
	closers sync.WaitGroup
}

// closeLater schedules closing the connection with end when run is called and
// returns run and cancel; cancel reports whether it prevented the close. The
// connection counts as closed only once a scheduled close has completed.
func (c *connection) closeLater(end *closeError) (run func(), cancel func() bool) {
	c.closers.Add(1)
	var once sync.Once
	run = func() {
		once.Do(func() {
			defer c.closers.Done()
			_ = c.conn.Close(end.code, end.reason)
		})
	}
	cancel = func() bool {
		canceled := false
		once.Do(func() {
			canceled = true
			c.closers.Done()
		})
		return canceled
	}
	return run, cancel
}

// ServeHTTP upgrades the request and serves the connection until it ends.
// The access token is never accepted from the URL, which is logged.
func (h *Realtime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Accept refuses browser origins other than the Gateway's own with 403.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxClientFrameBytes)
	c := &connection{Realtime: h, conn: conn, renewed: make(chan time.Time), ended: make(chan *closeError, 1)}
	// Reads must not be given a context that ends before the close handshake:
	// coder/websocket drops the connection without a close frame when it does.
	ctx, cancel := context.WithCancel(r.Context())
	if end := c.serve(ctx); end != nil {
		_ = conn.Close(end.code, end.reason)
	}
	// A close started by a timer must finish its handshake before the handler
	// returns.
	c.closers.Wait()
	_ = conn.CloseNow()
	cancel()
	c.workers.Wait()
}

// serve authenticates the connection and keeps it open while it stays
// authenticated. It returns how to close the connection, or nil when it is
// already gone.
func (c *connection) serve(ctx context.Context) *closeError {
	current, end := c.authenticate(ctx)
	if current.userID == "" {
		return end
	}
	if err := c.writeReady(ctx, current); err != nil {
		return nil
	}
	c.workers.Add(1)
	go c.readFrames(ctx, current.userID)
	return c.relay(ctx, current)
}

// authenticate reads the first frame within AuthTimeout and validates it. A
// session without a user ID comes with how to close the connection.
func (c *connection) authenticate(ctx context.Context) (session, *closeError) {
	closeOnTimeout, keepOpen := c.closeLater(&closeError{closeUnauthorized, "authentication timeout"})
	timer := time.AfterFunc(c.options.AuthTimeout, closeOnTimeout)
	token, end := c.nextToken(ctx)
	timer.Stop()
	if !keepOpen() {
		return session{}, nil
	}
	if token == "" {
		return session{}, end
	}
	return c.validate(ctx, token)
}

// nextToken reads an auth frame. An empty token without an end means the
// connection is gone; the library has already closed it as needed.
func (c *connection) nextToken(ctx context.Context) (string, *closeError) {
	kind, data, err := c.conn.Read(ctx)
	if err != nil {
		return "", nil
	}
	if kind != websocket.MessageText {
		return "", &closeError{websocket.StatusUnsupportedData, "binary frames are not supported"}
	}
	token, err := authFrame(data)
	if err != nil {
		return "", &closeError{websocket.StatusPolicyViolation, "expected an auth frame"}
	}
	return token, nil
}

// validate checks a token with Auth, as for every protected REST request.
func (c *connection) validate(ctx context.Context, token string) (session, *closeError) {
	ctx, cancel := context.WithTimeout(ctx, c.options.AuthTimeout)
	defer cancel()
	resp, err := c.validator.ValidateToken(ctx, &authv1.ValidateTokenRequest{Token: token})
	if err != nil {
		c.logger.WarnContext(ctx, "websocket token validation failed", "error", err)
		return session{}, &closeError{websocket.StatusTryAgainLater, "authentication unavailable"}
	}
	expiresAt := resp.GetExpiresAt().AsTime()
	if !resp.GetValid() || resp.GetUserId() == "" || resp.GetExpiresAt() == nil || !expiresAt.After(time.Now()) {
		return session{}, &closeError{closeUnauthorized, "invalid or expired token"}
	}
	return session{userID: resp.GetUserId(), expiresAt: expiresAt}, nil
}

// readFrames handles in-band re-authentication until the connection ends.
func (c *connection) readFrames(ctx context.Context, userID string) {
	defer c.workers.Done()
	last := time.Now()
	for {
		token, end := c.nextToken(ctx)
		if token == "" {
			c.finish(ctx, end)
			return
		}
		if time.Since(last) < c.options.ReauthInterval {
			c.finish(ctx, &closeError{websocket.StatusPolicyViolation, "authentication too frequent"})
			return
		}
		last = time.Now()
		renewed, end := c.validate(ctx, token)
		if end == nil && renewed.userID != userID {
			end = &closeError{closeUnauthorized, "identity changed"}
		}
		if end != nil {
			c.finish(ctx, end)
			return
		}
		select {
		case c.renewed <- renewed.expiresAt:
		case <-ctx.Done():
			return
		}
	}
}

// finish asks relay to end the connection.
func (c *connection) finish(ctx context.Context, end *closeError) {
	select {
	case c.ended <- end:
	case <-ctx.Done():
	}
}

// relay confirms re-authentications until the token expires or the reader
// finishes the connection.
func (c *connection) relay(ctx context.Context, current session) *closeError {
	expiry := time.NewTimer(time.Until(current.expiresAt))
	defer expiry.Stop()
	for {
		select {
		case current.expiresAt = <-c.renewed:
			expiry.Reset(time.Until(current.expiresAt))
			if err := c.writeReady(ctx, current); err != nil {
				return nil
			}
		case <-expiry.C:
			return &closeError{closeUnauthorized, "access token expired"}
		case end := <-c.ended:
			return end
		}
	}
}

// writeReady confirms an authentication and when the token expires.
func (c *connection) writeReady(ctx context.Context, current session) error {
	frame, err := json.Marshal(struct {
		Type      string    `json:"type"`
		UserID    string    `json:"user_id"`
		ExpiresAt time.Time `json:"expires_at"`
	}{"ready", current.userID, current.expiresAt})
	if err != nil {
		return err
	}
	return c.write(ctx, frame)
}

// write sends one frame; a write that times out drops the connection.
func (c *connection) write(ctx context.Context, frame []byte) error {
	ctx, cancel := context.WithTimeout(ctx, c.options.WriteTimeout)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, frame)
}
