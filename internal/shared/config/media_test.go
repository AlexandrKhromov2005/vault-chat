package config

import "testing"

func TestLoadMedia(t *testing.T) {
	for _, key := range []string{"MEDIA_DATABASE_URL", "MEDIA_S3_ACCESS_KEY", "MEDIA_S3_SECRET_KEY"} {
		t.Setenv(key, "")
	}
	if _, err := LoadMedia(); err == nil {
		t.Fatal("missing secrets accepted")
	}
	t.Setenv("MEDIA_DATABASE_URL", "postgres://media")
	t.Setenv("MEDIA_S3_ACCESS_KEY", "key")
	t.Setenv("MEDIA_S3_SECRET_KEY", "secret")
	c, err := LoadMedia()
	if err != nil {
		t.Fatal(err)
	}
	if c.GRPCAddr != ":50053" || c.S3Secure {
		t.Fatalf("defaults: %+v", c)
	}
	t.Setenv("MEDIA_S3_SECURE", "invalid")
	if _, err := LoadMedia(); err == nil {
		t.Fatal("invalid TLS value accepted")
	}
}
