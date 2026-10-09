package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// requestError is a client error found while reading a request.
type requestError struct {
	status  int
	code    string
	message string
}

func (e *requestError) write(w http.ResponseWriter) {
	response.Error(w, e.status, e.code, e.message)
}

// decodeJSON decodes a request body holding exactly one JSON value into dst.
// Unknown fields are rejected so that client typos ("emial") fail loudly
// instead of being silently dropped.
func decodeJSON(r *http.Request, dst any) *requestError {
	mediaType, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	if !strings.EqualFold(strings.TrimSpace(mediaType), "application/json") {
		return &requestError{http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Content-Type must be application/json"}
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxBytesErr):
			return &requestError{http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large"}
		case errors.Is(err, io.EOF):
			return &requestError{http.StatusBadRequest, "invalid_json", "request body is empty"}
		default:
			return &requestError{http.StatusBadRequest, "invalid_json", "invalid JSON body: " + err.Error()}
		}
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &requestError{http.StatusBadRequest, "invalid_json", "request body must contain a single JSON value"}
	}
	return nil
}
