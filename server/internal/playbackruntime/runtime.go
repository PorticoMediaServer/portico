package playbackruntime

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Runtime owns channel-session playback lifetime (v1 channels, spec §18.6).
// It starts no work until Start.
type Runtime struct {
	Linear          *LinearRuntime
	Channels        *playback.ChannelSessions
	probeSupervisor *storage.Supervisor
	mu              sync.Mutex
	cancel          context.CancelFunc
	done            chan struct{}
	wake            workerloop.Broadcast
}

func New(db *sql.DB, id *identity.Service, store *storage.Client) (*Runtime, error) {
	if db == nil || id == nil || store == nil {
		return nil, errors.New("invalid playback runtime dependencies")
	}
	channels, err := playback.NewChannelSessions(db, id)
	if err != nil {
		return nil, err
	}
	return &Runtime{Channels: channels, probeSupervisor: store.Supervisor}, nil
}

func (r *Runtime) Start(parent context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return errors.New("playback runtime already started")
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.done = make(chan struct{})
	var wg sync.WaitGroup
	if r.Linear != nil {
		wg.Add(1)
		supervise.Go("playbackruntime.linear", func() { defer wg.Done(); r.Linear.run(ctx) })
	}
	supervise.Go("playbackruntime.join", func() { wg.Wait(); close(r.done) })
	return nil
}
func (r *Runtime) Wake() { r.wake.Wake() }
func (r *Runtime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
