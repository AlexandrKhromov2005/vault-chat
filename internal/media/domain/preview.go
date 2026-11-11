package domain

import (
	"errors"
	"time"
)

const (
	PreviewPreset  = "p320"
	DecoderVersion = "zig-1"
)

var ErrStaleLease = errors.New("media: stale preview lease")

type PreviewJob struct {
	FileID                string
	Preset                string
	DecoderVersion        string
	State                 string
	Attempts              int
	InfrastructureRetries int
	NextAttemptAt         time.Time
	LeaseOwner            string
	LeaseUntil            time.Time
}

type PreviewObject struct {
	Key       string
	FileID    string
	State     string
	CreatedAt time.Time
}
