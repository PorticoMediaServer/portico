package dvr

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"time"
)

// The target loses exactly where at least capacity higher-ranked reservations
// overlap it. SQL streams grouped interval edges; memory is independent of the
// number of competing programmes. Explanations expose counts, never foreign IDs.
func conflictsTx(ctx context.Context, tx *sql.Tx, r Recording) ([]livechannels.LosingInterval, error) {
	out := []livechannels.LosingInterval{}
	cap, e := livechannels.CapacityTx(ctx, tx, r.Occurrence.SourceID)
	if e != nil {
		return out, e
	}
	if !cap.Known {
		return out, nil
	}
	start, e := time.Parse(time.RFC3339Nano, r.Start)
	if e != nil {
		return out, ErrUnavailable
	}
	end, e := time.Parse(time.RFC3339Nano, r.End)
	if e != nil {
		return out, ErrUnavailable
	}
	all, e := livechannels.ReservationsTx(ctx, tx, r.Occurrence.SourceID, start, end)
	if e != nil {
		return out, e
	}
	selected := false
	for i := range all {
		if all[i].ID == r.ID {
			all[i].ChannelID = r.Occurrence.ChannelID
			selected = true
		}
	}
	if !selected {
		all = append(all, livechannels.Reservation{ID: r.ID, ChannelID: r.Occurrence.ChannelID, Start: start, End: end, Priority: r.Options.Priority})
	}
	return livechannels.Conflicts(all, r.ID, cap), nil
}

// Determine loss before paging. At every possible change in higher-priority
// demand, count distinct other channels (same-channel captures share a tuner).
// This matches Conflicts' priority/start/id ordering without exposing foreign
// reservation identities in a response.
const currentConflictSQL = `(CASE WHEN r.state IN('scheduled','conflicted','waiting-source','waiting-guide') THEN EXISTS(
 SELECT 1 FROM live_sources src WHERE src.id=r.source_id AND src.state='active' AND src.tuner_count>0 AND EXISTS(
 SELECT 1 FROM (SELECT r.start_ms at UNION SELECT a.start_ms FROM live_reservations a WHERE a.source_id=r.source_id AND a.enabled=1 AND a.start_ms>=r.start_ms AND a.start_ms<r.end_ms) point
 WHERE (SELECT count(DISTINCT COALESCE(other.channel_id,h.id)) FROM live_reservations h LEFT JOIN main.dvr_recordings other ON other.id=h.id
 WHERE h.source_id=r.source_id AND h.enabled=1 AND h.id<>r.id AND COALESCE(other.channel_id,h.id)<>r.channel_id AND h.start_ms<=point.at AND h.end_ms>point.at
 AND (h.priority>r.priority OR h.priority=r.priority AND (h.start_ms<r.start_ms OR h.start_ms=r.start_ms AND h.id<r.id)))>=src.tuner_count)) ELSE 0 END)`
