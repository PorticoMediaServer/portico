package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

// Reading per-object residue is reclaimed by the retired library's existing
// phase-5 removal owner. Each raw source window uses the same phasedpurge
// engine and strict durable cursor; dirty work is last because earlier deletes
// can enqueue it. Final row removal runs once, then a settle pass proves the
// triggers' remaining residue gone. Publication never owns library lifetime.

const (
	readingRemovalResidueStage = "residue"
	readingRemovalSettleStage  = "settle"

	// readingRemovalStateVersion changes whenever a stage, unit, literal or
	// bound semantic changes. It is mixed into the digest with the literals.
	readingRemovalStateVersion = "reading-per-file-removal-v2"

	// readingRemovalOwnerReads includes the lifetime and source probes, the
	// engine's two total_changes() reads and the final row proofs.
	readingRemovalOwnerReads = 8
	// readingRemovalOwnerEffects is the caller's progress Save row.
	readingRemovalOwnerEffects = 1
)

var (
	errReadingRemovalLifetime = errors.New("Reading removal target is not the supplied retired library lifetime")
	// errReadingRemovalPlan means the stored state belongs to a different
	// compiled plan. It is a visible block, never a reason to reset.
	errReadingRemovalPlan = errors.New("Reading removal cursor does not belong to this compiled plan")
	// errReadingRemovalCursor means the stored state is malformed, is not the
	// canonical encoding of this plan, or names a row key this plan never
	// persists.
	errReadingRemovalCursor = errors.New("Reading removal cursor is not valid for this plan")
)

// The literal point statements. Every one is a leading-primary-key or
// leading-index access for the retired library; none joins a whole catalogue.
const (
	readingRemovalLifetimeSQL    = `SELECT library_id,retired FROM catalog_libraries WHERE id=?1`
	readingRemovalSourceFirstSQL = `SELECT id FROM library_sources INDEXED BY library_sources_library WHERE library_id=?1 ORDER BY id LIMIT 1`
	readingRemovalSourceNextSQL  = `SELECT id FROM library_sources INDEXED BY library_sources_library WHERE library_id=?1 AND id>?2 ORDER BY id LIMIT 1`
	// Two point reads after the final row removal: the removal's own savepoint
	// suppresses a failed DELETE, so its success must be proved, not assumed.
	readingRemovalRowGoneSQL = `SELECT (SELECT count(*) FROM libraries WHERE id=?1),(SELECT count(*) FROM catalog_libraries WHERE library_id=?1)`
)

type readingRemovalResidueShape uint8

const (
	// readingRemovalKeyThenOwner selects one child key, then names the owner:
	// DELETE ... WHERE <key>=?1 AND <owner>=?2.
	readingRemovalKeyThenOwner readingRemovalResidueShape = iota + 1
	// readingRemovalOwnerOnly deletes the owner-keyed row by its primary key.
	readingRemovalOwnerOnly
	// readingRemovalOwnerThenPair selects one (kind,work_key) pair under the
	// owner and deletes it by its full primary key.
	readingRemovalOwnerThenPair
)

// readingRemovalUnit is one residue unit. Order in the slice is the deletion
// order; dirty work is last because deleting the rows above writes it.
type readingRemovalUnit struct {
	name    string
	reads   int
	effects int
	shape   readingRemovalResidueShape
	source  bool
	pick    string
	del     string
	empty   string
}

var readingRemovalResidueUnits = []readingRemovalUnit{
	{name: "availability", reads: 3, effects: 2, shape: readingRemovalKeyThenOwner,
		pick:  `SELECT entity_id FROM reading_path_availability_pending INDEXED BY reading_path_availability_library WHERE library_id=?1 ORDER BY entity_id LIMIT 1`,
		del:   `DELETE FROM reading_path_availability_pending WHERE entity_id=?1 AND library_id=?2`,
		empty: `SELECT NOT EXISTS(SELECT 1 FROM reading_path_availability_pending INDEXED BY reading_path_availability_library WHERE library_id=?1)`},
	{name: "semantic", reads: 2, effects: 1, shape: readingRemovalOwnerOnly,
		pick:  `SELECT library_id FROM reading_path_semantic_pending WHERE library_id=?1`,
		del:   `DELETE FROM reading_path_semantic_pending WHERE library_id=?1`,
		empty: `SELECT NOT EXISTS(SELECT 1 FROM reading_path_semantic_pending WHERE library_id=?1)`},
	{name: "blocks", reads: 3, effects: 1, shape: readingRemovalKeyThenOwner, source: true,
		pick:  `SELECT id FROM reading_path_block INDEXED BY reading_path_block_source WHERE source_id=?1 ORDER BY id LIMIT 1`,
		del:   `DELETE FROM reading_path_block WHERE id=?1 AND source_id=?2`,
		empty: `SELECT NOT EXISTS(SELECT 1 FROM reading_path_block INDEXED BY reading_path_block_source WHERE source_id=?1)`},
	{name: "barrier", reads: 2, effects: 1, shape: readingRemovalOwnerOnly, source: true,
		pick:  `SELECT source_id FROM reading_path_source_barrier WHERE source_id=?1`,
		del:   `DELETE FROM reading_path_source_barrier WHERE source_id=?1`,
		empty: `SELECT NOT EXISTS(SELECT 1 FROM reading_path_source_barrier WHERE source_id=?1)`},
	{name: "objects", reads: 3, effects: 1, shape: readingRemovalKeyThenOwner,
		pick:  `SELECT object_id FROM reading_path_object_keys INDEXED BY reading_path_object_library WHERE library_id=?1 ORDER BY object_id LIMIT 1`,
		del:   `DELETE FROM reading_path_object_keys WHERE object_id=?1 AND library_id=?2`,
		empty: `SELECT NOT EXISTS(SELECT 1 FROM reading_path_object_keys INDEXED BY reading_path_object_library WHERE library_id=?1)`},
	{name: "work", reads: 3, effects: 1, shape: readingRemovalOwnerThenPair,
		pick:  `SELECT kind,work_key FROM reading_path_dirty_work WHERE library_id=?1 ORDER BY kind,work_key LIMIT 1`,
		del:   `DELETE FROM reading_path_dirty_work WHERE library_id=?1 AND kind=?2 AND work_key=?3`,
		empty: `SELECT NOT EXISTS(SELECT 1 FROM reading_path_dirty_work WHERE library_id=?1)`},
}

// readingRemovalDigest binds the stage semantics and the
// literal statements, each unit's declared read/effect bound and order, the
// owner bounds and the shared engine limits. No process randomness, so the
// same binary accepts its own cursor after a restart.
var readingRemovalDigest = func() string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			fmt.Fprintf(h, "%d:%s|", len(part), part)
		}
		fmt.Fprint(h, "\n")
	}
	write("version", readingRemovalStateVersion)
	write("lifetime", readingRemovalLifetimeSQL)
	write("source", readingRemovalSourceFirstSQL, readingRemovalSourceNextSQL)
	write("row-gone", readingRemovalRowGoneSQL)
	write("owner", strconv.Itoa(readingRemovalOwnerReads), strconv.Itoa(readingRemovalOwnerEffects))
	write("engine", strconv.Itoa(phasedpurge.EffectLimit), strconv.Itoa(phasedpurge.ReadLimit))
	for i := range readingRemovalResidueUnits {
		u := &readingRemovalResidueUnits[i]
		write("unit", u.name, strconv.Itoa(u.reads), strconv.Itoa(u.effects),
			strconv.Itoa(int(u.shape)), strconv.FormatBool(u.source), u.pick, u.del, u.empty)
	}
	return hex.EncodeToString(h.Sum(nil))
}()

// readingRemovalState is the whole durable phase-5 cursor.
type readingRemovalState struct {
	Digest string  `json:"d"`
	Handle int64   `json:"h"`
	Stage  string  `json:"s"`
	After  *string `json:"a,omitempty"`
	Cursor string  `json:"c,omitempty"`
}

// readingRemovalLoad reads one stored state. The empty string is the fresh
// state at phase-5 entry, with no handle captured yet. Anything else must be
// this plan's exact canonical encoding: the digest, the handle, the stage and
// its cross-field invariants, both inner cursors, and a byte-identical
// re-encoding (which also refuses duplicate, null and unknown fields).
func readingRemovalLoad(raw string) (readingRemovalState, error) {
	if raw == "" {
		return readingRemovalState{Digest: readingRemovalDigest, Stage: readingRemovalResidueStage}, nil
	}
	var st readingRemovalState
	if !utf8.ValidString(raw) {
		return st, errReadingRemovalCursor
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&st); err != nil {
		return st, fmt.Errorf("%w: %v", errReadingRemovalCursor, err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return st, errReadingRemovalCursor
	}
	if st.Digest != readingRemovalDigest {
		return st, errReadingRemovalPlan
	}
	if st.Handle <= 0 {
		return st, errReadingRemovalCursor
	}
	switch st.Stage {
	case readingRemovalResidueStage:
	case readingRemovalSettleStage:
		if st.After != nil {
			return st, errReadingRemovalCursor
		}
	default:
		return st, errReadingRemovalCursor
	}
	if st.Cursor != "" {
		cursor, err := phasedpurge.DecodeCursor(st.Cursor, readingRemovalResiduePlan(0, sql.NullString{}))
		if err != nil {
			return st, fmt.Errorf("%w: %w", errReadingRemovalCursor, err)
		}
		canonical, err := phasedpurge.EncodeCursor(cursor)
		if err != nil || canonical != st.Cursor {
			return st, errReadingRemovalCursor
		}
	}
	encoded, err := json.Marshal(st)
	if err != nil || string(encoded) != raw {
		return st, errReadingRemovalCursor
	}
	return st, nil
}

func readingRemovalSave(ctx context.Context, tx *sql.Tx, p *removalProgress, st readingRemovalState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	p.objectCursor = string(raw)
	return p.saveTx(ctx, tx)
}

// removeLibraryReadingTx is phase 5. It runs exactly one bounded piece and
// reports whether the whole library removal is finished. Every path persists
// its own state in this transaction, so the caller returns without a second
// save.
func removeLibraryReadingTx(ctx context.Context, tx *sql.Tx, library string, handle int64, p *removalProgress) (bool, error) {
	st, err := readingRemovalLoad(p.objectCursor)
	if err != nil {
		return false, err
	}
	if st.Handle == 0 {
		st.Handle = handle
	}
	if st.Handle != handle {
		return false, errReadingRemovalCursor
	}
	// Same-writer retirement proof, independent of publication, jobs, source
	// coverage and availability derivation.
	var actual string
	var retired int64
	if err := tx.QueryRowContext(ctx, readingRemovalLifetimeSQL, handle).Scan(&actual, &retired); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, errReadingRemovalLifetime
		}
		return false, err
	}
	if actual != library || retired != 1 {
		return false, fmt.Errorf("%w: library %q retired %d, supplied handle %d", errReadingRemovalLifetime, actual, retired, handle)
	}

	return readingRemovalResidueTurn(ctx, tx, library, handle, &st, p)
}

// readingRemovalAfterRowTx is the tolerant phase-5 entry for a library whose
// catalog_libraries row is already gone. A phase-5 state that is not the
// settle proof is a visible refusal: the row may have gone without the reading
// owner's authority, and claiming Done would abandon its private residue. Any
// other phase keeps the existing success path untouched.
func readingRemovalAfterRowTx(ctx context.Context, tx *sql.Tx, library string, p *removalProgress) (bool, error) {
	if p.phase != removalLibrary || p.objectCursor == "" {
		return true, nil
	}
	st, err := readingRemovalLoad(p.objectCursor)
	if err != nil {
		return false, err
	}
	if st.Stage != readingRemovalSettleStage {
		return false, fmt.Errorf("%w: library row gone while Reading stage %q still owes work", errReadingRemovalLifetime, st.Stage)
	}
	return readingRemovalResidueTurn(ctx, tx, library, st.Handle, &st, p)
}

func readingRemovalResidueTurn(ctx context.Context, tx *sql.Tx, library string, handle int64, st *readingRemovalState, p *removalProgress) (bool, error) {
	source, err := readingRemovalSourceTx(ctx, tx, library, st.After)
	if err != nil {
		return false, err
	}
	plan := readingRemovalResiduePlan(handle, source)
	cursor, err := phasedpurge.DecodeCursor(st.Cursor, plan)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errReadingRemovalCursor, err)
	}
	// Only the engine's own Finalize proves this window empty. A turn that saved
	// a position, or an interrupted verify, is never a completion: it yields and
	// touches neither the stage, nor the final rows, nor Done.
	finalized := false
	result, err := phasedpurge.Turn(ctx, tx, plan, cursor, phasedpurge.Owner{
		ReadBound: readingRemovalOwnerReads,
		Save: func(sctx context.Context, stx *sql.Tx, c phasedpurge.Cursor) error {
			inner, err := phasedpurge.EncodeCursor(c)
			if err != nil {
				return err
			}
			st.Cursor = inner
			return readingRemovalSave(sctx, stx, p, *st)
		},
		Finalize: func(fctx context.Context, ftx *sql.Tx) error {
			if source.Valid {
				// This raw source is proved empty; the successor is picked on
				// the next turn from the advanced window, never from a parent
				// LIMIT over its children.
				next := source.String
				st.After, st.Cursor = &next, ""
			} else {
				st.After, st.Cursor = nil, ""
			}
			if err := readingRemovalSave(fctx, ftx, p, *st); err != nil {
				return err
			}
			finalized = true
			return nil
		},
	})
	if err != nil {
		return false, err
	}
	if !finalized {
		return false, nil
	}
	if !result.Done {
		return false, fmt.Errorf("%w: finalized without completion", errReadingRemovalCursor)
	}
	if source.Valid {
		return false, nil
	}
	if st.Stage == readingRemovalResidueStage {
		// Precleanup is complete. The existing final row removal runs once, then
		// settle proves the rows its own triggers requeue are gone too, and that
		// the final rows are really absent.
		if err := removeLibraryRowTx(ctx, tx, library); err != nil {
			return false, err
		}
		var libraries, catalogRows int64
		if err := tx.QueryRowContext(ctx, readingRemovalRowGoneSQL, library).Scan(&libraries, &catalogRows); err != nil {
			return false, err
		}
		if libraries != 0 || catalogRows != 0 {
			// removeLibraryRowTx suppresses a failed DELETE with its own
			// savepoint. A remaining row is a visible blocker, never a reason to
			// enter settle and claim Done over a hidden retired library.
			return false, fmt.Errorf("%w: final rows remain libraries=%d catalog_libraries=%d", errReadingRemovalLifetime, libraries, catalogRows)
		}
		st.Stage = readingRemovalSettleStage
		st.After, st.Cursor = nil, ""
		return false, readingRemovalSave(ctx, tx, p, *st)
	}
	return true, clearRemovalProgressTx(ctx, tx, "library", library)
}

// readingRemovalSourceTx is the one raw window pick: the first source of the
// library, or the first source after the advanced window. The units filter it;
// it never bounds a child delete.
func readingRemovalSourceTx(ctx context.Context, tx *sql.Tx, library string, after *string) (sql.NullString, error) {
	var source sql.NullString
	var err error
	if after == nil {
		err = tx.QueryRowContext(ctx, readingRemovalSourceFirstSQL, library).Scan(&source)
	} else {
		err = tx.QueryRowContext(ctx, readingRemovalSourceNextSQL, library, *after).Scan(&source)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return source, err
	}
	return source, nil
}

func readingRemovalResiduePlan(handle int64, source sql.NullString) []phasedpurge.Unit {
	plan := make([]phasedpurge.Unit, 0, len(readingRemovalResidueUnits))
	for i := range readingRemovalResidueUnits {
		step := readingRemovalResidueUnits[i]
		var owner any = handle
		if step.source && source.Valid {
			owner = source.String
		}
		plan = append(plan, phasedpurge.Unit{
			Phase: 0, Name: step.name, ReadBound: step.reads,
			Run: func(ctx context.Context, tx *sql.Tx, _ []phasedpurge.Key) (phasedpurge.Outcome, error) {
				if step.source && !source.Valid {
					return phasedpurge.Outcome{Drained: true}, nil
				}
				return readingRemovalResidueRun(ctx, tx, step, owner)
			},
			Empty: func(ctx context.Context, tx *sql.Tx) (bool, error) {
				if step.source && !source.Valid {
					return true, nil
				}
				var empty bool
				if err := tx.QueryRowContext(ctx, step.empty, owner).Scan(&empty); err != nil {
					return false, err
				}
				return empty, nil
			},
		})
	}
	return plan
}

func readingRemovalResidueRun(ctx context.Context, tx *sql.Tx, step readingRemovalUnit, owner any) (phasedpurge.Outcome, error) {
	var args []any
	switch step.shape {
	case readingRemovalOwnerThenPair:
		var kind, key string
		err := tx.QueryRowContext(ctx, step.pick, owner).Scan(&kind, &key)
		if errors.Is(err, sql.ErrNoRows) {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		args = []any{owner, kind, key}
	case readingRemovalKeyThenOwner:
		// Entity/block keys are INTEGER, while object IDs are TEXT. Keep the
		// selected SQL scalar type when binding the exact guarded delete.
		var key any
		err := tx.QueryRowContext(ctx, step.pick, owner).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		args = []any{key, owner}
	default:
		var key any
		err := tx.QueryRowContext(ctx, step.pick, owner).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		args = []any{owner}
	}
	result, err := tx.ExecContext(ctx, step.del, args...)
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	if n != 1 {
		return phasedpurge.Outcome{}, fmt.Errorf("Reading removal %s: one selected row deleted %d", step.name, n)
	}
	return phasedpurge.Outcome{Changed: true}, nil
}
