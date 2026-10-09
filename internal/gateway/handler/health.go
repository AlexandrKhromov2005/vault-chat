package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// readyTimeout bounds all readiness checks together, so that a hanging
// dependency cannot hang the orchestrator's probe.
const readyTimeout = 2 * time.Second

// Check reports whether a dependency is usable.
type Check func(ctx context.Context) error

// Health serves the /health (liveness) and /ready (readiness) probes
// (ARCHITECTURE.md section 6.2).
type Health struct {
	checks map[string]Check
	logger *slog.Logger
}

// NewHealth creates probes; checks are the dependencies readiness requires.
func NewHealth(checks map[string]Check, logger *slog.Logger) *Health {
	return &Health{checks: checks, logger: logger}
}

// Live reports that the process is running. It never consults dependencies:
// restarting the gateway does not fix a broken backend.
func (h *Health) Live(w http.ResponseWriter, _ *http.Request) {
	response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Ready reports whether the gateway can serve traffic. Failure causes are
// logged, not returned: the endpoint is public.
func (h *Health) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()

	results := make(map[string]string, len(h.checks))
	ready := true
	for name, check := range h.checks {
		if err := check(ctx); err != nil {
			h.logger.WarnContext(ctx, "readiness check failed", "check", name, "error", err)
			results[name] = "fail"
			ready = false
			continue
		}
		results[name] = "ok"
	}

	if !ready {
		response.JSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "checks": results})
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"status": "ready", "checks": results})
}
