package chatload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// RequestError is a failed request, classified for the error counters.
type RequestError struct {
	// Kind is "http_<status>", "timeout", "network" or "decode".
	Kind   string
	Status int
	// RetryAfter is the server's Retry-After for 429 and 503, if any.
	RetryAfter time.Duration
	err        error
}

func (e *RequestError) Error() string {
	if e.err != nil {
		return e.Kind + ": " + e.err.Error()
	}
	return e.Kind
}

func (e *RequestError) Unwrap() error { return e.err }

// errorKind classifies an error for the counters.
func errorKind(err error) string {
	var request *RequestError
	if errors.As(err, &request) {
		return request.Kind
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "network"
}

// Tokens is the answer to a login or refresh.
type Tokens struct {
	UserID        string    `json:"user_id"`
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	AccessExpires time.Time `json:"access_expires_at"`
}

// API is a Gateway REST client.
type API struct {
	base   string
	client *http.Client
}

// NewAPI returns a client of the Gateway at base, such as https://gateway:8080.
func NewAPI(base string, client *http.Client) *API {
	return &API{base: base, client: client}
}

func (a *API) do(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := a.client.Do(request)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return &RequestError{Kind: "timeout", err: err}
		}
		return &RequestError{Kind: "network", err: err}
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return &RequestError{Kind: "network", err: err}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := &RequestError{Kind: "http_" + strconv.Itoa(response.StatusCode), Status: response.StatusCode}
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			failure.RetryAfter = time.Duration(seconds) * time.Second
		}
		return failure
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return &RequestError{Kind: "decode", err: err}
		}
	}
	return nil
}

// Register creates an account with an invitation and returns its user id.
func (a *API) Register(ctx context.Context, email, username, password, invitation string) (string, error) {
	var out struct {
		UserID string `json:"user_id"`
	}
	err := a.do(ctx, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": email, "username": username, "password": password, "invitation_token": invitation,
	}, &out)
	return out.UserID, err
}

// Login signs in with a password.
func (a *API) Login(ctx context.Context, email, password string) (Tokens, error) {
	var out Tokens
	err := a.do(ctx, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": email, "password": password}, &out)
	return out, err
}

// Refresh rotates a refresh token.
func (a *API) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	var out Tokens
	err := a.do(ctx, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh}, &out)
	return out, err
}

type room struct {
	ID string `json:"room_id"`
}

// Direct opens the direct dialog with peer and returns its room id.
func (a *API) Direct(ctx context.Context, token, peer string) (string, error) {
	var out room
	err := a.do(ctx, http.MethodPost, "/api/v1/rooms/direct", token, map[string]string{"peer_user_id": peer}, &out)
	return out.ID, err
}

// Channel creates a private channel and returns its room id.
func (a *API) Channel(ctx context.Context, token, name string) (string, error) {
	var out room
	err := a.do(ctx, http.MethodPost, "/api/v1/rooms/channels", token, map[string]any{"name": name, "private": true}, &out)
	return out.ID, err
}

// AddMember adds user to a channel owned by the caller.
func (a *API) AddMember(ctx context.Context, token, roomID, user string) error {
	return a.do(ctx, http.MethodPut, "/api/v1/rooms/"+url.PathEscape(roomID)+"/members/"+url.PathEscape(user), token, nil, nil)
}

// Send posts a message; an identical retry returns the original message.
func (a *API) Send(ctx context.Context, token, roomID, clientID, text string) (Message, error) {
	var out Message
	err := a.do(ctx, http.MethodPost, "/api/v1/rooms/"+url.PathEscape(roomID)+"/messages", token,
		map[string]string{"client_message_id": clientID, "text": text}, &out)
	return out, err
}

// History reads one page: newest first without after, or oldest first after
// a sequence when after is not negative. It returns the next position, or ""
// at the end.
func (a *API) History(ctx context.Context, token, roomID string, after int64, limit int) ([]Message, string, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if after >= 0 {
		query.Set("after", strconv.FormatInt(after, 10))
	}
	var out struct {
		Messages   []Message `json:"messages"`
		NextCursor string    `json:"next_cursor"`
	}
	err := a.do(ctx, http.MethodGet, "/api/v1/rooms/"+url.PathEscape(roomID)+"/messages?"+query.Encode(), token, nil, &out)
	return out.Messages, out.NextCursor, err
}

// RoomPage is one page of the caller's rooms.
type RoomPage struct {
	Rooms []struct {
		ID            string     `json:"room_id"`
		LastMessageAt *time.Time `json:"last_message_at"`
	} `json:"rooms"`
	NextCursor string `json:"next_cursor"`
}

// Rooms reads one page of the caller's rooms.
func (a *API) Rooms(ctx context.Context, token, cursor string, limit int) (RoomPage, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var out RoomPage
	err := a.do(ctx, http.MethodGet, "/api/v1/rooms?"+query.Encode(), token, nil, &out)
	return out, err
}

// Ready waits until the Gateway reports its backends reachable.
func (a *API) Ready(ctx context.Context) error {
	for {
		err := a.do(ctx, http.MethodGet, "/ready", "", nil, nil)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway not ready: %w", err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
