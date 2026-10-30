package catalog

// P2-59 / O-022 SeparateFolder retirement units.
//
// A SeparateFolder command publishes each original child's files without
// moving the download/prepared-media claims that belonged to the original
// title. This file owns only the bounded retirement of those claims: eight
// literal units executed by the shared phasedpurge engine inside the parent's
// existing catalog_entity_mutations writer. It adds no schema, no second
// command and no cursor store of its own.
//
// Order is part of the contract:
//
//  1. prepared-job-cancel    first queued/running prepared job for the item,
//     through the existing cancelTx semantics; the exact id is handed to the
//     existing post-commit Interrupt hook via dbwork.AfterCommit.
//  2. prepared-job-quiesce   a cancelling job is not retired here. This unit
//     makes no write and returns ErrSeparateFolderRetirementRetry so the
//     caller backs off while the existing worker generation cleanup finishes.
//     It never spins and never claims drained.
//  3. prepared-version-retire first published version becomes the existing
//     "deleting" state. Session pins and the artifact collector are untouched.
//  4. download-grant-retire  first unrevoked grant becomes revoked. No cascade.
//  5. download-receipt-retire first unrevoked receipt is revoked through the
//     supplied hook, which keeps the existing sequence allocation and
//     revocation feed. This file duplicates neither algorithm.
//  6. download-preparation-retire first live preparation becomes
//     unavailable/source_changed. Grants were drained by unit 4, so this point
//     update deliberately does not repeat endClaim's bulk grant update.
//  7. request-item-retire    first still-live request item becomes
//     source_changed, preserving the request, ordinal and real summary.
//  8. request-target-retire  first capturing request whose target_id is the
//     original item becomes failed/source_changed. Its child rows are the
//     separately retired items, never a bulk CASCADE.
//
// Every unit reads one raw indexed head with LIMIT 1, writes that exact PK and
// has a matching bounded emptiness probe. The changed row no longer matches the
// literal head, so a restart continues at the next head without a rejected
// prefix. None of the units deletes a row, re-points a claim, touches the
// parent/sibling title, or creates a new claim.
//
// The parent (root) supplies phasedpurge.Owner. Its Before rechecks the actual
// catalog_entity_mutations generation, public id, library, family and the
// original subject PK every turn; Save/Finalize persist the one existing
// catalog_purge_jobs cursor bound to SeparateFolderRetirementPlanDigest(item).
// The hooks are the only route to prepared-media cancellation and receipt
// revocation: this file imports neither package.

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

const separateFolderRetirementVersion = "p2-59-o022-separate-folder-retirement-v1"

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
	errSeparateFolderRetirementHead = errors.New("separate folder retirement: head no longer names the expected row")
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
	separateFolderJobCancelHeadSQL = `SELECT id FROM prepared_media_jobs INDEXED BY prepared_media_retirement_jobs
 WHERE item_id=? AND state IN('queued','running') LIMIT 1`
	separateFolderJobCancelProbeSQL = `SELECT EXISTS(SELECT 1 FROM prepared_media_jobs INDEXED BY prepared_media_retirement_jobs
 WHERE item_id=? AND state IN('queued','running'))`
	separateFolderJobQuiesceHeadSQL = `SELECT id FROM prepared_media_jobs INDEXED BY prepared_media_retirement_jobs
 WHERE item_id=? AND state='cancelling' LIMIT 1`
	separateFolderJobQuiesceProbeSQL = `SELECT EXISTS(SELECT 1 FROM prepared_media_jobs INDEXED BY prepared_media_retirement_jobs
 WHERE item_id=? AND state='cancelling')`

	separateFolderVersionHeadSQL = `SELECT id FROM prepared_media_versions INDEXED BY prepared_media_item_versions
 WHERE item_id=? AND state='published' ORDER BY created_ms LIMIT 1`
	separateFolderVersionRetireSQL = `UPDATE prepared_media_versions SET state='deleting',revision=revision+1
 WHERE id=? AND state='published'`
	separateFolderVersionProbeSQL = `SELECT EXISTS(SELECT 1 FROM prepared_media_versions INDEXED BY prepared_media_item_versions
 WHERE item_id=? AND state='published')`

	separateFolderGrantHeadSQL = `SELECT hash FROM download_grants INDEXED BY download_grants_retirement
 WHERE item_id=? AND revoked=0 ORDER BY hash LIMIT 1`
	separateFolderGrantRetireSQL = `UPDATE download_grants SET revoked=1 WHERE hash=? AND revoked=0`
	separateFolderGrantProbeSQL  = `SELECT EXISTS(SELECT 1 FROM download_grants INDEXED BY download_grants_retirement
 WHERE item_id=? AND revoked=0)`

	separateFolderReceiptHeadSQL = `SELECT id FROM download_receipts INDEXED BY download_receipts_retirement
 WHERE item_id=? AND revoked_ms=0 ORDER BY id LIMIT 1`
	separateFolderReceiptProbeSQL = `SELECT EXISTS(SELECT 1 FROM download_receipts INDEXED BY download_receipts_retirement
 WHERE item_id=? AND revoked_ms=0)`

	separateFolderPreparationHeadSQL = `SELECT id FROM download_preparations INDEXED BY download_preparations_retirement
 WHERE item_id=? AND state IN('queued','running','ready','paused','failed') LIMIT 1`
	// This is the existing endClaim state/reason/hash/revision transition as a
	// point write. endClaim's unbounded UPDATE download_grants is intentionally
	// absent: unit 4 already drained grants for this exact item.
	separateFolderPreparationRetireSQL = `UPDATE download_preparations
 SET state='unavailable',reason='source_changed',hash_state=NULL,revision=revision+1,updated_ms=?
 WHERE id=? AND state IN('queued','running','ready','paused','failed')`
	separateFolderPreparationProbeSQL = `SELECT EXISTS(SELECT 1 FROM download_preparations INDEXED BY download_preparations_retirement
 WHERE item_id=? AND state IN('queued','running','ready','paused','failed'))`

	separateFolderRequestItemHeadSQL = `SELECT request_id,ordinal FROM download_request_items INDEXED BY download_request_items_retirement
 WHERE item_id=? AND reason='' ORDER BY request_id,ordinal LIMIT 1`
	separateFolderRequestItemRetireSQL = `UPDATE download_request_items SET reason='source_changed'
 WHERE request_id=? AND ordinal=? AND reason=''`
	separateFolderRequestItemProbeSQL = `SELECT EXISTS(SELECT 1 FROM download_request_items INDEXED BY download_request_items_retirement
 WHERE item_id=? AND reason='')`

	separateFolderRequestTargetHeadSQL = `SELECT id FROM download_requests INDEXED BY download_requests_retirement
 WHERE target_id=? AND state='capturing' ORDER BY id LIMIT 1`
	separateFolderRequestTargetRetireSQL = `UPDATE download_requests SET state='failed',error_code='source_changed',updated_ms=?
 WHERE id=? AND state='capturing'`
	separateFolderRequestTargetProbeSQL = `SELECT EXISTS(SELECT 1 FROM download_requests INDEXED BY download_requests_retirement
 WHERE target_id=? AND state='capturing')`
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
	{phase: 3, name: "download-grant-retire", readBound: 5, args: retirementArgsKey,
		head: separateFolderGrantHeadSQL, update: separateFolderGrantRetireSQL, probe: separateFolderGrantProbeSQL},
	{phase: 4, name: "download-receipt-retire", readBound: 8, kind: retirementReceipt,
		head: separateFolderReceiptHeadSQL, probe: separateFolderReceiptProbeSQL},
	{phase: 5, name: "download-preparation-retire", readBound: 6, args: retirementArgsNowAndKey,
		head: separateFolderPreparationHeadSQL, update: separateFolderPreparationRetireSQL, probe: separateFolderPreparationProbeSQL},
	{phase: 6, name: "request-item-retire", readBound: 5, args: retirementArgsRequestItem,
		head: separateFolderRequestItemHeadSQL, update: separateFolderRequestItemRetireSQL, probe: separateFolderRequestItemProbeSQL},
	{phase: 7, name: "request-target-retire", readBound: 5, args: retirementArgsNowAndKey,
		head: separateFolderRequestTargetHeadSQL, update: separateFolderRequestTargetRetireSQL, probe: separateFolderRequestTargetProbeSQL},
}

// SeparateFolderRetirementUnits compiles the eight literal units for one exact
// original item and the supplied owner hooks. The returned plan carries no
// state: the caller owns the durable catalog_purge_jobs cursor, its digest
// check and the phasedpurge.Owner custody rechecks.
func SeparateFolderRetirementUnits(item int64, hooks SeparateFolderRetirementHooks) ([]phasedpurge.Unit, error) {
	if item <= 0 || hooks.CancelJob == nil || hooks.Interrupt == nil || hooks.RevokeReceipt == nil {
		return nil, errSeparateFolderRetirementHooks
	}
	plan := make([]phasedpurge.Unit, len(separateFolderRetirementUnits))
	for i := range separateFolderRetirementUnits {
		u := &separateFolderRetirementUnits[i]
		plan[i] = phasedpurge.Unit{
			Phase:     u.phase,
			Name:      u.name,
			ReadBound: u.readBound,
			Run: func(ctx context.Context, tx *sql.Tx, _ []phasedpurge.Key) (phasedpurge.Outcome, error) {
				return u.run(ctx, tx, item, hooks)
			},
			Empty: func(ctx context.Context, tx *sql.Tx) (bool, error) {
				return u.empty(ctx, tx, item)
			},
		}
	}
	return plan, nil
}

func (u *separateFolderRetirementUnit) run(ctx context.Context, tx *sql.Tx, item int64, hooks SeparateFolderRetirementHooks) (phasedpurge.Outcome, error) {
	var first string
	var ordinal int64
	var err error
	if u.kind == retirementPoint && u.args == retirementArgsRequestItem {
		err = tx.QueryRowContext(ctx, u.head, item).Scan(&first, &ordinal)
	} else {
		err = tx.QueryRowContext(ctx, u.head, item).Scan(&first)
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
		result, err = tx.ExecContext(ctx, u.update, first)
	}
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("separate folder retirement %s: %w", u.name, err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return phasedpurge.Outcome{}, errSeparateFolderRetirementHead
	}
	return phasedpurge.Outcome{Changed: true}, nil
}

func (u *separateFolderRetirementUnit) empty(ctx context.Context, tx *sql.Tx, item int64) (bool, error) {
	var found bool
	if err := tx.QueryRowContext(ctx, u.probe, item).Scan(&found); err != nil {
		return false, fmt.Errorf("separate folder retirement %s probe: %w", u.name, err)
	}
	return !found, nil
}

// SeparateFolderRetirementPlanDigest is the deterministic identity of the
// compiled retirement for one exact item: every literal, unit phase/bound,
// argument shape, the item and the shared engine limits. The parent stores it
// with the cursor and refuses a mismatch plus malformed cursors visibly; it
// never reinterprets or resets them.
func SeparateFolderRetirementPlanDigest(item int64) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			fmt.Fprintf(h, "%d:%s|", len(part), part)
		}
		fmt.Fprint(h, "\n")
	}
	write("item", strconv.FormatInt(item, 10))
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
