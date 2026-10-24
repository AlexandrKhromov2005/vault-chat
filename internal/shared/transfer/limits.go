// Package transfer defines the shared HTTP and RPC file transfer budgets.
package transfer

import "time"

const (
	// MaxDuration bounds even a client trickling bytes indefinitely.
	MaxDuration = 5 * time.Minute
	// IdleTimeout requires progress while leaving room for slow mobile links.
	IdleTimeout = 30 * time.Second
)
