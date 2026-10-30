package dbwork

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// A background loop that polls is really asking one question: has anything
// changed? The database knows the answer exactly, because every change in this
// server goes through one write gate. So instead of fifteen loops asking four
// times a second, every committed write tells them once.
//
// That is what makes an idle server genuinely idle: no writes, no wakes, no
// queries. And it is what keeps a busy server responsive, because a loop hears
// about work the moment it is enqueued rather than up to a tick later — which is
// better than the timers it replaces, not merely cheaper.
//
// It is deliberately coarse. A loop woken by a commit that had nothing to do
// with it asks one question and goes back to sleep; on a server where writes are
// happening at all, one extra query is not the cost worth optimising.

// changingCommits counts transactions that actually changed rows. It is the
// honest "did anything happen?" signal a background loop needs: the write gate's
// acquisition count moves for a transaction that wrote nothing, which is exactly
// the shape a "nothing to do" pass has, so a loop that watched acquisitions
// would see itself and never settle.
var changingCommits atomic.Uint64

// ChangingCommits reports how many transactions have committed a real change.
func ChangingCommits() uint64 { return changingCommits.Load() }

var commitWakes struct {
	mu      sync.Mutex
	signals []commitSubscription
}

// directWakes is reserved for subscribers that need to observe autocommit
// ExecWrite calls as well as explicit transactions. Most background workers
// intentionally do not: waking them from their own one-statement maintenance
// updates would create a feedback loop on an idle server.
var directWakes struct {
	mu      sync.Mutex
	signals []commitSubscription
}

type commitSubscription struct {
	signal *worker.Signal
	tables []string
}

// WakeOnTables subscribes to changes in named tables, including trigger and FK
// dependencies. A trailing * matches a table family. Unknown write shapes wake
// everyone rather than risk stranding durable work.
func WakeOnTables(signal *worker.Signal, tables ...string) func() {
	if signal == nil {
		return func() {}
	}
	commitWakes.mu.Lock()
	commitWakes.signals = append(commitWakes.signals, commitSubscription{signal, append([]string{}, tables...)})
	commitWakes.mu.Unlock()
	return func() {
		commitWakes.mu.Lock()
		defer commitWakes.mu.Unlock()
		remaining := make([]commitSubscription, 0, len(commitWakes.signals))
		for _, s := range commitWakes.signals {
			if s.signal != signal {
				remaining = append(remaining, s)
			}
		}
		commitWakes.signals = remaining
	}
}

func WakeOnDirectWrites(signal *worker.Signal, tables ...string) func() {
	if signal == nil {
		return func() {}
	}
	directWakes.mu.Lock()
	directWakes.signals = append(directWakes.signals, commitSubscription{signal, append([]string{}, tables...)})
	directWakes.mu.Unlock()
	return func() {
		directWakes.mu.Lock()
		defer directWakes.mu.Unlock()
		remaining := make([]commitSubscription, 0, len(directWakes.signals))
		for _, s := range directWakes.signals {
			if s.signal != signal {
				remaining = append(remaining, s)
			}
		}
		directWakes.signals = remaining
	}
}

// exactWakes are subscribers woken only by a committed statement whose head
// names one of their tables directly: in a transaction or an autocommit
// ExecWrite; never by a trigger or foreign-key neighbour, and never by a plain
// read that names a root. A statement whose scope couldn't be proven still
// counts when it named a root — `WITH x AS (SELECT 1) UPDATE accounts …` wakes
// the accounts subscriber, as it always has — so this is "the head named it",
// not "the write was proven". A loop whose idle state must cost nothing (no
// wake, no query) uses it, and keeps its own bounded poll while it has work in
// flight.
var exactWakes struct {
	mu      sync.Mutex
	signals []commitSubscription
}

// WakeOnTableWrites wakes signal only when a committed statement's head names
// one of tables directly (see exactWakes): not for a trigger or foreign-key
// neighbour, and not for a read that names the table. An unproven statement that
// still named a root does wake it.
func WakeOnTableWrites(signal *worker.Signal, tables ...string) func() {
	if signal == nil || len(tables) == 0 {
		return func() {}
	}
	exactWakes.mu.Lock()
	exactWakes.signals = append(exactWakes.signals, commitSubscription{signal, append([]string{}, tables...)})
	exactWakes.mu.Unlock()
	return func() {
		exactWakes.mu.Lock()
		defer exactWakes.mu.Unlock()
		remaining := make([]commitSubscription, 0, len(exactWakes.signals))
		for _, s := range exactWakes.signals {
			if s.signal != signal {
				remaining = append(remaining, s)
			}
		}
		exactWakes.signals = remaining
	}
}

func wakeExact(changes *changeSet) {
	exactWakes.mu.Lock()
	signals := append([]commitSubscription{}, exactWakes.signals...)
	exactWakes.mu.Unlock()
	if len(signals) == 0 {
		return
	}
	named := changes.known()
	for _, subscription := range signals {
		for _, table := range subscription.tables {
			if named[table] {
				subscription.signal.Wake()
				break
			}
		}
	}
}

// WakeOnCommit registers a loop's signal to be woken by any committed write.
// The safety tick in worker.Run remains the backstop, so a missed wake costs
// latency rather than correctness.
func WakeOnCommit(signal *worker.Signal) func() { return WakeOnTables(signal) }

// ResetCommitWakes drops every registration, for tests.
func ResetCommitWakes() {
	commitWakes.mu.Lock()
	defer commitWakes.mu.Unlock()
	commitWakes.signals = nil
	directWakes.mu.Lock()
	directWakes.signals = nil
	directWakes.mu.Unlock()
	exactWakes.mu.Lock()
	exactWakes.signals = nil
	exactWakes.mu.Unlock()
}

func wakeMatching(subscriptions []commitSubscription, changes *changeSet) {
	changed := expandedChanges(changes.roots())
	for _, subscription := range subscriptions {
		wake := changed == nil || len(subscription.tables) == 0
		for _, pattern := range subscription.tables {
			for table := range changed {
				if table == pattern || strings.HasSuffix(pattern, "*") && strings.HasPrefix(table, strings.TrimSuffix(pattern, "*")) {
					wake = true
					break
				}
			}
			if wake {
				break
			}
		}
		if wake {
			subscription.signal.Wake()
		}
	}
}

func notifyDirectWrite(changes *changeSet) {
	wakeExact(changes)
	directWakes.mu.Lock()
	signals := append([]commitSubscription{}, directWakes.signals...)
	directWakes.mu.Unlock()
	if len(signals) > 0 {
		wakeMatching(signals, changes)
	}
}

// notifyCommit wakes every registered loop. Signals coalesce, so this is safe to
// call on the commit path however often it happens.
func notifyCommit(changes *changeSet) {
	changingCommits.Add(1)
	wakeExact(changes)
	commitWakes.mu.Lock()
	signals := append([]commitSubscription{}, commitWakes.signals...)
	commitWakes.mu.Unlock()
	wakeMatching(signals, changes)
}
