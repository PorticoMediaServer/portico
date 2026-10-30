package remotesources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// sourceChoiceRef is the recorded exact association for one source. It is
// private bookkeeping, not a public baseline and not a custody claim.
type sourceChoiceRef struct {
	SourceID                string
	SourceRowGeneration     int64
	ConfigGeneration        int64
	MountID                 string
	ConfigFingerprint       string
	Name                    string
	DefinitionRevision      int64
	AppliedReadRevision     int64
	AppliedAdvancedRevision int64
	OperationID             string
	AppliedMS               int64
}

func readSourceChoiceRefTx(ctx context.Context, tx *sql.Tx, source string) (sourceChoiceRef, error) {
	var out sourceChoiceRef
	err := tx.QueryRowContext(ctx, `SELECT source_id,source_row_generation,config_generation,mount_id,config_fingerprint,name,definition_revision,applied_read_revision,applied_advanced_revision,operation_id,applied_ms FROM remote_source_choice_refs WHERE source_id=?`, source).Scan(
		&out.SourceID, &out.SourceRowGeneration, &out.ConfigGeneration, &out.MountID, &out.ConfigFingerprint, &out.Name, &out.DefinitionRevision,
		&out.AppliedReadRevision, &out.AppliedAdvancedRevision, &out.OperationID, &out.AppliedMS)
	return out, err
}

// readSourceChoiceDefinitionsTx publishes exactly the four authored v1
// definitions, in the same fixed order as the drawn choices. Each version is
// resolved by the foundation's exact SQL lookup; no newer version or code
// default is substituted.
func readSourceChoiceDefinitionsTx(ctx context.Context, tx *sql.Tx) ([]SourceChoiceDefinition, error) {
	names := [...]string{"list", "once", "everything", "ask"}
	out := make([]SourceChoiceDefinition, 0, len(names))
	for _, name := range names {
		definition, err := readSourceChoiceDefinitionTx(ctx, tx, name, 1)
		if errors.Is(err, sql.ErrNoRows) {
			// Optional definitions are absent; keep current read/Advanced facts
			// and omit the explicit-choice projection.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if definition.DefinitionRevision < 1 || definition.DefinitionRevision > 9007199254740991 || !validSourceReadProfile(definition.Reading.profile("definition")) || !definition.Advanced.valid() {
			return nil, nil
		}
		out = append(out, definition)
	}
	return out, nil
}

// sourceChoiceConfigFingerprintTx hashes the opened source configuration, not
// its sealed bytes: resealing the same configuration must not look changed.
// The fingerprint is private comparison evidence and is never projected.
func (s *Service) sourceChoiceConfigFingerprintTx(ctx context.Context, tx *sql.Tx, source string) (string, error) {
	if s == nil || s.mounts == nil || tx == nil || source == "" {
		return "", storage.ErrRemoteConfig
	}
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT config FROM remote_sources WHERE id=? AND removed=0`, source).Scan(&sealed); err != nil {
		return "", err
	}
	raw, err := s.mounts.Open(sealed)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// sourceChoiceBaselineTx emits only an exact coherent recorded association.
// Absence, a changed raw/effective generation, mount, configuration, a missing
// exact definition, or an applied revision newer than the current row all omit
// the baseline. Legacy sources therefore receive no fabricated Started-from,
// count, or reset baseline.
func (s *Service) sourceChoiceBaselineTx(ctx context.Context, tx *sql.Tx, source Source, reading SourceReadProfile, advanced advancedRow) (*SourceChoiceBaseline, error) {
	if !reading.Saved || !advanced.saved || reading.Revision < 1 || reading.Revision > 9007199254740991 || advanced.revision < 1 || advanced.revision > 9007199254740991 ||
		!validSourceReadProfile(reading) || !advanced.values.valid() {
		return nil, nil
	}
	ref, err := readSourceChoiceRefTx(ctx, tx, source.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	effective, err := observationGenerationTx(ctx, tx, source.ID)
	if err != nil {
		return nil, nil
	}
	if ref.SourceRowGeneration != source.Generation || ref.MountID != source.MountID || strconv.FormatInt(ref.ConfigGeneration, 10) != effective {
		return nil, nil
	}
	fingerprint, err := s.sourceChoiceConfigFingerprintTx(ctx, tx, source.ID)
	if err != nil || fingerprint != ref.ConfigFingerprint {
		return nil, nil
	}
	definition, err := readSourceChoiceDefinitionTx(ctx, tx, ref.Name, ref.DefinitionRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !validSourceReadProfile(definition.Reading.profile(source.ID)) || !definition.Advanced.valid() {
		return nil, nil
	}
	if ref.AppliedReadRevision > reading.Revision || ref.AppliedAdvancedRevision > advanced.revision {
		return nil, nil
	}
	applicable := sourceChoiceApplicabilityForSource(source)
	changed := sourceChoiceChangedFields(definition, applicable, sourceReadValues(reading), advanced.values)
	if changed == nil {
		changed = []string{}
	}
	return &SourceChoiceBaseline{
		SourceID:                source.ID,
		Generation:              strconv.FormatInt(source.Generation, 10),
		ConfigurationGeneration: effective,
		Name:                    definition.Name,
		DefinitionRevision:      definition.DefinitionRevision,
		ReadRevision:            reading.Revision,
		AdvancedRevision:        advanced.revision,
		Reading:                 definition.Reading,
		Advanced:                definition.Advanced,
		CurrentAdvanced:         advanced.values,
		ApplicableFields:        sourceChoiceApplicableFields(source),
		ChangedFields:           changed,
	}, nil
}

// readSourceReadSettingsProjectionTx is the one coherent GET snapshot. It reads
// the current read profile, the bounded four definitions, the current Advanced
// row and an optional exact recorded association without writing anything.
func (s *Service) readSourceReadSettingsProjectionTx(ctx context.Context, tx *sql.Tx, source string) (Source, SourceReadSettings, error) {
	row, settings, err := readSettingsTx(ctx, tx, source)
	if err != nil {
		return row, settings, err
	}
	definitions, err := readSourceChoiceDefinitionsTx(ctx, tx)
	if err != nil {
		return row, settings, err
	}
	reading, err := storedSourceReadProfileTx(ctx, tx, source)
	if err != nil {
		return row, settings, err
	}
	advanced, err := readAdvancedTx(ctx, tx, source)
	if err != nil {
		return row, settings, err
	}
	settings.ChoiceDefinitions = definitions
	settings.ChoiceApplicableFields = sourceChoiceApplicableFields(row)
	var baseline *SourceChoiceBaseline
	if definitions != nil {
		baseline, err = s.sourceChoiceBaselineTx(ctx, tx, row, reading, advanced)
		if err != nil {
			return row, settings, err
		}
	}
	settings.ChoiceBaseline = baseline
	return row, settings, nil
}
