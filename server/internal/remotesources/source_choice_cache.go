package remotesources

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// One deterministic, domain-separated child operation belongs to the original
// explicit source operation. It is not a new user intent or a latest-state key.
func (s *Service) sourceChoiceCacheOperation(ref sourceChoiceRef) (string, error) {
	if s == nil || s.mounts == nil || !opPattern.MatchString(ref.OperationID) {
		return "", storage.ErrRemoteConfig
	}
	digest := s.mounts.PrivateDigest([]byte("source-choice-cache-v1\x00" + ref.SourceID + "\x00" + ref.OperationID))
	if len(digest) != 64 {
		return "", storage.ErrRemoteConfig
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s-%s", ref.OperationID[:13], digest[:8], digest[8:12], digest[12:16], digest[16:20], digest[20:32]), nil
}

// sourceChoiceCacheAccepted continues exactly one recorded association to the
// child published by its own cache command. Mounts invokes it in that command's
// accepting transaction, after source publication and before receipt/commit.
// No provider I/O, latest lookup or definition substitution is possible here.
func (s *Service) sourceChoiceCacheAccepted(identities *identity.Service, owner identity.Principal, source Source, original sourceChoiceRef, values SourceAdvanced) func(context.Context, *sql.Tx, mounts.Mount, mounts.Mount) error {
	return func(ctx context.Context, tx *sql.Tx, before, child mounts.Mount) error {
		if ctx == nil || tx == nil {
			return storage.ErrRemoteConfig
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := currentLocalOwnerTx(ctx, tx, identities, owner); err != nil {
			return err
		}
		if before.ID != source.MountID || child.ID != before.ID || original.MountID != before.ID ||
			before.BackendRevision != original.ConfigGeneration || before.BackendRevision == 1<<63-1 || child.BackendRevision != before.BackendRevision+1 ||
			before.ControlRevision == 1<<63-1 || child.ControlRevision != before.ControlRevision+1 ||
			before.MountPath != source.RootPath || child.MountPath != before.MountPath ||
			child.Executable != before.Executable || child.ProgramIncluded != before.ProgramIncluded ||
			child.CacheBytes != values.CacheBytes || child.CacheFloor != effectiveFloor(values.CacheFloorBytes, before.CacheFloor) {
			return storage.ErrRemoteChanged
		}
		// The old reference has not been replaced by a newer choice/operation, even
		// when the source settings happen to have equal values.
		ref, err := readSourceChoiceRefTx(ctx, tx, source.ID)
		if err != nil {
			return err
		}
		if ref != original {
			return storage.ErrRemoteChanged
		}
		row, err := sourceRowTx(ctx, tx, source.ID)
		if err != nil {
			return err
		}
		effective, err := observationGenerationTx(ctx, tx, source.ID)
		if err != nil {
			return storage.ErrRemoteChanged
		}
		if row.MountID != child.ID || row.Generation != child.BackendRevision || effective != strconv.FormatInt(child.BackendRevision, 10) ||
			row.Kind != source.Kind || row.RootPath != source.RootPath {
			return storage.ErrRemoteChanged
		}
		reading, err := SourceReadProfileTx(ctx, tx, source.ID)
		if err != nil {
			return err
		}
		advanced, err := readAdvancedTx(ctx, tx, source.ID)
		if err != nil {
			return err
		}
		if !reading.Saved || !advanced.saved || reading.Revision != original.AppliedReadRevision || advanced.revision != original.AppliedAdvancedRevision ||
			advanced.values.CacheBytes != values.CacheBytes || advanced.values.CacheFloorBytes != values.CacheFloorBytes {
			return storage.ErrRemoteChanged
		}
		fingerprint, err := s.sourceChoiceConfigFingerprintTx(ctx, tx, source.ID)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE remote_source_choice_refs SET source_row_generation=?,config_generation=?,config_fingerprint=? WHERE source_id=? AND source_row_generation=? AND config_generation=? AND mount_id=? AND config_fingerprint=? AND name=? AND definition_revision=? AND applied_read_revision=? AND applied_advanced_revision=? AND operation_id=? AND applied_ms=?`,
			row.Generation, child.BackendRevision, fingerprint, original.SourceID, original.SourceRowGeneration, original.ConfigGeneration, original.MountID, original.ConfigFingerprint,
			original.Name, original.DefinitionRevision, original.AppliedReadRevision, original.AppliedAdvancedRevision, original.OperationID, original.AppliedMS)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return storage.ErrRemoteChanged
		}
		result, err = tx.ExecContext(ctx, `UPDATE remote_source_settings SET cache_applied_bytes=?,cache_applied_floor_bytes=? WHERE source_id=? AND revision=? AND cache_bytes=? AND cache_floor_bytes=?`,
			child.CacheBytes, child.CacheFloor, original.SourceID, original.AppliedAdvancedRevision, values.CacheBytes, values.CacheFloorBytes)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return storage.ErrRemoteChanged
		}
		return ctx.Err()
	}
}
