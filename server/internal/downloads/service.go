package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// SourceOpener reads an original catalog file. The server never opens a library
// path from this package directly: production wires storage.Client.OpenPlayback,
// which reads through the isolated media process, and a test wires a plain file
// opener. The signature matches OpenPlayback exactly so no adapter is needed.
type SourceOpener func(ctx context.Context, path string, size, modifiedNS int64) (io.ReadSeekCloser, error)

// ArtifactOpener reads a published prepared version's bytes while holding a
// deletion lease on them. internal/preparedmedia implements it.
type ArtifactOpener interface {
	OpenDownloadArtifact(ctx context.Context, versionID, digest string, size int64) (io.ReadSeekCloser, error)
}

// Optimizer asks the prepared-media producer for a version this package needs.
// Conversions are owner-authorized by design, so this is only reached for an
// owner viewer; a non-owner's rung preparation waits for an owner-prepared
// version instead of escalating the request.
type Optimizer interface {
	RequestOptimization(ctx context.Context, p identity.Principal, itemID, assetID, profileID, operationID string) error
}

type Options struct {
	// DeliveryConfiguration reads the same current owner policy as playback.
	DeliveryConfiguration func() playback.DeliveryConfiguration
	DB                    *sql.DB
	Now                   func() time.Time
	OpenSource            SourceOpener
	Artifacts             ArtifactOpener
	Optimizer             Optimizer
	Notifier              DownloadNotifier
	// Plays is the personal-state writer offline plays are recorded through
	// (catalog.AcceptPlaybackProgressAt): the one online playback uses.
	Plays playback.OfflineWriter
	// BaseGrantPath is the route prefix a grant URL is published under. It is a
	// path, never an absolute URL: the client already knows which server it is
	// talking to, and a server behind a proxy must not invent an origin.
	BaseGrantPath string
}

type Service struct {
	reading               *reading.Service
	deliveryConfiguration func() playback.DeliveryConfiguration
	libraryAccess         LibraryAccess
	access                RequestAccess
	accessMu              sync.RWMutex
	advanceMu             sync.Mutex
	tidied                time.Time
	pruned                time.Time
	transferSweep         int64 // raw process-local actual transfer ownership cleanup cursor
	db                    *sql.DB
	now                   func() time.Time
	openOriginal          SourceOpener
	artifacts             ArtifactOpener
	optimizer             Optimizer
	notifier              DownloadNotifier
	plays                 playback.OfflineWriter
	grantPath             string
	wake                  *worker.Signal
	done                  chan struct{}
	cancel                context.CancelFunc
}

func New(o Options) (*Service, error) {
	if o.DB == nil {
		return nil, ErrInput
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Notifier == nil {
		o.Notifier = inboxNotifier{}
	}
	if o.BaseGrantPath == "" {
		o.BaseGrantPath = "/v1/downloads/artifacts/"
	}
	return &Service{deliveryConfiguration: o.DeliveryConfiguration, db: o.DB, now: o.Now, openOriginal: o.OpenSource, artifacts: o.Artifacts, optimizer: o.Optimizer, notifier: o.Notifier, plays: o.Plays, grantPath: o.BaseGrantPath, wake: worker.NewSignal()}, nil
}

func (s *Service) millis() int64 { return s.now().UnixMilli() }
func (s *Service) signal()       { s.wake.Wake() }

// Settings is the owner policy for downloads, and with it the qualities this
// server can hand over at all. A client asking for a whole show or album has
// no single file to read a menu for, so it reads the list here: the original,
// and each smaller quality the conversion code can make while conversions are
// switched on. It is computed, never stored.
type Settings struct {
	MaxPreparedBytes int64           `json:"maxPreparedBytes"`
	RetentionDays    int             `json:"retentionDays"`
	Revision         int64           `json:"revision"`
	Qualities        []QualityChoice `json:"qualities"`
}

// QualityChoice is one quality a download may be asked for.
type QualityChoice struct {
	Quality            string  `json:"quality"`
	Label              string  `json:"label"`
	Kind               string  `json:"kind"`
	Height             int     `json:"targetDisplayHeight,omitempty"`
	MaxVideoBitrateBPS int     `json:"maxVideoBitrateBps,omitempty"`
	MaxAudioBitrateBPS int     `json:"maxAudioBitrateBps,omitempty"`
	DataGBPerHour      float64 `json:"dataGBPerHour,omitempty"`
}

func producibleQualities(tx *sql.Tx) ([]QualityChoice, error) {
	out := []QualityChoice{{Quality: "original", Label: "Original", Kind: "source"}, {Quality: "automatic", Label: "Automatic", Kind: "source"}}
	for _, rung := range playback.QualityLadder {
		out = append(out, QualityChoice{Quality: rung.ID, Label: rung.Label, Kind: "optimized", Height: rung.Height, MaxVideoBitrateBPS: rung.VideoBitrateBPS, MaxAudioBitrateBPS: rung.MaxAudioBitrateBPS, DataGBPerHour: rung.DataGBPerHour})
	}
	return out, nil
}

// SettingsChange fences an owner write the same way every other console write
// is fenced.
type SettingsChange struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	MaxPreparedBytes int64  `json:"maxPreparedBytes"`
	RetentionDays    int    `json:"retentionDays"`
}

func readSettings(tx *sql.Tx) (Settings, error) {
	var out Settings
	e := tx.QueryRow(`SELECT max_prepared_bytes,retention_days,revision FROM download_settings WHERE singleton=1`).Scan(&out.MaxPreparedBytes, &out.RetentionDays, &out.Revision)
	return out, e
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return Settings{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	out, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	if out.Qualities, e = producibleQualities(tx); e != nil {
		return out, e
	}
	out.Qualities = s.cappedQualities(out.Qualities)
	return out, gated.Commit()
}

func (s *Service) ApplySettings(ctx context.Context, p identity.Principal, c SettingsChange, authorize ...func(*sql.Tx) error) (Settings, error) {
	var out Settings
	if !validID.MatchString(c.OperationID) || c.MaxPreparedBytes < 0 || c.RetentionDays < 1 || c.RetentionDays > 365 {
		return out, ErrInput
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	now := s.millis()
	_, _, protocol := usermutation.RequestFrom(ctx)
	for _, auth := range authorize {
		if auth == nil {
			return out, ErrInput
		}
		if e = auth(tx); e != nil {
			return out, e
		}
	}
	if protocol && len(authorize) == 0 {
		return out, ErrInput
	}
	r, e := usermutation.CheckContext(ctx, tx, now)
	if e != nil {
		return out, e
	}
	if r != nil {
		out, e = readSettings(tx)
		if e == nil {
			out.Qualities, e = producibleQualities(tx)
		}
		return out, e
	}

	scope := "downloads-settings:" + operations.AccountKey(p)
	var raw, digest string
	if !protocol {
		raw, digest, e = operations.Receipt(tx, scope, c.OperationID, c, now)
	}
	if e != nil {
		return out, e
	}
	if raw != "" {
		return out, json.Unmarshal([]byte(raw), &out)
	}
	current, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	if current.Revision != c.ExpectedRevision {
		return current, ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `UPDATE download_settings SET max_prepared_bytes=?,retention_days=?,revision=revision+1 WHERE singleton=1`, c.MaxPreparedBytes, c.RetentionDays); e != nil {
		return out, e
	}
	if out, e = readSettings(tx); e != nil {
		return out, e
	}
	if out.Qualities, e = producibleQualities(tx); e != nil {
		return out, e
	}
	if e = operations.Audit(tx, now, operations.AccountKey(p), "downloads.settings", "downloads", out.Revision); e != nil {
		return out, e
	}
	if protocol {
		e = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: "downloads", Revision: out.Revision}, now)
	} else {
		e = operations.SaveReceipt(tx, scope, c.OperationID, digest, out, now)
	}
	if e != nil {
		return out, e
	}
	if e = gated2.Commit(); e == nil {
		s.signal()
	}
	return out, e
}

// Usage is the storage a viewer and the whole server have committed to prepared
// downloads. It counts claims, not files: two viewers holding the same prepared
// version each account for its bytes, because either of them removing the claim
// must free nothing while the other still holds it.
type Usage struct {
	ProfilesNextCursor string      `json:"profilesNextCursor"`
	ProfileID          string      `json:"profileId"`
	ProfileBytes       int64       `json:"profileBytes"`
	ProfileCount       int         `json:"profileCount"`
	ServerBytes        int64       `json:"serverBytes"`
	ServerCount        int         `json:"serverCount"`
	DistinctBytes      int64       `json:"distinctArtifactBytes"`
	MaxPreparedBytes   int64       `json:"maxPreparedBytes"`
	RemainingBytes     *int64      `json:"remainingBytes"`
	RetentionDays      int         `json:"retentionDays"`
	Profiles           []UsageLine `json:"profiles"`
}

// UsageLine is one profile's share of the server total. Only an owner reads the
// whole list; a viewer sees their own line.
type UsageLine struct {
	ProfileKey  string `json:"profileKey"`
	Authority   string `json:"authority"`
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	ProfileName string `json:"profileName"`
	StoredBytes int64  `json:"storedBytes"`
	ProfileID   string `json:"profileId"`
	Bytes       int64  `json:"bytes"`
	Count       int    `json:"count"`
}

// committedStates are the live preparations. A queued claim counts against the
// ceiling from the moment it is admitted; admitting work the store cannot hold
// and discovering it later would leave a viewer watching a progress bar that
// can only end in storage_full.
const committedStates = `('queued','running','ready','paused')`

// storedClaims are the live claims that stand for bytes this server keeps: the
// converted files. An original is the library file itself, read in place, so
// claiming one takes no room and is never refused for lack of it.
const storedClaims = committedStates + ` AND (quality<>'original' OR recipe<>'')`

func (s *Service) Usage(ctx context.Context, p identity.Principal, all bool) (Usage, error) {
	return s.UsagePage(ctx, p, all, "", 256)
}
func (s *Service) UsagePage(ctx context.Context, p identity.Principal, all bool, cursor string, limit int) (Usage, error) {
	out := Usage{ProfileID: p.ProfileID, Profiles: []UsageLine{}}
	if limit < 1 || limit > 256 || len(cursor) > 1024 {
		return out, ErrInput
	}
	gated3, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	settings, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	out.MaxPreparedBytes, out.RetentionDays = settings.MaxPreparedBytes, settings.RetentionDays
	key := operations.ViewerKey(p)
	var bytes sql.NullInt64
	if e = tx.QueryRowContext(ctx, `SELECT sum(bytes_total),count(*) FROM download_preparations WHERE profile_key=? AND state IN `+committedStates, key).Scan(&bytes, &out.ProfileCount); e != nil {
		return out, e
	}
	out.ProfileBytes = bytes.Int64
	if e = tx.QueryRowContext(ctx, `SELECT sum(bytes_total),count(*) FROM download_preparations WHERE state IN `+committedStates).Scan(&bytes, &out.ServerCount); e != nil {
		return out, e
	}
	out.ServerBytes = bytes.Int64
	// Distinct artifact bytes is what the disk actually holds: two viewers
	// claiming one prepared version share its file.
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(bytes),0) FROM (SELECT max(bytes_total) AS bytes FROM download_preparations WHERE state='ready' AND artifact_kind='prepared' AND artifact_digest!='' GROUP BY artifact_digest)`).Scan(&out.DistinctBytes); e != nil {
		return out, e
	}
	if settings.MaxPreparedBytes > 0 {
		var stored sql.NullInt64
		if e = tx.QueryRowContext(ctx, `SELECT sum(bytes_total) FROM download_preparations WHERE state IN `+storedClaims).Scan(&stored); e != nil {
			return out, e
		}
		remaining := settings.MaxPreparedBytes - stored.Int64
		if remaining < 0 {
			remaining = 0
		}
		out.RemainingBytes = &remaining
	}
	if all {
		rows, err := tx.QueryContext(ctx, `SELECT d.profile_key,d.authority,d.account_id,d.profile_id,COALESCE(a.username,d.account_id),COALESCE(p.name,d.profile_id),COALESCE(sum(d.bytes_total),0),count(*),COALESCE(sum(CASE WHEN d.quality<>'original' OR d.recipe<>'' THEN d.bytes_total ELSE 0 END),0)
 FROM download_preparations d LEFT JOIN accounts a ON a.id=d.account_id AND d.authority='local' LEFT JOIN direct_profiles p ON p.id=d.profile_id AND d.authority='local'
 WHERE d.state IN `+committedStates+` AND d.profile_key>? GROUP BY d.profile_key ORDER BY d.profile_key LIMIT ?`, cursor, limit+1)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var line UsageLine
			if err = rows.Scan(&line.ProfileKey, &line.Authority, &line.AccountID, &line.ProfileID, &line.AccountName, &line.ProfileName, &line.Bytes, &line.Count, &line.StoredBytes); err != nil {
				rows.Close()
				return out, err
			}
			if len(out.Profiles) == limit {
				out.ProfilesNextCursor = out.Profiles[len(out.Profiles)-1].ProfileKey
				break
			}
			out.Profiles = append(out.Profiles, line)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	} else {
		out.Profiles = append(out.Profiles, UsageLine{ProfileKey: key, Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, Bytes: out.ProfileBytes, Count: out.ProfileCount})
	}
	return out, gated3.Commit()
}

// storageAllowance refuses admission of a converted download once the owner's
// ceiling is reached. It belongs to one admitting transaction, so two
// concurrent batches cannot both squeeze past the same remaining allowance.
//
// The stored total is summed once per transaction, not once per item: each
// admission adds its own claim, which keeps the total exact, or an
// over-estimate once the transaction has ended claims (ended). An
// over-estimate never admits past the ceiling, and the allowance sums again
// before it refuses, so it never refuses what the ceiling has room for.
type storageAllowance struct {
	tx                    *sql.Tx
	loaded, summed, exact bool
	ceiling, reserved     int64
}

func newStorageAllowance(tx *sql.Tx) *storageAllowance { return &storageAllowance{tx: tx} }

func (a *storageAllowance) admit(quality string, want int64) error {
	if quality == QualityOriginal {
		return nil
	}
	if !a.loaded {
		settings, e := readSettings(a.tx)
		if e != nil {
			return e
		}
		a.ceiling, a.loaded = settings.MaxPreparedBytes, true
	}
	if a.ceiling <= 0 {
		return nil
	}
	if !a.summed || a.reserved+want > a.ceiling && !a.exact {
		if e := a.sum(); e != nil {
			return e
		}
	}
	if a.reserved+want > a.ceiling {
		return ErrStorageFull
	}
	a.reserved += want
	return nil
}

func (a *storageAllowance) sum() error {
	var committed sql.NullInt64
	if e := a.tx.QueryRow(`SELECT sum(bytes_total) FROM download_preparations WHERE state IN ` + storedClaims).Scan(&committed); e != nil {
		return e
	}
	a.reserved, a.summed, a.exact = committed.Int64, true, true
	return nil
}

// ended notes that the transaction ended claims, so the total may now be high.
func (a *storageAllowance) ended() { a.exact = false }

const preparationColumns = `id,source_asset_id,item_public,library_id,profile_id,title_json,NOT (` + titleGone + `),quality,origin,batch_id,state,reason,artifact_kind,artifact_ref,artifact_digest,artifact_container,bytes_done,bytes_total,estimated,revision,created_ms,updated_ms,started_ms,ready_ms`

type row struct {
	Preparation
	StartedMS int64
	CreatedMS int64
}

func readPreparation(scan interface{ Scan(...any) error }) (row, error) {
	var r row
	var estimated int
	var updatedMS, readyMS int64
	var title string
	var onServer bool
	e := scan.Scan(&r.ID, &r.VersionID, &r.ItemID, &r.LibraryID, &r.ProfileID, &title, &onServer, &r.Quality, &r.Origin, &r.BatchID, &r.State, &r.Reason,
		&r.Artifact.Kind, &r.Artifact.Ref, &r.Artifact.SHA256, &r.Artifact.Container,
		&r.Progress.BytesDone, &r.Progress.BytesTotal, &estimated, &r.Revision, &r.CreatedMS, &updatedMS, &r.StartedMS, &readyMS)
	if e != nil {
		return r, e
	}
	r.Title = storedTitle(title, onServer)
	// An estimate is a moving lower bound once the worker has produced more.
	// Exact ready sizes remain authoritative.
	if estimated != 0 && r.State != StateReady && r.Progress.BytesDone > r.Progress.BytesTotal {
		r.Progress.BytesTotal = r.Progress.BytesDone
	}
	r.Artifact.Estimated = estimated != 0
	r.Artifact.Bytes = r.Progress.BytesTotal
	r.Artifact.ContentType, r.Artifact.FileName = artifactDelivery(r.Artifact.Container, r.ID)
	r.Progress.Estimated = estimated != 0
	r.CreatedAt, r.UpdatedAt, r.ReadyAt = stamp(r.CreatedMS), stamp(updatedMS), stamp(readyMS)
	r.Actions = actionsFor(r.State)
	if r.Progress.BytesTotal > 0 {
		percent := float64(r.Progress.BytesDone) / float64(r.Progress.BytesTotal) * 100
		if percent > 100 {
			percent = 100
		}
		r.Progress.Percent = percent
	}
	if r.State == StateReady {
		r.Progress.Percent = 100
		r.Progress.BytesDone = r.Progress.BytesTotal
	}
	return r, nil
}

// publish finishes a stored row into its wire view: the retention deadline of a
// ready claim and the ETA of a running one are both computed, never stored, so
// a settings change or a stalled worker cannot leave a stale promise behind.
func (s *Service) publish(r row, retentionDays int) Preparation {
	out := r.Preparation
	if r.State == StateReady && r.ReadyAt != "" {
		if ready, e := time.Parse(time.RFC3339, r.ReadyAt); e == nil {
			out.ExpiresAt = ready.Add(time.Duration(retentionDays) * 24 * time.Hour).UTC().Format(time.RFC3339)
		}
	}
	if r.State == StateRunning && r.StartedMS > 0 && r.Progress.BytesDone > 0 && r.Progress.BytesTotal > r.Progress.BytesDone {
		elapsed := float64(s.millis()-r.StartedMS) / 1000
		if elapsed > 0 {
			rate := float64(r.Progress.BytesDone) / elapsed
			if rate > 0 {
				eta := int64(float64(r.Progress.BytesTotal-r.Progress.BytesDone) / rate * 1000)
				out.Progress.ETAMs = &eta
			}
		}
	}
	return out
}

// Get reads one preparation belonging to this viewer.
func (s *Service) Get(ctx context.Context, p identity.Principal, id string) (Preparation, error) {
	var out Preparation
	if !validID.MatchString(id) {
		return out, ErrInput
	}
	gated4, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	settings, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	r, e := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=? AND profile_key=?`, id, operations.ViewerKey(p)))
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if e = s.visiblePreparation(ctx, tx, p, r); e != nil {
		if errors.Is(e, identity.ErrContentRestricted) {
			return out, ErrNotFound
		}
		return out, e
	}
	return s.publish(r, settings.RetentionDays), gated4.Commit()
}

// ListQuery is one page request. ProfileID is only honoured for a caller the
// routes layer has established as the server owner; a viewer naming another
// profile reads their own, because a download list is personal state.
type ListQuery struct {
	IDs       []string
	State     string
	ProfileID string
	Cursor    string
	Limit     int
}

// List pages preparations newest first, keyset-ordered on (createdMs, id) so a
// page is stable while new claims are admitted above it.
func (s *Service) List(ctx context.Context, p identity.Principal, q ListQuery, anyProfile bool) (Page, error) {
	out := Page{Items: []Preparation{}}
	if q.Limit < 1 || q.Limit > 200 {
		q.Limit = 50
	}
	switch q.State {
	case "", StateQueued, StateRunning, StateReady, StatePaused, StateFailed, StateUnavailable, StateCancelled, StateExpired:
	default:
		return out, ErrInput
	}
	if q.ProfileID != "" && !validID.MatchString(q.ProfileID) {
		return out, ErrInput
	}
	if len(q.IDs) > 200 || len(q.IDs) > 0 && q.Cursor != "" {
		return out, ErrInput
	}
	for _, id := range q.IDs {
		if !validID.MatchString(id) {
			return out, ErrInput
		}
	}
	if len(q.IDs) > 0 {
		q.Limit = 200
	}
	before := int64(0)
	after := ""
	if q.Cursor != "" {
		ms, id, e := decodeCursor(q.Cursor)
		if e != nil {
			return out, e
		}
		before, after = ms, id
	}
	gated5, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	settings, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	query := `SELECT ` + preparationColumns + ` FROM download_preparations WHERE `
	args := []any{}
	if anyProfile && q.ProfileID != "" {
		query += `profile_id=?`
		args = append(args, q.ProfileID)
	} else {
		query += `profile_key=?`
		args = append(args, operations.ViewerKey(p))
	}
	if len(q.IDs) > 0 {
		ids, _ := json.Marshal(q.IDs)
		query += ` AND id IN (SELECT value FROM json_each(?))`
		args = append(args, string(ids))
	}
	if q.State != "" {
		query += ` AND state=?`
		args = append(args, q.State)
	}
	if q.Cursor != "" {
		query += ` AND (created_ms<? OR (created_ms=? AND id<?))`
		args = append(args, before, before, after)
	}
	query += ` ORDER BY created_ms DESC,id DESC LIMIT ?`
	args = append(args, q.Limit+1)
	rows, e := tx.QueryContext(ctx, query, args...)
	if e != nil {
		return out, e
	}
	collected := []row{}
	for rows.Next() {
		r, err := readPreparation(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		collected = append(collected, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(collected) > q.Limit {
		last := collected[q.Limit-1]
		out.NextCursor = encodeCursor(last.CreatedMS, last.ID)
		collected = collected[:q.Limit]
	}
	for _, r := range collected {
		if !(anyProfile && q.ProfileID != "") {
			if e = s.visiblePreparation(ctx, tx, p, r); errors.Is(e, identity.ErrContentRestricted) {
				continue
			} else if e != nil {
				return out, e
			}
		}
		out.Items = append(out.Items, s.publish(r, settings.RetentionDays))
	}
	return out, gated5.Commit()
}

func encodeCursor(ms int64, id string) string {
	return strconv.FormatInt(ms, 10) + ":" + id
}
func decodeCursor(v string) (int64, string, error) {
	for i := 0; i < len(v); i++ {
		if v[i] == ':' {
			ms, e := strconv.ParseInt(v[:i], 10, 64)
			id := v[i+1:]
			if e != nil || !validID.MatchString(id) {
				return 0, "", ErrInput
			}
			return ms, id, nil
		}
	}
	return 0, "", ErrInput
}

// preparationQuality validates a requested quality against the published ladder
// rather than accepting any string that later fails deep in the worker.
func preparationQuality(v string) error {
	if v == QualityOriginal || v == "automatic" || canonicalMusicQuality(v) {
		return nil
	}
	if _, ok := playback.LadderPreset(v); ok {
		return nil
	}
	return ErrInput
}
