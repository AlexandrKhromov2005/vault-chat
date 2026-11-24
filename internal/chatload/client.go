package chatload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// user is one synthetic account with its tokens and WebSocket.
type user struct {
	index    int
	id       string
	email    string
	name     string
	password string
	api      *API
	rooms    []*chatRoom

	mu     sync.Mutex
	tokens Tokens

	conn atomic.Pointer[socket]
}

func (u *user) access() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.tokens.AccessToken
}

func (u *user) connected() bool { return u.conn.Load() != nil }

type chatRoom struct {
	id      string
	direct  bool
	members []*user
}

func (r *chatRoom) memberIDs() []string {
	ids := make([]string, len(r.members))
	for i, m := range r.members {
		ids[i] = m.id
	}
	return ids
}

// socket is an authenticated WebSocket and its read loop.
type socket struct {
	conn *websocket.Conn
	// closing is set before the generator closes the socket itself.
	closing atomic.Bool
	done    chan struct{}
}

type frame struct {
	Type    string   `json:"type"`
	RoomID  string   `json:"room_id"`
	Message *Message `json:"message"`
}

// connect opens the user's WebSocket, authenticates in-band and starts the
// read loop. A refused upgrade returns a *RequestError with Retry-After.
func (r *Runner) connect(ctx context.Context, u *user) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, r.wsURL, &websocket.DialOptions{HTTPClient: r.wsClient})
	if err != nil {
		if response != nil {
			failure := &RequestError{Kind: "http_" + strconv.Itoa(response.StatusCode), Status: response.StatusCode, err: err}
			if seconds, parseErr := strconv.Atoi(response.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
				failure.RetryAfter = time.Duration(seconds) * time.Second
			}
			return failure
		}
		return &RequestError{Kind: "network", err: err}
	}
	conn.SetReadLimit(64 << 10)
	auth, _ := json.Marshal(map[string]string{"type": "auth", "token": u.access()})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		_ = conn.CloseNow()
		return &RequestError{Kind: "network", err: err}
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			_ = conn.CloseNow()
			return &RequestError{Kind: "ws_" + closeCode(err), err: err}
		}
		var f frame
		if json.Unmarshal(data, &f) == nil && f.Type == "ready" {
			break
		}
	}
	s := &socket{conn: conn, done: make(chan struct{})}
	u.conn.Store(s)
	go r.read(u, s)
	return nil
}

// read applies the events of one socket until it closes.
func (r *Runner) read(u *user, s *socket) {
	defer close(s.done)
	for {
		_, data, err := s.conn.Read(context.Background())
		if err != nil {
			u.conn.CompareAndSwap(s, nil)
			if !s.closing.Load() {
				r.stats().closed(closeCode(err))
				r.logger.Warn("websocket closed", "user", u.index, "code", closeCode(err))
			}
			return
		}
		received := time.Now()
		var f frame
		if err := json.Unmarshal(data, &f); err != nil {
			r.stats().fail("ws_decode")
			continue
		}
		if f.Type == "message.created" && f.Message != nil {
			r.ledger.Live(u.id, *f.Message, received)
		}
	}
}

// disconnect closes the user's socket as a client leaving would.
func (r *Runner) disconnect(u *user) {
	s := u.conn.Load()
	if s == nil {
		return
	}
	s.closing.Store(true)
	_ = s.conn.Close(websocket.StatusNormalClosure, "")
	<-s.done
	u.conn.CompareAndSwap(s, nil)
}

func closeCode(err error) string {
	if code := websocket.CloseStatus(err); code != -1 {
		return strconv.Itoa(int(code))
	}
	return "lost"
}

// connectAll connects users that have no socket, at most four at a time.
// Upgrades take a per-address token that is not refunded, so refused
// upgrades wait for Retry-After; they are counted, not failed.
func (r *Runner) connectAll(ctx context.Context, users []*user, latencies *Latencies) (int64, error) {
	var retries atomic.Int64
	work := make(chan *user)
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range work {
				if err := r.connectRetrying(ctx, u, latencies, &retries); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	var err error
feed:
	for _, u := range users {
		if u.connected() {
			continue
		}
		select {
		case work <- u:
		case err = <-errs:
			break feed
		case <-ctx.Done():
			err = ctx.Err()
			break feed
		}
	}
	close(work)
	wg.Wait()
	if err == nil {
		select {
		case err = <-errs:
		default:
		}
	}
	return retries.Load(), err
}

func (r *Runner) connectRetrying(ctx context.Context, u *user, latencies *Latencies, retries *atomic.Int64) error {
	for attempt := 0; ; attempt++ {
		start := time.Now()
		err := r.connect(ctx, u)
		if err == nil {
			if latencies != nil {
				latencies.Add(time.Since(start))
			}
			return nil
		}
		var failure *RequestError
		if attempt >= 60 || !errors.As(err, &failure) || (failure.Status != http.StatusTooManyRequests && failure.Status != http.StatusServiceUnavailable) {
			return fmt.Errorf("connect user %d: %w", u.index, err)
		}
		retries.Add(1)
		wait := max(failure.RetryAfter, 500*time.Millisecond)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// keepFresh refreshes the user's tokens before the access token expires and
// re-authenticates the open socket, as the web client does.
func (r *Runner) keepFresh(ctx context.Context, u *user) {
	for {
		u.mu.Lock()
		expires := u.tokens.AccessExpires
		u.mu.Unlock()
		wait := time.Until(expires) - 90*time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(max(wait, time.Second)):
		}
		if err := r.refresh(ctx, u); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.stats().fail("refresh_" + errorKind(err))
			r.logger.Warn("token refresh failed", "user", u.index, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (r *Runner) refresh(ctx context.Context, u *user) error {
	u.mu.Lock()
	refresh := u.tokens.RefreshToken
	u.mu.Unlock()
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tokens, err := u.api.Refresh(requestCtx, refresh)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.tokens = tokens
	u.mu.Unlock()
	if s := u.conn.Load(); s != nil {
		auth, _ := json.Marshal(map[string]string{"type": "auth", "token": tokens.AccessToken})
		if err := s.conn.Write(requestCtx, websocket.MessageText, auth); err != nil {
			return &RequestError{Kind: "network", err: err}
		}
	}
	return nil
}
