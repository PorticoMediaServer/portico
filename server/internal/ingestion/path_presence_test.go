package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSamePathPresenceRecoversMovedLibraryWithoutScan(t *testing.T) {
	f := presenceFixture(t)
	file := filepath.Join(f.root, "Film (2000).mkv")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.root, filepath.Join(t.TempDir(), "old-root")); err != nil {
		t.Fatal(err)
	}
	f.write("Film (2000).mkv", "media")
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE library_sources SET health='root_changed' WHERE id=?`, f.source)
	jobs := f.count(`SELECT count(*) FROM jobs`)
	dbwork.ResetPlans()
	runPresence(t, f)
	if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='ready' AND identity_confirmed=1`, f.source) != 1 {
		t.Fatal("moved source did not recover")
	}
	if f.count(`SELECT count(*) FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id WHERE s.id=? AND o.retired=0 AND o.state='available' AND o.root_incarnation=s.incarnation`, f.source) != 1 {
		t.Fatal("present file was lost")
	}
	if f.count(`SELECT count(*) FROM jobs`) != jobs {
		t.Fatal("presence check queued a content scan")
	}
	var raw, object, revision string
	if err := f.db.QueryRow(`SELECT evidence_json,object_identity,revision FROM inventory_objects WHERE source_id=? AND retired=0`, f.source).Scan(&raw, &object, &revision); err != nil {
		t.Fatal(err)
	}
	var evidence mediasource.InventoryEvidence
	if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
		t.Fatal(err)
	}
	source, err := f.s.catalog.LibrarySource(context.Background(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := storage.LocalInventoryStat(storage.InventoryRequest{Root: f.root, RootIdentity: source.RootIdentity, RelativePath: "Film (2000).mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if want := storage.StableObjectIdentity(observed.ObjectIdentity, source.RootIdentity); object != want || evidence.Object != want {
		t.Fatal("recovery did not publish usable file custody", object, evidence.Object, want)
	}
	if want, err := evidence.Revision(); err != nil || revision != want {
		t.Fatal("recovery did not keep the inventory evidence contract", revision, want, err)
	}
	if f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND analysis_state='done' AND analysis_revision=revision`, f.source) != 1 {
		t.Fatal("presence recovery lost completed content analysis")
	}
	if stats := dbwork.Plans(); stats.Overruns != 0 {
		t.Fatalf("presence exceeded its unchanged plans: %+v", stats.Recent)
	}
}

func TestSamePathEmptyRootAssumesPresentWithoutItemFlips(t *testing.T) {
	f := presenceFixture(t)
	if err := os.Rename(f.root, filepath.Join(t.TempDir(), "unmounted-root")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.root, 0755); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE library_sources SET health='root_changed' WHERE id=?`, f.source)
	runPresence(t, f)
	if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='offline'`, f.source) != 1 {
		t.Fatal("empty former mount was not reported offline")
	}
	if f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND (state!='available' OR retired!=0 OR missing_observations!=0)`, f.source) != 0 {
		t.Fatal("offline source changed individual presence")
	}
}

func TestSamePathDifferentMediaNeedsAttentionWithoutDeleting(t *testing.T) {
	f := presenceFixture(t)
	if err := os.Remove(filepath.Join(f.root, "Film (2000).mkv")); err != nil {
		t.Fatal(err)
	}
	f.write("Different (2020).mkv", "different")
	f.exec(`UPDATE library_sources SET health='root_changed' WHERE id=?`, f.source)
	runPresence(t, f)
	if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='root_changed' AND identity_confirmed=0`, f.source) != 1 {
		t.Fatal("different readable media did not request attention")
	}
	if f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND state='available' AND retired=0`, f.source) != 1 {
		t.Fatal("ambiguous source deleted or marked its catalogue missing")
	}
	source, err := f.s.catalog.CheckSource(context.Background(), f.library, f.source, func(*sql.Tx) error { return nil })
	if err != nil || source.Health != "root_changed" {
		t.Fatal("a root check bypassed the different-media decision", source.Health, err)
	}
	if err := f.s.readySource(context.Background(), "unaccepted-media", &source); !errors.Is(err, catalog.ErrSourceChanged) {
		t.Fatal("a scheduled scan bypassed the different-media decision", err)
	}
}

func TestSamePathPresenceHelperDeadlineDoesNotDeclareStorageOffline(t *testing.T) {
	f := presenceFixture(t)
	source, err := f.s.catalog.LibrarySource(context.Background(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	f.s.storage = &storage.Client{Guard: func(string) error { return context.DeadlineExceeded }}
	err = f.s.readySource(context.Background(), "deadline-proof", &source)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, storage.ErrBusy) || !quantumRetryable(context.Background(), err) {
		t.Fatal("helper timeout became a terminal source failure", err)
	}
	if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='ready'`, f.source) != 1 {
		t.Fatal("helper timeout changed source health")
	}
}

func TestSamePathPresenceRecoversPreviouslyAcceptedIncarnation(t *testing.T) {
	f := presenceFixture(t)
	f.exec(`UPDATE library_sources SET incarnation='accepted-before-fix',generation=generation+1,health='offline',last_complete_at='' WHERE id=?`, f.source)
	runPresence(t, f)
	if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='ready'`, f.source) != 1 ||
		f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND root_incarnation='accepted-before-fix' AND state='available' AND retired=0`, f.source) != 1 {
		t.Fatal("previous manual acceptance did not recover its files")
	}
}

func TestSamePathPresenceRecoversAcceptedReadySourceBeforeItsScan(t *testing.T) {
	f := presenceFixture(t)
	f.exec(`UPDATE library_sources SET incarnation='accepted-before-fix',generation=generation+1,health='ready',last_complete_at='' WHERE id=?`, f.source)
	runPresence(t, f)
	if f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND root_incarnation='accepted-before-fix' AND state='available' AND retired=0`, f.source) != 1 {
		t.Fatal("accepted ready source skipped its existing files")
	}
}

func TestSamePathPresenceSkipsUnchangedHealthySource(t *testing.T) {
	f := presenceFixture(t)
	f.exec(`UPDATE library_sources SET health='healthy' WHERE id=?`, f.source)
	dbwork.ResetPlans()
	runPresence(t, f)
	if stats := dbwork.Plans(); stats.Measured != 0 {
		t.Fatal("unchanged healthy source started a presence walk", stats)
	}
	if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='healthy'`, f.source) != 1 {
		t.Fatal("unchanged source lost its completed-scan health")
	}
}

func TestSamePathPresenceLeavesRemoteOwnedSourcesAlone(t *testing.T) {
	f := presenceFixture(t)
	f.exec(`INSERT INTO remote_sources(id,kind,name,root) VALUES('remote-owner','webdav','Remote','/remote')`)
	f.exec(`UPDATE library_sources SET remote_source_id='remote-owner',health='root_changed',root_identity='remote-custody' WHERE id=?`, f.source)
	dbwork.ResetPlans()
	runPresence(t, f)
	if stats := dbwork.Plans(); stats.Measured != 0 {
		t.Fatal("local presence worker wrote a remote-owned source", stats)
	}
	if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='root_changed' AND root_identity='remote-custody'`, f.source) != 1 {
		t.Fatal("local presence worker changed remote custody")
	}
}

func TestSamePathDifferentMediaScanStopsBeforePublishingAbsence(t *testing.T) {
	for _, state := range []string{"native-change", "offline", "settings-change"} {
		t.Run(state, func(t *testing.T) {
			f := presenceFixture(t)
			if err := os.Remove(filepath.Join(f.root, "Film (2000).mkv")); err != nil {
				t.Fatal(err)
			}
			f.write("Different (2020).mkv", "different")
			if state == "offline" {
				f.exec(`UPDATE library_sources SET health='offline' WHERE id=?`, f.source)
			} else {
				f.exec(`UPDATE library_sources SET root_identity='prior-machine',health='healthy' WHERE id=?`, f.source)
				if state == "settings-change" {
					f.exec(`UPDATE library_sources SET generation=generation+1,last_complete_at='' WHERE id=?`, f.source)
				}
			}
			job := f.queue()
			for turn := 0; turn < 100; turn++ {
				f.s.process(context.Background(), job.ID, f.library)
				current, err := f.s.Get(job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if current.Status == "failed" {
					var code string
					if err := f.db.QueryRow(`SELECT error FROM jobs WHERE id=?`, job.ID).Scan(&code); err != nil {
						t.Fatal(err)
					}
					if code != "scan_root_changed" {
						t.Fatal("scan failed for an unrelated reason", current)
					}
					if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='root_changed' AND identity_confirmed=0`, f.source) != 1 ||
						f.count(`SELECT count(*) FROM inventory_objects WHERE id='presence-object' AND state='available' AND retired=0 AND missing_observations=0`) != 1 {
						t.Fatal("different-media scan did not preserve the old catalogue for its owner")
					}
					return
				}
				if current.Status == "complete" || current.Status == "complete_with_warnings" {
					t.Fatal("different-media scan completed without confirmation", current)
				}
			}
			t.Fatal("different-media scan did not stop")
		})
	}
}

func TestSamePathScanAcceptsAnExistingPathOnTheMovedRoot(t *testing.T) {
	f := presenceFixture(t)
	f.exec(`UPDATE library_sources SET root_identity='prior-machine',health='healthy' WHERE id=?`, f.source)
	job := f.queue()
	f.finish(job)
	if f.count(`SELECT count(*) FROM inventory_runs WHERE job_id=? AND root_rebound=2`, job.ID) != 1 {
		t.Fatal("scan did not carry its catalogued-path witness")
	}
}

func TestSamePathMoveDuringScanRetainsFilesWithoutOwnerConfirmation(t *testing.T) {
	f := presenceFixture(t)
	job := f.queue()
	f.exec(`UPDATE inventory_runs SET discovered=1 WHERE job_id=?`, job.ID)
	f.exec(`UPDATE library_sources SET root_identity='prior-machine' WHERE id=?`, f.source)
	f.s.process(context.Background(), job.ID, f.library)
	if f.count(`SELECT count(*) FROM jobs WHERE id=? AND status='failed' AND error='scan_source_unavailable'`, job.ID) != 1 ||
		f.count(`SELECT count(*) FROM inventory_objects WHERE id='presence-object' AND state='available' AND retired=0 AND missing_observations=0`) != 1 {
		t.Fatal("a moved root published the interrupted listing's absences")
	}
	runPresence(t, f)
	if f.count(`SELECT count(*) FROM library_sources WHERE id=? AND health='ready' AND identity_confirmed=1`, f.source) != 1 ||
		f.count(`SELECT count(*) FROM jobs WHERE status IN('queued','running')`) != 0 {
		t.Fatal("interrupted move did not recover without confirmation or a rescan")
	}
}

func runPresence(t *testing.T, f *loopFixture) {
	t.Helper()
	p := &pathPresence{s: f.s}
	for turn := 0; turn < 100; turn++ {
		if err := p.advance(dbwork.WithClass(context.Background(), dbwork.ClassMaintenance)); err != nil {
			t.Fatal(err)
		}
		if p.current == nil && p.after == "" {
			return
		}
	}
	t.Fatal("bounded presence walk did not finish")
}

func presenceFixture(t *testing.T) *loopFixture {
	t.Helper()
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	source, err := f.s.catalog.LibrarySource(context.Background(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := storage.LocalInventoryStat(storage.InventoryRequest{Root: f.root, RootIdentity: source.RootIdentity, RelativePath: "Film (2000).mkv"})
	if err != nil {
		t.Fatal(err)
	}
	observed.ObjectIdentity = storage.StableObjectIdentity(observed.ObjectIdentity, source.RootIdentity)
	revision, evidence, err := catalog.ObservationRevision(source, observed)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available) VALUES('presence-asset',?,?,?,'mkv','','',0,0,0,1)`, observed.Path, observed.Size, observed.ModifiedNS)
	f.exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,analysis_state,analysis_revision) VALUES('presence-object',?,'presence-asset',?,'Film (2000).mkv',?,?,?,?,?,'done',?)`, source.ID, source.Incarnation, observed.ObjectIdentity, revision, evidence, observed.Size, observed.ModifiedNS, revision)
	return f
}
