package catalog

// SeparateFolder split retirement is bound to the original item and moved asset.
// Whole reparent never invokes this plan. Three direct prepared-media heads
// precede one raw preparation group. Each turn changes at most one claim row;
// a preparation is marked only after its grant, receipt and request heads drain.
// Guarded baseline triggers reopen marked debt for late producers. No claim or
// session pin is deleted, repointed, or revoked through a bare request target.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

const separateFolderRetirementVersion = "p2-59-o022-separate-folder-retirement-v2-asset"

var (
	// ErrSeparateFolderRetirementRetry reports a prepared job in the existing
	// cancelling state. The caller must roll this turn back, record a visible
	// retry/backoff on the actual command and try later. This walk never spins
	// and never fabricates a drained/complete outcome.
	ErrSeparateFolderRetirementRetry = errors.New("separate folder retirement: prepared job still cancelling")

	// errSeparateFolderRetirementHooks means a caller omitted a hook the units
	// need. The plan is refused before any candidate is read.
	errSeparateFolderRetirementHooks = errors.New("separate folder retirement: cancel, interrupt and receipt hooks are required")

	// errSeparateFolderRetirementHead means the exact point write did not match
	// the head this unit just read inside the same writer transaction.
	errSeparateFolderRetirementHead  = errors.New("separate folder retirement: head no longer names the expected row")
	errSeparateFolderRetirementScope = errors.New("separate folder retirement: original item and moved asset are required")
)

// SeparateFolderRetirementHooks are the two existing owner operations this
// bounded walk is allowed to invoke. CancelJob performs exactly the existing
// prepared-media cancelTx transition. Interrupt is the existing non-IO worker
// interrupt and runs only after a successful commit. RevokeReceipt is the
// existing receipt revocation, including its sequence allocation and feed.
type SeparateFolderRetirementHooks struct {
	CancelJob     func(context.Context, *sql.Tx, string) error
	Interrupt     func(string)
	RevokeReceipt func(context.Context, *sql.Tx, string) error
}

// Head, point-write and emptiness literals. The literal predicates are part of
// the plan digest and, for the partial indexes, are required for the planner to
// prove that the index applies. Binding state as a parameter would forfeit the
// partial-index seek; the negative-control test pins that difference.
const (
	separateFolderJobCancelHeadSQL = `SELECT id FROM prepared_media_jobs INDEXED BY separate_asset_jobs
 WHERE item_id=?1 AND asset_id=?2 AND state IN('queued','running') LIMIT 1`
	separateFolderJobCancelProbeSQL = `SELECT EXISTS(SELECT 1 FROM prepared_media_jobs INDEXED BY separate_asset_jobs
 WHERE item_id=?1 AND asset_id=?2 AND state IN('queued','running'))`
	separateFolderJobQuiesceHeadSQL = `SELECT id FROM prepared_media_jobs INDEXED BY separate_asset_jobs
 WHERE item_id=?1 AND asset_id=?2 AND state='cancelling' LIMIT 1`
	separateFolderJobQuiesceProbeSQL = `SELECT EXISTS(SELECT 1 FROM prepared_media_jobs INDEXED BY separate_asset_jobs
 WHERE item_id=?1 AND asset_id=?2 AND state='cancelling')`

	separateFolderVersionHeadSQL = `SELECT id FROM prepared_media_versions INDEXED BY separate_asset_versions
 WHERE item_id=?1 AND asset_id=?2 AND state='published' ORDER BY created_ms LIMIT 1`
	separateFolderVersionRetireSQL = `UPDATE prepared_media_versions SET state='deleting',revision=revision+1
 WHERE id=? AND item_id=? AND asset_id=? AND state='published'`
	separateFolderVersionProbeSQL = `SELECT EXISTS(SELECT 1 FROM prepared_media_versions INDEXED BY separate_asset_versions
 WHERE item_id=?1 AND asset_id=?2 AND state='published')`
)

type separateFolderRetirementKind int

const (
	retirementPoint separateFolderRetirementKind = iota
	retirementCancelJob
	retirementQuiesce
	retirementReceipt
)

type separateFolderRetirementArgs int

const (
	retirementArgsKey separateFolderRetirementArgs = iota
	retirementArgsNowAndKey
	retirementArgsRequestItem
)

type separateFolderRetirementUnit struct {
	phase     int
	name      string
	readBound int
	kind      separateFolderRetirementKind
	args      separateFolderRetirementArgs
	head      string
	update    string
	probe     string
}

var separateFolderRetirementUnits = [...]separateFolderRetirementUnit{
	{phase: 0, name: "prepared-job-cancel", readBound: 6, kind: retirementCancelJob,
		head: separateFolderJobCancelHeadSQL, probe: separateFolderJobCancelProbeSQL},
	{phase: 1, name: "prepared-job-quiesce", readBound: 3, kind: retirementQuiesce,
		head: separateFolderJobQuiesceHeadSQL, probe: separateFolderJobQuiesceProbeSQL},
	{phase: 2, name: "prepared-version-retire", readBound: 5, args: retirementArgsKey,
		head: separateFolderVersionHeadSQL, update: separateFolderVersionRetireSQL, probe: separateFolderVersionProbeSQL},
}

// SeparateFolderRetirementUnits compiles the asset-scoped units for one exact
// original item and the supplied owner hooks. The returned plan carries no
// state: the caller owns the durable catalog_purge_jobs cursor, its digest
// check and the phasedpurge.Owner custody rechecks.
func SeparateFolderRetirementUnits(item int64, hooks SeparateFolderRetirementHooks, asset string) ([]phasedpurge.Unit, error) {
	if item <= 0 || asset == "" {
		return nil, errSeparateFolderRetirementScope
	}
	if hooks.CancelJob == nil || hooks.Interrupt == nil || hooks.RevokeReceipt == nil {
		return nil, errSeparateFolderRetirementHooks
	}
	plan := make([]phasedpurge.Unit, 4)
	for i := 0; i < 3; i++ {
		u := &separateFolderRetirementUnits[i]
		plan[i] = phasedpurge.Unit{
			Phase:     u.phase,
			Name:      u.name,
			ReadBound: u.readBound,
			Run: func(ctx context.Context, tx *sql.Tx, _ []phasedpurge.Key) (phasedpurge.Outcome, error) {
				return u.run(ctx, tx, item, asset, hooks)
			},
			Empty: func(ctx context.Context, tx *sql.Tx) (bool, error) {
				return u.empty(ctx, tx, item, asset)
			},
		}
	}
	plan[3] = phasedpurge.Unit{Phase: 3, Name: "asset-preparation-drain", ReadBound: 14,
		Run: func(ctx context.Context, tx *sql.Tx, _ []phasedpurge.Key) (phasedpurge.Outcome, error) {
			return separateFolderPreparationTurn(ctx, tx, item, asset, hooks)
		},
		Empty: func(ctx context.Context, tx *sql.Tx) (bool, error) {
			var found bool
			err := tx.QueryRowContext(ctx, separateAssetPreparationProbe, item, asset).Scan(&found)
			return !found, err
		}}
	return plan, nil
}

func (u *separateFolderRetirementUnit) run(ctx context.Context, tx *sql.Tx, item int64, asset string, hooks SeparateFolderRetirementHooks) (phasedpurge.Outcome, error) {
	var first string
	var ordinal int64
	var err error
	if u.kind == retirementPoint && u.args == retirementArgsRequestItem {
		err = tx.QueryRowContext(ctx, u.head, item, asset).Scan(&first, &ordinal)
	} else {
		err = tx.QueryRowContext(ctx, u.head, item, asset).Scan(&first)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{Drained: true}, nil
	}
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("separate folder retirement %s head: %w", u.name, err)
	}
	switch u.kind {
	case retirementQuiesce:
		// The row exists and is in the existing cancelling state. No write here:
		// the caller records a retry and the worker's generation cleanup advances.
		return phasedpurge.Outcome{}, ErrSeparateFolderRetirementRetry
	case retirementCancelJob:
		if err = hooks.CancelJob(ctx, tx, first); err != nil {
			return phasedpurge.Outcome{}, fmt.Errorf("separate folder retirement %s: %w", u.name, err)
		}
		if err = dbwork.AfterCommit(tx, func() { hooks.Interrupt(first) }); err != nil {
			return phasedpurge.Outcome{}, fmt.Errorf("separate folder retirement %s interrupt: %w", u.name, err)
		}
		return phasedpurge.Outcome{Changed: true}, nil
	case retirementReceipt:
		if err = hooks.RevokeReceipt(ctx, tx, first); err != nil {
			return phasedpurge.Outcome{}, fmt.Errorf("separate folder retirement %s: %w", u.name, err)
		}
		return phasedpurge.Outcome{Changed: true}, nil
	}
	var result sql.Result
	now := time.Now().UnixMilli()
	switch u.args {
	case retirementArgsNowAndKey:
		result, err = tx.ExecContext(ctx, u.update, now, first)
	case retirementArgsRequestItem:
		result, err = tx.ExecContext(ctx, u.update, first, ordinal)
	default:
		result, err = tx.ExecContext(ctx, u.update, first, item, asset)
	}
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("separate folder retirement %s: %w", u.name, err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return phasedpurge.Outcome{}, errSeparateFolderRetirementHead
	}
	return phasedpurge.Outcome{Changed: true}, nil
}

func (u *separateFolderRetirementUnit) empty(ctx context.Context, tx *sql.Tx, item int64, asset string) (bool, error) {
	var found bool
	if err := tx.QueryRowContext(ctx, u.probe, item, asset).Scan(&found); err != nil {
		return false, fmt.Errorf("separate folder retirement %s probe: %w", u.name, err)
	}
	return !found, nil
}

// SeparateFolderRetirementPlanDigest is the deterministic identity of the
// compiled retirement for one exact item: every literal, unit phase/bound,
// argument shape, the item and the shared engine limits. The parent stores it
// with the cursor and refuses a mismatch plus malformed cursors visibly; it
// never reinterprets or resets them.
func SeparateFolderRetirementPlanDigest(item int64, asset string) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			fmt.Fprintf(h, "%d:%s|", len(part), part)
		}
		fmt.Fprint(h, "\n")
	}
	write("item", strconv.FormatInt(item, 10))
	write("asset", asset)
	write("preparation-group", separateAssetPreparationHead, separateAssetPreparationProbe, separateAssetGrantHead, separateAssetReceiptHead, separateAssetRequestHead, separateAssetMark, separateAssetGrantRetire, separateAssetRequestRetire, "phase3", "reads14")
	for i := range separateFolderRetirementUnits {
		u := &separateFolderRetirementUnits[i]
		write("unit", u.name, strconv.Itoa(u.phase), strconv.Itoa(u.readBound),
			strconv.Itoa(int(u.kind)), strconv.Itoa(int(u.args)), u.head, u.update, u.probe)
	}
	write("engine", strconv.Itoa(phasedpurge.EffectLimit), strconv.Itoa(phasedpurge.ReadLimit),
		strconv.Itoa(phasedpurge.CursorLimit))
	write("version", separateFolderRetirementVersion)
	return hex.EncodeToString(h.Sum(nil))
}

// Raw debt heads skip neither terminal preparations nor unmatched prefixes.
const separateAssetPreparationHead = `SELECT id FROM download_preparations INDEXED BY separate_asset_preparations WHERE item_id=?1 AND source_asset_id=?2 AND separate_retired=0 ORDER BY id LIMIT 1`
const separateAssetPreparationProbe = `SELECT EXISTS(SELECT 1 FROM download_preparations INDEXED BY separate_asset_preparations WHERE item_id=?1 AND source_asset_id=?2 AND separate_retired=0)`
const separateAssetGrantHead = `SELECT hash FROM download_grants INDEXED BY separate_preparation_grants WHERE preparation_id=?1 AND item_id=?2 AND revoked=0 ORDER BY hash LIMIT 1`
const separateAssetReceiptHead = `SELECT id FROM download_receipts INDEXED BY separate_preparation_receipts WHERE preparation_id=?1 AND item_id=?2 AND revoked_ms=0 ORDER BY id LIMIT 1`
const separateAssetRequestHead = `SELECT request_id,ordinal FROM download_request_items INDEXED BY separate_preparation_requests WHERE preparation_id=?1 AND item_id=?2 AND reason='' ORDER BY request_id,ordinal LIMIT 1`
const separateAssetMark = `UPDATE download_preparations SET separate_retired=1,
 state=CASE WHEN state IN('queued','running','ready','paused','failed') THEN 'unavailable' ELSE state END,
 reason=CASE WHEN state IN('queued','running','ready','paused','failed') THEN 'source_changed' ELSE reason END,
 hash_state=CASE WHEN state IN('queued','running','ready','paused','failed') THEN NULL ELSE hash_state END,
 revision=revision+CASE WHEN state IN('queued','running','ready','paused','failed') THEN 1 ELSE 0 END,
 updated_ms=CASE WHEN state IN('queued','running','ready','paused','failed') THEN ?4 ELSE updated_ms END
 WHERE id=?1 AND item_id=?2 AND source_asset_id=?3 AND separate_retired=0`

func separateFolderPreparationTurn(ctx context.Context, tx *sql.Tx, item int64, asset string, hooks SeparateFolderRetirementHooks) (phasedpurge.Outcome, error) {
	var prep, key string
	var ordinal int64
	err := tx.QueryRowContext(ctx, separateAssetPreparationHead, item, asset).Scan(&prep)
	if errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{Drained: true}, nil
	}
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	err = tx.QueryRowContext(ctx, separateAssetGrantHead, prep, item).Scan(&key)
	if err == nil {
		_, err = tx.ExecContext(ctx, separateAssetGrantRetire, key, prep, item)
		return phasedpurge.Outcome{Changed: true}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{}, err
	}
	err = tx.QueryRowContext(ctx, separateAssetReceiptHead, prep, item).Scan(&key)
	if err == nil {
		err = hooks.RevokeReceipt(ctx, tx, key)
		return phasedpurge.Outcome{Changed: true}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{}, err
	}
	err = tx.QueryRowContext(ctx, separateAssetRequestHead, prep, item).Scan(&key, &ordinal)
	if err == nil {
		_, err = tx.ExecContext(ctx, separateAssetRequestRetire, key, ordinal, prep, item)
		return phasedpurge.Outcome{Changed: true}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{}, err
	}
	_, err = tx.ExecContext(ctx, separateAssetMark, prep, item, asset, time.Now().UnixMilli())
	return phasedpurge.Outcome{Changed: true}, err
}

const separateAssetGrantRetire = `UPDATE download_grants SET revoked=1 WHERE hash=? AND preparation_id=? AND item_id=? AND revoked=0`
const separateAssetRequestRetire = `UPDATE download_request_items SET reason='source_changed' WHERE request_id=? AND ordinal=? AND preparation_id=? AND item_id=? AND reason=''`
