package streaming

import (
	"testing"
	"time"
)

// One plan load must carry its caller's context through every read (plan, pin
// existence, prepared version and conversion target). With the pool genuinely
// exhausted by a held connection, a cancelled caller must return at once instead
// of waiting for a background-context query to get a slot.

const planContextBound = 2 * time.Second

// boundedCall is one lookup running in its own goroutine. Cleanup is registered
// before the goroutine starts: it cancels, releases the held slot and joins on a
// bounded channel, so an early Fatal cannot leak the goroutine or block db.Close.
type boundedCall struct {
	done   chan error
	joined chan struct{}
}

func (c *boundedCall) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-c.done:
		return err
	case <-time.After(planContextBound):
		t.Fatalf("lookup still waiting after %s; a cancelled caller must not wait for a pool slot", planContextBound)
		return nil
	}
}
