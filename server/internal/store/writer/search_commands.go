package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

var (
	// Missing/corrupt bank configuration is a cohort-wide configuration error,
	// never an individual file's poison disposition or a request to initialize.
	errSearchInputConfiguration = errors.New("search input configuration unavailable")
	errSearchInputConflict      = errors.New("search input witness changed")
)

type searchInputBankFacts struct {
	id, pending, nextPending               int64
	key, recipe, engine, fingerprint, role string
}

// applySearchInputIntent runs ONCE after the Inventory command's historical
// receipt resolution and actual new-source INSERT RETURNING. It owns no turn,
// receipt, retry, clock or initialization fallback. Any error must roll back
// the caller's complete source cohort. It never normalizes or writes native FTS.
func applySearchInputIntent(ctx context.Context, turn *commandTurn, change *searchInputChange) error {
	if ctx == nil || turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	if err := validateNewSearchInput(change); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(change.NewEntities) == 0 {
		return nil
	}
	// Copy the admitted fixed FIRST cohort before SQL. This post-allocation
	// effect value never enters the original Inventory request/result digest.
	entity, acceptedAt := change.NewEntities[0], change.AcceptedAtMS
	ids := [...]stmt.ID{searchInputHead, searchInputBank, searchInputEntity, searchInputSubject,
		searchInputExisting, searchInputInsert, searchInputPending, searchInputSequence, searchInputBuilding}
	for _, id := range ids {
		if _, ok := turn.steps[id]; !ok {
			return invalid("unregistered_search_input_step")
		}
	}
	// W's ONE complete Inventory recipe supplies the immutable numeric work.
	// No domain/caller budget, registry or executor is constructed here.
	step := func(id stmt.ID) statementStep { return turn.steps[id].statementStep }
	row, err := turn.queryRow(ctx, step(searchInputHead))
	if err != nil {
		return err
	}
	var servingRevision, sequence int64
	var active, building sql.NullInt64
	if err := row.Scan(&servingRevision, &active, &building, &sequence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errSearchInputConfiguration
		}
		return err
	}
	if !active.Valid || active.Int64 <= 0 || servingRevision <= 0 ||
		building.Valid && (building.Int64 <= 0 || building.Int64 == active.Int64) {
		return errSearchInputConfiguration
	}
	nextSequence, err := nextSearchInputCounter(sequence, 1)
	if err != nil {
		return fmt.Errorf("%w: input clock exhausted", errSearchInputConfiguration)
	}
	banks := [2]searchInputBankFacts{{id: active.Int64, role: "active"}, {id: building.Int64, role: "building"}}
	bankCount := 1
	if building.Valid {
		bankCount = 2
	}
	for i := 0; i < bankCount; i++ {
		bank := &banks[i]
		row, err := turn.queryRow(ctx, step(searchInputBank), bank.id)
		if err != nil {
			return err
		}
		var role string
		if err := row.Scan(&bank.key, &bank.recipe, &bank.engine, &bank.fingerprint, &role, &bank.pending); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errSearchInputConfiguration
			}
			return err
		}
		if role != bank.role || bank.key == "" || !searchpolicy.StoredWellFormed(searchpolicy.Profile{
			RecipeVersion: bank.recipe, EngineVersion: bank.engine, UnicodeFingerprint: bank.fingerprint,
		}) {
			return errSearchInputConfiguration
		}
		if i > 0 && bank.key == banks[0].key {
			return errSearchInputConfiguration
		}
		// RAW accounting binds the STORED immutable tuple. A compiled-profile
		// mismatch parks native publication, never this accepted source change.
		bank.nextPending, err = nextSearchInputCounter(bank.pending, 1)
		if err != nil {
			return fmt.Errorf("%w: pending count exhausted", errSearchInputConfiguration)
		}
	}
	row, err = turn.queryRow(ctx, step(searchInputEntity), entity.SubjectID, entity.LibraryID, entity.Kind)
	if err != nil {
		return err
	}
	var incarnation []byte
	var revision int64
	if err := row.Scan(&incarnation, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errSearchInputConflict
		}
		return err
	}
	if revision != entity.EntityRevision || !bytes.Equal(incarnation, entity.MetadataIncarnation[:]) {
		return errSearchInputConflict
	}
	row, err = turn.queryRow(ctx, step(searchInputSubject), entity.SubjectID, entity.LibraryID, entity.Kind)
	if err != nil {
		return err
	}
	var subject int64
	if err := row.Scan(&subject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errSearchInputConflict
		}
		return err
	}
	if subject != entity.SubjectID {
		return errSearchInputConflict
	}
	row, err = turn.queryRow(ctx, step(searchInputExisting), entity.SubjectID)
	if err != nil {
		return err
	}
	var prior int64
	if err := row.Scan(&prior); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return err
		}
		// FIRST is only genuinely inserted entities, never reimport/coalesce.
		return errSearchInputConflict
	}
	exactlyOne := func(id stmt.ID, args ...any) error {
		result, err := turn.exec(ctx, step(id), args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errSearchInputConflict
		}
		return nil
	}
	for i := 0; i < bankCount; i++ {
		bank := &banks[i]
		if err := exactlyOne(searchInputInsert, entity.SubjectID, bank.id, firstSearchInputGeneration,
			entity.MetadataIncarnation[:], entity.EntityRevision, bank.recipe, firstSearchInputPhase, acceptedAt); err != nil {
			return err
		}
		if err := exactlyOne(searchInputPending, bank.nextPending, bank.id, bank.role, bank.key,
			bank.recipe, bank.engine, bank.fingerprint, bank.pending); err != nil {
			return err
		}
	}
	if building.Valid {
		return exactlyOne(searchInputBuilding, nextSequence, servingRevision, active.Int64, building.Int64, sequence)
	}
	return exactlyOne(searchInputSequence, nextSequence, servingRevision, active.Int64, sequence)
}
