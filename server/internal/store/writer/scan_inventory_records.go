package writer

import (
	"context"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const scanInventoryLibraryRecordLengths stmt.ID = "scan.inventory_accept.library_record_lengths"

// An envelope within the ONE complete profile, not a product limit or another
// ledger. Work includes native old-row rebuild/amplification at maxBytes. The
// profile owner supplies that bound from source facts and MAX qualification.
type inventoryRecordEnvelope struct {
	maxBytes uint64
	work     Work
}

func inventoryLibraryRecordDefinition() stmt.Definition {
	return stmt.Definition{ID: scanInventoryLibraryRecordLengths, Owner: "store/writer N1 record preflight", CallSites: []string{"store/writer/scan_inventory_records.go"}, Kind: stmt.Read,
		SQL:    `SELECT octet_length(id),octet_length(name),octet_length(kind),octet_length(root),octet_length(preference_local_tags),octet_length(preference_lyrics_source),octet_length(preference_title_source),octet_length(preference_episode_numbering),octet_length(playback_origin_incarnation),octet_length(metadata_language) FROM libraries WHERE id=?`,
		Params: []stmt.Param{{Name: "library_key", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "own_library", Bindings: []any{"lifecycle-library"}}, {Name: "missing", Bindings: []any{"missing-library"}}},
		Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"}}}
}

// Four current touched records have at most 64 physical columns each. This
// bounds fixed scalar/serial-header storage (4*(64*17+9)<8KiB), separately from
// their actual variable bytes. The native schema control checks this premise.
const inventoryRecordFixedBytes = uint64(8 << 10)

func inventoryRecordActualBytes(keys []string, vectors ...scanByteVector) (uint64, error) {
	total := inventoryRecordFixedBytes
	// Existing job/root points omit their own PK text because their payload
	// copies already have it. Account for that exact frozen key here as well.
	for _, key := range keys {
		if uint64(len(key)) > math.MaxUint64-total {
			return 0, ErrCap
		}
		total += uint64(len(key))
	}
	for _, v := range vectors {
		if !v.exists {
			return 0, ErrConflict
		}
		if v.total > math.MaxUint64-total {
			return 0, ErrCap
		}
		total += v.total
	}
	return total, nil
}

func checkInventoryExistingRecords(ctx context.Context, turn *commandTurn, in *inventoryInvocation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if in == nil || in.input == nil || in.issuer == nil || in.issuer.driver == nil || in.issuer.driver.writer == nil || turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	r := in.issuer.driver.writer.effective
	if r == nil || r.inventoryDriverPlan == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	plan := r.inventoryDriverPlan
	c := in.input
	run, err := readScanByteLengths(ctx, turn, scanRunByteLengths, c.request.scope.runID)
	if err != nil {
		return err
	}
	job, err := readScanByteLengths(ctx, turn, scanJobByteLengths, c.request.scope.runID)
	if err != nil {
		return err
	}
	root, err := readScanByteLengths(ctx, turn, scanRootByteLengths, c.request.scope.runID, c.request.directory.path)
	if err != nil {
		return err
	}
	library, err := readScanByteLengths(ctx, turn, scanInventoryLibraryRecordLengths, c.request.scope.libraryKey)
	if err != nil {
		return err
	}
	actual, err := inventoryRecordActualBytes([]string{c.request.scope.runID, c.request.scope.runID, c.request.directory.path}, run, job, root, library)
	if err != nil {
		return err
	}
	if plan.inventoryRecords.maxBytes == 0 || actual > plan.inventoryRecords.maxBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	fresh, err := inventoryFreshRequiredBytes(c)
	if err != nil {
		return err
	}
	if actual > (math.MaxUint64-scanByteFixedWorkspace)/32 {
		return ErrCap
	}
	native := scanByteFixedWorkspace + 32*actual
	if fresh > math.MaxUint64-native {
		return ErrCap
	}
	if err = turn.requireInventoryMemory(ctx, fresh+native); err != nil {
		return err
	}
	// The installed envelope's COMPLETE native work was reserved before BEGIN.
	// No per-record amount is minted here and no extra holding is borrowed. Its
	// IO is part of the same original request; a mismatch refuses before effects.
	if plan.inventoryRecords.work.WALBytes < actual || plan.need[governor.IOBytes] <= 0 || uint64(plan.need[governor.IOBytes]) < actual {
		return &Refusal{Code: RefusalCapacity}
	}
	return turn.transaction.before(plan.inventoryRecords.work)
}
