package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// MP's three durable heads select their business row only after a same-snapshot
// octet preflight. The pin below is a fixed-width source comparison: it never
// projects free text, so a wide head is not materialized merely to prove the
// publication source. QueryExactBytes repeats the point lengths inside the
// admitted business snapshot before any variable value is decoded.
//
// These are compiled statement names and columns, never caller SQL, a grant or
// an independent resource ledger.
const mpQueueSourceSQL = `SELECT q.revision,q.custody_generation,q.current_position,q.shuffle_on,q.shuffle_seed,q.shuffle_lap,q.shuffle_domain,q.shuffle_first,q.automatic_advances,q.created_ms,q.updated_ms FROM queues_v1 AS q INDEXED BY sqlite_autoindex_queues_v1_1
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
WHERE q.id=? AND t.retired=0 AND t.generation=f.current_generation
AND f.revoked=0 AND f.pin_gate=0 AND f.authority='local'
AND q.owner_kind='device' AND q.owner_id=f.device_id AND q.authority=f.authority AND q.account_id=f.account_id AND q.profile_id=f.profile_id`

const mpQueueOctetsSQL = `SELECT octet_length(q.id),octet_length(q.current_entry_id),octet_length(q.repeat),octet_length(q.session_id),octet_length(q.owner_kind),octet_length(q.owner_id),octet_length(q.authority),octet_length(q.account_id),octet_length(q.profile_id),octet_length(f.id),octet_length(f.device_id) FROM queues_v1 AS q INDEXED BY sqlite_autoindex_queues_v1_1
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
WHERE q.id=? AND t.retired=0 AND t.generation=f.current_generation
AND f.revoked=0 AND f.pin_gate=0 AND f.authority='local'
AND q.owner_kind='device' AND q.owner_id=f.device_id AND q.authority=f.authority AND q.account_id=f.account_id AND q.profile_id=f.profile_id`

const mpSessionSourceSQL = `SELECT s.part_index,s.revision,s.generation,s.media_generation,s.position_ms,s.start_position_ms,s.last_seq,s.lease_expires_ms,s.created_ms,s.updated_ms,s.ended_ms,e.id,e.retired,e.revision,e.admission_own_age,e.library_id,e.kind FROM playback_v1_sessions AS s INDEXED BY sqlite_autoindex_playback_v1_sessions_1
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN catalog_entities AS e ON e.id=s.item_id
WHERE s.id=? AND s.item_id=? AND e.library_id=? AND e.kind=? AND e.retired=0
AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0 AND f.pin_gate=0 AND f.authority='local'
AND s.device_id=f.device_id AND s.account_id=f.account_id AND s.profile_id=f.profile_id AND s.authority=f.authority
AND s.role='local' AND s.kind IN('vod','audio') AND s.group_id='' AND s.transfer_id='' AND s.channel_id=''`

const mpSessionOctetsSQL = `SELECT octet_length(s.id),octet_length(s.kind),octet_length(s.role),octet_length(s.state),octet_length(s.version_id),octet_length(s.queue_id),octet_length(s.entry_id),octet_length(s.media_session_id),octet_length(s.end_reason),octet_length(s.device_id),octet_length(f.id),octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(s.account_id),octet_length(s.profile_id),octet_length(s.authority) FROM playback_v1_sessions AS s INDEXED BY sqlite_autoindex_playback_v1_sessions_1
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN catalog_entities AS e ON e.id=s.item_id
WHERE s.id=? AND s.item_id=? AND e.library_id=? AND e.kind=? AND e.retired=0
AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0 AND f.pin_gate=0 AND f.authority='local'
AND s.device_id=f.device_id AND s.account_id=f.account_id AND s.profile_id=f.profile_id AND s.authority=f.authority
AND s.role='local' AND s.kind IN('vod','audio') AND s.group_id='' AND s.transfer_id='' AND s.channel_id=''`

const mpPreparedSourceSQL = `SELECT j.generation,j.cleaned_generation,j.bytes,j.revision,j.created_ms,j.updated_ms,j.job_lease_owner,j.job_lease_generation,j.job_lease_expires_ms,e.id,e.retired,e.revision,e.admission_own_age,e.library_id FROM prepared_media_jobs AS j INDEXED BY sqlite_autoindex_prepared_media_jobs_1
CROSS JOIN catalog_entities AS e ON e.id=j.item_id
WHERE j.id=? AND j.item_id=? AND e.library_id=? AND e.kind=? AND j.library_id=? AND e.retired=0`

const mpPreparedOctetsSQL = `SELECT octet_length(j.id),octet_length(j.state),octet_length(j.phase),octet_length(j.error_code),octet_length(j.version_id),octet_length(j.asset_id),octet_length(j.target_id),octet_length(j.profile_id),octet_length(j.source_revision),octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(j.library_id) FROM prepared_media_jobs AS j INDEXED BY sqlite_autoindex_prepared_media_jobs_1
CROSS JOIN catalog_entities AS e ON e.id=j.item_id
WHERE j.id=? AND j.item_id=? AND e.library_id=? AND e.kind=? AND j.library_id=? AND e.retired=0`

type mpBytePoint struct {
	lengths string
	columns []int
}

func registerMPBytePoint(payload string, p mpBytePoint) {
	if payload == "" || p.lengths == "" || len(p.columns) == 0 {
		panic("incomplete MP exact-byte point")
	}
	seen := make(map[int]bool, len(p.columns))
	for _, c := range p.columns {
		if c < 0 || c >= 128 || seen[c] {
			panic("invalid MP byte projection")
		}
		seen[c] = true
	}
	// Reuse the package's closed point dispatch. The RS-named helper is the
	// existing compiled registration entry point, not an RS-only capability;
	// each MP payload still owns its lengths definition and Capture receipt.
	registerRSBytePoint(payload, rsBytePoint{lengths: p.lengths, columns: append([]int(nil), p.columns...)})
}

func init() {
	registerMPBytePoint("foreground.mp.queue_head", mpBytePoint{lengths: "foreground.mp.queue_head_octets", columns: []int{0, 3, 5, 11, 15, 16, 17, 18, 19, 20, 21}})
	registerMPBytePoint("foreground.mp.session_head", mpBytePoint{lengths: "foreground.mp.session_head_octets", columns: []int{0, 1, 2, 3, 4, 5, 6, 7, 19, 20, 21, 23, 24, 25, 29, 30, 31}})
	registerMPBytePoint("foreground.mp.prepared_job", mpBytePoint{lengths: "foreground.mp.prepared_job_octets", columns: []int{0, 1, 2, 6, 10, 11, 12, 13, 14, 16, 17, 18, 22}})
}

// mpByteWindow selects an actual-byte forecast for the business window after a
// fixed point preflight measured the selected lengths in the initial snapshot.
// It adds no grant, ledger, queue, pool or lifecycle authority.
type mpByteWindow struct {
	source      browseSnapshots
	pending     bool
	bytes       int64
	calls       uint16
	goCopyBytes int64
}

func (w *mpByteWindow) NativeRowReservation() int64 { return w.source.NativeRowReservation() }

func (w *mpByteWindow) arm(bytes int64, calls uint16) error {
	if bytes <= 0 || bytes > w.source.NativeRowReservation() || calls == 0 {
		return governor.ErrSnapshotBudget
	}
	w.bytes, w.calls, w.pending = bytes, calls, true
	return nil
}

func (w *mpByteWindow) Read(ctx context.Context, r governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	if !w.pending {
		return w.source.Read(ctx, r, budget, read)
	}
	w.pending = false
	source, ok := w.source.(rsForecastSource)
	if !ok {
		return governor.SnapshotResult{}, ErrInput
	}
	result, err := source.ReadForecasted(ctx, r, budget, governor.SnapshotForecast{NativeRowBytes: w.bytes, MaxQueries: w.calls, GoCopyBytes: w.goCopyBytes}, read)
	if err != nil || result.Refusal != nil {
		w.pending = false
	}
	return result, err
}

func mpPointBytes(row []any) (int64, error) {
	var total int64
	for _, v := range row {
		if v == nil {
			continue
		}
		n, ok := v.(int64)
		if !ok || n < 0 || n > (1<<62)-total {
			return 0, governor.ErrSnapshotBudget
		}
		total += n
	}
	return total, nil
}

// mpByteGrowth separates a same-snapshot length change from an inherently
// oversized selection. Only the former is retryable: the caller re-measures in
// a fresh first window. The latter remains a terminal budget refusal.
func mpByteGrowth(w *mpByteWindow, q browseQuery, lengths string, args ...any) error {
	if w.bytes <= 0 {
		return governor.ErrSnapshotBudget
	}
	sizes, err := q(lengths, args...)
	if err != nil {
		return err
	}
	if len(sizes) != 1 {
		return ErrDenied
	}
	now, err := mpPointBytes(sizes[0])
	if err != nil {
		return err
	}
	if now <= w.NativeRowReservation() && now != w.bytes {
		return ErrStale
	}
	return governor.ErrSnapshotBudget
}
