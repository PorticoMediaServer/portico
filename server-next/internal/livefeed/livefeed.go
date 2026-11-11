// Package livefeed tells apps about things that live only in memory: a device being asked to
// play, what a device is playing, a group changing. The change feed (GET /v1/events) merges
// these in beside the changes it reads from the database.
//
// Like the database's changes, an event only says "this changed, read it again". Unlike them,
// nothing here is kept for long: an event is kept two minutes, and a reader who was away
// longer than that, or whose cursor is from before a restart, is told to read everything
// again. That is the whole promise across a reconnect.
package livefeed

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

// Topics of the events this package carries.
const (
	// TopicReceiver: a request to one of the account's devices was made or answered, or the
	// account's devices open to playback changed. ID is the request, or empty.
	TopicReceiver = "receiver"
	// TopicPlayback: one of the account's devices started, changed or stopped playing, or was
	// sent a command from another device. ID is the playback session.
	TopicPlayback = "playback"
	// TopicGroup: a Watch Together group this profile is in changed or ended. ID is the group.
	TopicGroup = "group"
)

// Ops of an event.
const (
	OpUpdated = "updated"
	OpDeleted = "deleted"
)

// Event is one thing to read again.
type Event struct {
	Seq   int64
	Topic string
	Op    string
	ID    string
}

const (
	// keep is how long an event is kept for a reader that has not looked yet. A reader looks
	// at least every 30 seconds while it is connected.
	keep = 2 * time.Minute
	// perRing is the most events kept for one account or one profile. A reader that missed
	// more than this reads everything again.
	perRing = 64
)

type ring struct {
	events []Event
	// dropped is the newest event let go because the ring was full.
	dropped int64
}

// Publisher keeps the recent events of each account and each profile. It is safe for
// concurrent use and its size is bounded by the accounts and profiles that had an event in
// the last two minutes.
type Publisher struct {
	now func() time.Time

	mu       sync.Mutex
	start    int64
	seq      int64
	accounts map[int64]*ring
	profiles map[int64]*ring
	swept    time.Time
}

// New returns a publisher. now is the server's clock.
func New(now func() time.Time) *Publisher {
	p := &Publisher{now: now, accounts: map[int64]*ring{}, profiles: map[int64]*ring{}}
	p.start = p.next()
	return p
}

// next is a new sequence number. Numbers are microseconds of the clock, so a cursor says when
// it was made, and they always grow within one run of the server.
func (p *Publisher) next() int64 {
	p.seq = max(p.seq+1, p.now().UnixMicro())
	return p.seq
}

// ToAccount tells every device and profile of an account.
func (p *Publisher) ToAccount(userID int64, topic, op, id string) {
	p.publish(p.accounts, userID, topic, op, id)
}

// ToProfile tells one profile, on every device it is open on.
func (p *Publisher) ToProfile(profileID int64, topic, op, id string) {
	p.publish(p.profiles, profileID, topic, op, id)
}

func (p *Publisher) publish(rings map[int64]*ring, key int64, topic, op, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := rings[key]
	if r == nil {
		r = &ring{}
		rings[key] = r
	}
	if len(r.events) == perRing {
		r.dropped = r.events[0].Seq
		r.events = r.events[1:]
	}
	r.events = append(r.events, Event{Seq: p.next(), Topic: topic, Op: op, ID: id})
	p.sweepLocked()
}

// sweepLocked lets go of rings whose newest event is older than keep, at most every so often.
func (p *Publisher) sweepLocked() {
	now := p.now()
	if now.Sub(p.swept) < keep/4 {
		return
	}
	p.swept = now
	oldest := now.Add(-keep).UnixMicro()
	for _, rings := range []map[int64]*ring{p.accounts, p.profiles} {
		for key, r := range rings {
			if r.events[len(r.events)-1].Seq < oldest {
				delete(rings, key)
			}
		}
	}
}

// Now is the cursor of a reader that starts from now.
func (p *Publisher) Now() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.next()
}

// After returns what an account's profile should hear after a cursor, oldest first, and the
// cursor to ask from next. Lost is true when the reader may have missed something: its cursor
// is from before this run, older than events are kept, or older than an event let go. It
// should then read everything again.
func (p *Publisher) After(userID, profileID, cursor int64) (events []Event, next int64, lost bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	next = p.next()
	if cursor < p.start || cursor < p.now().Add(-keep).UnixMicro() || cursor > next {
		return nil, next, true
	}
	for _, r := range []*ring{p.accounts[userID], p.profiles[profileID]} {
		if r == nil {
			continue
		}
		if r.dropped > cursor {
			return nil, next, true
		}
		for _, e := range r.events {
			if e.Seq > cursor {
				events = append(events, e)
			}
		}
	}
	// The two rings are each in order; together they are put in order.
	slices.SortFunc(events, func(a, b Event) int { return cmp.Compare(a.Seq, b.Seq) })
	return events, next, false
}
