// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds the auth service configuration. Secrets are only ever read
// from the environment; see .env.example for the full list.
type Config struct {
	GRPCAddr          string
	DatabaseURL       string
	JWTPrivateKeyPath string
	JWTPublicKeyPath  string
	JWTAccessTTL      time.Duration
	JWTRefreshTTL     time.Duration
	Argon2MemoryKiB   uint32
	Argon2Time        uint32
	Argon2Parallelism uint8
	LogLevel          string
}

// Load reads configuration from AUTH_* environment variables. DatabaseURL
// and both key paths are required; everything else has defaults.
func Load() (*Config, error) {
	cfg := &Config{
		GRPCAddr:          envOr("AUTH_GRPC_ADDR", ":50051"),
		JWTAccessTTL:      15 * time.Minute,
		JWTRefreshTTL:     720 * time.Hour,
		Argon2MemoryKiB:   65536,
		Argon2Time:        3,
		Argon2Parallelism: 2,
		LogLevel:          envOr("AUTH_LOG_LEVEL", "info"),
	}

	var err error
	if cfg.DatabaseURL, err = requiredEnv("AUTH_DATABASE_URL"); err != nil {
		return nil, err
	}
	if cfg.JWTPrivateKeyPath, err = requiredEnv("AUTH_JWT_PRIVATE_KEY_PATH"); err != nil {
		return nil, err
	}
	if cfg.JWTPublicKeyPath, err = requiredEnv("AUTH_JWT_PUBLIC_KEY_PATH"); err != nil {
		return nil, err
	}
	if cfg.JWTAccessTTL, err = durationEnv("AUTH_JWT_ACCESS_TTL", cfg.JWTAccessTTL); err != nil {
		return nil, err
	}
	if cfg.JWTRefreshTTL, err = durationEnv("AUTH_JWT_REFRESH_TTL", cfg.JWTRefreshTTL); err != nil {
		return nil, err
	}
	if cfg.Argon2MemoryKiB, err = uintEnv("AUTH_ARGON2_MEMORY_KIB", cfg.Argon2MemoryKiB, 32); err != nil {
		return nil, err
	}
	if cfg.Argon2Time, err = uintEnv("AUTH_ARGON2_TIME", cfg.Argon2Time, 32); err != nil {
		return nil, err
	}
	parallelism, err := uintEnv("AUTH_ARGON2_PARALLELISM", uint32(cfg.Argon2Parallelism), 8)
	if err != nil {
		return nil, err
	}
	cfg.Argon2Parallelism = uint8(parallelism)

	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return value, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("environment variable %s: %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("environment variable %s must be positive", key)
	}
	return parsed, nil
}

func uintEnv(key string, fallback uint32, bits int) (uint32, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("environment variable %s: %w", key, err)
	}
	if parsed == 0 {
		return 0, fmt.Errorf("environment variable %s must be positive", key)
	}
	return uint32(parsed), nil
}
