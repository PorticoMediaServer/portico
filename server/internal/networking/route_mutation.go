package networking

import (
	"context"
	"database/sql"
	"sync"
)

// Route naming and withdrawal are remote mutations, not ordinary SQLite writes.
// Their shared lane is keyed by the root-owned DB handle and immutable server
// identity, so independent manager instances for that server cannot reorder them.
// References include waiters; the entry disappears after the last caller leaves.
// No database transaction or dbwork writer permit is retained across network I/O.
type routeMutationKey struct {
	db     *sql.DB
	server string
}
type routeMutationLane struct {
	gate       chan struct{}
	references int
}

var routeMutations = struct {
	sync.Mutex
	lanes map[routeMutationKey]*routeMutationLane
}{lanes: make(map[routeMutationKey]*routeMutationLane)}

func withRouteMutation(ctx context.Context, db *sql.DB, server string, fn func(context.Context) error) error {
	if db == nil || !validID(server) || fn == nil {
		return ErrInvalid
	}
	key := routeMutationKey{db, server}
	routeMutations.Lock()
	lane := routeMutations.lanes[key]
	if lane == nil {
		lane = &routeMutationLane{gate: make(chan struct{}, 1)}
		routeMutations.lanes[key] = lane
	}
	lane.references++
	routeMutations.Unlock()
	defer func() {
		routeMutations.Lock()
		lane.references--
		if lane.references == 0 {
			delete(routeMutations.lanes, key)
		}
		routeMutations.Unlock()
	}()
	select {
	case lane.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-lane.gate }()
	if _, e := claimAuthority(ctx); e != nil {
		return e
	}
	return fn(ctx)
}
