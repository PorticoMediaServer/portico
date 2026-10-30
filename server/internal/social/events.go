package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"sync"
)

// Event kinds published on GET /v1/groups/{id}/events. Every kind except
// `heartbeat` and `resume-gap` is durable and carries a monotonic `id:` that a
// client returns in `Last-Event-ID` to resume.
const (
	EventSnapshot  = "group.snapshot"
	EventTransport = "group.transport"
	EventSettings  = "group.settings"
	EventMembers   = "group.members"
	EventReadiness = "group.readiness"
	EventQueue     = "group.queue"
	EventHost      = "group.host"
	EventEnded     = "group.ended"
	EventHeartbeat = "heartbeat"
	EventResumeGap = "resume-gap"
)

// Event is one durable row of a group's event ledger.
type Event struct {
	Ordinal int64           `json:"-"`
	Kind    string          `json:"-"`
	Payload json.RawMessage `json:"-"`
}

// hub wakes live streams the instant a writer commits, so the ordinary case
// costs no polling latency. It is advisory only: a missed wake-up is caught by
// the stream's own tick, and correctness never depends on it.
type hub struct {
	mu      sync.Mutex
	waiters map[string][]chan struct{}
}

func newHub() *hub { return &hub{waiters: map[string][]chan struct{}{}} }

func (h *hub) subscribe(group string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.waiters[group] = append(h.waiters[group], ch)
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		list := h.waiters[group]
		for i, c := range list {
			if c == ch {
				h.waiters[group] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(h.waiters[group]) == 0 {
			delete(h.waiters, group)
		}
		h.mu.Unlock()
	}
}

func (h *hub) wake(group string) {
	h.mu.Lock()
	list := append([]chan struct{}{}, h.waiters[group]...)
	h.mu.Unlock()
	for _, ch := range list {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Subscribe registers a live listener for one group and returns the channel it
// is woken on plus the release it must call when the stream ends.
func (s *Store) Subscribe(group string) (<-chan struct{}, func()) { return s.hub().subscribe(group) }

// Wake is exported so a caller that commits its own transaction (HTTP
// composition, the sweeper) can publish without reaching into the hub.
func (s *Store) Wake(group string) { s.hub().wake(group) }

// publish appends one event inside the caller's transaction. Ordinals are
// allocated from the group row, so they are gapless per group and survive
// restart; the ledger is trimmed to MaxRetainedEvents on every append.
func (s *Store) publish(ctx context.Context, tx *sql.Tx, group, kind string, payload any) error {
	raw, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE social_groups SET event_ordinal=event_ordinal+1 WHERE id=?`, group); e != nil {
		return e
	}
	var ordinal int64
	if e = tx.QueryRowContext(ctx, `SELECT event_ordinal FROM social_groups WHERE id=?`, group).Scan(&ordinal); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO social_group_events(group_id,ordinal,kind,payload,created_at_ms) VALUES(?,?,?,?,?)`, group, ordinal, kind, string(raw), s.ms()); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `DELETE FROM social_group_events WHERE group_id=? AND ordinal<=?`, group, ordinal-MaxRetainedEvents)
	return e
}

// EventsSince reads the durable ledger after `after`. The second return value is
// the lowest ordinal still retained: when it is greater than after+1 the caller
// has fallen out of the retention window and must be resynchronised with a fresh
// snapshot rather than a partial replay.
func (s *Store) EventsSince(ctx context.Context, p identity.Principal, group string, after int64) ([]Event, int64, error) {
	// One snapshot for both statements: the ledger and its retention floor have
	// to be read from the same state, or a caller can be told it is inside the
	// window by one and outside it by the other.
	tx, done, e := dbwork.BeginRead(ctx, s.DB)
	if e != nil {
		return nil, 0, e
	}
	defer done()
	if _, e = loadJoinedMember(ctx, tx, group, p.Viewer); e != nil {
		return nil, 0, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT ordinal,kind,payload FROM social_group_events WHERE group_id=? AND ordinal>? ORDER BY ordinal LIMIT ?`, group, after, MaxRetainedEvents)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var ev Event
		var payload string
		if e = rows.Scan(&ev.Ordinal, &ev.Kind, &payload); e != nil {
			return nil, 0, e
		}
		ev.Payload = json.RawMessage(payload)
		out = append(out, ev)
	}
	if e = rows.Err(); e != nil {
		return nil, 0, e
	}
	rows.Close()
	visibility := map[string]bool{}
	var room map[string]activityPrivacy
	for i := range out {
		if out[i].Kind == EventTransport {
			var payload map[string]any
			if e = json.Unmarshal(out[i].Payload, &payload); e != nil {
				return nil, 0, e
			}
			if item, ok := payload["itemId"].(string); ok {
				visible, known := visibility[item]
				if !known {
					visible, e = s.visible(ctx, tx, p.Viewer, item)
					if e != nil {
						return nil, 0, e
					}
					visibility[item] = visible
				}
				if !visible {
					payload["itemId"] = ""
				}
			}
			out[i].Payload, e = json.Marshal(payload)
			if e != nil {
				return nil, 0, e
			}
		}
		if out[i].Kind == EventReadiness {
			if room == nil {
				if room, e = roomPrivacy(ctx, tx, group); e != nil {
					return nil, 0, e
				}
			}
			out[i].Payload, e = projectActivityWith(room, out[i].Payload)
			if e != nil {
				return nil, 0, e
			}
		}

	}
	var floor sql.NullInt64
	if e = tx.QueryRowContext(ctx, `SELECT min(ordinal) FROM social_group_events WHERE group_id=?`, group).Scan(&floor); e != nil {
		return nil, 0, e
	}
	return out, floor.Int64, nil
}
