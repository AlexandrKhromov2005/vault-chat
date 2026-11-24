package chatload

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// scraper reads the private metrics listeners of Auth, Chat and Gateway.
type scraper struct {
	targets map[string]string
	client  *http.Client
}

func newScraper(targets map[string]string) *scraper {
	return &scraper{targets: targets, client: &http.Client{Timeout: 2 * time.Second}}
}

func (s *scraper) scrape(ctx context.Context, service string) (Metrics, error) {
	url, ok := s.targets[service]
	if !ok {
		return nil, fmt.Errorf("no metrics target for %s", service)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s metrics: %s", service, response.Status)
	}
	return ParseMetrics(response.Body)
}

// counters are the cumulative values compared at the edges of a phase.
type counters struct {
	ok                                       bool
	published, dropped, failed, capacity     float64
	gateway5xx, gateway429, authRPC, chatRPC float64
}

func (s *scraper) counters(ctx context.Context) counters {
	chat, chatErr := s.scrape(ctx, "chat")
	gateway, gatewayErr := s.scrape(ctx, "gateway")
	auth, authErr := s.scrape(ctx, "auth")
	if chatErr != nil || gatewayErr != nil || authErr != nil {
		return counters{}
	}
	api := func(prefix string) float64 {
		return gateway.SumWhere("vault_http_requests_total", func(labels map[string]string) bool {
			return labels["kind"] == "api" && strings.HasPrefix(labels["status"], prefix)
		})
	}
	return counters{
		ok:         true,
		published:  chat.Sum("vault_events_published_total", nil),
		dropped:    chat.Sum("vault_events_dropped_total", nil),
		failed:     chat.Sum("vault_events_publish_failures_total", nil),
		capacity:   chat.Sum("vault_event_queue_capacity", nil),
		gateway5xx: api("5"), gateway429: api("429"),
		authRPC: auth.Sum("vault_grpc_requests_total", nil),
		chatRPC: chat.Sum("vault_grpc_requests_total", nil),
	}
}

// sample polls the gauges once a second until ctx ends.
func (s *scraper) sample(ctx context.Context) Server {
	server := Server{WebSocketsMin: math.Inf(1)}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		chat, chatErr := s.scrape(ctx, "chat")
		gateway, gatewayErr := s.scrape(ctx, "gateway")
		if ctx.Err() != nil {
			break
		}
		server.Scrapes++
		if chatErr != nil || gatewayErr != nil {
			server.ScrapeErrors++
		} else {
			server.EventQueueMax = max(server.EventQueueMax, chat.Sum("vault_event_queue_pending", nil))
			sockets := gateway.Sum("vault_websocket_connections", nil)
			server.WebSocketsMin = min(server.WebSocketsMin, sockets)
			server.WebSocketsMax = max(server.WebSocketsMax, sockets)
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
			continue
		}
		break
	}
	if math.IsInf(server.WebSocketsMin, 1) {
		server.WebSocketsMin = 0
	}
	return server
}

// delta adds the change of the counters since before to server.
func (s *scraper) delta(ctx context.Context, before counters, server *Server) {
	after := s.counters(ctx)
	if !before.ok || !after.ok {
		server.ScrapeErrors++
		return
	}
	server.EventQueueCapacity = after.capacity
	server.EventsPublished = after.published - before.published
	server.EventsDropped = after.dropped - before.dropped
	server.PublishFailures = after.failed - before.failed
	server.Gateway5xx = after.gateway5xx - before.gateway5xx
	server.Gateway429 = after.gateway429 - before.gateway429
	server.AuthRPCs = after.authRPC - before.authRPC
	server.ChatRPCs = after.chatRPC - before.chatRPC
}
