package config

import (
	"time"
)

// ChatConfig contains Chat's independent database and internal RPC settings.
type ChatConfig struct {
	GRPCAddr       string
	AuthGRPCAddr   string
	DatabaseURL    string
	RequestTimeout time.Duration
	LogLevel       string
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
	return &ChatConfig{GRPCAddr: envOr("CHAT_GRPC_ADDR", ":50052"), AuthGRPCAddr: envOr("CHAT_AUTH_GRPC_ADDR", "localhost:50051"), DatabaseURL: database, RequestTimeout: timeout, LogLevel: envOr("CHAT_LOG_LEVEL", "info")}, nil
}
