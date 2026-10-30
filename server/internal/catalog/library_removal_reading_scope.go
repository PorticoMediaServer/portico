package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

// Private Reading-scope teardown for one retired library lifetime.
//
// The Reading dispatcher only reclaims scopes it can claim. A scope that is
// queued=0, parked=1, or behind domain28 work is never selected, so removing a
// library can leave its private scope, builds, captures, representatives,
// stage rows, object keys and scope entities behind forever. This file drains
// exactly ONE caller-supplied scope through the existing phasedpurge executor
// and nothing else. It is called per page in the caller's writer transaction:
// it never opens a transaction, never chooses durability, and never consults
// dispatch/admission state.
//
// The supplied library handle is the captured `catalog_libraries.id` of the
// retired library. Every page re-proves, in the SAME writer transaction as its
// deletions, that the scope still names that handle and that the handle is
// still retired. A restored library has a different handle; a deleted handle
// is never reused (AUTOINCREMENT), so neither can inherit this teardown.
// Terminal cleanup after `libraries`/`catalog_libraries` itself is gone is NOT
// claimed here; the library/source removal owner keeps that responsibility,
// together with `reading_path_inputs`, dirty work, semantic/availability
// pending rows, barriers, blocks and every public/derived projection.
//
// Literal order, one selected raw row per turn:
//
//  1. detach the singleton slot only when a column names a build of this
//     supplied scope; build_id and overlay_id are cleared independently, so a
//     column that names another library's build is preserved;
//  2. capture, then representative, then stage: one row at a time, selected by
//     the full leading primary-key prefix of that build. Limiting parent build
//     ids is never used as a child bound;
//  3. the build row, only after all three child probes are empty;
//  4. if another build of the scope remains, rewind to step 1 and drain that
//     build completely before touching the next build's children;
//  5. object keys BEFORE scope entities (the key-delete trigger maintains the
//     entity counters), then the scope row only after builds, keys and
//     entities are all empty.
//
// Every Empty probe is a point access: scope PK, build PK, capture/
// representative/stage leading-PK prefix, reading_path_build_scope prefix,
// reading_path_object_scope prefix, scope_entity PK prefix, or the singleton
// slot. No probe joins the whole library, and no unit reads a sibling
// library's rows. Slot detach is scope-gated per column, so another library's
// published build is never detached or cleared.
//
// Costs. The owner reserves five reads: the two total_changes() meter reads,
// the lifetime probe (scope PK plus catalog handle), and one build pick from
// reading_path_build_scope. Across the whole plan the units declare twenty
// more, so owner+plan is 25 of the shared 128; one page runs at most four
// units, and a changed unit ends the page immediately. Effects are charged by
// SQLite's total_changes(), which includes triggers and FK actions: slot
// detach 1; each capture/representative/stage/build row 1; each object key up
// to 6 (delete, scope desired/queued, entity objects, entity delete + pending,
// parked unpark); each explicit scope-entity row up to 3; scope delete 1. The
// owner's Save writes one row (1). The worst declared page is therefore 7 of
// the shared 24 effects. No unit may exceed these declarations without the
// plan digest changing.
//
// Cursor. The owner stores the same envelope as the profile purge: the caller's
// durable string is `{d:<plan digest>,c:<phasedpurge.Cursor>}`. EncodeCursor and
// DecodeCursor remain the shared engine's only cursor format; there is no
// second cursor type or second deletion framework. A cursor written by another
// plan, or a cursor that carries a row key this unit-only plan never persists,
// is refused before any unit runs. Save is called in the same transaction as
// the effect.

const (
	// readingScopePurgePlanVersion changes whenever a builder, predicate or
	// bound semantic changes. It is mixed into the digest with the literal SQL.
	readingScopePurgePlanVersion = "p2-59-reading-scope-purge-v2"

	// readingScopePurgeOwnerReadBound: two total_changes() reads, the lifetime
	// point probe (scope + catalog handle), and the build pick.
	readingScopePurgeOwnerReadBound = 5
	// readingScopePurgeOwnerEffects is the caller's Save row. The engine charges
	// it in the same total_changes() delta as the unit's effects.
	readingScopePurgeOwnerEffects = 1
)

var (
	// ErrReadingScopePurgePlan means the stored cursor belongs to a different
	// compiled plan. It is a visible block, never a reason to reset the cursor.
	ErrReadingScopePurgePlan = errors.New("Reading scope purge cursor does not belong to this compiled plan")
	// ErrReadingScopePurgeCursor means the stored cursor is malformed or names
	// a row key this unit-only plan never persists.
	ErrReadingScopePurgeCursor = errors.New("Reading scope purge cursor is not valid for this plan")
	// ErrReadingScopePurgeLifetime means the scope is not in the supplied retired
	// library lifetime; no row was changed.
	ErrReadingScopePurgeLifetime = errors.New("Reading scope purge target is not the supplied retired library lifetime")
	// ErrReadingScopePurgeRequest means the caller omitted a target or Save hook.
	ErrReadingScopePurgeRequest = errors.New("Reading scope purge request is incomplete")
)

// ReadingScopePurgeRequest names one private scope and the exact retired
// library handle captured for it. StoredCursor is empty on the first page.
// Save must persist the supplied string in tx, atomically with the deletions.
type ReadingScopePurgeRequest struct {
	ScopeID       int64
	LibraryHandle int64
	StoredCursor  string
	Save          func(context.Context, *sql.Tx, string) error
}

// ReadingScopePurgeResult is one metered page. Done means the scope row is
// proved gone; the caller may discard StoredCursor. Build is the build selected
// for this page, or zero when none remained.
type ReadingScopePurgeResult struct {
	StoredCursor string
	Build        int64
	Worked       bool
	Done         bool
	Reads        int64
	Effects      int64
}

type readingScopePurgeMode uint8

const (
	readingScopePurgeSlot readingScopePurgeMode = iota + 1
	readingScopePurgeCapture
	readingScopePurgeRepresentative
	readingScopePurgeStage
	readingScopePurgeBuild
	readingScopePurgeNextBuild
	readingScopePurgeObjectKeys
	readingScopePurgeScopeEntities
	readingScopePurgeScope
)

// The literal statements. Values are always the supplied scope id and, for
// child/build units, ids selected by the leading primary-key prefix.
const (
	// Lifetime: scope PK, then catalog handle PK. COALESCE keeps a vanished
	// handle distinguishable from a live retired one.
	readingScopePurgeLifetimeSQL = `SELECT q.library_id,COALESCE((SELECT cl.retired FROM catalog_libraries cl WHERE cl.id=q.library_id),-1) FROM reading_path_scope q WHERE q.id=?1`

	// Each reference is cleared on its own. The slot's CHECK keeps overlay and
	// build equal, so a valid row is either both this scope's or both another
	// library's; clearing both columns from one match would still be wrong for a
	// mixed row, and this form preserves the column it did not match.
	readingScopePurgeSlotDetachSQL = `UPDATE reading_path_slot SET
 build_id=CASE WHEN EXISTS(SELECT 1 FROM reading_path_build WHERE id=reading_path_slot.build_id AND scope_id=?1) THEN NULL ELSE build_id END,
 overlay_id=CASE WHEN EXISTS(SELECT 1 FROM reading_path_build WHERE id=reading_path_slot.overlay_id AND scope_id=?1) THEN NULL ELSE overlay_id END
 WHERE singleton=1 AND (EXISTS(SELECT 1 FROM reading_path_build WHERE id=reading_path_slot.build_id AND scope_id=?1) OR EXISTS(SELECT 1 FROM reading_path_build WHERE id=reading_path_slot.overlay_id AND scope_id=?1))`
	readingScopePurgeSlotEmptySQL = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_slot WHERE singleton=1 AND (EXISTS(SELECT 1 FROM reading_path_build WHERE id=reading_path_slot.build_id AND scope_id=?1) OR EXISTS(SELECT 1 FROM reading_path_build WHERE id=reading_path_slot.overlay_id AND scope_id=?1)))`

	readingScopePurgeBuildPickSQL   = `SELECT id FROM reading_path_build INDEXED BY reading_path_build_scope WHERE scope_id=?1 ORDER BY id LIMIT 1`
	readingScopePurgeBuildDeleteSQL = `DELETE FROM reading_path_build WHERE id=?1 AND scope_id=?2`
	readingScopePurgeBuildEmptySQL  = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_build WHERE id=?1 AND scope_id=?2)`
	readingScopePurgeNoBuildSQL     = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_build INDEXED BY reading_path_build_scope WHERE scope_id=?1)`

	readingScopePurgeCapturePickSQL   = `SELECT item_id,asset_id FROM reading_path_capture WHERE build_id=?1 ORDER BY item_id,asset_id LIMIT 1`
	readingScopePurgeCaptureDeleteSQL = `DELETE FROM reading_path_capture WHERE build_id=?1 AND item_id=?2 AND asset_id=?3`
	readingScopePurgeCaptureEmptySQL  = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_capture WHERE build_id=?1)`

	readingScopePurgeRepresentativePickSQL   = `SELECT item_id FROM reading_path_representative WHERE build_id=?1 ORDER BY item_id LIMIT 1`
	readingScopePurgeRepresentativeDeleteSQL = `DELETE FROM reading_path_representative WHERE build_id=?1 AND item_id=?2`
	readingScopePurgeRepresentativeEmptySQL  = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_representative WHERE build_id=?1)`

	readingScopePurgeStagePickSQL   = `SELECT item_id,asset_id FROM reading_path_stage WHERE build_id=?1 ORDER BY item_id,asset_id LIMIT 1`
	readingScopePurgeStageDeleteSQL = `DELETE FROM reading_path_stage WHERE build_id=?1 AND item_id=?2 AND asset_id=?3`
	readingScopePurgeStageEmptySQL  = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_stage WHERE build_id=?1)`

	readingScopePurgeKeysPickSQL   = `SELECT object_id FROM reading_path_object_keys INDEXED BY reading_path_object_scope WHERE scope_id=?1 ORDER BY object_id LIMIT 1`
	readingScopePurgeKeysDeleteSQL = `DELETE FROM reading_path_object_keys WHERE object_id=?1 AND scope_id=?2`
	readingScopePurgeKeysEmptySQL  = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_object_keys INDEXED BY reading_path_object_scope WHERE scope_id=?1)`

	readingScopePurgeEntitiesPickSQL   = `SELECT entity_id FROM reading_path_scope_entities WHERE scope_id=?1 AND NOT EXISTS(SELECT 1 FROM reading_path_object_keys INDEXED BY reading_path_object_scope WHERE scope_id=?1) ORDER BY entity_id LIMIT 1`
	readingScopePurgeEntitiesDeleteSQL = `DELETE FROM reading_path_scope_entities WHERE scope_id=?1 AND entity_id=?2`
	readingScopePurgeEntitiesEmptySQL  = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_scope_entities WHERE scope_id=?1)`

	readingScopePurgeScopeDeleteSQL = `DELETE FROM reading_path_scope WHERE id=?1 AND NOT EXISTS(SELECT 1 FROM reading_path_build INDEXED BY reading_path_build_scope WHERE scope_id=?1) AND NOT EXISTS(SELECT 1 FROM reading_path_object_keys INDEXED BY reading_path_object_scope WHERE scope_id=?1) AND NOT EXISTS(SELECT 1 FROM reading_path_scope_entities WHERE scope_id=?1)`
	readingScopePurgeScopeEmptySQL  = `SELECT NOT EXISTS(SELECT 1 FROM reading_path_scope WHERE id=?1)`
)

// readingScopePurgeStep is one compiled unit descriptor. pickSQL is empty for
// units that do not select a child row.
type readingScopePurgeStep struct {
	mode      readingScopePurgeMode
	phase     int
	name      string
	readBound int
	effects   int
	pickSQL   string
	deleteSQL string
	probeSQL  string
}

// readingScopePurgeSteps is the one plan. Phase numbers are engine-local and
// unrelated to catalog_removal_progress's CHECK(phase BETWEEN 0 AND 5); this
// file does not add or rename a removal phase.
var readingScopePurgeSteps = []readingScopePurgeStep{
	{mode: readingScopePurgeSlot, phase: 0, name: "detach_slot", readBound: 3, effects: 1,
		deleteSQL: readingScopePurgeSlotDetachSQL, probeSQL: readingScopePurgeSlotEmptySQL},
	{mode: readingScopePurgeCapture, phase: 0, name: "capture", readBound: 2, effects: 1,
		pickSQL: readingScopePurgeCapturePickSQL, deleteSQL: readingScopePurgeCaptureDeleteSQL, probeSQL: readingScopePurgeCaptureEmptySQL},
	{mode: readingScopePurgeRepresentative, phase: 0, name: "representative", readBound: 2, effects: 1,
		pickSQL: readingScopePurgeRepresentativePickSQL, deleteSQL: readingScopePurgeRepresentativeDeleteSQL, probeSQL: readingScopePurgeRepresentativeEmptySQL},
	{mode: readingScopePurgeStage, phase: 0, name: "stage", readBound: 2, effects: 1,
		pickSQL: readingScopePurgeStagePickSQL, deleteSQL: readingScopePurgeStageDeleteSQL, probeSQL: readingScopePurgeStageEmptySQL},
	{mode: readingScopePurgeBuild, phase: 0, name: "build", readBound: 1, effects: 1,
		deleteSQL: readingScopePurgeBuildDeleteSQL, probeSQL: readingScopePurgeBuildEmptySQL},
	{mode: readingScopePurgeNextBuild, phase: 0, name: "next_build", readBound: 1, effects: 0,
		probeSQL: readingScopePurgeNoBuildSQL},
	{mode: readingScopePurgeObjectKeys, phase: 1, name: "object_keys", readBound: 2, effects: 6,
		pickSQL: readingScopePurgeKeysPickSQL, deleteSQL: readingScopePurgeKeysDeleteSQL, probeSQL: readingScopePurgeKeysEmptySQL},
	{mode: readingScopePurgeScopeEntities, phase: 2, name: "scope_entities", readBound: 3, effects: 3,
		pickSQL: readingScopePurgeEntitiesPickSQL, deleteSQL: readingScopePurgeEntitiesDeleteSQL, probeSQL: readingScopePurgeEntitiesEmptySQL},
	{mode: readingScopePurgeScope, phase: 3, name: "scope", readBound: 4, effects: 1,
		deleteSQL: readingScopePurgeScopeDeleteSQL, probeSQL: readingScopePurgeScopeEmptySQL},
}

// readingScopePurgePlanDigest is the deterministic identity of the compiled
// plan. It covers every unit's mode, phase, name, bounds and selection SQL, the
// owner probes and bounds, the plan version and the shared engine limits. It
// contains no process randomness, so a restart of the same binary accepts its
// own cursor.
var readingScopePurgePlanDigest = func() string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			fmt.Fprintf(h, "%d:%s|", len(part), part)
		}
		fmt.Fprint(h, "\n")
	}
	for i := range readingScopePurgeSteps {
		st := &readingScopePurgeSteps[i]
		write("unit", st.name, strconv.Itoa(st.phase), strconv.Itoa(int(st.mode)),
			strconv.Itoa(st.readBound), strconv.Itoa(st.effects),
			st.pickSQL, st.deleteSQL, st.probeSQL)
	}
	write("owner", strconv.Itoa(readingScopePurgeOwnerReadBound), strconv.Itoa(readingScopePurgeOwnerEffects))
	write("lifetime", readingScopePurgeLifetimeSQL)
	write("build-pick", readingScopePurgeBuildPickSQL)
	write("engine", strconv.Itoa(phasedpurge.EffectLimit), strconv.Itoa(phasedpurge.ReadLimit))
	write("version", readingScopePurgePlanVersion)
	return hex.EncodeToString(h.Sum(nil))
}()

// ReadingScopePurgePlanDigest exposes the compiled-plan identity to the parent
// that owns durable cursor storage. It changes whenever a literal, unit bound
// or engine limit changes.
func ReadingScopePurgePlanDigest() string { return readingScopePurgePlanDigest }

// readingScopePurgeStoredCursor is the owner envelope around the shared
// engine's cursor, the same shape used by the profile purge. It is not a second
// state machine: the inner cursor is exactly phasedpurge.EncodeCursor output.
type readingScopePurgeStoredCursor struct {
	Digest string `json:"d"`
	Cursor string `json:"c"`
}

func readingScopePurgeDecodeStored(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	var stored readingScopePurgeStoredCursor
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return "", fmt.Errorf("%w: %v", ErrReadingScopePurgePlan, err)
	}
	if stored.Digest != readingScopePurgePlanDigest {
		return "", ErrReadingScopePurgePlan
	}
	return stored.Cursor, nil
}

func readingScopePurgeEncodeStored(c phasedpurge.Cursor) (string, error) {
	inner, err := phasedpurge.EncodeCursor(c)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(readingScopePurgeStoredCursor{Digest: readingScopePurgePlanDigest, Cursor: inner})
	if err != nil || len(raw) > phasedpurge.CursorLimit {
		return "", ErrReadingScopePurgePlan
	}
	return string(raw), nil
}

type readingScopePurgeState struct {
	scope    int64
	build    int64
	hasBuild bool
}

// RetireReadingScopePage runs ONE metered page for one supplied private scope.
// It deletes only rows that point at scopeID; all other scopes, libraries, the
// singleton slot when it names another scope's build, and the library-wide
// input/work counters are left untouched.
func RetireReadingScopePage(ctx context.Context, tx *sql.Tx, req ReadingScopePurgeRequest) (ReadingScopePurgeResult, error) {
	if tx == nil || req.Save == nil {
		return ReadingScopePurgeResult{}, ErrReadingScopePurgeRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	st := &readingScopePurgeState{scope: req.ScopeID}
	plan := readingScopePurgePlan(st)
	inner, err := readingScopePurgeDecodeStored(req.StoredCursor)
	if err != nil {
		return ReadingScopePurgeResult{}, err
	}
	cursor, err := phasedpurge.DecodeCursor(inner, plan)
	if err != nil {
		return ReadingScopePurgeResult{}, fmt.Errorf("%w: %w", ErrReadingScopePurgeCursor, err)
	}
	present, err := readingScopePurgeLifetime(ctx, tx, req.ScopeID, req.LibraryHandle)
	if err != nil {
		return ReadingScopePurgeResult{}, err
	}
	if !present {
		// The scope row is already gone. That is the terminal fixed point for
		// this scope; the caller may discard its cursor. No sibling row is read.
		return ReadingScopePurgeResult{StoredCursor: req.StoredCursor, Done: true}, nil
	}
	owner := phasedpurge.Owner{
		ReadBound: readingScopePurgeOwnerReadBound,
		Before: func(bctx context.Context, btx *sql.Tx) error {
			var id sql.NullInt64
			err := btx.QueryRowContext(bctx, readingScopePurgeBuildPickSQL, req.ScopeID).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				st.hasBuild = false
				return nil
			}
			if err != nil {
				return err
			}
			st.build, st.hasBuild = id.Int64, id.Valid
			return nil
		},
		Save: func(sctx context.Context, stx *sql.Tx, c phasedpurge.Cursor) error {
			stored, err := readingScopePurgeEncodeStored(c)
			if err != nil {
				return err
			}
			return req.Save(sctx, stx, stored)
		},
		// Completion is a proof, not another write. The caller discards the
		// stored cursor when Done is returned.
		Finalize: func(context.Context, *sql.Tx) error { return nil },
	}
	result, err := phasedpurge.Turn(ctx, tx, plan, cursor, owner)
	if err != nil {
		return ReadingScopePurgeResult{StoredCursor: req.StoredCursor, Build: st.build, Reads: result.Reads, Effects: result.Effects}, err
	}
	stored, err := readingScopePurgeEncodeStored(result.Cursor)
	if err != nil {
		return ReadingScopePurgeResult{}, err
	}
	return ReadingScopePurgeResult{
		StoredCursor: stored,
		Build:        st.build,
		Worked:       result.Worked,
		Done:         result.Done,
		Reads:        result.Reads,
		Effects:      result.Effects,
	}, nil
}

// readingScopePurgeLifetime is the same-writer retirement proof. It deliberately
// does not read the dispatcher's runnable/parked state, the source barrier, a
// job, or domain28 rows: private cleanup must finish for a settled, parked or
// blocked scope. A missing catalog handle is a refused lifetime, not a silent
// success; terminal cleanup is the library removal owner's separate step.
func readingScopePurgeLifetime(ctx context.Context, tx *sql.Tx, scope, handle int64) (bool, error) {
	var library, retired int64
	err := tx.QueryRowContext(ctx, readingScopePurgeLifetimeSQL, scope).Scan(&library, &retired)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if library != handle || retired != 1 {
		return false, fmt.Errorf("%w: scope library %d retired %d, supplied handle %d", ErrReadingScopePurgeLifetime, library, retired, handle)
	}
	return true, nil
}

func readingScopePurgePlan(st *readingScopePurgeState) []phasedpurge.Unit {
	plan := make([]phasedpurge.Unit, 0, len(readingScopePurgeSteps))
	for i := range readingScopePurgeSteps {
		step := readingScopePurgeSteps[i]
		plan = append(plan, phasedpurge.Unit{
			Phase:     step.phase,
			Name:      step.name,
			ReadBound: step.readBound,
			Run: func(ctx context.Context, tx *sql.Tx, _ []phasedpurge.Key) (phasedpurge.Outcome, error) {
				return readingScopePurgeRun(ctx, tx, st, step)
			},
			Empty: func(ctx context.Context, tx *sql.Tx) (bool, error) {
				return readingScopePurgeEmpty(ctx, tx, st, step)
			},
		})
	}
	return plan
}

func readingScopePurgeRun(ctx context.Context, tx *sql.Tx, st *readingScopePurgeState, step readingScopePurgeStep) (phasedpurge.Outcome, error) {
	switch step.mode {
	case readingScopePurgeSlot:
		result, err := tx.ExecContext(ctx, step.deleteSQL, st.scope)
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		if n == 0 {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		return phasedpurge.Outcome{Changed: true}, nil
	case readingScopePurgeCapture, readingScopePurgeStage:
		if !st.hasBuild {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		return readingScopePurgePairChild(ctx, tx, st.build, step)
	case readingScopePurgeRepresentative:
		if !st.hasBuild {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		return readingScopePurgeSingleChild(ctx, tx, st.build, step)
	case readingScopePurgeBuild:
		if !st.hasBuild {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		result, err := tx.ExecContext(ctx, step.deleteSQL, st.build, st.scope)
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		if n != 1 {
			return phasedpurge.Outcome{}, fmt.Errorf("Reading scope purge %s: build %d deleted %d rows after its children were empty", step.name, st.build, n)
		}
		// The build row is gone. Advance to next_build before another build can
		// be selected, so a remaining build is rewound to detach_slot and drained
		// from its own children first.
		return phasedpurge.Outcome{Drained: true, Changed: true}, nil
	case readingScopePurgeNextBuild:
		if st.hasBuild {
			return phasedpurge.Outcome{Rewind: true}, nil
		}
		return phasedpurge.Outcome{Drained: true}, nil
	case readingScopePurgeObjectKeys:
		var object string
		err := tx.QueryRowContext(ctx, step.pickSQL, st.scope).Scan(&object)
		if errors.Is(err, sql.ErrNoRows) {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		result, err := tx.ExecContext(ctx, step.deleteSQL, object, st.scope)
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		if n != 1 {
			return phasedpurge.Outcome{}, fmt.Errorf("Reading scope purge %s: one selected key deleted %d rows", step.name, n)
		}
		return phasedpurge.Outcome{Changed: true}, nil
	case readingScopePurgeScopeEntities:
		var entity int64
		err := tx.QueryRowContext(ctx, step.pickSQL, st.scope).Scan(&entity)
		if errors.Is(err, sql.ErrNoRows) {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		result, err := tx.ExecContext(ctx, step.deleteSQL, st.scope, entity)
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		if n != 1 {
			return phasedpurge.Outcome{}, fmt.Errorf("Reading scope purge %s: one selected entity deleted %d rows", step.name, n)
		}
		return phasedpurge.Outcome{Changed: true}, nil
	case readingScopePurgeScope:
		result, err := tx.ExecContext(ctx, step.deleteSQL, st.scope)
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		if n == 0 {
			// Either the scope is already gone or a build/key/entity appeared
			// after the last turn. Verification below decides which; it rewinds
			// the phase for a real child and passes for an absent scope.
			return phasedpurge.Outcome{Drained: true}, nil
		}
		return phasedpurge.Outcome{Drained: true, Changed: true}, nil
	}
	return phasedpurge.Outcome{}, fmt.Errorf("Reading scope purge: unknown unit %q", step.name)
}

// readingScopePurgePairChild deletes at most one capture/stage row, selected by
// the build-leading primary-key prefix. The child row is removed, not a batch:
// a parent-build LIMIT would not bound these rows.
func readingScopePurgePairChild(ctx context.Context, tx *sql.Tx, build int64, step readingScopePurgeStep) (phasedpurge.Outcome, error) {
	var item, asset int64
	err := tx.QueryRowContext(ctx, step.pickSQL, build).Scan(&item, &asset)
	if errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{Drained: true}, nil
	}
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	result, err := tx.ExecContext(ctx, step.deleteSQL, build, item, asset)
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	if n != 1 {
		return phasedpurge.Outcome{}, fmt.Errorf("Reading scope purge %s: one selected child deleted %d rows", step.name, n)
	}
	return phasedpurge.Outcome{Changed: true}, nil
}

func readingScopePurgeSingleChild(ctx context.Context, tx *sql.Tx, build int64, step readingScopePurgeStep) (phasedpurge.Outcome, error) {
	var item int64
	err := tx.QueryRowContext(ctx, step.pickSQL, build).Scan(&item)
	if errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{Drained: true}, nil
	}
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	result, err := tx.ExecContext(ctx, step.deleteSQL, build, item)
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	if n != 1 {
		return phasedpurge.Outcome{}, fmt.Errorf("Reading scope purge %s: one selected child deleted %d rows", step.name, n)
	}
	return phasedpurge.Outcome{Changed: true}, nil
}

func readingScopePurgeEmpty(ctx context.Context, tx *sql.Tx, st *readingScopePurgeState, step readingScopePurgeStep) (bool, error) {
	switch step.mode {
	case readingScopePurgeBuild:
		if !st.hasBuild {
			return true, nil
		}
		return readingScopePurgeEmptyProbe(ctx, tx, step.probeSQL, st.build, st.scope)
	case readingScopePurgeCapture, readingScopePurgeRepresentative, readingScopePurgeStage:
		if !st.hasBuild {
			return true, nil
		}
		return readingScopePurgeEmptyProbe(ctx, tx, step.probeSQL, st.build)
	default:
		return readingScopePurgeEmptyProbe(ctx, tx, step.probeSQL, st.scope)
	}
}

func readingScopePurgeEmptyProbe(ctx context.Context, tx *sql.Tx, query string, args ...any) (bool, error) {
	var n int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return false, err
	}
	return n != 0, nil
}
