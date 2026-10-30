package preparedmedia

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"runtime"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type Options struct {
	DB                         *sql.DB
	Directory, FFmpeg, FFprobe string
	Sandbox                    string // Linux bwrap; empty resolves from the server process PATH.
	Libraries                  []string
	Supervisor                 *storage.Supervisor
	OpenInput                  func(context.Context, string, string, string) (subtitles.RenderInput, error)
	Authorize, Continue        Authority
	// MaxOutputBytes is an explicit storage safety limit, never a stream cap.
	MaxOutputBytes int64
}
type Service struct {
	db                      *sql.DB
	artifacts               *mediaartifact.Store
	locks                   *livechannels.PhysicalLocks
	ffmpeg, ffprobe         string
	sandbox                 string
	sandboxCapable          bool
	libraries               []string
	supervisor              *storage.Supervisor
	input                   func(context.Context, string, string, string) (subtitles.RenderInput, error)
	authorize, continuation Authority
	maxBytes                int64
	mu                      sync.Mutex // publication/read admission/pruning; never held while probing
	running                 map[string]context.CancelFunc
	readers                 map[string]*artifactLease
	inventory               *mediaartifact.Inventory
	deletingCursor          string
	wake                    *worker.Signal
	done                    chan struct{}
	cancel                  context.CancelFunc
}

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Authorize == nil || o.Continue == nil || o.Supervisor == nil || o.OpenInput == nil || !filepath.IsAbs(o.Directory) {
		return nil, ErrInput
	}
	a, e := mediaartifact.New(o.Directory)
	if e != nil {
		return nil, e
	}
	locks, e := livechannels.NewPhysicalLocks(filepath.Join(o.Directory, "custody"))
	if e != nil {
		a.Close()
		return nil, e
	}
	resolve := func(v string) string {
		p, _ := exec.LookPath(v)
		if p != "" {
			p, _ = filepath.Abs(p)
		}
		return p
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = 128 << 30
	}
	if o.Sandbox == "" {
		o.Sandbox = "bwrap"
	}
	sandbox := ""
	capable := false
	if runtime.GOOS == "linux" {
		sandbox = resolve(o.Sandbox)
		capable = decoder.PreparedFileSandboxAvailable(o.Supervisor, sandbox)
	}
	s := &Service{sandbox: sandbox, sandboxCapable: capable, db: o.DB, artifacts: a, locks: locks, ffmpeg: resolve(o.FFmpeg), ffprobe: resolve(o.FFprobe), libraries: append([]string(nil), o.Libraries...), supervisor: o.Supervisor, input: o.OpenInput, authorize: o.Authorize, continuation: o.Continue, maxBytes: o.MaxOutputBytes, running: map[string]context.CancelFunc{}, readers: map[string]*artifactLease{}, wake: worker.NewSignal()}
	ok, reason := s.configured()
	var cause error
	switch {
	case ok:
	case reason == "encoder_not_configured":
		cause = fmt.Errorf("ffmpeg (%q) or ffprobe (%q) was not found on the server's PATH", o.FFmpeg, o.FFprobe)
	default:
		cause = decoder.RequiredConfinement(s.ffmpeg)
	}
	capabilityreport.Report(capabilityreport.PreparedMedia, ok, reason, cause)
	return s, nil
}

// configured is whether prepared versions can be made here. They can on every
// platform: on Linux with a capable bubblewrap through the fd-bound file
// sandbox, elsewhere through the loopback bridge, sandboxed where the platform
// can and with the baseline otherwise (D-MEDIA-6, mediaexec). Only an owner who
// requires the sandbox on a host without one is refused.
func (s *Service) configured() (bool, string) {
	if s.ffmpeg == "" || s.ffprobe == "" {
		return false, "encoder_not_configured"
	}
	if decoder.RequiredConfinement(s.ffmpeg) != nil {
		return false, "decoder_confinement_unavailable"
	}
	return true, ""
}

// fileSandbox is whether jobs use the fd-bound Linux file sandbox (no network
// namespace route at all) rather than the loopback bridge.
func (s *Service) fileSandbox() bool {
	return runtime.GOOS == "linux" && s.sandboxCapable && mediaexec.SandboxSetting() != mediaexec.SettingOff
}
func owner(p identity.Principal) bool {
	return p.Role == "owner" && p.Authority == "local"
}
func (s *Service) check(ctx context.Context, tx *sql.Tx, p identity.Principal, item string, continued bool) (identity.Principal, error) {
	f := s.authorize
	if continued {
		f = s.continuation
	}
	p, e := f(ctx, tx, p, item)
	if e != nil {
		return p, e
	}
	if !owner(p) {
		return p, ErrOwner
	}
	return p, nil
}
func (s *Service) signal() { s.wake.Wake() }

const jobColumns = `j.id,COALESCE(pid(e.public_id),''),j.asset_id,j.profile_id,j.target_id,j.state,j.phase,j.generation,j.bytes,j.error_code,j.revision,j.created_ms,j.updated_ms,j.version_id`

const jobSource = `prepared_media_jobs j LEFT JOIN catalog_entities e ON e.id=j.item_id`

func readJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	e := row.Scan(&j.ID, &j.ItemID, &j.SourceID, &j.ProfileID, &j.TargetID, &j.State, &j.Phase, &j.Generation, &j.Bytes, &j.ErrorCode, &j.Revision, &j.CreatedMS, &j.UpdatedMS, &j.VersionID)
	return j, e
}

func (s *Service) View(ctx context.Context, p identity.Principal, item string) (View, error) {
	out := View{ItemID: item, Profiles: Profiles(), Targets: []Target{{TargetID, "Prepared media on this server"}}, Sources: []SourceChoice{}, Jobs: []Job{}, Versions: []Version{}, PollAfterMS: 3000}
	out.Configured, out.ConfigurationReason = s.configured()
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	p, e = s.authorize(ctx, tx, p, item)
	if e != nil {
		return out, e
	}
	out.CanManage = owner(p)
	rows, e := tx.QueryContext(ctx, `SELECT a.token,a.container,a.height,link.part_index FROM catalog_entities e JOIN catalog_asset_links link ON link.entity_id=e.id JOIN catalog_assets a ON a.id=link.asset_id WHERE e.public_id=pid_blob(?) ORDER BY link.part_index,a.token`, item)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var c SourceChoice
		if e = rows.Scan(&c.ID, &c.Container, &c.Height, &c.PartIndex); e != nil {
			rows.Close()
			return out, e
		}
		out.Sources = append(out.Sources, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for i := range out.Sources {
		c := &out.Sources[i]
		sel, err := selectSource(ctx, tx, item, c.ID, true)
		if err == nil {
			c.Available = true
			c.Revision = hash(sel)
		} else {
			c.Reason = errorCode(err)
		}
	}
	out.Versions, e = VersionsTx(ctx, tx, item)
	if e != nil {
		return out, e
	}
	out.PreparedOffersRevision = OffersRevision(item, out.Versions)
	if out.CanManage {
		rows, e = tx.QueryContext(ctx, `SELECT `+jobColumns+` FROM `+jobSource+` WHERE e.public_id=pid_blob(?) ORDER BY j.created_ms DESC,j.id`, item)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			j, err := readJob(rows)
			if err != nil {
				rows.Close()
				return out, err
			}
			out.Jobs = append(out.Jobs, j)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	return out, gated.Commit()
}
func (s *Service) Submit(ctx context.Context, p identity.Principal, r Request) (Job, error) {
	var out Job
	if !validID.MatchString(r.ItemID) || !validID.MatchString(r.SourceID) || !validID.MatchString(r.IdempotencyKey) || !validDigest.MatchString(r.ExpectedSourceRevision) || r.TargetID != TargetID {
		return out, ErrInput
	}
	if _, e := profile(r.ProfileID); e != nil {
		return out, e
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	p, e = s.check(ctx, tx, p, r.ItemID, false)
	if e != nil {
		return out, e
	}
	now := time.Now().UnixMilli()
	scope := "prepared-submit:" + operations.AccountKey(p)
	raw, digest, e := operations.Receipt(tx, scope, r.IdempotencyKey, r, now)
	if e != nil {
		return out, e
	}
	if raw != "" {
		e = json.Unmarshal([]byte(raw), &out)
		return out, e
	}
	if ok, _ := s.configured(); !ok {
		return out, ErrConfiguration
	}
	sel, e := selectSource(ctx, tx, r.ItemID, r.SourceID, true)
	if e != nil {
		return out, e
	}
	if hash(sel) != r.ExpectedSourceRevision {
		return out, ErrSourceChanged
	}
	if e = conversionAllowed(ctx, tx); e != nil {
		return out, e
	}
	recipe, _ := profile(r.ProfileID)
	// The selected source's channels decide which audio path this recipe
	// actually writes, so a 7.1-only carrier is not advertised from a 5.1
	// qualification and vice versa.
	channels, e := sourceAudioChannels(ctx, tx, sel.Asset)
	if e != nil {
		return out, e
	}
	if !ProducibleForSource(recipe.ID, channels) {
		return out, ErrUnsupported
	}
	if (recipe.Kind == "audio") != (sel.Kind == "song" || sel.Kind == "audiobook_file") {
		return out, ErrUnsupported
	}
	if e = capacity(ctx, tx, sel.Item); e != nil {
		return out, e
	}
	id := hash(identity.Token())
	selectionJSON, _ := json.Marshal(sel)
	principalJSON, _ := json.Marshal(p)
	entity, e := entityid.Resolve(ctx, tx, r.ItemID)
	if e != nil {
		return out, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,'queued','waiting',?,?)`, id, entity, sel.Asset, sel.Library, r.ProfileID, r.TargetID, string(selectionJSON), hash(sel), string(principalJSON), now, now)
	if e != nil {
		return out, e
	}
	if e = enqueueConsole(ctx, tx, p, id, now); e != nil {
		return out, e
	}
	out, e = readJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM `+jobSource+` WHERE j.id=?`, id))
	if e != nil {
		return out, e
	}
	if e = operations.Audit(tx, now, operations.AccountKey(p), "optimization.request", id, out.Revision); e != nil {
		return out, e
	}
	if e = operations.SaveReceipt(tx, scope, r.IdempotencyKey, digest, out, now); e != nil {
		return out, e
	}
	if e = gated2.Commit(); e == nil {
		s.signal()
	}
	return out, e
}

// All admissions, including retries, share the same transactional bounds.
func capacity(ctx context.Context, tx *sql.Tx, item string) error {
	var n int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM prepared_media_jobs WHERE state IN('queued','running','cancelling')`).Scan(&n); e != nil {
		return e
	}
	if n >= 64 {
		return operations.ErrCapacity
	}
	if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM prepared_media_versions WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND state!='deleted')+(SELECT count(*) FROM prepared_media_jobs WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND state IN('queued','running','cancelling'))`, item, item).Scan(&n); e != nil {
		return e
	}
	if n >= 128 {
		return operations.ErrCapacity
	}
	return nil
}
func conversionAllowed(ctx context.Context, tx *sql.Tx) error {
	var ok bool
	if e := tx.QueryRowContext(ctx, `SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return ErrUnsupported
	}
	return nil
}
func enqueueConsole(ctx context.Context, tx *sql.Tx, p identity.Principal, id string, now int64) error {
	opID := identity.Token()
	if _, err := tx.ExecContext(ctx, `INSERT INTO console_operations(id,kind,resource,actor,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision) VALUES(?,?,?,?,'owner','queued','waiting',1,0,?,?,?,?, '', '',COALESCE((SELECT revision FROM console_settings_revision WHERE singleton=1),1))`, opID, Kind, id, operations.Hash(operations.AccountKey(p)), now, now, now, id); err != nil {
		return err
	}
	return operations.AppendOperationTx(tx, opID)
}
func cancelTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, e := tx.ExecContext(ctx, `UPDATE prepared_media_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'cancelling' END,phase='cancellation requested',revision=revision+1,updated_ms=? WHERE id=? AND state IN('queued','running')`, time.Now().UnixMilli(), id)
	return e
}
func (s *Service) Interrupt(_ context.Context, id string) {
	s.mu.Lock()
	cancel := s.running[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.signal()
}
func (s *Service) Command(ctx context.Context, p identity.Principal, id, action string, c Command) (Job, error) {
	var out Job
	if !validID.MatchString(id) || !validID.MatchString(c.IdempotencyKey) || (action != "cancel" && action != "retry") {
		return out, ErrInput
	}
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return out, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	out, e = readJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM `+jobSource+` WHERE j.id=?`, id))
	if e != nil {
		return out, e
	}
	p, e = s.check(ctx, tx, p, out.ItemID, false)
	if e != nil {
		return out, e
	}
	now := time.Now().UnixMilli()
	scope := "prepared-command:" + operations.AccountKey(p)
	raw, digest, e := operations.Receipt(tx, scope, c.IdempotencyKey, []any{id, action, c}, now)
	if e != nil {
		return out, e
	}
	if raw != "" {
		e = json.Unmarshal([]byte(raw), &out)
		return out, e
	}
	if c.ExpectedRevision != out.Revision {
		return out, ErrConflict
	}
	if action == "cancel" {
		if out.State != "queued" && out.State != "running" && out.State != "cancelling" {
			return out, ErrConflict
		}
		e = cancelTx(ctx, tx, id)
	} else {
		if out.State != "failed" && out.State != "cancelled" {
			return out, ErrConflict
		}
		// Explicit retry obtains current owner authority, but cannot silently retarget
		// changed source bytes. A changed source requires a fresh optimization request.
		var rawSel string
		if e = tx.QueryRowContext(ctx, `SELECT selection_json FROM prepared_media_jobs WHERE id=?`, id).Scan(&rawSel); e != nil {
			return out, e
		}
		var sel selection
		if e = json.Unmarshal([]byte(rawSel), &sel); e != nil {
			return out, e
		}
		if e = validateSelection(ctx, tx, sel, true); e != nil {
			return out, e
		}
		if e = conversionAllowed(ctx, tx); e != nil {
			return out, e
		}
		if ok, _ := s.configured(); !ok {
			return out, ErrConfiguration
		}
		if e = capacity(ctx, tx, out.ItemID); e != nil {
			return out, e
		}
		principal, _ := json.Marshal(p)
		_, e = tx.ExecContext(ctx, `UPDATE prepared_media_jobs SET state='queued',phase='waiting',bytes=0,error_code='',principal_json=?,revision=revision+1,updated_ms=? WHERE id=?`, string(principal), now, id)
		if e == nil {
			rows, err := tx.QueryContext(ctx, `UPDATE console_operations SET state='failed',phase='superseded by explicit retry',revision=revision+1 WHERE kind=? AND resource=? AND state IN('queued','running','cancellation-requested','reconciling','paused') RETURNING id`, Kind, id)
			if err != nil {
				e = err
			} else {
				var changed []string
				for rows.Next() {
					var opID string
					if e = rows.Scan(&opID); e != nil {
						break
					}
					changed = append(changed, opID)
				}
				if e == nil {
					e = rows.Err()
				}
				rows.Close()
				for _, opID := range changed {
					if e != nil {
						break
					}
					e = operations.AppendOperationTx(tx, opID)
				}
			}
		}
		if e == nil {
			e = enqueueConsole(ctx, tx, p, id, now)
		}
	}
	if e != nil {
		return out, e
	}
	out, e = readJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM `+jobSource+` WHERE j.id=?`, id))
	if e != nil {
		return out, e
	}
	if e = operations.Audit(tx, now, operations.AccountKey(p), "optimization."+action, id, out.Revision); e != nil {
		return out, e
	}
	if e = operations.SaveReceipt(tx, scope, c.IdempotencyKey, digest, out, now); e != nil {
		return out, e
	}
	if e = gated3.Commit(); e == nil {
		// A retry may already have been admitted by the worker after commit.
		// Interrupting by job ID here would cancel that fresh generation.
		if action == "cancel" {
			s.Interrupt(ctx, id)
		} else {
			s.signal()
		}
	}
	return out, e
}

func (s *Service) Delete(ctx context.Context, p identity.Principal, id string, c Command) (Version, error) {
	var out Version
	if !validID.MatchString(id) || !validID.MatchString(c.IdempotencyKey) {
		return out, ErrInput
	}
	// Fence new session admission in the same database transaction as deletion.
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return out, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	out, e = readVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM `+versionSource+` WHERE v.id=?`, id))
	if e != nil {
		return out, e
	}
	p, e = s.check(ctx, tx, p, out.ItemID, false)
	if e != nil {
		return out, e
	}
	now := time.Now().UnixMilli()
	scope := "prepared-delete:" + operations.AccountKey(p)
	raw, digest, e := operations.Receipt(tx, scope, c.IdempotencyKey, []any{id, c}, now)
	if e != nil {
		return out, e
	}
	if raw != "" {
		e = json.Unmarshal([]byte(raw), &out)
		return out, e
	}
	if out.Revision != c.ExpectedRevision {
		return out, ErrConflict
	}
	if out.State == "published" {
		out.State = "deleting"
		out.Revision++
		_, e = tx.ExecContext(ctx, `UPDATE prepared_media_versions SET state='deleting',revision=revision+1 WHERE id=?`, id)
		if e != nil {
			return out, e
		}
	}
	out.Selectable = false
	out.Reason = "pending_deletion"
	if e = operations.Audit(tx, now, operations.AccountKey(p), "optimization.delete", id, out.Revision); e != nil {
		return out, e
	}
	if e = operations.SaveReceipt(tx, scope, c.IdempotencyKey, digest, out, now); e != nil {
		return out, e
	}
	if e = gated4.Commit(); e == nil {
		s.signal()
	}
	return out, e
}
func errorCode(e error) string {
	switch {
	case e == nil:
		return ""
	case errors.Is(e, ErrCancelled), errors.Is(e, context.Canceled):
		return "cancelled"
	case errors.Is(e, ErrSourceChanged), errors.Is(e, subtitles.ErrConflict):
		return "source_changed"
	case errors.Is(e, ErrUnsupported):
		return "source_or_profile_unsupported"
	case errors.Is(e, ErrConfiguration):
		return "encoder_not_configured"
	case errors.Is(e, ErrOutput):
		return "output_invalid"
	case errors.Is(e, identity.ErrUnauthorized), errors.Is(e, ErrOwner):
		return "permission_changed"
	case errors.Is(e, mediaartifact.ErrLimit):
		return "output_limit"
	case errors.Is(e, storage.ErrBusy):
		return "waiting_for_storage"
	case errors.Is(e, ErrUnavailable), errors.Is(e, sql.ErrNoRows):
		return "source_unavailable"
	default:
		return "optimization_failed"
	}
}
