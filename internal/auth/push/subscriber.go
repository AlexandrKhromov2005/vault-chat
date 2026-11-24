package push

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// Subscribe feeds best-effort Calls hints from eventbus.PushSignalChannel to d
// until ctx ends. Chat's durable signals use the JetStream consumer instead.
// Pub/Sub signals published while Auth is disconnected are lost; Calls clients
// recover through active-call REST reconciliation. Invalid payloads are dropped.
func Subscribe(ctx context.Context, client *redis.Client, d *Dispatcher, logger *slog.Logger) error {
	sub := client.Subscribe(ctx, eventbus.PushSignalChannel)
	defer func() { _ = sub.Close() }()
	messages := sub.Channel()
	var lastWarning time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case message, ok := <-messages:
			if !ok {
				return fmt.Errorf("push signal subscription closed")
			}
			signal, err := eventbus.DecodePushSignal([]byte(message.Payload))
			if err != nil {
				if total := d.malformed.Add(1); time.Since(lastWarning) >= 10*time.Second {
					lastWarning = time.Now()
					logger.WarnContext(ctx, "invalid Calls push hints dropped", "total", total)
				}
				continue
			}
			d.Signal(signal)
		}
	}
}
