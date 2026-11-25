package app

import (
	"context"
	"time"
)

// blockedRetry is how often agent folders the OS refused are read again.
const blockedRetry = 15 * time.Second

// retryBlocked re-reads the agents while some agent folder is refused
// ("operation not permitted", registry.Blocked), so once the user allows
// access in macOS the agents there appear and start without a restart.
func (a *App) retryBlocked(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if len(a.Registry.Blocked()) == 0 {
			continue
		}
		a.mutate.Lock()
		_ = a.Supervisor.Reload(ctx)
		a.mutate.Unlock()
	}
}
