package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/realtime"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// Close codes of the WebSocket endpoint in the range RFC 6455 leaves to
// applications.
const (
	// closeUnauthorized: authentication is missing, invalid or expired; the
	// client refreshes its access token before reconnecting.
	closeUnauthorized websocket.StatusCode = 4001
	// closeLagging: the client did not keep up with its events; it reconnects
	// and catches up.
	closeLagging websocket.StatusCode = 4002
)

// maxClientFrameBytes bounds client frames; an auth frame needs about 1 KiB.
const maxClientFrameBytes = 4096

// retryAfterSeconds is the Retry-After of refused upgrades.
const retryAfterSeconds = "5"

// RealtimeOptions are the limits of WebSocket connections.
type RealtimeOptions struct {
	// AllowedOrigins are the browser origins allowed besides the Gateway's own,
	// as for CORS; a single "*" allows any origin.
	AllowedOrigins []string
	// AuthTimeout bounds waiting for the first auth frame, each token check
	// and subscribing to the user's events.
	AuthTimeout time.Duration
	// ReauthInterval is the shortest time between auth frames of a connection,
	// so a client cannot turn its connection into a stream of Auth calls.
	ReauthInterval time.Duration
	// PingInterval is the time between server pings; a client that does not
	// answer within WriteTimeout is disconnected.
	PingInterval time.Duration
	// WriteTimeout bounds every write to a client.
	WriteTimeout time.Duration
}

// Realtime serves GET /api/v1/ws: authenticated connections that receive the
// Chat events of their user.
type Realtime struct {
	hub       *realtime.Hub
	validator middleware.TokenValidator
	options   RealtimeOptions
	allowAll  bool
	origins   map[string]bool
	logger    *slog.Logger

	// stopping ends when Shutdown starts; every connection then closes.
	stopping context.Context
	stop     context.CancelFunc
	mu       sync.Mutex
	closed   bool // guarded by mu
	// conns counts upgraded connections until they are fully closed.
	conns sync.WaitGroup
}

// NewRealtime binds the event hub, the Auth token validator and the limits.
func NewRealtime(hub *realtime.Hub, validator middleware.TokenValidator, options RealtimeOptions, logger *slog.Logger) *Realtime {
	stopping, stop := context.WithCancel(context.Background())
	h := &Realtime{hub: hub, validator: validator, options: options, origins: make(map[string]bool), logger: logger,
		stopping: stopping, stop: stop}
	for _, origin := range options.AllowedOrigins {
		h.allowAll = h.allowAll || origin == "*"
		h.origins[origin] = true
	}
	return h
}

// Shutdown refuses new connections, closes every connection with 1001, whether
// it has authenticated or not, and waits until all of them are closed or ctx
// ends. http.Server.Shutdown does not wait for hijacked connections, so the
// process must call this before it exits.
func (h *Realtime) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.stop()
	closed := make(chan struct{})
	go func() {
		h.conns.Wait()
		close(closed)
	}()
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for WebSocket connections: %w", ctx.Err())
	}
}

// admit registers a connection about to be upgraded, unless the endpoint is
// shutting down. Registering under mu keeps Shutdown from missing it.
func (h *Realtime) admit() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.conns.Add(1)
	return true
}

// refuse answers an upgrade during shutdown with 503 and a hint when to retry.
func refuse(w http.ResponseWriter) {
	w.Header().Set("Retry-After", retryAfterSeconds)
	response.Error(w, http.StatusServiceUnavailable, "shutting_down", "server shutting down")
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
	// closers counts closes scheduled from timers and Shutdown, each until
	// it has run to completion or been canceled.
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
	if !h.originAllowed(r) {
		response.Error(w, http.StatusForbidden, "origin_forbidden", "origin not allowed")
		return
	}
	// Refusing before the upgrade spares the connection and lets clients
	// read the status and Retry-After.
	if !h.admit() {
		refuse(w)
		return
	}
	defer h.conns.Done()
	// The origin was checked above against the configured list.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxClientFrameBytes)
	c := &connection{Realtime: h, conn: conn, renewed: make(chan time.Time), ended: make(chan *closeError, 2)}
	// Shutdown closes the connection at any stage; reads and writes in
	// progress then fail and serve returns.
	closeOnShutdown, keepOpen := c.closeLater(goingAway())
	stopWatching := context.AfterFunc(h.stopping, closeOnShutdown)
	// Reads must not be given a context that ends before the close handshake:
	// coder/websocket drops the connection without a close frame when it does.
	ctx, cancel := context.WithCancel(r.Context())
	if end := c.serve(ctx); end != nil {
		_ = conn.Close(end.code, end.reason)
	}
	stopWatching()
	keepOpen()
	// A close started by a timer or Shutdown must finish its handshake before
	// the handler returns and Shutdown stops waiting for it.
	c.closers.Wait()
	_ = conn.CloseNow()
	cancel()
	c.workers.Wait()
}

// originAllowed accepts non-browser clients, which send no Origin, the
// Gateway's own origin and the configured origins.
func (h *Realtime) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || h.allowAll || h.origins[origin] {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host)
}

// serve authenticates the connection, attaches it to the hub and relays events.
// It returns how to close the connection, or nil when it is already gone.
func (c *connection) serve(ctx context.Context) *closeError {
	current, end := c.authenticate(ctx)
	if current.userID == "" {
		return end
	}
	attachCtx, cancel := context.WithTimeout(ctx, c.options.AuthTimeout)
	client, err := c.hub.Attach(attachCtx, current.userID)
	cancel()
	switch {
	case err == nil:
	case errors.Is(err, realtime.ErrClosed):
		return goingAway()
	case errors.Is(err, realtime.ErrTooManyConnections):
		return &closeError{websocket.StatusTryAgainLater, "too many connections"}
	default:
		c.logger.WarnContext(ctx, "subscribe to user events failed", "error", err)
		return &closeError{websocket.StatusTryAgainLater, "events unavailable"}
	}
	defer c.hub.Detach(client)
	// Events queued since Attach wait in the client's queue until ready is sent.
	if err := c.writeReady(ctx, current); err != nil {
		return nil
	}
	c.workers.Add(2)
	go c.readFrames(ctx, current.userID)
	go c.ping(ctx)
	return c.relay(ctx, client, current)
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

// goingAway ends a connection because the Gateway is shutting down.
func goingAway() *closeError {
	return &closeError{websocket.StatusGoingAway, "server shutting down"}
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

// ping disconnects a client that stops answering pings.
func (c *connection) ping(ctx context.Context) {
	defer c.workers.Done()
	ticker := time.NewTicker(c.options.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, c.options.WriteTimeout)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				c.finish(ctx, nil)
				return
			}
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

// relay writes the user's events until the token expires, the hub ends the
// client or a reader or pinger finishes the connection.
func (c *connection) relay(ctx context.Context, client *realtime.Client, current session) *closeError {
	expiry := time.NewTimer(time.Until(current.expiresAt))
	defer expiry.Stop()
	for {
		select {
		case payload := <-client.Frames():
			frame, known, err := eventFrame(payload)
			if err != nil {
				c.logger.WarnContext(ctx, "skipping undecodable chat event", "error", err)
			}
			if !known {
				continue
			}
			if err := c.write(ctx, frame); err != nil {
				return nil
			}
		case current.expiresAt = <-c.renewed:
			expiry.Reset(time.Until(current.expiresAt))
			if err := c.writeReady(ctx, current); err != nil {
				return nil
			}
		case <-expiry.C:
			return &closeError{closeUnauthorized, "access token expired"}
		case <-client.Done():
			return clientEnd(client.Reason())
		case end := <-c.ended:
			return end
		}
	}
}

// clientEnd maps why the hub ended a client to a close code.
func clientEnd(reason realtime.Reason) *closeError {
	switch reason {
	case realtime.ReasonLagging:
		return &closeError{closeLagging, "too slow, reconnect and catch up"}
	case realtime.ReasonShutdown:
		return goingAway()
	default:
		return &closeError{websocket.StatusTryAgainLater, "event stream interrupted"}
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
