package config

import (
	"os"
	"time"
)

// ChatConfig contains Chat's independent database, event and internal RPC settings.
type ChatConfig struct {
	GRPCAddr       string
	AuthGRPCAddr   string
	DatabaseURL    string
	RedisAddr      string
	RedisPassword  string
	RequestTimeout time.Duration
	LogLevel       string
	// EventQueueSize is how many committed changes may wait for Redis before
	// new ones are dropped.
	EventQueueSize int
	// EventPublishTimeout bounds one Redis round trip of the event worker.
	EventPublishTimeout time.Duration
	// EventDrainTimeout bounds publishing the queued events on shutdown.
	EventDrainTimeout time.Duration
}

// LoadChat requires CHAT_DATABASE_URL; other settings have development defaults.
func LoadChat() (*ChatConfig, error) {
	database, err := requiredEnv("CHAT_DATABASE_URL")
	if err != nil {
		return nil, err
	}
	timeout, err := durationEnv("CHAT_REQUEST_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, err
	}
	// Up to 2^20-1 changes, so a misconfigured size cannot allocate unbounded memory.
	queueSize, err := uintEnv("CHAT_EVENT_QUEUE_SIZE", 1024, 20)
	if err != nil {
		return nil, err
	}
	publishTimeout, err := durationEnv("CHAT_EVENT_PUBLISH_TIMEOUT", time.Second)
	if err != nil {
		return nil, err
	}
	drainTimeout, err := durationEnv("CHAT_EVENT_DRAIN_TIMEOUT", 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &ChatConfig{GRPCAddr: envOr("CHAT_GRPC_ADDR", ":50052"), AuthGRPCAddr: envOr("CHAT_AUTH_GRPC_ADDR", "localhost:50051"), DatabaseURL: database,
		RedisAddr: envOr("CHAT_REDIS_ADDR", "localhost:6379"), RedisPassword: os.Getenv("CHAT_REDIS_PASSWORD"), RequestTimeout: timeout, LogLevel: envOr("CHAT_LOG_LEVEL", "info"),
		EventQueueSize: int(queueSize), EventPublishTimeout: publishTimeout, EventDrainTimeout: drainTimeout}, nil
}
