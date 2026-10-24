// Package events wakes gateways when Auth commits a session revocation.
package events

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// Notifier publishes invalidation hints; no tokens or email are sent.
type Notifier struct{ Client *redis.Client }

// Notify is bounded by the caller's context. Auth's database is authoritative.
func (n Notifier) Notify(ctx context.Context, userID string) error {
	return n.Client.Publish(ctx, eventbus.UserChannel(userID), eventbus.SessionRevoked).Err()
}
