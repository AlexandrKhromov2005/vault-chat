package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no target", nil},
		{"both targets", []string{"-http", "http://127.0.0.1/health", "-grpc", "127.0.0.1:50051"}},
		{"unknown flag", []string{"-tcp", "127.0.0.1:1"}},
		{"non-positive timeout", []string{"-http", "http://127.0.0.1/health", "-timeout", "0s"}},
		{"extra arguments", []string{"-grpc", "127.0.0.1:50051", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			require.Equal(t, exitUsage, run(tt.args, &stderr))
			require.NotEmpty(t, stderr.String())
		})
	}
}

func TestRunHTTP(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(healthy.Close)
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(unhealthy.Close)

	var stderr bytes.Buffer
	require.Equal(t, exitHealthy, run([]string{"-http", healthy.URL}, &stderr))
	require.Empty(t, stderr.String())

	require.Equal(t, exitUnhealthy, run([]string{"-http", unhealthy.URL, "-timeout", "1s"}, &stderr))
	require.Contains(t, stderr.String(), "unhealthy")
}
