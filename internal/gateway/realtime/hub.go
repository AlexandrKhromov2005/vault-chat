// Package realtime delivers Chat events to the WebSocket connections of one
// Gateway instance. Chat publishes each event to the Redis channel of every
// recipient; every instance subscribes to the channels of the users connected
// to it, so no state is shared between instances.
package realtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrTooManyConnections rejects a connection over the per-user limit.
	ErrTooManyConnections = errors.New("realtime: too many connections for user")
	// ErrClosed rejects a connection after Shutdown.
	ErrClosed = errors.New("realtime: hub closed")
)

// unsubscribeTimeout bounds unsubscribing, which no request waits for.
const unsubscribeTimeout = 5 * time.Second

// Stream is the subscription to the event channels of users. Defined at the
// consumer per CONTRIBUTING.md.
type Stream interface {
	// Subscribe returns once events published for the user from then on are
	// delivered to the hub.
	Subscribe(ctx context.Context, userID string) error
	Unsubscribe(ctx context.Context, userID string) error
}

// Reason tells why the hub ended a client.
type Reason int

const (
	// ReasonLagging means the client's queue was full, so an event was dropped.
	ReasonLagging Reason = iota + 1
	// ReasonInterrupted means events of the user may have been lost upstream.
	ReasonInterrupted
	// ReasonShutdown means the Gateway instance is stopping.
	ReasonShutdown
)

// Client is one connection's queue of frames.
type Client struct {
	userID   string
	frames   chan []byte
	done     chan struct{}
	once     sync.Once
	reason   Reason
	detached bool // guarded by Hub.mu
}

// Frames yields the frames delivered to the client, in delivery order.
func (c *Client) Frames() <-chan []byte { return c.frames }

// Done is closed when the hub ends the client; the connection must then close.
func (c *Client) Done() <-chan struct{} { return c.done }

// Reason tells why the client was ended. It is valid once Done is closed.
func (c *Client) Reason() Reason { return c.reason }

func (c *Client) end(reason Reason) {
	c.once.Do(func() {
		c.reason = reason
		close(c.done)
	})
}

// user is the state of one user's connections on this instance.
type user struct {
	// op serializes the user's subscription changes so they reach Redis in order.
	op         sync.Mutex
	subscribed bool // guarded by op
	refs       int  // attached and attaching clients, guarded by Hub.mu
	clients    map[*Client]struct{}
}

// Hub routes each user's events to that user's clients on this instance.
type Hub struct {
	stream   Stream
	maxConns int
	buffer   int
	mu       sync.Mutex
	users    map[string]*user
	closed   bool
	clients  sync.WaitGroup
}

// NewHub creates a hub that allows maxConns connections per user and queues up
// to buffer frames per connection.
func NewHub(stream Stream, maxConns, buffer int) *Hub {
	return &Hub{stream: stream, maxConns: maxConns, buffer: buffer, users: make(map[string]*user)}
}

// Attach registers a connection of userID. It returns once the user's events
// reach the client, so nothing published afterwards can be missed.
func (h *Hub) Attach(ctx context.Context, userID string) (*Client, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrClosed
	}
	u := h.users[userID]
	if u == nil {
		u = &user{clients: make(map[*Client]struct{})}
		h.users[userID] = u
	}
	if u.refs >= h.maxConns {
		h.mu.Unlock()
		return nil, ErrTooManyConnections
	}
	u.refs++
	h.clients.Add(1)
	h.mu.Unlock()

	u.op.Lock()
	defer u.op.Unlock()
	if !u.subscribed {
		if err := h.stream.Subscribe(ctx, userID); err != nil {
			h.release(userID, u)
			return nil, fmt.Errorf("subscribe to user events: %w", err)
		}
		u.subscribed = true
	}
	client := &Client{userID: userID, frames: make(chan []byte, h.buffer), done: make(chan struct{})}
	h.mu.Lock()
	u.clients[client] = struct{}{}
	if h.closed {
		client.end(ReasonShutdown)
	}
	h.mu.Unlock()
	return client, nil
}

// Detach unregisters a client once its connection has ended. The user's
// channel is unsubscribed when their last client on this instance leaves.
func (h *Hub) Detach(client *Client) {
	h.mu.Lock()
	if client.detached {
		h.mu.Unlock()
		return
	}
	client.detached = true
	u := h.users[client.userID]
	delete(u.clients, client)
	h.mu.Unlock()

	u.op.Lock()
	defer u.op.Unlock()
	h.release(client.userID, u)
}

// release drops one reference to u, which must be locked by u.op, and
// unsubscribes the user when it was the last one.
func (h *Hub) release(userID string, u *user) {
	defer h.clients.Done()
	h.mu.Lock()
	u.refs--
	idle := u.refs == 0
	h.mu.Unlock()
	if !idle {
		return
	}
	if u.subscribed {
		ctx, cancel := context.WithTimeout(context.Background(), unsubscribeTimeout)
		defer cancel()
		// A failure leaves a subscription without clients; its events are dropped.
		_ = h.stream.Unsubscribe(ctx, userID)
		u.subscribed = false
	}
	h.mu.Lock()
	// An attach that found u while it was being unsubscribed keeps it.
	if u.refs == 0 && h.users[userID] == u {
		delete(h.users, userID)
	}
	h.mu.Unlock()
}

// Deliver queues frame for every client of userID without blocking. A client
// whose queue is full is ended rather than allowed to hold memory.
func (h *Hub) Deliver(userID string, frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if u := h.users[userID]; u != nil {
		for client := range u.clients {
			select {
			case client.frames <- frame:
			default:
				client.end(ReasonLagging)
			}
		}
	}
}

// Interrupt ends every client of userID because some of the user's events may
// have been lost; the clients reconnect and catch up.
func (h *Hub) Interrupt(userID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if u := h.users[userID]; u != nil {
		for client := range u.clients {
			client.end(ReasonInterrupted)
		}
	}
}

// Shutdown rejects new clients, ends the current ones and waits until all of
// them have detached or ctx ends.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	for _, u := range h.users {
		for client := range u.clients {
			client.end(ReasonShutdown)
		}
	}
	h.mu.Unlock()
	detached := make(chan struct{})
	go func() {
		h.clients.Wait()
		close(detached)
	}()
	select {
	case <-detached:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for connections: %w", ctx.Err())
	}
}
