package api

import (
	"maps"
	"slices"
	"sync"
	"time"
)

// A key's notices: what a key, token or app password sends through POST
// /v1/admin/notifications, directly or through the compatible routes' notice to
// administrators, is bounded per key, so a tool that sends one on every download cannot fill
// the owner's and administrators' inboxes. A signed-in person is not bounded.

// How many notices a tool may put to administrators, per key.
const (
	toolNoticesPerHour = 30
	toolNoticeRepeat   = 10 * time.Minute
)

// toolNotices remembers, in memory, when each key sent its notices in the last hour and the
// words of those in the last toolNoticeRepeat, with what was stored for them. A restart
// forgets it, which lets a tool send at most one more hour's worth. Both are bounded by
// toolNoticesPerHour for each key.
type toolNotices struct {
	mu   sync.Mutex
	keys map[int64]*toolNoticeLog
}

type toolNoticeLog struct {
	sent  []time.Time
	words map[[32]byte]toolNoticeSent
}

type toolNoticeSent struct {
	at   time.Time
	sent ServerMessageSent
}

// check says whether these words from this key were sent within toolNoticeRepeat, and what was
// stored then, or else how long until the key may send another this hour (0: now).
func (n *toolNotices) check(key int64, words [32]byte, now time.Time) (repeat *ServerMessageSent, wait time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	log := n.keys[key]
	if log == nil {
		return nil, 0
	}
	log.sent = slices.DeleteFunc(log.sent, func(at time.Time) bool { return now.Sub(at) >= time.Hour })
	maps.DeleteFunc(log.words, func(_ [32]byte, w toolNoticeSent) bool { return now.Sub(w.at) >= toolNoticeRepeat })
	if w, ok := log.words[words]; ok {
		return &w.sent, 0
	}
	if len(log.sent) >= toolNoticesPerHour {
		return nil, log.sent[0].Add(time.Hour).Sub(now)
	}
	return nil, 0
}

// record notes a notice the key has sent, and what was stored.
func (n *toolNotices) record(key int64, words [32]byte, now time.Time, sent ServerMessageSent) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.keys == nil {
		n.keys = map[int64]*toolNoticeLog{}
	}
	log := n.keys[key]
	if log == nil {
		log = &toolNoticeLog{words: map[[32]byte]toolNoticeSent{}}
		n.keys[key] = log
	}
	log.sent, log.words[words] = append(log.sent, now), toolNoticeSent{now, sent}
}
