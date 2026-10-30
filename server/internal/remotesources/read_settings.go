package remotesources

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// SourceReadValues is the existing normalized read-profile subset of the
// source's Reading settings. It does not stand in for scheduling or caching.
type SourceReadValues struct {
	ScanDepth                  string `json:"scanDepth"`
	WholeFileReads             bool   `json:"wholeFileReads"`
	Connections                int    `json:"connections"`
	RequestsPerSecond          int    `json:"requestsPerSecond"`
	BytesPerSecond             int64  `json:"bytesPerSecond"`
	AllowAdvancedMediaAnalysis *bool  `json:"allowAdvancedMediaAnalysis,omitempty"`
}

// Decode one closed object without allowing JSON's last-key-wins behavior to
// turn a duplicated consent field into an explicit accepting command.
func readSettingsFields(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, storage.ErrRemoteConfig
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok {
			return nil, storage.ErrRemoteConfig
		}
		if _, exists := fields[name]; exists {
			return nil, storage.ErrRemoteConfig
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, storage.ErrRemoteConfig
		}
		fields[name] = value
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return nil, storage.ErrRemoteConfig
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, storage.ErrRemoteConfig
	}
	return fields, nil
}

// A complete settings value is required, particularly for false and zero:
// omitted/null fields must not silently switch off a restriction.
func (v *SourceReadValues) UnmarshalJSON(raw []byte) error {
	fields, err := readSettingsFields(raw)
	if err != nil || len(fields) < 5 || len(fields) > 6 {
		return storage.ErrRemoteConfig
	}
	for name := range fields {
		switch name {
		case "scanDepth", "wholeFileReads", "connections", "requestsPerSecond", "bytesPerSecond", "allowAdvancedMediaAnalysis":
		default:
			return storage.ErrRemoteConfig
		}
	}
	if consent, present := fields["allowAdvancedMediaAnalysis"]; present && bytes.Equal(bytes.TrimSpace(consent), []byte("null")) {
		return storage.ErrRemoteConfig
	}
	for _, name := range []string{"scanDepth", "wholeFileReads", "connections", "requestsPerSecond", "bytesPerSecond"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return storage.ErrRemoteConfig
		}
	}
	type plain SourceReadValues
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return storage.ErrRemoteConfig
	}
	*v = SourceReadValues(decoded)
	return nil
}

type SourceReadSettings struct {
	SourceID                string                   `json:"sourceId"`
	Generation              string                   `json:"generation"`
	ConfigurationGeneration string                   `json:"configurationGeneration,omitempty"`
	Revision                int64                    `json:"revision"`
	Saved                   bool                     `json:"saved"`
	Values                  SourceReadValues         `json:"values"`
	ChoiceDefinitions       []SourceChoiceDefinition `json:"choiceDefinitions,omitempty"`
	ChoiceApplicableFields  []string                 `json:"choiceApplicableFields,omitempty"`
	ChoiceBaseline          *SourceChoiceBaseline    `json:"choiceBaseline,omitempty"`
}

type SourceReadSettingsChange struct {
	OperationID        string             `json:"operationId"`
	ExpectedGeneration string             `json:"expectedGeneration"`
	ExpectedRevision   int64              `json:"expectedRevision"`
	Values             SourceReadValues   `json:"values"`
	StartChoice        *SourceStartChoice `json:"startChoice,omitempty"`
}

func (v *SourceReadSettingsChange) UnmarshalJSON(raw []byte) error {
	fields, err := readSettingsFields(raw)
	if err != nil || len(fields) < 4 || len(fields) > 5 {
		return storage.ErrRemoteConfig
	}
	for _, name := range []string{"operationId", "expectedGeneration", "expectedRevision", "values"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return storage.ErrRemoteConfig
		}
	}
	for name, value := range fields {
		if name != "operationId" && name != "expectedGeneration" && name != "expectedRevision" && name != "values" && name != "startChoice" {
			return storage.ErrRemoteConfig
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return storage.ErrRemoteConfig
		}
	}
	type plain SourceReadSettingsChange
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return storage.ErrRemoteConfig
	}
	if rawChoice, ok := fields["startChoice"]; ok {
		choice, valid := decodeSourceStartChoice(rawChoice)
		if !valid {
			return storage.ErrRemoteConfig
		}
		decoded.StartChoice = &choice
	} else {
		decoded.StartChoice = nil
	}
	*v = SourceReadSettingsChange(decoded)
	return nil
}

func decodeSourceStartChoice(raw json.RawMessage) (SourceStartChoice, bool) {
	fields, err := readSettingsFields(raw)
	if err != nil || len(fields) != 4 {
		return SourceStartChoice{}, false
	}
	for _, name := range []string{"name", "definitionRevision", "expectedAdvancedRevision", "expectedConfigurationGeneration"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return SourceStartChoice{}, false
		}
	}
	for name := range fields {
		if name != "name" && name != "definitionRevision" && name != "expectedAdvancedRevision" && name != "expectedConfigurationGeneration" {
			return SourceStartChoice{}, false
		}
	}
	var out SourceStartChoice
	if json.Unmarshal(raw, &out) != nil {
		return SourceStartChoice{}, false
	}
	return out, true
}

func sourceReadValues(p SourceReadProfile) SourceReadValues {
	consent := p.AllowAdvancedMediaAnalysis
	return SourceReadValues{ScanDepth: p.ScanDepth, WholeFileReads: p.WholeFileReads, Connections: p.Limits.Connections, RequestsPerSecond: p.Limits.RequestsPerSecond, BytesPerSecond: p.Limits.BytesPerSecond, AllowAdvancedMediaAnalysis: &consent}
}

func (v SourceReadValues) profile(source string) SourceReadProfile {
	return SourceReadProfile{SourceID: source, ScanDepth: v.ScanDepth, WholeFileReads: v.WholeFileReads, Limits: ReadLimits{Connections: v.Connections, RequestsPerSecond: v.RequestsPerSecond, BytesPerSecond: v.BytesPerSecond}}
}

func readSettingsTx(ctx context.Context, tx *sql.Tx, source string) (Source, SourceReadSettings, error) {
	var row Source
	err := tx.QueryRowContext(ctx, `SELECT id,kind,name,root,generation,state,error_code,range_support,origin,insecure,credential_present,mount_id FROM remote_sources WHERE id=? AND removed=0`, source).Scan(&row.ID, &row.Kind, &row.Name, &row.RootPath, &row.Generation, &row.State, &row.ErrorCode, &row.RangeSupport, &row.Origin, &row.InsecureLocal, &row.CredentialPresent, &row.MountID)
	if err != nil {
		return row, SourceReadSettings{}, err
	}
	profile, err := storedSourceReadProfileTx(ctx, tx, source)
	if err != nil {
		return row, SourceReadSettings{}, err
	}
	settings := SourceReadSettings{SourceID: source, Generation: strconv.FormatInt(row.Generation, 10), Revision: profile.Revision, Saved: profile.Saved, Values: sourceReadValues(profile)}
	if configuration, e := observationGenerationTx(ctx, tx, source); e == nil {
		settings.ConfigurationGeneration = configuration
	}
	return row, settings, nil
}

func (s *Service) ReadSourceReadSettings(ctx context.Context, identities *identity.Service, owner identity.Principal, source string) (SourceReadSettings, error) {
	if s == nil || s.db == nil || source == "" {
		return SourceReadSettings{}, storage.ErrRemoteConfig
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return SourceReadSettings{}, err
	}
	defer snapshot.Rollback()
	if err = currentLocalOwnerTx(ctx, snapshot.Tx(), identities, owner); err != nil {
		return SourceReadSettings{}, err
	}
	_, out, err := s.readSourceReadSettingsProjectionTx(ctx, snapshot.Tx(), source)
	return out, err
}

// SaveSourceReadSettings is the owner command boundary around the sole profile
// writer. The existing source-operation receipt and profile/library revisions
// commit together. Its post-commit notification never changes source identity.
func (s *Service) SaveSourceReadSettings(ctx context.Context, identities *identity.Service, owner identity.Principal, source string, in SourceReadSettingsChange) (Receipt, error) {
	if s == nil || s.db == nil || s.mounts == nil || source == "" || in.ExpectedGeneration == "" || in.ExpectedRevision < 0 || !validSourceReadProfile(in.Values.profile(source)) {
		return Receipt{}, storage.ErrRemoteConfig
	}
	if in.Values.AllowAdvancedMediaAnalysis != nil {
		consent := *in.Values.AllowAdvancedMediaAnalysis
		in.Values.AllowAdvancedMediaAnalysis = &consent
	}
	if in.StartChoice != nil {
		choice := *in.StartChoice
		in.StartChoice = &choice
	}
	s.commands.Lock()
	defer s.commands.Unlock()
	authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }
	if err := s.authorize(ctx, authorize); err != nil {
		return Receipt{}, err
	}
	actorBytes, _ := json.Marshal([]string{owner.Authority, owner.AccountID, owner.ProfileID})
	actor := string(actorBytes)
	facts, _ := json.Marshal(struct {
		Kind, Actor, Source string
		Change              SourceReadSettingsChange
	}{"read_settings", actor, source, in})
	fingerprint := s.mounts.PrivateDigest(facts)
	clear(facts)
	_, _, protocol := usermutation.RequestFrom(ctx)
	if !protocol && strings.HasPrefix(in.OperationID, "p251_") {
		return Receipt{}, usermutation.ErrInvalidKey
	}
	if !protocol {
		if receipt, err := s.receipt(ctx, actor, in.OperationID, fingerprint); err == nil {
			if receipt.ReadSettings == nil {
				return Receipt{}, storage.ErrRemoteConfig
			}
			return receipt, s.finishReadSettings(ctx, source, receipt.ReadSettings.Revision)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Receipt{}, err
		}
	}
	if !protocol {
		if err := checkOperation(in.OperationID); err != nil {
			return Receipt{}, err
		}
	}
	writer, err := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if err != nil {
		return Receipt{}, err
	}
	defer writer.Rollback()
	if err = authorize(writer.Tx()); err != nil {
		return Receipt{}, err
	}
	r, err := usermutation.CheckContext(ctx, writer.Tx(), time.Now().UnixMilli())
	if err != nil {
		return Receipt{}, err
	}
	if r != nil {
		row, settings, err := readSettingsTx(ctx, writer.Tx(), source)
		writer.Rollback()
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{OperationID: in.OperationID, Accepted: true, Source: row, ReadSettings: &settings}, s.finishReadSettings(ctx, source, settings.Revision)
	}

	var row Source
	var settings SourceReadSettings
	var apply sourceChoiceApply
	if in.StartChoice != nil {
		row, settings, apply, err = s.applySourceChoiceTx(ctx, writer.Tx(), identities, owner, source, in)
	} else {
		var rowBefore Source
		rowBefore, _, err = readSettingsTx(ctx, writer.Tx(), source)
		if err == nil && strconv.FormatInt(rowBefore.Generation, 10) != in.ExpectedGeneration {
			err = storage.ErrRemoteChanged
		}
		var configuration string
		if err == nil {
			configuration, err = observationGenerationTx(ctx, writer.Tx(), source)
		}
		if err == nil {
			_, err = s.ApplySourceReadProfileTx(ctx, writer.Tx(), identities, owner, SourceReadProfileChange{SourceID: source, Generation: configuration, ExpectedRevision: in.ExpectedRevision, Profile: in.Values.profile(source), AdvancedMediaAnalysis: in.Values.AllowAdvancedMediaAnalysis})
		}
		if err == nil {
			row, settings, err = readSettingsTx(ctx, writer.Tx(), source)
		}
	}
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{OperationID: in.OperationID, Accepted: true, Source: row, ReadSettings: &settings}
	if protocol {
		if err = usermutation.SaveContext(ctx, writer.Tx(), usermutation.Result{Status: "applied", TargetID: source, Revision: settings.Revision}, time.Now().UnixMilli()); err != nil {
			return Receipt{}, err
		}
	} else {
		response, err := json.Marshal(receipt)
		if err != nil {
			return Receipt{}, err
		}
		if _, err = writer.Tx().ExecContext(ctx, `INSERT INTO remote_source_operations(operation_id,actor,fingerprint,response,created_at) VALUES(?,?,?,?,?)`, in.OperationID, actor, fingerprint, string(response), time.Now().Unix()); err != nil {
			return Receipt{}, err
		}
	}
	if err = writer.Commit(); err != nil {
		return Receipt{}, err
	}
	if in.StartChoice != nil {
		s.finishSourceChoice(ctx, identities, owner, apply)
	}
	return receipt, s.finishReadSettings(ctx, source, settings.Revision)
}

// A committed command must retire obsolete readers even when its HTTP request
// has ended. This bounded cleanup does only DB reads and cancellation: it never
// opens a source or grants authority. Receipt replay retries it without a write.
// Read the current profile, so an old receipt never retires its replacement.
// No request context values are inherited: they may hold an ended/stale SQL
// snapshot. This cleanup has no actor admission or provider-I/O path.
func (s *Service) finishReadSettings(_ context.Context, source string, committedRevision int64) error {
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot, err := dbwork.BeginSnapshot(cleanup, s.db)
	if err != nil {
		return err
	}
	var removed bool
	err = snapshot.Tx().QueryRowContext(cleanup, `SELECT removed FROM remote_sources WHERE id=?`, source).Scan(&removed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && removed {
		snapshot.Rollback()
		s.stopServeProcess(source)
		s.stopReadLeases(source, storage.ErrRemoteChanged, nil)
		return nil
	}
	if err != nil {
		snapshot.Rollback()
		return err
	}
	profile, err := SourceReadProfileTx(cleanup, snapshot.Tx(), source)
	snapshot.Rollback()
	if err != nil {
		return err
	}
	if !profile.Saved || profile.Revision < committedRevision {
		return storage.ErrRemoteChanged
	}
	s.retireReadProfile(source, profile)
	return nil
}
