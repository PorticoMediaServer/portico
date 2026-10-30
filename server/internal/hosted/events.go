package hosted

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"time"
)

type serverEvent struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// Store the exact claim binding with each event so reset/reclaim cannot replay
// a previous owner's change using a successor's credential.
func (s *Service) QueueServerEvent(ctx context.Context, kind string) error {
	if s == nil || s.current == nil {
		return nil
	}
	return s.current.runner.Do(ctx, func(ctx context.Context) error {
		v, e := s.current.store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		q := serverEvent{Kind: kind}
		if kind == "rename" {
			q.Name = s.identity.Name()
		}
		raw, _ := json.Marshal(q)
		e = s.current.store.WithInstalledTransaction(ctx, v, func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, `INSERT INTO hosted_server_events(operation_id,kind,payload,revision) VALUES(?,?,?,1) ON CONFLICT(operation_id,kind) DO UPDATE SET payload=excluded.payload,revision=revision+1,attempts=0,next_at=0`, v.OperationID, kind, string(raw))
			return e
		})
		if e == nil {
			s.wakeControl()
		}
		return e
	})
}

// eventBatch bounds one pass over the outbox; the kinds are few (rename,
// storage, shutdown), so this is every pending row in practice.
const eventBatch = 16

// sendServerEvents drains the server-event outbox for the installed claim and
// reports when the earliest remaining event is due (zero when none is).
//
// A41: an empty outbox, or no installed claim, is nothing to do. It used to be
// an error, so a server configured for Hosted but never claimed woke every
// minute and took the lease plus two write transactions each time.
// A46: every due row is tried, each with its own attempts and backoff; a
// permanent refusal drops that row instead of blocking the others for ever.
func (s *Service) sendServerEvents(ctx context.Context) (time.Time, error) {
	var pending int
	if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM hosted_server_events`).Scan(&pending); e != nil || pending == 0 {
		return time.Time{}, e
	}
	v, e := s.current.store.InstalledIntent(ctx)
	if errors.Is(e, networking.ErrStale) {
		// Queued for a claim that is not installed now. A new install wakes the loop.
		return time.Time{}, nil
	}
	if e != nil {
		return time.Time{}, e
	}
	type row struct {
		kind, raw          string
		revision, attempts int64
		next               int64
	}
	var rows []row
	e = s.current.store.WithInstalledTransaction(ctx, v, func(ctx context.Context, tx *sql.Tx) error {
		// Events bound to an earlier claim can never be sent with this credential.
		if _, e := tx.ExecContext(ctx, `DELETE FROM hosted_server_events WHERE operation_id<>?`, v.OperationID); e != nil {
			return e
		}
		list, e := tx.QueryContext(ctx, `SELECT kind,payload,revision,attempts,next_at FROM hosted_server_events WHERE operation_id=? ORDER BY next_at,kind LIMIT ?`, v.OperationID, eventBatch)
		if e != nil {
			return e
		}
		defer list.Close()
		for list.Next() {
			var r row
			if e = list.Scan(&r.kind, &r.raw, &r.revision, &r.attempts, &r.next); e != nil {
				return e
			}
			rows = append(rows, r)
		}
		return list.Err()
	})
	if e != nil {
		return time.Time{}, e
	}
	now := time.Now().UTC()
	var due time.Time
	var firstErr error
	unreachable := false
	for _, r := range rows {
		if r.next > now.Unix() || unreachable {
			at := time.Unix(r.next, 0).UTC()
			if unreachable && !at.After(now) {
				at = now.Add(time.Minute)
			}
			if due.IsZero() || at.Before(due) {
				due = at
			}
			continue
		}
		var q serverEvent
		sendErr := networking.ErrInvalid
		if json.Unmarshal([]byte(r.raw), &q) == nil {
			sendErr = s.current.transport.CallServer(ctx, v, networking.PublishServerEvent, q, nil)
		}
		drop := sendErr == nil || errors.Is(sendErr, networking.ErrInvalid) || networking.ControlRejected(sendErr)
		var next time.Time
		if !drop {
			next = networking.RetryAt(now, time.Minute, int(r.attempts), 6, networking.ControlRetryAt(sendErr))
			if due.IsZero() || next.Before(due) {
				due = next
			}
			if firstErr == nil {
				firstErr = sendErr
			}
			// Hosted is down or refusing us; the rest wait for the same backoff.
			unreachable = errors.Is(sendErr, networking.ErrUnavailable) || networking.NeedsClaimReconciliation(sendErr)
		}
		e = s.current.store.WithInstalledTransaction(ctx, v, func(ctx context.Context, tx *sql.Tx) error {
			if drop {
				// A newer revision queued meanwhile stays, and is sent next pass.
				_, e := tx.ExecContext(ctx, `DELETE FROM hosted_server_events WHERE operation_id=? AND kind=? AND revision=?`, v.OperationID, r.kind, r.revision)
				return e
			}
			_, e := tx.ExecContext(ctx, `UPDATE hosted_server_events SET attempts=attempts+1,next_at=? WHERE operation_id=? AND kind=? AND revision=?`, next.Unix(), v.OperationID, r.kind, r.revision)
			return e
		})
		if e != nil {
			return due, e
		}
	}
	if len(rows) == eventBatch && (due.IsZero() || due.After(now)) {
		due = now
	}
	return due, firstErr
}

// Local disk observation generates a Hosted request only on pressure onset or
// a retry of that durable event. It does not poll Hosted.
func (s *Service) ObserveStorage(ctx context.Context, path string) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	pressure := false
	for {
		free, e := diskspace.Free(path)
		if e == nil {
			low := free < diskspace.ProducerFloor
			if low && !pressure {
				c, cancel := context.WithTimeout(ctx, 3*time.Second)
				if s.QueueServerEvent(c, "storage_nearly_full") == nil {
					pressure = true
				}
				cancel()
			}
			if !low {
				pressure = false
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// NotifySettingsChanged queues a rename only when the name actually changed
// (A61); every other settings save is not Hosted's business.
func (s *Service) NotifySettingsChanged(ctx context.Context) {
	if s == nil || s.identity == nil {
		return
	}
	name := s.identity.Name()
	s.nameMu.Lock()
	if s.publishedName == nil {
		// First save since start: the name Hosted last saw is the one we booted with.
		booted := s.bootName
		s.publishedName = &booted
	}
	changed := *s.publishedName != name
	s.nameMu.Unlock()
	if !changed {
		return
	}
	if s.QueueServerEvent(ctx, "rename") == nil {
		s.nameMu.Lock()
		s.publishedName = &name
		s.nameMu.Unlock()
	}
}

// shutdownNotifyBudget bounds the shutdown notice so a slow Hosted never eats
// the HTTP drain budget (A51). Hosted infers the rest from silence.
const shutdownNotifyBudget = 2 * time.Second

func (s *Service) NotifyShutdown(ctx context.Context) error {
	if s == nil || s.current == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, shutdownNotifyBudget)
	defer cancel()
	return s.current.runner.Do(ctx, func(ctx context.Context) error {
		v, e := s.current.store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		return s.current.transport.CallServer(ctx, v, networking.PublishServerEvent, serverEvent{Kind: "shutdown"}, nil)
	})
}
