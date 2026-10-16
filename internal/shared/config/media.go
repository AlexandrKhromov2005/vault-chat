package config

import (
	"fmt"
	"strconv"
)

// MediaConfig holds private storage and internal RPC settings.
type MediaConfig struct {
	GRPCAddr, DatabaseURL, AuthGRPCAddr, ChatGRPCAddr, S3Endpoint, S3AccessKey, S3SecretKey, S3Bucket, LogLevel string
	S3Secure                                                                                                    bool
}

// LoadMedia requires independent database and object storage credentials.
func LoadMedia() (*MediaConfig, error) {
	c := &MediaConfig{GRPCAddr: envOr("MEDIA_GRPC_ADDR", ":50053"), AuthGRPCAddr: envOr("MEDIA_AUTH_GRPC_ADDR", "localhost:50051"), ChatGRPCAddr: envOr("MEDIA_CHAT_GRPC_ADDR", "localhost:50052"), S3Endpoint: envOr("MEDIA_S3_ENDPOINT", "localhost:9000"), S3Bucket: envOr("MEDIA_S3_BUCKET", "vault-chat-media"), LogLevel: envOr("MEDIA_LOG_LEVEL", "info")}
	var err error
	if c.DatabaseURL, err = requiredEnv("MEDIA_DATABASE_URL"); err != nil {
		return nil, err
	}
	if c.S3AccessKey, err = requiredEnv("MEDIA_S3_ACCESS_KEY"); err != nil {
		return nil, err
	}
	if c.S3SecretKey, err = requiredEnv("MEDIA_S3_SECRET_KEY"); err != nil {
		return nil, err
	}
	if c.S3Secure, err = strconv.ParseBool(envOr("MEDIA_S3_SECURE", "false")); err != nil {
		return nil, fmt.Errorf("MEDIA_S3_SECURE: %w", err)
	}
	return c, nil
}
