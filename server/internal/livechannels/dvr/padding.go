package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"time"
)

// Programme time wins over padding. When all core programmes fit but their
// padding does not, remove padding from this capture rather than drop its show.
func (s *Store) captureWindowTx(ctx context.Context, tx *sql.Tx, r Recording) (time.Time, time.Time, bool, error) {
	start, e := time.Parse(time.RFC3339Nano, r.Start)
	if e != nil {
		return start, time.Time{}, false, e
	}
	end, e := time.Parse(time.RFC3339Nano, r.End)
	if e != nil {
		return start, end, false, e
	}
	capacity, e := livechannels.CapacityTx(ctx, tx, r.Occurrence.SourceID)
	if e != nil || !capacity.Known {
		return start, end, false, e
	}
	all, e := livechannels.ReservationsTx(ctx, tx, r.Occurrence.SourceID, start, end)
	if e != nil {
		return start, end, false, e
	}
	for i := range all {
		if all[i].ID == r.ID {
			all[i].Priority = -1001
		}
	}
	padded := livechannels.Conflicts(all, r.ID, capacity)
	if len(padded) == 0 {
		return start, end, false, nil
	}
	core := append([]livechannels.Reservation{}, all...)
	rows, e := tx.QueryContext(ctx, `SELECT id,programme_json FROM dvr_recordings WHERE source_id=? AND id IN(SELECT id FROM live_reservations WHERE source_id=? AND enabled=1 AND start_ms<? AND end_ms>?)`, r.Occurrence.SourceID, r.Occurrence.SourceID, end.UnixMilli(), start.UnixMilli())
	if e != nil {
		return start, end, false, e
	}
	programmes := map[string]livechannels.Programme{}
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return start, end, false, e
		}
		var p livechannels.Programme
		if json.Unmarshal([]byte(raw), &p) == nil {
			programmes[id] = p
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return start, end, false, e
	}
	for i := range core {
		if p, ok := programmes[core[i].ID]; ok {
			a, ea := time.Parse(time.RFC3339Nano, p.Start)
			b, eb := time.Parse(time.RFC3339Nano, p.End)
			if ea == nil && eb == nil {
				core[i].Start, core[i].End = a, b
			}
		}
		if core[i].ID == r.ID {
			core[i].Priority = r.Options.Priority
		}
	}
	losses := livechannels.Conflicts(core, r.ID, capacity)
	if len(losses) == 0 {
		a, ea := time.Parse(time.RFC3339Nano, r.Programme.Start)
		b, eb := time.Parse(time.RFC3339Nano, r.Programme.End)
		if ea == nil && eb == nil {
			return a, b, s.now().Before(a), nil
		}
	}
	for _, loss := range losses {
		a, _ := time.Parse(time.RFC3339Nano, loss.Start)
		b, _ := time.Parse(time.RFC3339Nano, loss.End)
		if !s.now().Before(a) && s.now().Before(b) {
			return start, end, true, nil
		}
		if a.After(s.now()) && a.Before(end) {
			end = a
		}
	}
	return start, end, false, nil
}
