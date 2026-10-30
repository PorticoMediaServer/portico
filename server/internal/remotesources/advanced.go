package remotesources

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"reflect"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// SourceAdvanced is the drawn Advanced page for one source: how often its
// folders are checked, whether it watches for changes, scans at start or asks
// for a full re-check, the disk the read cache may use, and which analysis
// steps run for files found through it. Every field is consumed by a real
// scanner, scheduler or analysis path; nothing is a stored-only toggle.
type SourceAdvanced struct {
	ScanIntervalSeconds int   `json:"scanIntervalSeconds"`
	WatchForChanges     bool  `json:"watchForChanges"`
	ScanOnStart         bool  `json:"scanOnStart"`
	FullRecheckDays     int   `json:"fullRecheckDays"`
	CacheBytes          int64 `json:"cacheBytes"`
	CacheFloorBytes     int64 `json:"cacheFloorBytes"`
	SeekPreviews        bool  `json:"seekPreviews"`
	ChapterImages       bool  `json:"chapterImages"`
	Waveforms           bool  `json:"waveforms"`
	Loudness            bool  `json:"loudness"`
	Checksums           bool  `json:"checksums"`
	// IntrosMode chooses whether intro and credit detection runs for the files
	// found through this source, and whether it follows the library's mode.
	IntrosMode string `json:"introsMode"`
	// DoubleCheckFolders lists each folder a second time during a scan, so a
	// move during the first listing is caught instead of silently missed.
	DoubleCheckFolders bool `json:"doubleCheckFolders"`
	// Transport values belong to a mounted source's rclone program. Zero keeps
	// the bundled program's own default, reported separately as the effective
	// value by SourceTransportEffective.
	DirCacheSeconds       int   `json:"dirCacheSeconds"`
	ReadBufferBytes       int64 `json:"readBufferBytes"`
	ConnectTimeoutSeconds int   `json:"connectTimeoutSeconds"`
	IdleTimeoutSeconds    int   `json:"idleTimeoutSeconds"`
	Retries               int   `json:"retries"`
	LowLevelRetries       int   `json:"lowLevelRetries"`
	// present records which optional wire fields a command actually sent, so
	// an older client that omits them keeps the saved values instead of
	// resetting them to zero.
	present map[string]bool
}

// SourceTransport is the mounted program's transport subset, applied to the
// rclone serve command of this source and to nothing else.
type SourceTransport struct {
	DirCacheSeconds       int
	ReadBufferBytes       int64
	ConnectTimeoutSeconds int
	IdleTimeoutSeconds    int
	Retries               int
	LowLevelRetries       int
}

// Effective fills zero values with the bundled program's current defaults, for
// display only; the stored values keep zero as "the program's default".
func (t SourceTransport) Effective() SourceTransport {
	out := t
	if out.ConnectTimeoutSeconds == 0 {
		out.ConnectTimeoutSeconds = 10
	}
	if out.IdleTimeoutSeconds == 0 {
		out.IdleTimeoutSeconds = 30
	}
	if out.Retries == 0 {
		out.Retries = 1
	}
	if out.LowLevelRetries == 0 {
		out.LowLevelRetries = 1
	}
	return out
}

// AnalysisSwitches is the analysis subset the scan pipeline consults for one
// run. It is the only part of the page the analysis path needs.
type AnalysisSwitches struct {
	advancedMediaAnalysis bool
	SeekPreviews          bool
	ChapterImages         bool
	Waveforms             bool
	Loudness              bool
	Checksums             bool
}

type SourceAdvancedSettings struct {
	SourceID       string `json:"sourceId"`
	Generation     string `json:"generation"`
	Revision       int64  `json:"revision"`
	Saved          bool   `json:"saved"`
	CacheSupported bool   `json:"cacheSupported"`
	// CacheApplied is the durable projection: the mount's applied cache equals
	// what this page asks for. A saved value whose mount change failed reads false.
	CacheApplied bool `json:"cacheApplied"`
	// CacheError names why the mount did not take the saved cache, when it did not.
	CacheError string `json:"cacheError,omitempty"`
	// CacheUsedBytes is what this source's own cache holds now.
	CacheUsedBytes int64 `json:"cacheUsedBytes"`
	// ScanIntervalObserved is the shortest interval the folders really have,
	// even when it is not one the page offers. It is reported, never relabelled.
	ScanIntervalObserved int            `json:"scanIntervalObserved"`
	Values               SourceAdvanced `json:"values"`
}

type SourceAdvancedChange struct {
	ExpectedGeneration string         `json:"expectedGeneration"`
	ExpectedRevision   int64          `json:"expectedRevision"`
	Values             SourceAdvanced `json:"values"`
}

var advancedRequiredFields = []string{"scanIntervalSeconds", "watchForChanges", "scanOnStart", "fullRecheckDays", "cacheBytes", "cacheFloorBytes", "seekPreviews", "chapterImages", "waveforms", "loudness", "checksums"}

var advancedOptionalFields = []string{"introsMode", "doubleCheckFolders", "dirCacheSeconds", "readBufferBytes", "connectTimeoutSeconds", "idleTimeoutSeconds", "retries", "lowLevelRetries"}

// A settings value always carries the original eleven fields, complete and
// non-null. The later fields are optional on the wire: an older client that
// omits them keeps what is saved, and an explicit zero, false or "inherit" is
// still a real choice.
func (v *SourceAdvanced) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return storage.ErrRemoteConfig
	}
	if len(fields) < len(advancedRequiredFields) || len(fields) > len(advancedRequiredFields)+len(advancedOptionalFields) {
		return storage.ErrRemoteConfig
	}
	known := map[string]bool{}
	for _, name := range advancedRequiredFields {
		known[name] = true
	}
	for _, name := range advancedOptionalFields {
		known[name] = true
	}
	for name, value := range fields {
		if !known[name] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return storage.ErrRemoteConfig
		}
	}
	for _, name := range advancedRequiredFields {
		if _, ok := fields[name]; !ok {
			return storage.ErrRemoteConfig
		}
	}
	type plain SourceAdvanced
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return storage.ErrRemoteConfig
	}
	*v = SourceAdvanced(decoded)
	v.present = make(map[string]bool, len(advancedOptionalFields))
	for _, name := range advancedOptionalFields {
		_, v.present[name] = fields[name]
	}
	return nil
}

// withOmitted fills fields an older command did not send from the saved values,
// inside the same authorized CAS transaction that saves the result.
func (v SourceAdvanced) withOmitted(current SourceAdvanced) SourceAdvanced {
	if v.present == nil {
		return v
	}
	if !v.present["introsMode"] {
		v.IntrosMode = current.IntrosMode
	}
	if !v.present["doubleCheckFolders"] {
		v.DoubleCheckFolders = current.DoubleCheckFolders
	}
	if !v.present["dirCacheSeconds"] {
		v.DirCacheSeconds = current.DirCacheSeconds
	}
	if !v.present["readBufferBytes"] {
		v.ReadBufferBytes = current.ReadBufferBytes
	}
	if !v.present["connectTimeoutSeconds"] {
		v.ConnectTimeoutSeconds = current.ConnectTimeoutSeconds
	}
	if !v.present["idleTimeoutSeconds"] {
		v.IdleTimeoutSeconds = current.IdleTimeoutSeconds
	}
	if !v.present["retries"] {
		v.Retries = current.Retries
	}
	if !v.present["lowLevelRetries"] {
		v.LowLevelRetries = current.LowLevelRetries
	}
	return v
}

// JSONSchema publishes the closed reason codes and the projection distinction
// on the page response, so the official schema carries them too.
func (SourceAdvancedSettings) JSONSchema() map[string]any {
	type wire SourceAdvancedSettings
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	props, _ := shape["properties"].(map[string]any)
	if props == nil {
		return shape
	}
	if field, ok := props["cacheError"].(map[string]any); ok {
		field["enum"] = []string{"source_changed", "mount_unavailable", "cache_refused", "cache_record_failed"}
	}
	if field, ok := props["cacheApplied"].(map[string]any); ok {
		field["description"] = "Observed projection of the cache the mount has actually applied, not the desired configuration."
	}
	return shape
}

func (c *SourceAdvancedChange) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 3 {
		return storage.ErrRemoteConfig
	}
	for _, name := range []string{"expectedGeneration", "expectedRevision", "values"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return storage.ErrRemoteConfig
		}
	}
	type plain SourceAdvancedChange
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return storage.ErrRemoteConfig
	}
	*c = SourceAdvancedChange(decoded)
	return nil
}

// JSONSchema publishes the exact domains Release generates from: the offered
// intervals and re-check days as enums, and the cache sizes as bounded ranges.
func (SourceAdvanced) JSONSchema() map[string]any {
	type wire SourceAdvanced
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	props, _ := shape["properties"].(map[string]any)
	if props == nil {
		return shape
	}
	if field, ok := props["scanIntervalSeconds"].(map[string]any); ok {
		field["enum"] = []int{0, 900, 3600, 21600, 86400, 604800}
	}
	if field, ok := props["fullRecheckDays"].(map[string]any); ok {
		field["enum"] = []int{0, 7, 14, 30}
	}
	if field, ok := props["cacheBytes"].(map[string]any); ok {
		field["description"] = "0 disables the read cache; otherwise 64 MiB through 64 GiB."
	}
	if field, ok := props["cacheFloorBytes"].(map[string]any); ok {
		field["description"] = "0 keeps the mount's current free-space floor; otherwise 1 GiB through 1 TiB."
	}
	if field, ok := props["introsMode"].(map[string]any); ok {
		field["enum"] = []string{"off", "inherit", "sound", "picture"}
	}
	// Only the original eleven fields are required; the later ones are additive.
	shape["required"] = append([]string(nil), advancedRequiredFields...)
	return shape
}

// SourceScanTask is one due scan the scheduler should enqueue for a source's
// folder. Mode is the ingestion scan mode; Watch marks the five-minute pass.
type SourceScanTask struct {
	SourceID  string
	LibraryID string
	FolderID  string
	Mode      string
	Watch     bool
	Full      bool
}

var scanIntervals = map[int]bool{0: true, 900: true, 3600: true, 21600: true, 86400: true, 604800: true}
var fullRecheckDays = map[int]bool{0: true, 7: true, 14: true, 30: true}

func defaultAdvanced() SourceAdvanced {
	return SourceAdvanced{ScanIntervalSeconds: 0, FullRecheckDays: 0, SeekPreviews: true, ChapterImages: true, Waveforms: true, Loudness: true, Checksums: true, IntrosMode: "inherit"}
}

func (v SourceAdvanced) valid() bool {
	if !scanIntervals[v.ScanIntervalSeconds] || !fullRecheckDays[v.FullRecheckDays] {
		return false
	}
	if v.CacheBytes != 0 && (v.CacheBytes < 64<<20 || v.CacheBytes > 64<<30) {
		return false
	}
	if v.CacheFloorBytes != 0 && (v.CacheFloorBytes < 1<<30 || v.CacheFloorBytes > 1<<40) {
		return false
	}
	if v.IntrosMode != "off" && v.IntrosMode != "inherit" && v.IntrosMode != "sound" && v.IntrosMode != "picture" {
		return false
	}
	if v.DirCacheSeconds < 0 || v.DirCacheSeconds > 86400 || v.ReadBufferBytes < 0 || v.ReadBufferBytes > 1<<30 ||
		v.ConnectTimeoutSeconds < 0 || v.ConnectTimeoutSeconds > 600 || v.IdleTimeoutSeconds < 0 || v.IdleTimeoutSeconds > 3600 ||
		v.Retries < 0 || v.Retries > 10 || v.LowLevelRetries < 0 || v.LowLevelRetries > 10 {
		return false
	}
	return true
}

func (v SourceAdvanced) analysis() AnalysisSwitches {
	return AnalysisSwitches{SeekPreviews: v.SeekPreviews, ChapterImages: v.ChapterImages, Waveforms: v.Waveforms, Loudness: v.Loudness, Checksums: v.Checksums}
}

// readAdvancedTx reads the saved page, or the defaults a new source starts from.
// A source with folders reports the shortest interval its folders already have,
// so the page never claims "follow the library" when a real interval is set.
type advancedRow struct {
	values                 SourceAdvanced
	saved                  bool
	revision               int64
	appliedBytes           int64
	appliedFloor           int64
	observedIntervalSecond int
}

func readAdvancedTx(ctx context.Context, tx *sql.Tx, source string) (advancedRow, error) {
	var row advancedRow
	err := tx.QueryRowContext(ctx, `SELECT revision,scan_interval_seconds,watch_for_changes,scan_on_start,full_recheck_days,cache_bytes,cache_floor_bytes,cache_applied_bytes,cache_applied_floor_bytes,seek_previews,chapter_images,waveforms,loudness,checksums,intros_mode,double_check_folders,dir_cache_seconds,read_buffer_bytes,connect_timeout_seconds,idle_timeout_seconds,retries,low_level_retries FROM remote_source_settings WHERE source_id=?`, source).
		Scan(&row.revision, &row.values.ScanIntervalSeconds, &row.values.WatchForChanges, &row.values.ScanOnStart, &row.values.FullRecheckDays, &row.values.CacheBytes, &row.values.CacheFloorBytes, &row.appliedBytes, &row.appliedFloor, &row.values.SeekPreviews, &row.values.ChapterImages, &row.values.Waveforms, &row.values.Loudness, &row.values.Checksums, &row.values.IntrosMode, &row.values.DoubleCheckFolders, &row.values.DirCacheSeconds, &row.values.ReadBufferBytes, &row.values.ConnectTimeoutSeconds, &row.values.IdleTimeoutSeconds, &row.values.Retries, &row.values.LowLevelRetries)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return advancedRow{}, err
	}
	row.saved = err == nil
	if !row.saved {
		row.values = defaultAdvanced()
	}
	// The folders' real interval is always reported as observed, whether or not
	// it is one this page offers; an unsupported one is never relabelled.
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(NULLIF(interval_seconds,0)),0) FROM library_sources WHERE remote_source_id=?`, source).Scan(&row.observedIntervalSecond); err != nil {
		return advancedRow{}, err
	}
	if !row.saved {
		if scanIntervals[row.observedIntervalSecond] {
			row.values.ScanIntervalSeconds = row.observedIntervalSecond
		}
	}
	return row, nil
}

// ReadSourceAdvanced is the owner's bounded read of one source's Advanced page.
func (s *Service) ReadSourceAdvanced(ctx context.Context, identities *identity.Service, owner identity.Principal, source string) (SourceAdvancedSettings, error) {
	if s == nil || s.db == nil || source == "" {
		return SourceAdvancedSettings{}, storage.ErrRemoteConfig
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return SourceAdvancedSettings{}, err
	}
	defer snapshot.Rollback()
	if err = currentLocalOwnerTx(ctx, snapshot.Tx(), identities, owner); err != nil {
		return SourceAdvancedSettings{}, err
	}
	row, err := sourceRowTx(ctx, snapshot.Tx(), source)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SourceAdvancedSettings{}, storage.ErrRemoteOffline
		}
		return SourceAdvancedSettings{}, err
	}
	settings, err := readAdvancedTx(ctx, snapshot.Tx(), source)
	if err != nil {
		return SourceAdvancedSettings{}, err
	}
	out := advancedSettings(row, settings, "")
	out.CacheSupported = true
	if err = snapshot.Tx().QueryRowContext(ctx, `SELECT COALESCE(SUM(used_bytes),0) FROM remote_source_cache_entries WHERE source_id=?`, source).Scan(&out.CacheUsedBytes); err != nil {
		return SourceAdvancedSettings{}, err
	}
	return out, nil
}

func advancedSettings(row Source, settings advancedRow, cacheError string) SourceAdvancedSettings {
	// Applied is read from the stored projection alone: asking to disable the
	// cache is only applied once the mount really reports no cache.
	var applied bool
	if row.MountID == "" {
		// The server's own cache is in force as soon as the page is saved.
		applied = settings.saved
	} else {
		// A mounted cache is applied only when its stored projection matches,
		// including a requested disable (zero) that the mount has not taken yet.
		applied = settings.appliedBytes == settings.values.CacheBytes &&
			(settings.values.CacheFloorBytes == 0 || settings.appliedFloor == settings.values.CacheFloorBytes)
	}
	return SourceAdvancedSettings{
		SourceID:             row.ID,
		Generation:           strconv.FormatInt(row.Generation, 10),
		Revision:             settings.revision,
		Saved:                settings.saved,
		CacheSupported:       true,
		CacheApplied:         applied,
		CacheError:           cacheError,
		ScanIntervalObserved: settings.observedIntervalSecond,
		Values:               settings.values,
	}
}

// SaveSourceAdvanced stores one CAS write and applies it: the folders' scan
// interval and next check, and the mount's real read cache. The folder update
// is what the scanner already reads; no second scheduler is invented.
func (s *Service) SaveSourceAdvanced(ctx context.Context, identities *identity.Service, owner identity.Principal, source string, change SourceAdvancedChange) (SourceAdvancedSettings, error) {
	if s == nil || s.db == nil || source == "" {
		return SourceAdvancedSettings{}, storage.ErrRemoteConfig
	}
	s.commands.Lock()
	defer s.commands.Unlock()
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if err != nil {
		return SourceAdvancedSettings{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = currentLocalOwnerTx(ctx, tx, identities, owner); err != nil {
		return SourceAdvancedSettings{}, err
	}
	r, err := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if err != nil {
		return SourceAdvancedSettings{}, err
	}
	if r != nil {
		gated.Rollback()
		return s.ReadSourceAdvanced(ctx, identities, owner, source)
	}
	row, err := sourceRowTx(ctx, tx, source)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SourceAdvancedSettings{}, storage.ErrRemoteOffline
		}
		return SourceAdvancedSettings{}, err
	}
	generation := strconv.FormatInt(row.Generation, 10)
	if change.ExpectedGeneration != generation {
		return SourceAdvancedSettings{}, storage.ErrRemoteChanged
	}
	current, err := readAdvancedTx(ctx, tx, source)
	if err != nil {
		return SourceAdvancedSettings{}, err
	}
	revision := current.revision
	if change.ExpectedRevision != revision {
		return SourceAdvancedSettings{}, storage.ErrRemoteChanged
	}
	v := change.Values
	if v.present != nil {
		v = v.withOmitted(current.values)
	}
	if !v.valid() {
		return SourceAdvancedSettings{}, storage.ErrRemoteConfig
	}
	settings, err := s.applySourceAdvancedValuesTx(ctx, tx, source, current, v)
	if err != nil {
		return SourceAdvancedSettings{}, err
	}
	if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: source, Revision: settings.revision}, time.Now().UnixMilli()); err != nil {
		return SourceAdvancedSettings{}, err
	}
	if err = gated.Commit(); err != nil {
		return SourceAdvancedSettings{}, err
	}
	if row.MountID == "" {
		// A source read directly by the server keeps its cache itself: the saved
		// size is what its cache uses, and nothing mount-side has to apply it.
		settings.appliedBytes, settings.appliedFloor = v.CacheBytes, v.CacheFloorBytes
	}
	cacheError := ""
	if row.MountID != "" {
		apply := cacheApply{admitted: row, revision: settings.revision, values: v}
		var applied bool
		applied, cacheError = s.applyCache(ctx, identities, owner, apply)
		if applied {
			settings.appliedBytes, settings.appliedFloor = v.CacheBytes, effectiveFloor(v.CacheFloorBytes, current.appliedFloor)
			// The projection belongs to the exact saved revision it describes.
			result, e := dbwork.ExecWrite(ctx, s.db, dbwork.ClassInteractive, `UPDATE remote_source_settings SET cache_applied_bytes=?,cache_applied_floor_bytes=? WHERE source_id=? AND revision=?`, settings.appliedBytes, settings.appliedFloor, source, settings.revision)
			if e != nil {
				applied = false
				cacheError = cacheReasonCacheLost
			} else if n, _ := result.RowsAffected(); n != 1 {
				applied = false
				cacheError = cacheReasonChanged
			}
		}
	}
	if current.values.transport() != v.transport() {
		s.transportCommitted(source)
	}
	out := advancedSettings(row, settings, cacheError)
	out.CacheSupported = true
	if used, e := s.cacheUsed(ctx, source); e == nil {
		out.CacheUsedBytes = used
	}
	return out, nil
}

func (v SourceAdvanced) transport() SourceTransport {
	return SourceTransport{DirCacheSeconds: v.DirCacheSeconds, ReadBufferBytes: v.ReadBufferBytes, ConnectTimeoutSeconds: v.ConnectTimeoutSeconds, IdleTimeoutSeconds: v.IdleTimeoutSeconds, Retries: v.Retries, LowLevelRetries: v.LowLevelRetries}
}

// transportCommitted retires a source's serve process after its transport
// values changed, so the next read starts the program with the saved values.
func (s *Service) transportCommitted(source string) {
	s.serveMu.Lock()
	if lease := s.serveLeases[source]; lease != nil {
		lease.cancel()
	}
	s.serveMu.Unlock()
}

// cacheApply is the exact accepted settings a cache change belongs to.
type cacheApply struct {
	admitted Source
	revision int64
	values   SourceAdvanced
	// Non-wire continuation for one recorded choice cache operation only.
	operationID             string
	configurationGeneration int64
	accepted                func(context.Context, *sql.Tx, mounts.Mount, mounts.Mount) error
}

const (
	cacheReasonChanged     = "source_changed"
	cacheReasonUnavailable = "mount_unavailable"
	cacheReasonRefused     = "cache_refused"
	cacheReasonCacheLost   = "cache_record_failed"
)

// applyCache changes a mounted source's real read cache through the existing
// mount command. Its authorizer re-checks the captured owner, source
// generation, root, mount and saved settings revision in the mount writer's
// own transaction, so a change accepted after the settings moved on is refused
// rather than applied to the wrong source. Reasons are closed codes, never
// provider or database text.
func (s *Service) applyCache(ctx context.Context, identities *identity.Service, owner identity.Principal, accept cacheApply) (bool, string) {
	if s.mounts == nil || accept.admitted.MountID == "" {
		return false, cacheReasonUnavailable
	}
	list, err := s.mounts.List()
	if err != nil {
		return false, cacheReasonUnavailable
	}
	controlRevision := int64(-1)
	for _, mount := range list {
		if mount.ID == accept.admitted.MountID {
			controlRevision = mount.ControlRevision
			break
		}
	}
	if controlRevision < 1 {
		return false, cacheReasonUnavailable
	}
	floor := effectiveFloor(accept.values.CacheFloorBytes, 0)
	cacheBytes := accept.values.CacheBytes
	authorize := func(tx *sql.Tx) error {
		if err := currentLocalOwnerTx(ctx, tx, identities, owner); err != nil {
			return err
		}
		current, err := sourceRowTx(ctx, tx, accept.admitted.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return storage.ErrRemoteChanged
			}
			return err
		}
		if current.Generation != accept.admitted.Generation || current.RootPath != accept.admitted.RootPath || current.MountID != accept.admitted.MountID || current.Kind != accept.admitted.Kind {
			return storage.ErrRemoteChanged
		}
		var revision, savedBytes, savedFloor int64
		if err = tx.QueryRowContext(ctx, `SELECT revision,cache_bytes,cache_floor_bytes FROM remote_source_settings WHERE source_id=?`, accept.admitted.ID).Scan(&revision, &savedBytes, &savedFloor); err != nil {
			return storage.ErrRemoteChanged
		}
		if revision != accept.revision || savedBytes != accept.values.CacheBytes || savedFloor != accept.values.CacheFloorBytes {
			return storage.ErrRemoteChanged
		}
		return nil
	}
	if floor == 0 {
		// No floor requested means keep the mount's current floor, read under
		// the same association check before the command runs.
		for _, mount := range list {
			if mount.ID == accept.admitted.MountID {
				floor = effectiveFloor(accept.values.CacheFloorBytes, mount.CacheFloor)
			}
		}
	}
	if accept.accepted != nil {
		_, err = s.mounts.ConfigureReadCache(ctx, owner.Hash, accept.admitted.MountID, accept.operationID,
			controlRevision, accept.configurationGeneration, cacheBytes, floor, authorize, accept.accepted)
	} else {
		_, err = s.mounts.Command(ctx, owner.Hash, accept.admitted.MountID, mounts.Command{
			OperationID: scanOperationID(), Action: "configure", ExpectedRevision: controlRevision,
			CacheBytes: &cacheBytes, CacheFloor: &floor,
		}, authorize)
	}
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrRemoteChanged), errors.Is(err, mounts.ErrCommandConflict), errors.Is(err, mounts.ErrCommandExpired):
			return false, cacheReasonChanged
		case errors.Is(err, mounts.ErrUnavailable):
			return false, cacheReasonUnavailable
		default:
			return false, cacheReasonRefused
		}
	}
	return true, ""
}

func effectiveFloor(requested, current int64) int64 {
	if requested <= 0 {
		return current
	}
	return requested
}

func scanOperationID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), identity.Token())
}

// DueSourceScans lists the scans a source's own page asks for now: the
// five-minute watch pass, the scan at server start, and a full re-check whose
// interval has passed. The caller enqueues them through the normal pipeline.
func (s *Service) DueSourceScans(ctx context.Context) ([]SourceScanTask, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	now := time.Now().UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT st.source_id,r.root,st.watch_for_changes,st.scan_on_start,st.full_recheck_days,st.last_full_recheck_ms,st.last_watch_ms FROM remote_source_settings st JOIN remote_sources r ON r.id=st.source_id WHERE r.removed=0`)
	if err != nil {
		return nil, err
	}
	type due struct {
		source, root        string
		watch, start        bool
		days                int
		lastFull, lastWatch int64
	}
	pending := []due{}
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.source, &d.root, &d.watch, &d.start, &d.days, &d.lastFull, &d.lastWatch); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, d)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	tasks := []SourceScanTask{}
	for _, d := range pending {
		watchDue := d.watch && (d.lastWatch == 0 || now-d.lastWatch >= 5*60*1000)
		fullDue := d.days > 0 && (d.lastFull == 0 || now-d.lastFull >= int64(d.days)*24*60*60*1000)
		if !watchDue && !fullDue {
			continue
		}
		folders, err := folderSources(ctx, s.db, d.source)
		if err != nil {
			return nil, err
		}
		for _, folder := range folders {
			mode := "incremental"
			if fullDue {
				mode = "full"
			}
			tasks = append(tasks, SourceScanTask{SourceID: d.source, LibraryID: folder.library, FolderID: folder.id, Mode: mode, Watch: watchDue, Full: fullDue})
		}
	}
	return tasks, nil
}

type folderSource struct{ id, library string }

func folderSources(ctx context.Context, db *sql.DB, source string) ([]folderSource, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,library_id FROM library_sources WHERE enabled=1 AND remote_source_id=? ORDER BY id LIMIT 65`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []folderSource{}
	for rows.Next() {
		var f folderSource
		if err = rows.Scan(&f.id, &f.library); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// StartupSourceScans lists one scan per folder for sources that asked to be
// checked when the server starts. The runner calls this once per start.
func (s *Service) StartupSourceScans(ctx context.Context) ([]SourceScanTask, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT st.source_id,r.root FROM remote_source_settings st JOIN remote_sources r ON r.id=st.source_id WHERE r.removed=0 AND st.scan_on_start=1`)
	if err != nil {
		return nil, err
	}
	type started struct{ source, root string }
	all := []started{}
	for rows.Next() {
		var one started
		if err = rows.Scan(&one.source, &one.root); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, one)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	tasks := []SourceScanTask{}
	for _, one := range all {
		folders, err := folderSources(ctx, s.db, one.source)
		if err != nil {
			return nil, err
		}
		for _, folder := range folders {
			tasks = append(tasks, SourceScanTask{SourceID: one.source, LibraryID: folder.library, FolderID: folder.id, Mode: "incremental"})
		}
	}
	return tasks, nil
}

// MarkSourceScans records that the scheduler applied these scans, so a watch
// pass is not repeated within five minutes and a full re-check waits its days.
func (s *Service) MarkSourceScans(ctx context.Context, tasks []SourceScanTask) error {
	if s == nil || s.db == nil || len(tasks) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	seen := map[string]bool{}
	for _, task := range tasks {
		if seen[task.SourceID] {
			continue
		}
		seen[task.SourceID] = true
		if task.Full {
			if _, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE remote_source_settings SET last_full_recheck_ms=? WHERE source_id=?`, now, task.SourceID); err != nil {
				return err
			}
		}
		if task.Watch {
			if _, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE remote_source_settings SET last_watch_ms=? WHERE source_id=?`, now, task.SourceID); err != nil {
				return err
			}
		}
	}
	return nil
}

// AnalysisSwitchesTx reads the analysis switches for the remote source that
// holds a library source's root. `ok` is false for an ordinary local folder.
func AnalysisSwitchesTx(ctx context.Context, tx *sql.Tx, librarySource string) (AnalysisSwitches, bool, error) {
	consent, err := catalog.SourceAnalysisConsentTx(ctx, tx, librarySource)
	if err != nil {
		return AnalysisSwitches{}, false, err
	}
	if !consent.Remote {
		return AnalysisSwitches{}, false, nil
	}
	s := defaultAdvanced().analysis()
	s.advancedMediaAnalysis = consent.Allowed
	err = tx.QueryRowContext(ctx, `SELECT st.seek_previews,st.chapter_images,st.waveforms,st.loudness,st.checksums FROM remote_source_settings st WHERE st.source_id=?`, consent.SourceID).Scan(&s.SeekPreviews, &s.ChapterImages, &s.Waveforms, &s.Loudness, &s.Checksums)
	if errors.Is(err, sql.ErrNoRows) {
		return s, true, nil
	}
	if err != nil {
		return AnalysisSwitches{}, false, err
	}
	return s, true, nil
}

// Filter removes remote heavy analysis without independent explicit consent,
// including ranged stages. Basic metadata and ordinary playback stay separate.
func (s AnalysisSwitches) Filter(operations []string) []string {
	kept := make([]string, 0, len(operations))
	for _, op := range operations {
		if !s.advancedMediaAnalysis {
			heavy := false
			for _, stage := range catalog.DeepScanOperations {
				heavy = heavy || op == stage
			}
			if heavy {
				continue
			}
		}
		switch op {
		case "trickplay":
			if !s.SeekPreviews {
				continue
			}
		case "chapter_images":
			if !s.ChapterImages {
				continue
			}
		case "waveform":
			if !s.Waveforms {
				continue
			}
		case "loudness":
			if !s.Loudness {
				continue
			}
		case "checksum":
			if !s.Checksums {
				continue
			}
		}
		kept = append(kept, op)
	}
	return kept
}

// TransportTx reads the mounted program's transport values for one source.
// Zero values keep the bundled program's defaults.
func TransportTx(ctx context.Context, tx *sql.Tx, source string) (SourceTransport, error) {
	var t SourceTransport
	err := tx.QueryRowContext(ctx, `SELECT dir_cache_seconds,read_buffer_bytes,connect_timeout_seconds,idle_timeout_seconds,retries,low_level_retries FROM remote_source_settings WHERE source_id=?`, source).
		Scan(&t.DirCacheSeconds, &t.ReadBufferBytes, &t.ConnectTimeoutSeconds, &t.IdleTimeoutSeconds, &t.Retries, &t.LowLevelRetries)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceTransport{}, nil
	}
	return t, err
}

// DoubleCheckFoldersTx reports the saved second-listing choice for the remote
// source that contains a library source, if any.
func DoubleCheckFoldersTx(ctx context.Context, tx *sql.Tx, librarySource string) (bool, error) {
	var on bool
	err := tx.QueryRowContext(ctx, `SELECT st.double_check_folders FROM library_sources s JOIN remote_source_settings st ON st.source_id=s.remote_source_id WHERE s.id=?`, librarySource).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return on, err
}
