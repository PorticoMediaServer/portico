package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// O-101 read batching. Capture and every normalize sweep used to run one
// multi-join point statement per raw object; a bounded window now runs one
// set-based statement and the per-row Go work stays exactly as it was.
//
// reading_publications.asset_id is UNIQUE, so one raw object still hydrates to
// at most one row: batching repeats the same index seeks, it does not join
// more. Nothing here is stored between steps: the map lives for one quantum.
//
// Two independent bounds apply to one window: the RAW count (<=128) and the
// ordinary scalar BYTES a single hydrate may retain (facetScalarBudget, the
// same 256KiB stop facet scalars already use). The byte stop is a source
// workload bound only: no schema, accepted data or cap changes. Because a
// window may now stop before its last id, every batched caller consumes a RAW
// PREFIX of its input: ids inside the prefix that hydrate to nothing are still
// consumed (they are the point read's sql.ErrNoRows), while ids past the prefix
// were never read, are never reported as missing and stay queued for the next
// step.

// pathObjectBatchMax bounds one hydrated window. It equals the Reading raw
// budget, so a step never asks for more objects than it could consume.
const pathObjectBatchMax = readingPathBatch

// pathObjectByteBudget is the ordinary scalar bytes one hydrated window may
// retain. It reuses the existing facet scalar stop rather than introducing a
// new cap.
const pathObjectByteBudget = facetScalarBudget

// errPathObjectProgress guards the capped read's only invariant: a non-empty id
// window always admits at least its first hydrated object, so callers always
// advance. Reaching it would be a bug, never a workload condition.
var errPathObjectProgress = errors.New("reading path byte-bounded hydration consumed no id")

// bytes is the ordinary scalar bytes one hydrated object retains. Fixed-width
// columns are excluded: only stored strings can grow with source data.
func (v pathObject) bytes() int {
	return len(v.ID) + len(v.Source) + len(v.Incarnation) + len(v.Relative) + len(v.Revision) +
		len(v.Root) + len(v.RootIdentity) + len(v.Actual) + len(v.LibraryKind) + len(v.Format)
}

// pathObjectBatch is pathObjectColumns over a json_each id list, carrying the
// list ordinal as its leading column. json_each is the leading table (CROSS
// JOIN) so the planner seeks inventory_objects by id for each value instead of
// scanning it. SQLite's json_each array xNext visits ordinals in array order;
// the forced outer loop and unique point joins preserve that order. Do not sort
// hydrated rows: a sorter would materialize every string before the byte stop.
// The ordinal guard below makes this execution-order requirement explicit.
const pathObjectBatch = `SELECT j.key,` + pathObjectColumns + ` FROM json_each(?) j CROSS JOIN inventory_objects o ON o.id=j.value` + pathObjectJoins + ` WHERE o.retired=0`

// scanPathObjectRow scans one batch row: its json_each ordinal and the object.
func scanPathObjectRow(rows *sql.Rows) (int, pathObject, error) {
	var ordinal int
	var v pathObject
	if err := rows.Scan(append([]any{&ordinal}, v.scan()...)...); err != nil {
		return 0, v, err
	}
	v.finish()
	return ordinal, v, nil
}

// readPathObjectsCapped hydrates ids in caller order and returns the RAW PREFIX
// length it consumed. The map holds every object hydrated from ids[:n] and
// nothing from ids[n:].
//
// The stop is decided per hydrated object: a window never adds an object that
// would take its ordinary scalar bytes past pathObjectByteBudget, except for the
// first hydrated object of a window, which is admitted alone so one legal
// oversized pathObject scalar costs O(B) once instead of stalling the walk.
//
// n counts raw ids, not hydrated rows: ids inside the prefix whose joins are
// absent are consumed exactly like the point read's sql.ErrNoRows. Callers must
// only advance a cursor or acknowledge work for ids[:n] and must never treat an
// id past n as missing.
func readPathObjectsCapped(ctx context.Context, tx *sql.Tx, ids []string) (map[string]pathObject, int, error) {
	out := make(map[string]pathObject, min(len(ids), pathObjectBatchMax))
	bytes := 0
	for start := 0; start < len(ids); start += pathObjectBatchMax {
		// The budget is spent: stop before reading the next id so nothing past
		// the returned prefix is ever mistaken for absent.
		if bytes >= pathObjectByteBudget {
			return out, start, nil
		}
		chunk := ids[start:min(start+pathObjectBatchMax, len(ids))]
		if len(chunk) == 1 {
			v, err := readPathObject(ctx, tx, chunk[0])
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, 0, err
			}
			if _, seen := out[v.ID]; seen {
				continue
			}
			if len(out) > 0 && bytes+v.bytes() > pathObjectByteBudget {
				return out, start, nil
			}
			out[v.ID] = v
			bytes += v.bytes()
			continue
		}
		listed, err := json.Marshal(chunk)
		if err != nil {
			return nil, 0, err
		}
		rows, err := tx.QueryContext(ctx, pathObjectBatch, string(listed))
		if err != nil {
			return nil, 0, err
		}
		consumed := len(chunk)
		lastOrdinal := -1
		for rows.Next() {
			ordinal, v, err := scanPathObjectRow(rows)
			if err != nil {
				rows.Close()
				return nil, 0, err
			}
			if ordinal <= lastOrdinal || ordinal >= len(chunk) {
				rows.Close()
				return nil, 0, errors.New("reading hydration lost raw ordinal order")
			}
			lastOrdinal = ordinal
			if _, seen := out[v.ID]; seen {
				continue
			}
			if len(out) > 0 && bytes+v.bytes() > pathObjectByteBudget {
				// Stop BEFORE this object: the row's ordinal is exactly the
				// number of raw ids in front of it.
				consumed = ordinal
				break
			}
			out[v.ID] = v
			bytes += v.bytes()
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, 0, err
		}
		if consumed < len(chunk) {
			return out, start + consumed, nil
		}
	}
	return out, len(ids), nil
}

// normalizePathObjectBatch normalizes one already-hydrated raw prefix. The
// write order, per-object body and returned errors are those of
// ReadingNormalizeObjectTx called once per id.
func normalizePathObjectBatch(ctx context.Context, tx *sql.Tx, ids []string, objects map[string]pathObject) error {
	for _, id := range ids {
		v, found := objects[id]
		if err := normalizePathObjectTx(ctx, tx, id, v, found); err != nil {
			return err
		}
	}
	return nil
}

// normalizePathObjects normalizes a bounded id window in byte-bounded chunks.
// A window larger than the byte budget repeats the remaining prefix, so each
// hydrate retains at most one budget and no raw id is skipped.
func normalizePathObjects(ctx context.Context, tx *sql.Tx, ids []string) error {
	for start := 0; start < len(ids); {
		chunk := ids[start:min(start+pathObjectBatchMax, len(ids))]
		objects, n, err := readPathObjectsCapped(ctx, tx, chunk)
		if err != nil {
			return err
		}
		if n <= 0 {
			return errPathObjectProgress
		}
		if err = normalizePathObjectBatch(ctx, tx, chunk[:n], objects); err != nil {
			return err
		}
		start += n
	}
	return nil
}

// pathObjectWorkStep drains consecutive 'object' work rows that share the head
// row's served stamp. Under the queue's ORDER BY served,kind,work_key they are
// exactly the next rows, so batching them records the same sequence as one-row
// steps. Each row keeps its own requested/captured guard: a row requested again
// while captured stays queued and is re-read on the next claim.
//
// The byte-bounded hydrate may consume only a prefix of the group. Only that
// prefix is normalized and acknowledged: a key past the prefix was never read,
// so it must stay queued rather than be deleted as if its object were missing.
func pathObjectWorkStep(ctx context.Context, tx *sql.Tx, w pathWork, limit int, turn int64) (int, error) {
	peers, err := pathObjectPeers(ctx, tx, w, limit-1)
	if err != nil {
		return 0, err
	}
	group := append([]pathWork{w}, peers...)
	ids := make([]string, len(group))
	for i := range group {
		ids[i] = group[i].Key
	}
	objects, n, err := readPathObjectsCapped(ctx, tx, ids)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, errPathObjectProgress
	}
	if err = normalizePathObjectBatch(ctx, tx, ids[:n], objects); err != nil {
		return 0, err
	}
	group = group[:n]
	var retire []int64
	seen := map[int64]bool{}
	for i := range group {
		r := group[i]
		if r.Requested != r.Captured {
			// Same re-arm as a one-row step: zero the claim and keep the row.
			r.Captured = 0
			if _, err = tx.ExecContext(ctx, `UPDATE reading_path_dirty_work SET captured=?,after_object=?,after_asset=?,after_scope=?,phase=?,served=?,fence_object=?,fence_asset=?,fence_scope=? WHERE library_id=? AND kind=? AND work_key=?`, r.Captured, r.Object, r.Asset, r.Scope, r.Phase, turn, r.FenceObject, r.FenceAsset, r.FenceScope, r.Library, r.Kind, r.Key); err != nil {
				return 0, err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM reading_path_dirty_work WHERE library_id=? AND kind=? AND work_key=? AND requested=?`, r.Library, r.Kind, r.Key, r.Captured); err != nil {
			return 0, err
		}
		if !seen[r.Library] {
			seen[r.Library] = true
			retire = append(retire, r.Library)
		}
	}
	for _, library := range retire {
		if err = pathRetireInput(ctx, tx, library); err != nil {
			return 0, err
		}
	}
	return max(1, n), nil
}

// pathObjectPeers reads the rest of the head row's object batch, in the queue's
// own served,kind,work_key order. Each row gets the same claim-time
// normalization the head row already had: a fresh capture takes the current
// request count, and object rows carry no cursor or fence.
func pathObjectPeers(ctx context.Context, tx *sql.Tx, w pathWork, limit int) ([]pathWork, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT library_id,work_key,requested,captured,after_object,after_asset,after_scope,phase,fence_object,fence_asset,fence_scope FROM reading_path_dirty_work WHERE kind='object' AND served=? AND (library_id<>? OR work_key<>?) ORDER BY served,kind,work_key LIMIT ?`, w.Served, w.Library, w.Key, limit)
	if err != nil {
		return nil, err
	}
	var out []pathWork
	for rows.Next() {
		r := pathWork{Kind: "object", Served: w.Served}
		if err = rows.Scan(&r.Library, &r.Key, &r.Requested, &r.Captured, &r.Object, &r.Asset, &r.Scope, &r.Phase, &r.FenceObject, &r.FenceAsset, &r.FenceScope); err != nil {
			rows.Close()
			return nil, err
		}
		if r.Captured == 0 {
			r.Captured = r.Requested
			r.Object = ""
			r.Asset = 0
			r.Scope = 0
			r.Phase = 0
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	return out, err
}
