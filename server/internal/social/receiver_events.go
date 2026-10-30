package social

import (
	"database/sql"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// ReceiverEvent is an invalidation hint, never a playback command or grant.
// The authenticated inbox remains the durable source of truth.
type ReceiverEvent struct {
	Kind       string `json:"kind"`
	ReceiverID string `json:"receiverId,omitempty"`
}

type receiverScope struct {
	db                          *sql.DB
	authority, account, profile string
}

var receiverEvents = struct {
	sync.Mutex
	waiters map[receiverScope]map[chan ReceiverEvent]struct{}
}{waiters: map[receiverScope]map[chan ReceiverEvent]struct{}{}}

func receiverEventScope(db *sql.DB, p identity.Principal) receiverScope {
	return receiverScope{db, p.Authority, p.AccountID, p.ProfileID}
}

// Every connection starts with resync. This recovers arrivals during a network
// gap or server restart without adding another event ledger beside the inbox.
func SubscribeReceiverEvents(db *sql.DB, p identity.Principal) (<-chan ReceiverEvent, func()) {
	key := receiverEventScope(db, p)
	ch := make(chan ReceiverEvent, 1)
	receiverEvents.Lock()
	if receiverEvents.waiters[key] == nil {
		receiverEvents.waiters[key] = map[chan ReceiverEvent]struct{}{}
	}
	receiverEvents.waiters[key][ch] = struct{}{}
	ch <- ReceiverEvent{Kind: "resync"}
	receiverEvents.Unlock()
	return ch, func() {
		receiverEvents.Lock()
		defer receiverEvents.Unlock()
		delete(receiverEvents.waiters[key], ch)
		if len(receiverEvents.waiters[key]) == 0 {
			delete(receiverEvents.waiters, key)
		}
	}
}

func (s *Store) publishReceiverEvent(p identity.Principal, id, kind string) {
	receiverEvents.Lock()
	defer receiverEvents.Unlock()
	for ch := range receiverEvents.waiters[receiverEventScope(s.DB, p)] {
		select {
		case ch <- ReceiverEvent{Kind: kind, ReceiverID: id}:
		default:
			// Bounded coalescing: a resync represents all pending receiver changes.
			select {
			case <-ch:
			default:
			}
			ch <- ReceiverEvent{Kind: "resync"}
		}
	}
}
