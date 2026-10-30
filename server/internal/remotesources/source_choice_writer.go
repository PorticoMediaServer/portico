package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// sourceChoiceApply is the committed half of a startChoice command. It is used
// only after the accepting transaction commits. Its mount-cache child uses
// the same-transaction continuation; receipt replay never repeats effects.
type sourceChoiceApply struct {
	source           Source
	values           SourceAdvanced
	ref              sourceChoiceRef
	cacheNeeded      bool
	transportChanged bool
}

// applySourceAdvancedValuesTx is the sole Advanced row writer body. The caller
// owns the transaction, owner/generation/revision checks and optional-field
// merge. Zero scan interval clears the real enabled library schedules so
// "Only when I ask" stops automatic work rather than merely storing zero.
func (s *Service) applySourceAdvancedValuesTx(ctx context.Context, tx *sql.Tx, source string, current advancedRow, values SourceAdvanced) (advancedRow, error) {
	if tx == nil || source == "" || !values.valid() {
		return advancedRow{}, storage.ErrRemoteConfig
	}
	if current.revision == 1<<63-1 {
		return advancedRow{}, storage.ErrRemoteLimit
	}
	revision := current.revision
	nextRevision := revision + 1
	if revision == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO remote_source_settings(source_id,revision,scan_interval_seconds,watch_for_changes,scan_on_start,full_recheck_days,cache_bytes,cache_floor_bytes,seek_previews,chapter_images,waveforms,loudness,checksums,intros_mode,double_check_folders,dir_cache_seconds,read_buffer_bytes,connect_timeout_seconds,idle_timeout_seconds,retries,low_level_retries) VALUES(?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			source, values.ScanIntervalSeconds, values.WatchForChanges, values.ScanOnStart, values.FullRecheckDays, values.CacheBytes, values.CacheFloorBytes, values.SeekPreviews, values.ChapterImages, values.Waveforms, values.Loudness, values.Checksums, values.IntrosMode, values.DoubleCheckFolders, values.DirCacheSeconds, values.ReadBufferBytes, values.ConnectTimeoutSeconds, values.IdleTimeoutSeconds, values.Retries, values.LowLevelRetries); err != nil {
			return advancedRow{}, err
		}
	} else {
		result, err := tx.ExecContext(ctx, `UPDATE remote_source_settings SET revision=revision+1,scan_interval_seconds=?,watch_for_changes=?,scan_on_start=?,full_recheck_days=?,cache_bytes=?,cache_floor_bytes=?,seek_previews=?,chapter_images=?,waveforms=?,loudness=?,checksums=?,intros_mode=?,double_check_folders=?,dir_cache_seconds=?,read_buffer_bytes=?,connect_timeout_seconds=?,idle_timeout_seconds=?,retries=?,low_level_retries=? WHERE source_id=? AND revision=?`,
			values.ScanIntervalSeconds, values.WatchForChanges, values.ScanOnStart, values.FullRecheckDays, values.CacheBytes, values.CacheFloorBytes, values.SeekPreviews, values.ChapterImages, values.Waveforms, values.Loudness, values.Checksums, values.IntrosMode, values.DoubleCheckFolders, values.DirCacheSeconds, values.ReadBufferBytes, values.ConnectTimeoutSeconds, values.IdleTimeoutSeconds, values.Retries, values.LowLevelRetries, source, revision)
		if err != nil {
			return advancedRow{}, err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return advancedRow{}, storage.ErrRemoteChanged
		}
	}
	if values.ScanIntervalSeconds > 0 {
		// The drawn page owns these folders' check interval; the scanner reads
		// library_sources, so the same rows are updated here, once.
		next := time.Now().UTC().Add(time.Duration(values.ScanIntervalSeconds) * time.Second).Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE library_sources SET interval_seconds=?,next_scan_at=CASE WHEN next_scan_at='' OR next_scan_at>? THEN ? ELSE next_scan_at END WHERE remote_source_id=? AND interval_seconds IS NOT ?`, values.ScanIntervalSeconds, next, next, source, values.ScanIntervalSeconds); err != nil {
			return advancedRow{}, err
		}
	} else {
		// The ingestion scheduler gates on library_sources.interval_seconds>0
		// and next_scan_at; clear both for actual enabled folders.
		if _, err := tx.ExecContext(ctx, `UPDATE library_sources SET interval_seconds=0,next_scan_at='' WHERE remote_source_id=? AND enabled=1`, source); err != nil {
			return advancedRow{}, err
		}
	}
	if err := dbwork.AfterCommit(tx, func() { s.retireCaptureSettings(source, nextRevision) }); err != nil {
		return advancedRow{}, err
	}
	return advancedRow{
		values:                 values,
		saved:                  true,
		revision:               nextRevision,
		appliedBytes:           current.appliedBytes,
		appliedFloor:           current.appliedFloor,
		observedIntervalSecond: values.ScanIntervalSeconds,
	}, nil
}

// applySourceChoiceTx writes both settings halves in the caller's one accepting
// transaction. Its exact cache child may continue the recorded association in
// that child's accepting transaction; replay never repeats the cache effect.
func (s *Service) applySourceChoiceTx(ctx context.Context, tx *sql.Tx, identities *identity.Service, owner identity.Principal, source string, in SourceReadSettingsChange) (Source, SourceReadSettings, sourceChoiceApply, error) {
	var zero Source
	var zeroSettings SourceReadSettings
	var zeroApply sourceChoiceApply
	choice := in.StartChoice
	if choice == nil || source == "" || choice.Name == "" || choice.DefinitionRevision < 1 || choice.ExpectedAdvancedRevision < 0 ||
		!validSourceReadProfile(in.Values.profile(source)) {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteConfig
	}
	row, err := sourceRowTx(ctx, tx, source)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, zeroSettings, zeroApply, storage.ErrRemoteOffline
		}
		return zero, zeroSettings, zeroApply, err
	}
	if strconv.FormatInt(row.Generation, 10) != in.ExpectedGeneration {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteChanged
	}
	if !sourceChoiceGeneration(choice.ExpectedConfigurationGeneration) {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteConfig
	}
	effective, err := observationGenerationTx(ctx, tx, source)
	if err != nil {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteChanged
	}
	if effective != choice.ExpectedConfigurationGeneration {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteChanged
	}
	definition, err := readSourceChoiceDefinitionTx(ctx, tx, choice.Name, choice.DefinitionRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteConfig
	}
	if err != nil {
		return zero, zeroSettings, zeroApply, err
	}
	if !sourceChoiceReadingMatches(definition, in.Values) {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteConfig
	}
	reading, err := SourceReadProfileTx(ctx, tx, source)
	if err != nil {
		return zero, zeroSettings, zeroApply, err
	}
	advanced, err := readAdvancedTx(ctx, tx, source)
	if err != nil {
		return zero, zeroSettings, zeroApply, err
	}
	if reading.Revision != in.ExpectedRevision || advanced.revision != choice.ExpectedAdvancedRevision {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteChanged
	}
	applicable := sourceChoiceApplicabilityForSource(row)
	values := sourceChoicePreserveInapplicable(definition.Advanced, advanced.values, applicable)
	if !values.valid() {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteConfig
	}
	profile := definition.Reading.profile(source)
	// Retired compatibility values and explicit analysis consent are outside a
	// preset's authored rows. Only the four actual read controls are applied.
	profile.WholeFileReads = reading.WholeFileReads
	readResult, err := s.ApplySourceReadProfileTx(ctx, tx, identities, owner, SourceReadProfileChange{SourceID: source, Generation: effective, ExpectedRevision: reading.Revision, Profile: profile})
	if err != nil {
		return zero, zeroSettings, zeroApply, err
	}
	if readResult.Profile.Revision < 1 {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteChanged
	}
	written, err := s.applySourceAdvancedValuesTx(ctx, tx, source, advanced, values)
	if err != nil {
		return zero, zeroSettings, zeroApply, err
	}
	if written.revision < 1 {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteChanged
	}
	configGeneration, err := strconv.ParseInt(effective, 10, 64)
	if err != nil || configGeneration < 1 {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteChanged
	}
	fingerprint, err := s.sourceChoiceConfigFingerprintTx(ctx, tx, source)
	if err != nil {
		return zero, zeroSettings, zeroApply, err
	}
	ref := sourceChoiceRef{SourceID: source, SourceRowGeneration: row.Generation, ConfigGeneration: configGeneration,
		MountID: row.MountID, ConfigFingerprint: fingerprint, Name: definition.Name, DefinitionRevision: definition.DefinitionRevision,
		AppliedReadRevision: readResult.Profile.Revision, AppliedAdvancedRevision: written.revision, OperationID: in.OperationID, AppliedMS: time.Now().UnixMilli()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO remote_source_choice_refs(source_id,source_row_generation,config_generation,mount_id,config_fingerprint,name,definition_revision,applied_read_revision,applied_advanced_revision,operation_id,applied_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET source_row_generation=excluded.source_row_generation,config_generation=excluded.config_generation,mount_id=excluded.mount_id,config_fingerprint=excluded.config_fingerprint,name=excluded.name,definition_revision=excluded.definition_revision,applied_read_revision=excluded.applied_read_revision,applied_advanced_revision=excluded.applied_advanced_revision,operation_id=excluded.operation_id,applied_ms=excluded.applied_ms`,
		ref.SourceID, ref.SourceRowGeneration, ref.ConfigGeneration, ref.MountID, ref.ConfigFingerprint, ref.Name, ref.DefinitionRevision, ref.AppliedReadRevision, ref.AppliedAdvancedRevision, ref.OperationID, ref.AppliedMS); err != nil {
		return zero, zeroSettings, zeroApply, err
	}
	_, settings, err := s.readSourceReadSettingsProjectionTx(ctx, tx, source)
	if err != nil {
		return zero, zeroSettings, zeroApply, err
	}
	if settings.ChoiceBaseline == nil {
		return zero, zeroSettings, zeroApply, storage.ErrRemoteChanged
	}
	apply := sourceChoiceApply{source: row, values: values, ref: ref,
		cacheNeeded: row.MountID != "" && (advanced.values.CacheBytes != values.CacheBytes || advanced.values.CacheFloorBytes != values.CacheFloorBytes ||
			advanced.appliedBytes != values.CacheBytes || advanced.appliedFloor != values.CacheFloorBytes),
		transportChanged: advanced.values.transport() != values.transport()}
	return row, settings, apply, nil
}

// finishSourceChoice performs no acceptance or replay. A refused/unfinished
// cache command leaves the saved configuration honest as not yet applied. Its
// successful child and reference/applied projection publish in one mount Tx.
func (s *Service) finishSourceChoice(ctx context.Context, identities *identity.Service, owner identity.Principal, apply sourceChoiceApply) {
	if apply.cacheNeeded {
		if operation, err := s.sourceChoiceCacheOperation(apply.ref); err == nil {
			_, _ = s.applyCache(ctx, identities, owner, cacheApply{admitted: apply.source, revision: apply.ref.AppliedAdvancedRevision, values: apply.values,
				operationID: operation, configurationGeneration: apply.ref.ConfigGeneration,
				accepted: s.sourceChoiceCacheAccepted(identities, owner, apply.source, apply.ref, apply.values)})
		}
	}
	if apply.transportChanged {
		s.transportCommitted(apply.source.ID)
	}
}
