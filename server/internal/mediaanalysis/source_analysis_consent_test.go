package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Root §64: advanced analysis needs one separately recorded per-source consent,
// default off, before any stage or read. These regressions use the real schema and
// the catalog fixture; no test-only Allow callback supplies consent.

type analysisConsentFixture struct {
	t                *testing.T
	c                *catalogtest.Catalog
	source, remote   string
	object, revision string
	asset, item      string
}

func newAnalysisConsentFixture(t *testing.T, remote bool, allow bool) *analysisConsentFixture {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(library, "/movies/a.mkv", "A", 2000)
	c.Drain()
	f := &analysisConsentFixture{t: t, c: c, object: "obj-1", revision: "r1", asset: movie.Token, item: movie.Public}
	if err := c.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='movies'`).Scan(&f.source); err != nil {
		t.Fatal(err)
	}
	if remote {
		f.remote = "remote-1"
		c.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation,state,removed) VALUES(?,'webdav','Remote','/mnt/remote',5,'ready',0)`, f.remote)
		c.Exec(`INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,allow_advanced_media_analysis,connections,requests_per_second,bytes_per_second) VALUES(?,2,'complete',1,?,4,5,0)`, f.remote, consentFlag(allow))
		c.Exec(`INSERT INTO remote_source_settings(source_id,revision,scan_interval_seconds,watch_for_changes,scan_on_start,full_recheck_days,seek_previews,chapter_images,waveforms,loudness,checksums) VALUES(?,1,0,1,1,7,1,1,1,1,1)`, f.remote)
		c.Exec(`UPDATE library_sources SET kind='remote',remote_source_id=?,root='/mnt/remote',generation=5,incarnation='inc',root_identity='rootid',enabled=1,identity_confirmed=1 WHERE library_id='movies'`, f.remote)
	} else {
		c.Exec(`UPDATE library_sources SET kind='local',incarnation='inc',generation=1,root_identity='rootid',enabled=1,identity_confirmed=1 WHERE library_id='movies'`)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,analysis_revision) VALUES(?,?,?,'inc','a.mkv','r1','{}',1000,1,'available','r1')`, f.object, f.source, f.asset)
	return f
}

func consentFlag(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (f *analysisConsentFixture) setConsent(revision int64, allow bool) {
	f.c.Exec(`UPDATE remote_source_read_profiles SET revision=?,allow_advanced_media_analysis=? WHERE source_id=?`, revision, consentFlag(allow), f.remote)
}

func (f *analysisConsentFixture) setSeekPreviews(on bool) {
	f.c.Exec(`UPDATE remote_source_settings SET seek_previews=? WHERE source_id=?`, consentFlag(on), f.remote)
}

func (f *analysisConsentFixture) stageRun(job, object, stage, state string) {
	f.c.Exec(`INSERT INTO analysis_stage_runs(job_id,object_id,source_revision,policy_revision,stage,algorithm,state,attempt,updated_ms) VALUES(?,?,'r1',1,?,'fixture',?,1,1)`, job, object, stage, state)
}

func (f *analysisConsentFixture) readTx(t *testing.T) (*sql.Tx, func()) {
	t.Helper()
	tx, closeRead, err := dbwork.BeginRead(context.Background(), f.c.DB)
	if err != nil {
		t.Fatal(err)
	}
	return tx, closeRead
}

// Every stage name is refused while the exact remote source has no consent, before
// any engine is built. Sonic, fingerprint, episode thumbnail and segment work are
// not exceptions, and a preset/legacy whole-file flag never supplies consent.
func TestRemoteStageGateNeedsConsentForEveryStage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	operations := []string{"checksum", "chapter_images", "trickplay", "waveform", "loudness", "fingerprint", "sonic", catalog.EpisodeThumbnailOperation, "segment_audio", ""}
	f := newAnalysisConsentFixture(t, true, false)
	tx, closeRead := f.readTx(t)
	for _, operation := range operations {
		allowed, err := sourceStageAllowedTx(ctx, tx, f.source, operation)
		if err != nil || allowed {
			closeRead()
			t.Fatalf("consent off still allowed stage %q: %v %v", operation, allowed, err)
		}
	}
	closeRead()
	// Consent on admits every name; the legacy per-op switch narrows only its own
	// five stages.
	f.setConsent(2, true)
	tx, closeRead = f.readTx(t)
	for _, operation := range operations {
		allowed, err := sourceStageAllowedTx(ctx, tx, f.source, operation)
		if err != nil || !allowed {
			closeRead()
			t.Fatalf("consent on refused stage %q: %v %v", operation, allowed, err)
		}
	}
	closeRead()
	// A new snapshot is required: the legacy switch changed after the previous one.
	f.setSeekPreviews(false)
	tx, closeRead = f.readTx(t)
	for _, c := range []struct {
		operation string
		allowed   bool
	}{{"trickplay", false}, {"chapter_images", true}, {"sonic", true}, {"segment_audio", true}} {
		allowed, err := sourceStageAllowedTx(ctx, tx, f.source, c.operation)
		if err != nil || allowed != c.allowed {
			closeRead()
			t.Fatalf("legacy switch on %q: allowed=%v want %v err=%v", c.operation, allowed, c.allowed, err)
		}
	}
	closeRead()
	// A local folder keeps its library policy: the source gate itself allows.
	local := newAnalysisConsentFixture(t, false, false)
	tx, closeRead = local.readTx(t)
	defer closeRead()
	for _, operation := range operations {
		allowed, err := sourceStageAllowedTx(ctx, tx, local.source, operation)
		if err != nil || !allowed {
			t.Fatalf("local folder was gated for %q: %v %v", operation, allowed, err)
		}
	}
}

// A missing or default-off profile is refused even when the legacy whole-file flag
// and a complete preset are on.
func TestRemoteConsentIsNotWholeFileOrPreset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	f := newAnalysisConsentFixture(t, true, false)
	tx, closeRead := f.readTx(t)
	allowed, err := sourceStageAllowedTx(ctx, tx, f.source, "sonic")
	closeRead()
	if err != nil || allowed {
		t.Fatalf("legacy whole-file flag supplied consent: %v %v", allowed, err)
	}
	f.c.Exec(`DELETE FROM remote_source_read_profiles WHERE source_id=?`, f.remote)
	tx, closeRead = f.readTx(t)
	defer closeRead()
	allowed, err = sourceStageAllowedTx(ctx, tx, f.source, "sonic")
	if err != nil || allowed {
		t.Fatalf("missing profile supplied consent: %v %v", allowed, err)
	}
}

// The existing policy/cancel watcher refuses off consent and a missing object even
// when no scheduling hook is wired.
func TestContinueAdmissionRefusesWithoutConsentOrObject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	f := newAnalysisConsentFixture(t, true, false)
	f.stageRun("job", f.object, "sonic", "running")
	s := &Service{db: f.c.DB}
	if allowed, err := s.ContinueAdmission(ctx, "job", f.object); err != nil || allowed {
		t.Fatalf("nil schedule hook admitted a source without consent: %v %v", allowed, err)
	}
	f.setConsent(2, true)
	if allowed, err := s.ContinueAdmission(ctx, "job", f.object); err != nil || !allowed {
		t.Fatalf("consented source was refused: %v %v", allowed, err)
	}
	f.setSeekPreviews(false)
	f.c.Exec(`UPDATE analysis_stage_runs SET stage='trickplay' WHERE job_id='job' AND object_id=?`, f.object)
	if allowed, err := s.ContinueAdmission(ctx, "job", f.object); err != nil || allowed {
		t.Fatalf("mid-decode legacy switch was ignored: %v %v", allowed, err)
	}
	f.stageRun("job", "obj-missing", "sonic", "running")
	if allowed, err := s.ContinueAdmission(ctx, "job", "obj-missing"); err != nil || allowed {
		t.Fatalf("missing object was admitted: %v %v", allowed, err)
	}
}

// Read facts capture the exact profile/generation and refuse a later disable,
// re-enable or generation change as the same original work instead of adopting a
// new witness.
func TestReadFactsCaptureProfileRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	f := newAnalysisConsentFixture(t, true, true)
	capture := func() ReadFacts {
		t.Helper()
		tx, closeRead := f.readTx(t)
		defer closeRead()
		facts, err := readFactsTx(ctx, tx, f.source, f.object, f.revision, f.item, f.asset)
		if err != nil {
			t.Fatal(err)
		}
		return facts
	}
	check := func(facts ReadFacts) error {
		tx, closeRead := f.readTx(t)
		defer closeRead()
		return checkReadFactsTx(ctx, tx, facts)
	}
	facts := capture()
	if !facts.Consent.Remote || !facts.Consent.Allowed || facts.Consent.ProfileRevision != 2 || facts.Consent.SourceID != f.remote || facts.Consent.ConfigurationGeneration != "5" {
		t.Fatalf("captured consent was not the exact profile: %+v", facts.Consent)
	}
	if facts.Size != 1000 || facts.ModifiedNS != 1 || facts.ObjectID != f.object || facts.ObjectRevision != f.revision || !facts.Source.Enabled || !facts.Source.IdentityConfirmed {
		t.Fatalf("captured physical facts were wrong: %+v", facts)
	}
	if err := check(facts); err != nil {
		t.Fatalf("unchanged facts refused: %v", err)
	}
	// A matching scalar root is not confirmation: the ready-source prerequisite
	// requires the explicit identity confirmation on every read.
	f.c.Exec(`UPDATE library_sources SET identity_confirmed=0 WHERE id=?`, f.source)
	if err := check(facts); err == nil {
		t.Fatal("unconfirmed root identity was accepted for a read")
	}
	f.c.Exec(`UPDATE library_sources SET identity_confirmed=1 WHERE id=?`, f.source)
	if err := check(facts); err != nil {
		t.Fatalf("restored confirmed facts refused: %v", err)
	}
	// Consent is one-way for this original: a later revision may enable it again
	// for new work, but the captured proof is never the current one.
	f.setConsent(3, false)
	if err := check(facts); !errors.Is(err, storage.ErrRemoteAnalysisDisabled) {
		t.Fatalf("disabled consent was not refused: %v", err)
	}
	f.setConsent(4, true)
	if err := check(facts); !errors.Is(err, context.Canceled) {
		t.Fatalf("the older proof was accepted after re-enabling at a new revision: %v", err)
	}
	fresh := capture()
	if fresh.Consent.ProfileRevision != 4 || !fresh.Consent.Allowed {
		t.Fatalf("new work did not capture the new profile: %+v", fresh.Consent)
	}
	if err := check(fresh); err != nil {
		t.Fatalf("new work's own facts were refused: %v", err)
	}
	f.c.Exec(`UPDATE library_sources SET generation=6 WHERE id=?`, f.source)
	if err := check(fresh); !errors.Is(err, context.Canceled) {
		t.Fatalf("new source generation was adopted by the earlier proof: %v", err)
	}
}

// The scan producer's read context binds the original job, object, queued
// revision and policy membership. A failed check retires that original for good:
// restoring the tuple does not revive it, and cleanup of one invocation leaves
// another intact.
func TestScanReadContextRequiresOriginalQueuedMembership(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	f := newAnalysisConsentFixture(t, false, false)
	f.c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('job-1','movies','running','2026-01-01T00:00:00Z')`)
	f.c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES('job-1',?,1,'inc','rootid',1)`, f.source)
	f.c.Exec(`INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision,state) VALUES('job-1','obj-1','r1',1,'pending')`)
	tx, closeRead, err := dbwork.BeginRead(ctx, f.c.DB)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := readFactsTx(ctx, tx, f.source, f.object, f.revision, f.item, f.asset)
	closeRead()
	if err != nil {
		t.Fatal(err)
	}
	bindingFor := func(policyRevision int64) binding {
		return binding{Request: Request{JobID: "job-1", ObjectID: f.object, SourceRevision: f.revision, PolicyRevision: policyRevision},
			Source: facts.Source, Consent: facts.Consent, AssetID: f.asset, ItemID: f.item, SourceSize: facts.Size, SourceModifiedNS: facts.ModifiedNS}
	}
	s := &Service{db: f.c.DB}
	open := func(policyRevision int64) (context.Context, *ReadInvocation) {
		return s.scanReadContext(ctx, bindingFor(policyRevision))
	}
	// fresh returns an invocation that is accepted under the current valid setup,
	// so every mutation below starts from a live original.
	fresh := func(t *testing.T) (context.Context, *ReadInvocation) {
		t.Helper()
		life, invocation := open(1)
		if err := invocation.Check(life); err != nil {
			t.Fatalf("valid queued tuple was refused: %v", err)
		}
		return life, invocation
	}
	firstLife, first := fresh(t)
	defer first.Close()
	secondLife, second := fresh(t)
	defer second.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Check(firstLife); err == nil {
		t.Fatal("closed invocation still read")
	}
	if err := second.Check(secondLife); err != nil {
		t.Fatalf("closing one invocation disturbed another: %v", err)
	}
	// A moved queued revision fails and retires this original; restoring the same
	// tuple afterwards must not revive it.
	f.c.Exec(`UPDATE inventory_analysis_queue SET revision='r2' WHERE job_id='job-1'`)
	if err := second.Check(secondLife); err == nil {
		t.Fatal("a moved queued revision was accepted")
	}
	f.c.Exec(`UPDATE inventory_analysis_queue SET revision='r1' WHERE job_id='job-1'`)
	if err := second.Check(secondLife); err == nil {
		t.Fatal("a retired invocation revived after its tuple was restored")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	// Removed tuple: a genuinely new original is refused and retires.
	removedLife, removed := fresh(t)
	f.c.Exec(`DELETE FROM inventory_analysis_queue WHERE job_id='job-1'`)
	if err := removed.Check(removedLife); err == nil {
		t.Fatal("a removed queued tuple was accepted")
	}
	f.c.Exec(`INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision,state) VALUES('job-1','obj-1','r1',1,'pending')`)
	defer removed.Close()
	// Replaced object revision.
	replacedLife, replaced := fresh(t)
	f.c.Exec(`UPDATE inventory_objects SET revision='r2' WHERE id='obj-1'`)
	if err := replaced.Check(replacedLife); err == nil {
		t.Fatal("a replaced object revision was accepted")
	}
	f.c.Exec(`UPDATE inventory_objects SET revision='r1' WHERE id='obj-1'`)
	defer replaced.Close()
	// A job that is no longer running.
	stoppedLife, stopped := fresh(t)
	f.c.Exec(`UPDATE jobs SET status='failed' WHERE id='job-1'`)
	if err := stopped.Check(stoppedLife); err == nil {
		t.Fatal("a job that is no longer running was accepted")
	}
	f.c.Exec(`UPDATE jobs SET status='running' WHERE id='job-1'`)
	defer stopped.Close()
	// Another policy revision cannot be adopted by an original binding.
	policyLife, policy := open(2)
	defer policy.Close()
	if err := policy.Check(policyLife); !errors.Is(err, context.Canceled) {
		t.Fatalf("another policy revision was accepted: %v", err)
	}
	// The restored setup still admits a genuinely new original.
	_, final := fresh(t)
	defer final.Close()
}

// Segment detection is queued work under the same consent: off drops the pending
// row without a failure penalty, an explicit thorough mode grants nothing, and the
// opt-in itself changes the catch-up digest so excluded files are reconsidered.
func TestSegmentsNeedConsentAndDigestTracksIt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	f := newAnalysisConsentFixture(t, true, false)
	s := &Service{db: f.c.DB}
	f.c.Exec(`INSERT INTO segment_files(object_id,source_revision,library_id,show_id,mode,planning_policy,state,attempt,updated_ms) VALUES(?,'r1','movies',0,'thorough','planning','pending',2,1)`, f.object)
	enabled := map[string]segmentLibrary{"movies": {ID: "movies", Kind: "movie", Mode: SegmentModeThorough, Planning: "planning"}}
	gated, err := dbwork.Begin(ctx, f.c.DB, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.segmentConsider(ctx, gated.Tx(), f.object, enabled); err != nil {
		gated.Rollback()
		t.Fatalf("consent-off consideration failed: %v", err)
	}
	if err = gated.Commit(); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM segment_files WHERE object_id=?`, f.object).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("consent-off pending row survived: %d %v", rows, err)
	}
	tx, closeRead := f.readTx(t)
	before, err := segmentSourceModesTx(ctx, tx)
	closeRead()
	if err != nil {
		t.Fatal(err)
	}
	f.setConsent(2, true)
	tx, closeRead = f.readTx(t)
	after, err := segmentSourceModesTx(ctx, tx)
	closeRead()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("consent opt-in did not change the catch-up digest")
	}
	f.setConsent(3, true)
	tx, closeRead = f.readTx(t)
	revised, err := segmentSourceModesTx(ctx, tx)
	closeRead()
	if err != nil {
		t.Fatal(err)
	}
	if revised == after {
		t.Fatal("profile revision change did not change the catch-up digest")
	}
}
