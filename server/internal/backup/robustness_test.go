package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

func TestPreRestoreCopyKeepsCurrentOnlyOwnerArtworkAndKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, state, db := testService(t)
	ctx := context.Background()
	catalogue := catalogtest.New(t, db)
	library := catalogue.Library("films", "Films", "movie", "/fixture")
	item := catalogue.Movie(library, "/fixture/film.mkv", "Fixture", 2020)
	older, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	meta := metadata.New(db, "")
	if err = meta.SetArtworkDirectory(filepath.Join(state, "Metadata", "artwork")); err != nil {
		t.Fatal(err)
	}
	target := metadata.RepairTarget{Kind: "item", ID: item.Public}
	authorize := func(*sql.Tx) error { return nil }
	before, err := meta.RepairState(ctx, target, authorize)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 24, 36))
	picture.Set(0, 0, color.RGBA{R: 90, A: 255})
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	actor := metadata.MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	if _, err = meta.UploadArtwork(ctx, target, "poster", "", before.Revision, encoded.Bytes(), actor, authorize); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]string{}
	rows, err := db.Query(ownerArtworkReferences)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var digest string
		if err = rows.Scan(&digest); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		wanted["Metadata/artwork/"+digest+".img"] = digest
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(wanted) == 0 {
		t.Fatal("owner artwork fixture was not recorded", err)
	}
	key := "Keys/current-only.key"
	if err = writeTestFile(filepath.Join(state, key), []byte("disposable identity fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	wanted[key] = fileHashOrEmpty(t, filepath.Join(state, key))
	if _, err = s.Stage(ctx, Source{BackupID: older}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = ApplyStaged(ctx, state, nil); err != nil {
		t.Fatal(err)
	}
	listed, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var recovery string
	for _, info := range listed {
		if info.Kind == KindPreRestore {
			recovery = filepath.Join(state, BackupsDir, info.ID)
		}
	}
	if recovery == "" {
		t.Fatal("pre-restore copy was not published")
	}
	manifest, err := readManifest(recovery)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkManifestHashes(recovery, manifest.Files); err != nil {
		t.Fatal(err)
	}
	for name, digest := range wanted {
		if fileHashOrEmpty(t, filepath.Join(recovery, name)) != digest {
			t.Fatalf("recovery copy omitted %s", name)
		}
		if err = os.Remove(filepath.Join(state, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate the restored database's GC dropping the now-unreferenced live
	// files. The recovery folder must still stage and restore by itself.
	if _, err = s.Stage(ctx, Source{Path: recovery}); err != nil {
		t.Fatal("recovery depended on live current-only files", err)
	}
	if err = ApplyStaged(ctx, state, nil); err != nil {
		t.Fatal(err)
	}
	for name, digest := range wanted {
		if fileHashOrEmpty(t, filepath.Join(state, name)) != digest {
			t.Fatalf("recovery did not restore %s", name)
		}
	}
}

func TestPreRestoreCurrentOnlyCopiesSurvivePreparingCrashAndRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, refuse := range []bool{false, true} {
		name := "complete"
		if refuse {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			fixture := stageAfterWrite(t)
			files := []string{"Keys/current-a.key", "Keys/current-b.key"}
			for _, file := range files {
				if err := writeTestFile(filepath.Join(fixture.state, file), []byte(file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			crashAt(t, "preparing:"+files[1])
			if err := ApplyStaged(context.Background(), fixture.state, nil); !errors.Is(err, errCrash) {
				t.Fatalf("did not reach partially copied recovery files: %v", err)
			}
			if got := fileHashOrEmpty(t, storagepaths.Path(fixture.state, "server.sqlite")); got != fixture.liveHash {
				t.Fatal("preparing changed live database")
			}
			for _, file := range files {
				if got, err := os.ReadFile(filepath.Join(fixture.state, file)); err != nil || string(got) != file {
					t.Fatal("preparing changed original key", file, err)
				}
			}
			var verify VerifyFunc
			refused := errors.New("restored state refused")
			if refuse {
				verify = func(context.Context, string) error { return refused }
			}
			err := restart(t, fixture.state, verify)
			if refuse && !errors.Is(err, refused) || !refuse && err != nil {
				t.Fatal(err)
			}
			if RestoreUnresolved(fixture.state) {
				t.Fatal("current-only copies left the restore unresolved")
			}
			for _, file := range files {
				raw, err := os.ReadFile(filepath.Join(fixture.state, file))
				if err != nil || string(raw) != file {
					t.Fatalf("current file %s was changed: %v", file, err)
				}
			}
			if refuse && probe(t, storagepaths.Path(fixture.state, "server.sqlite")) != "live" {
				t.Fatal("rollback did not return the previous database")
			}
		})
	}
}

func TestCurrentOnlyRecoveryInventoryDoesNotRequireAReadablePreviousDatabase(t *testing.T) {
	state, pre := t.TempDir(), t.TempDir()
	if err := writeTestFile(filepath.Join(pre, DatabaseName), []byte("damaged previous database"), 0600); err != nil {
		t.Fatal(err)
	}
	file := "Metadata/artwork/previous.img"
	if err := writeTestFile(filepath.Join(state, file), []byte("previous bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	names, err := currentOnlyStateNames(context.Background(), state, pre)
	if err != nil || len(names) != 1 || names[0] != file {
		t.Fatalf("unreadable previous database prevented preservation: %v %v", names, err)
	}
}

func TestRepairRestorePreservesAvailablePreviousArtwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"missing", "damaged", "unsafe-directory"} {
		t.Run(kind, func(t *testing.T) {
			fixture := stageAfterWrite(t)
			db, err := persistence.Open(storagepaths.Path(fixture.state, "server.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			digest := strings.Repeat("a", 64)
			_, err = db.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at)
 VALUES('previous-upload','item',1,'poster','local','previous-upload','local','fixture','','now')`)
			if err == nil {
				_, err = db.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',1,1,1,'now')`, digest)
			}
			if err == nil {
				_, err = db.Exec(`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?1,'image/jpeg',1,1,?1,?1,?1,1920,0,0,1,'now')`, digest)
			}
			if err == nil {
				_, err = db.Exec(`INSERT INTO artwork_uploads(candidate_id,kind,entity_id,role,digest,mime,bytes,width,height,actor,observed_at) SELECT column1,column2,column3,column4,column5,column7,column8,column9,column10,column11,column12 FROM (VALUES('previous-upload','item',1,'poster',?,?,'image/png',1,1,1,'owner','now'))`, digest, digest)
			}
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			name := "Metadata/artwork/" + digest + ".img"
			previous := []byte("already damaged previous artwork")
			if kind == "damaged" {
				if err = writeTestFile(filepath.Join(fixture.state, name), previous, 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "unsafe-directory" {
				if err = os.MkdirAll(filepath.Join(fixture.state, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			err = ApplyStaged(context.Background(), fixture.state, nil)
			if kind == "unsafe-directory" {
				if !errors.Is(err, ErrSourceInvalid) || probe(t, storagepaths.Path(fixture.state, "server.sqlite")) != "live" {
					t.Fatal("unsafe existing artwork was silently skipped", err)
				}
				return
			}
			if err != nil || probe(t, storagepaths.Path(fixture.state, "server.sqlite")) != "backup" {
				t.Fatal("previous artwork damage prevented the repair restore", err)
			}
			listed, err := fixture.svc.List()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, info := range listed {
				if info.Kind != KindPreRestore {
					continue
				}
				found = true
				pre := filepath.Join(fixture.state, BackupsDir, info.ID)
				manifest, err := readManifest(pre)
				if err != nil {
					t.Fatal(err)
				}
				if err = checkManifestHashes(pre, manifest.Files); err != nil {
					t.Fatal("recovery bytes were not verified", err)
				}
				raw, err := os.ReadFile(filepath.Join(pre, name))
				if kind == "missing" && !os.IsNotExist(err) || kind == "damaged" && (err != nil || !bytes.Equal(raw, previous)) {
					t.Fatal("recovery did not preserve the previous available bytes", err)
				}
			}
			if !found {
				t.Fatal("repair restore did not retain a recovery copy")
			}
		})
	}
}

func waitRestoreTerminal(t *testing.T, s *Service) *RestoreJob {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		progress := s.RestoreProgress()
		if progress != nil && (progress.Phase == "failed" || progress.Phase == "staged") {
			return progress
		}
		select {
		case <-deadline.C:
			t.Fatal("restore did not reach a terminal staging outcome", progress)
		case <-tick.C:
		}
	}
}

func TestCompletedRestoreReplayCannotReplaceAnActiveWorker(t *testing.T) {
	s, state, _ := testService(t)
	sourceID, err := s.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	source := Source{BackupID: sourceID}
	previous := restoreMarker{JobID: "previous-job", OperationID: "previous-operation", Source: source, SchemaVersion: s.schemaVersion}
	if err = writeResult(state, markerRestored, "", previous); err != nil {
		t.Fatal(err)
	}
	// Hold the actual staging boundary so the replay certainly arrives while
	// the new worker is alive, rather than relying on a scheduling sleep.
	s.stageMu.Lock()
	id, startErr := s.StartRestore(context.Background(), source, "current-operation")
	_, replayErr := s.StartRestore(context.Background(), source, "previous-operation")
	active := s.RestoreProgress()
	retry, retryErr := s.StartRestore(context.Background(), source, "current-operation")
	s.stageMu.Unlock()
	progress := waitRestoreTerminal(t, s)
	if startErr != nil || !errors.Is(replayErr, ErrRestorePending) || active == nil || active.JobID != id || retryErr != nil || retry != id {
		t.Fatalf("replay changed active identity: start=%v replay=%v active=%+v retry=%s/%v", startErr, replayErr, active, retry, retryErr)
	}
	if progress.JobID != id || progress.Phase != "staged" {
		t.Fatal(progress)
	}
	raw, err := os.ReadFile(filepath.Join(state, RestoreStagedDir, RestoreMarkerFile))
	if err != nil {
		t.Fatal(err)
	}
	var marker restoreMarker
	if err = json.Unmarshal(raw, &marker); err != nil || marker.JobID != id || marker.OperationID != "current-operation" {
		t.Fatalf("worker journal borrowed another identity: %+v %v", marker, err)
	}
	// A stale callback is also fenced if a later owner has already replaced it.
	s.restoreProgress(restoreIdentity{jobID: "previous-job", operation: "previous-operation"}, "copy", 99, 100)
	if got := s.RestoreProgress(); got.Phase != "staged" || got.JobID != id {
		t.Fatal("stale worker updated current progress", got)
	}
}

func waitBackupJob(t *testing.T, job *Job) {
	t.Helper()
	select {
	case <-job.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("backup worker did not finalize")
	}
}

func TestBackupPanicFinalizesJobAndReleasesAdmission(t *testing.T) {
	s, _, _ := testService(t)
	s.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error { panic("injected backup worker panic") })
	id, err := s.Start(context.Background(), KindManual, "panic-operation")
	if err != nil {
		t.Fatal(err)
	}
	job, ok := s.Job(id)
	if !ok {
		t.Fatal("accepted job disappeared")
	}
	waitBackupJob(t, job)
	if job.Result() == nil {
		t.Fatal("panic reported success")
	}
	if _, running := s.Running(); running {
		t.Fatal("panic retained backup admission")
	}
	if last := s.LastBackup(); last == nil || last.Outcome != "failed" {
		t.Fatal("panic did not publish a failed outcome", last)
	}
	s.SetCopyScrubber(nil)
	retry, err := s.Start(context.Background(), KindManual, "panic-operation")
	if err != nil || retry == id {
		t.Fatal("failed operation could not retry", retry, err)
	}
	job, ok = s.Job(retry)
	if !ok {
		t.Fatal("retry job disappeared")
	}
	waitBackupJob(t, job)
	if err = job.Result(); err != nil {
		t.Fatal("retry failed", err)
	}
}

func TestRestorePanicFinalizesFailureOrRetainsPublishedJournal(t *testing.T) {
	for _, afterPublication := range []bool{false, true} {
		name := "before-publication"
		if afterPublication {
			name = "after-publication"
		}
		t.Run(name, func(t *testing.T) {
			s, state, _ := testService(t)
			id, err := s.Create(context.Background(), KindManual)
			if err != nil {
				t.Fatal(err)
			}
			if afterPublication {
				s.SetRestartFunc(func() { panic("injected restart callback panic") })
			} else {
				s.policy = func(context.Context) (Snapshot, error) { panic("injected source lookup panic") }
			}
			job, err := s.StartRestore(context.Background(), Source{BackupID: id}, "panic-restore-operation")
			if err != nil {
				t.Fatal(err)
			}
			progress := waitRestoreTerminal(t, s)
			if progress.JobID != job {
				t.Fatal("panic changed job identity", progress)
			}
			if afterPublication {
				if progress.Phase != "staged" || progress.Result == nil || !progress.Result.Staged {
					t.Fatal("published restore was marked retryable", progress)
				}
				if _, ok := stagedResult(state, Source{BackupID: id}, restoreIdentity{jobID: job, operation: "panic-restore-operation"}); !ok {
					t.Fatal("published journal was lost")
				}
			} else {
				if progress.Phase != "failed" || progress.ErrorCode != "internal" {
					t.Fatal("panic did not fail restore", progress)
				}
				s.policy = nil
				retry, err := s.StartRestore(context.Background(), Source{BackupID: id}, "panic-restore-operation")
				if err != nil || retry == job {
					t.Fatal("failed restore could not retry", retry, err)
				}
				if progress = waitRestoreTerminal(t, s); progress.Phase != "staged" {
					t.Fatal("restore retry failed", progress)
				}
			}
		})
	}
}

func TestOfflineFormerDestinationDoesNotBlockHealthyBackups(t *testing.T) {
	s, policy, _ := policyFixture(t)
	ctx := context.Background()
	former, healthy := t.TempDir(), t.TempDir()
	if _, err := policy.Update(ctx, SettingsPatch{BackupFolder: &former}); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Update(ctx, SettingsPatch{BackupFolder: &healthy}); err != nil {
		t.Fatal(err)
	}
	// ENOTDIR is a real I/O refusal even under root, unlike chmod-based tests.
	if err := os.Remove(former); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(former, []byte("offline destination fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatal("former destination blocked healthy creation", err)
	}
	listed, err := s.List()
	if err != nil || len(listed) != 1 || listed[0].ID != id {
		t.Fatal("former destination blocked healthy listing", listed, err)
	}
	if err = os.Rename(healthy, healthy+"-offline"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(healthy + "-offline") })
	if err = os.WriteFile(healthy, []byte("current destination unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.List(); err == nil {
		t.Fatal("current destination failure was hidden")
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	window := workpolicy.Policy{BackgroundPriority: "lower", Windows: []workpolicy.Window{{ID: "fixture", Name: "Fixture", Enabled: true, Cadence: "custom", Days: []string{"tuesday"}, StartMinute: 11 * 60, DurationMinutes: 120, Timezone: "UTC", Tasks: []string{"backup"}}}}
	if err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return workpolicy.SaveTx(ctx, tx, window) }); err != nil {
		t.Fatal(err)
	}
	scheduler := s.Schedule(s.db, nil)
	scheduler.now = func() time.Time { return now }
	failures := 0
	scheduler.ReportFailure = func(_ context.Context, failed bool) {
		if failed {
			failures++
		}
	}
	scheduler.check(ctx)
	if failures != 1 {
		t.Fatalf("scheduler hid the current destination failure: %d reports", failures)
	}
}

func TestBackupReceiptsAndAllAliasesExpireTogether(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	s := New(t.TempDir(), nil, "fixture", "fixture", 0)
	s.now = func() time.Time { return now }
	expired := &Job{id: "expired", done: make(chan struct{})}
	expired.finish(nil, now.Add(-backupReplayWindow))
	recent := &Job{id: "recent", done: make(chan struct{})}
	recent.finish(nil, now.Add(-backupReplayWindow+time.Second))
	running := &Job{id: "running", done: make(chan struct{})}
	s.jobs = map[string]*Job{expired.id: expired, recent.id: recent, running.id: running}
	s.operations = map[string]string{"expired-one": expired.id, "expired-two": expired.id, "recent-one": recent.id, "running-one": running.id}
	s.running = running
	if _, ok := s.Job(expired.id); ok {
		t.Fatal("expired job retained")
	}
	if len(s.jobs) != 2 || len(s.operations) != 2 || s.operations["recent-one"] != recent.id || s.operations["running-one"] != running.id {
		t.Fatal("expiry retained aliases or removed a live receipt", s.operations)
	}
	if _, ok := s.Job(recent.id); !ok {
		t.Fatal("receipt inside the replay window was removed")
	}
	now = now.Add(backupReplayWindow)
	if _, ok := s.Job(running.id); !ok || s.running != running {
		t.Fatal("live worker was evicted")
	}
	if len(s.jobs) != 1 || len(s.operations) != 1 {
		t.Fatal("terminal receipts did not leave the replay window")
	}
}
