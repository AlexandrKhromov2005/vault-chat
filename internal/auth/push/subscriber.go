package push

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// Subscribe feeds the signals of eventbus.PushSignalChannel to d until ctx
// ends. Pub/Sub is best effort: signals published while Auth is
// disconnected are lost, and clients catch up when they next sync. Invalid
// payloads are dropped; anyone with Redis access could have written them.
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
				// Counted always, logged at most every 10 seconds.
				if total := d.malformed.Add(1); time.Since(lastWarning) >= 10*time.Second {
					lastWarning = time.Now()
					logger.WarnContext(ctx, "invalid push signals dropped", "total", total)
				}
				continue
			}
			d.Signal(signal)
		}
	}
}
