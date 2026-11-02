package decoder

import (
	"context"
	"os"
	"sync"
	"syscall"
	"time"
)

// Scratch prototype (server load review), not for merge: reuse the runtime
// identity while the executable's inode, size and mtime are unchanged, for up
// to ten minutes, instead of hashing its whole library closure on every
// playback-options request. A real fix would key on the closure's stat set.
var scratchIdentity struct {
	sync.Mutex
	m map[string]scratchIdentityEntry
}

type scratchIdentityEntry struct {
	identity string
	ino      uint64
	size     int64
	mtime    time.Time
	at       time.Time
}

func scratchCachedIdentity(ctx context.Context, executable string, compute func(context.Context, string) (string, error)) (string, error) {
	info, err := os.Stat(executable)
	if err != nil {
		return compute(ctx, executable)
	}
	var ino uint64
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		ino = st.Ino
	}
	scratchIdentity.Lock()
	e, ok := scratchIdentity.m[executable]
	scratchIdentity.Unlock()
	if ok && e.ino == ino && e.size == info.Size() && e.mtime.Equal(info.ModTime()) && time.Since(e.at) < 10*time.Minute {
		return e.identity, nil
	}
	identity, err := compute(ctx, executable)
	if err == nil {
		scratchIdentity.Lock()
		if scratchIdentity.m == nil {
			scratchIdentity.m = map[string]scratchIdentityEntry{}
		}
		scratchIdentity.m[executable] = scratchIdentityEntry{identity, ino, info.Size(), info.ModTime(), time.Now()}
		scratchIdentity.Unlock()
	}
	return identity, err
}
