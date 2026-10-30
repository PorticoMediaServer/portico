package httpapi

import (
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// streamVerdictLife bounds how long a byte stream trusts its last successful
// authorization without asking the database again. Authority-bearing writes
// invalidate the verdict immediately through dbwork.AuthorityGeneration, so
// this only bounds the things that are not writes in this process: a session
// or grant reaching its expiry time, and a playback session its own client
// stopped.
const streamVerdictLife = time.Second

// streamAuthority makes the per-read authorization of a media stream cheap
// without making it late. The guards call check before and after every read,
// which for a 10 Mbps stream is about eighty times a second; the full check is
// two read transactions and about ten queries, so fifty streams were several
// thousand transactions a second of re-authorization against a pool of eight.
//
// A verdict is reused only while no authority-bearing write has committed
// anywhere in the process and it is younger than streamVerdictLife. A
// revocation therefore still stops the very next read, and a denial is never
// cached: it ends the stream.
type streamAuthority struct {
	full func() error

	mu         sync.Mutex
	generation uint64
	checkedAt  time.Time
	valid      bool
}

func newStreamAuthority(full func() error) *streamAuthority {
	return &streamAuthority{full: full}
}

func (a *streamAuthority) check() error {
	generation := dbwork.AuthorityGeneration()
	a.mu.Lock()
	if a.valid && a.generation == generation && time.Since(a.checkedAt) < streamVerdictLife {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	// The generation is read BEFORE the full check: a revocation that commits
	// while the check runs leaves this verdict stamped with the older value,
	// so the next read re-validates rather than trusting it.
	err := a.full()
	a.mu.Lock()
	defer a.mu.Unlock()
	if errors.Is(err, compactcatalog.ErrBuilding) && a.valid {
		// The item has a catalogue change that isn't published yet: keep the
		// last good verdict for another verdict life, so a title edit doesn't
		// cut the stream (and the full check isn't repeated on every read). A
		// restriction still ends the stream once it publishes, and a
		// revocation (an authority write) at the next read.
		a.generation, a.checkedAt = generation, time.Now()
		return nil
	}
	a.valid, a.generation, a.checkedAt = err == nil, generation, time.Now()
	return err
}
