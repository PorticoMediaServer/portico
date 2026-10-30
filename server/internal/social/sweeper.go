package social

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// The host-disconnect timeline is wall-clock: a host who stops sending
// heartbeats has to become "reconnecting", then paused, then ended, whether or
// not anybody asks. Something must therefore advance it on a clock.
//
// That something used to be every open event stream. Each stream ticked every
// five seconds and called Sweep, and Sweep opened a gated write transaction for
// every live group and rolled it back when — as almost always — nothing had
// changed. Six people watching together meant six streams, so six transactions
// through the server's single writer every five seconds, for a group sitting
// paused. The cost was proportional to how many people were watching, which is
// exactly backwards: the timeline is a property of the group, not of how many
// people are looking at it.
//
// So there is one sweeper for the process, running while any live group exists
// (state not ended/failed), whether or not a stream is open, and Sweep now
// decides what is due with a read and writes only for the groups that actually
// need it. A server where nothing has timed out pays bounded reads every
// second, no matter how many people are watching, and a server with no live
// group runs no loop and no query.

// SweepInterval is how often the host timeline advances. It is the resolution of
// bounded seek/load wait as well as the host-reconnect timeline.
var SweepInterval = time.Second

type sweeper struct {
	mu sync.Mutex
	// holders counts open event streams. They may hold the sweeper open, but
	// they are no longer required: a live group advances with nobody watching.
	holders int
	// live is held while any live group exists. It is set after a commit that
	// makes a group live and at process start when a live group survives a
	// restart, and cleared when a sweep finds no live group.
	live   bool
	cancel context.CancelFunc
	// lives counts ensureLive calls, so a stop decided from a read taken
	// before a group went live never stops that group's sweeper.
	lives uint64
}

func (s *Store) startSweeperLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	s.sweeps.cancel = cancel
	supervise.Go("social.sweeper", func() { s.runSweeper(ctx) })
}

// HoldSweeper starts the process-wide host-timeline sweeper if it is not
// already running, and returns the release for this holder.
//
// Stream holds stay for compatibility, but they are no longer required: the
// sweeper also runs while any live group exists with no stream open, and it
// stops itself when a sweep finds no live group. Exactly one goroutine runs
// per Store: a hold while the live-driven loop is already running only counts,
// it never starts a second loop.
//
// The returned function is idempotent, which is what makes it safe as a defer
// beside an explicit close.
func (s *Store) HoldSweeper() func() {
	s.sweeps.mu.Lock()
	s.sweeps.holders++
	if s.sweeps.cancel == nil {
		s.startSweeperLocked()
	}
	s.sweeps.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.sweeps.mu.Lock()
			s.sweeps.holders--
			var cancel context.CancelFunc
			if s.sweeps.holders <= 0 {
				s.sweeps.holders = 0
				if !s.sweeps.live {
					cancel, s.sweeps.cancel = s.sweeps.cancel, nil
				}
			}
			s.sweeps.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		})
	}
}

// ensureLive records that a live group exists and starts the sweeper when it
// is not running. Call after the commit that made the group live, never inside
// the transaction.
func (s *Store) ensureLive() {
	s.sweeps.mu.Lock()
	s.sweeps.live = true
	s.sweeps.lives++
	if s.sweeps.cancel == nil {
		s.startSweeperLocked()
	}
	s.sweeps.mu.Unlock()
}

// hasLiveGroups reports whether any group still needs its timeline advanced.
func (s *Store) hasLiveGroups(ctx context.Context) (bool, error) {
	if s.DB == nil {
		return false, nil
	}
	var live bool
	e := dbwork.ReadHandle(ctx, s.DB).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM social_groups WHERE state NOT IN ('ended','failed'))`).Scan(&live)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return live, e
}

// ResumeSweeperIfLive starts the sweeper when a live group survives a restart.
// Call once at process start after the store is wired; it runs one read and no
// writes.
func (s *Store) ResumeSweeperIfLive(ctx context.Context) error {
	live, e := s.hasLiveGroups(ctx)
	if e != nil {
		return e
	}
	if live {
		s.ensureLive()
	}
	return nil
}

// stopIfIdle clears the live hold and, when no stream holds the sweeper,
// stops it. A server with no live group then runs no loop and no query.
func (s *Store) stopIfIdle(ctx context.Context) error {
	s.sweeps.mu.Lock()
	seen := s.sweeps.lives
	s.sweeps.mu.Unlock()
	live, e := s.hasLiveGroups(ctx)
	if e != nil {
		return e
	}
	if live {
		return nil
	}
	s.sweeps.mu.Lock()
	if s.sweeps.lives != seen {
		// A group went live after the read: keep sweeping.
		s.sweeps.mu.Unlock()
		return nil
	}
	s.sweeps.live = false
	var cancel context.CancelFunc
	if s.sweeps.holders <= 0 {
		cancel, s.sweeps.cancel = s.sweeps.cancel, nil
	}
	s.sweeps.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *Store) runSweeper(ctx context.Context) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		// Bounded: a sweep that cannot finish in ten seconds is a sweep that has
		// found a database in trouble, and holding the next one behind it helps
		// nobody.
		step, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = s.Sweep(step)
		cancel()
	}
}

// sweeperRunning is for tests: whether the process-wide sweeper is live.
func (s *Store) sweeperRunning() bool {
	s.sweeps.mu.Lock()
	defer s.sweeps.mu.Unlock()
	return s.sweeps.cancel != nil
}
