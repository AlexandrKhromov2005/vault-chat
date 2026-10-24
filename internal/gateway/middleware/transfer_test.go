package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
)

func TestFileTransferExtendsDeadlinesWhileMakingProgress(t *testing.T) {
	h := middleware.FileTransfer(time.Second, 150*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "stalled", http.StatusRequestTimeout)
			return
		}
		_, _ = w.Write(body)
	}))
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 30*time.Millisecond, 30*time.Millisecond
	srv.Start()
	defer srv.Close()
	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		for range 5 {
			time.Sleep(40 * time.Millisecond)
			if _, err := writer.Write([]byte("x")); err != nil {
				return
			}
		}
	}()
	resp, err := srv.Client().Post(srv.URL, "application/octet-stream", reader)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "xxxxx", string(body))
}

func TestFileTransferCutsOffAnIdleBody(t *testing.T) {
	done := make(chan error, 1)
	h := middleware.FileTransfer(time.Second, 40*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		done <- err
		w.WriteHeader(http.StatusRequestTimeout)
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	reader, writer := io.Pipe()
	defer writer.Close()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		resp, err := srv.Client().Post(srv.URL, "application/octet-stream", reader)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("idle upload kept its slot")
	}
	_ = writer.Close()
	<-requestDone
}
