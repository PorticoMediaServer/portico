package remotesources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type observationFixture struct {
	s                  *Service
	file, job, library string
}

// This fixture needs no rclone/native helper, provider, network or actual media
// bytes. It supplies scan metadata and exercises the real durable job fences.
func newObservationFixture(t *testing.T) observationFixture {
	t.Helper()
	state := t.TempDir()
	root := filepath.Join(state, "remote")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(state, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	lib, err := catalog.New(db).Create("Remote", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO remote_sources(id,kind,name,root,generation) VALUES('remote','webdav','Remote',?,1)`, []any{root}},
		// The normalized binding is part of the known fixture: the source above and
		// this library source are the same remote source, so record its id.
		{`UPDATE library_sources SET kind='remote',remote_source_id='remote',root_identity=?,identity_confirmed=1 WHERE id=?`, []any{storage.RemoteDirectoryIdentity("remote", root) + ":generation:1", lib.ID}},
		{`INSERT INTO jobs(id,library_id,status,created_at) VALUES('scan',?,'running','2026-10-23T00:00:00Z')`, []any{lib.ID}},
		{`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) SELECT 'scan',id,generation,incarnation,root_identity,1 FROM library_sources WHERE id=?`, []any{lib.ID}},
		{`INSERT INTO remote_scan_generations(job_id,source_id,generation) VALUES('scan','remote','1')`, nil},
	} {
		if _, err = db.Exec(command.sql, command.args...); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{db: db}
	s.bindings.Store([]binding{{ID: "remote", Kind: "webdav", Root: root}})
	return observationFixture{s: s, file: filepath.Join(root, "film.mkv"), job: "scan", library: lib.ID}
}

func scanFacts(label string) ScanObservation {
	return ScanObservation{Facts: remotemedia.FileObservation{SourceID: "remote", Incarnation: "1", Object: "film.mkv", ObjectID: "object", Size: 16, ModifiedNS: 23, ModifiedKnown: true, ProviderETag: `"provider-` + label + `"`, ServeETag: `"served"`, ProviderHashes: []remotemedia.ProviderHash{{Kind: "opaque-provider-kind", Value: label}}}}
}

func (f observationFixture) record(t *testing.T, value ScanObservation, expected int64) (ObservationRef, error) {
	t.Helper()
	gated, err := dbwork.Begin(context.Background(), f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer gated.Rollback()
	if value.ScanRevision == "" {
		b, _, _ := f.s.find(f.file)
		snapshot, e := observationSnapshot(b, value.Facts.Incarnation, value.Facts)
		if e != nil {
			return ObservationRef{}, e
		}
		value.ScanRevision = snapshot.Revision
	}
	ref, err := f.s.RecordScanObservationTx(context.Background(), gated.Tx(), f.job, f.file, value, expected)
	if err != nil {
		return ObservationRef{}, err
	}
	return ref, gated.Commit()
}

func TestScanObservationCASAndABANeverReviveOldReference(t *testing.T) {
	f := newObservationFixture(t)
	first, err := f.record(t, scanFacts("a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.record(t, scanFacts("b"), 0); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("stale create accepted", err)
	}
	same, err := f.record(t, scanFacts("a"), first.Revision)
	if err != nil || same != first {
		t.Fatal("unchanged facts changed revision", same, err)
	}
	second, err := f.record(t, scanFacts("b"), first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	third, err := f.record(t, scanFacts("a"), second.Revision)
	if err != nil || third.Revision != first.Revision+2 {
		t.Fatal(third, err)
	}
	if err = f.s.CheckScanObservation(context.Background(), first); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("old A reference revived", err)
	}
	out, err := f.s.ScanObservation(context.Background(), f.file)
	if err != nil || out.Ref != third || !out.Observation.Facts.Equal(scanFacts("a").Facts) {
		t.Fatal(out, err)
	}
	if len(out.Observation.Facts.ProviderHashes) != 1 || out.Observation.Facts.ProviderETag == out.Observation.Facts.ServeETag {
		t.Fatal("provider/served facts conflated")
	}
}

func TestScanObservationRequiresLiveJobRootAndRemoteGeneration(t *testing.T) {
	for _, change := range []string{"cancel", "root", "remote", "scope", "missing-job"} {
		t.Run(change, func(t *testing.T) {
			f := newObservationFixture(t)
			facts := scanFacts("a")
			var err error
			switch change {
			case "cancel":
				_, err = f.s.db.Exec(`UPDATE jobs SET status='cancelled' WHERE id='scan'`)
			case "root":
				_, err = f.s.db.Exec(`UPDATE library_sources SET incarnation='replacement' WHERE id=?`, f.library)
			case "remote":
				_, err = f.s.db.Exec(`UPDATE remote_sources SET generation=generation+1 WHERE id='remote'`)
			case "scope":
				facts.Facts.SourceID = "other"
			case "missing-job":
				f.job = "not-a-job"
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.record(t, facts, 0); err == nil {
				t.Fatal("missing scan authority accepted")
			}
			var count int
			if err = f.s.db.QueryRow(`SELECT count(*) FROM remote_scan_observations`).Scan(&count); err != nil || count != 0 {
				t.Fatal("partial facts committed", count, err)
			}
		})
	}
}

func TestScanObservationStoresMissingTimeAndHashesWithoutInventingThem(t *testing.T) {
	f := newObservationFixture(t)
	value := scanFacts("unknown")
	value.Facts.ModifiedKnown = false
	value.Facts.ModifiedNS = 0
	value.Facts.ProviderETag = ""
	value.Facts.ProviderHashes = nil
	value.Facts.ServeETag = ""
	if _, err := f.record(t, value, 0); err != nil {
		t.Fatal(err)
	}
	out, err := f.s.ScanObservation(context.Background(), f.file)
	if err != nil || out.Observation.Facts.ModifiedKnown || out.Observation.Facts.ProviderETag != "" || len(out.Observation.Facts.ProviderHashes) != 0 || out.Observation.Facts.ValidateForRange() == nil {
		t.Fatal("unknown scan facts promoted", out, err)
	}
}

func (f observationFixture) catalogObservationRevision(t *testing.T) string {
	t.Helper()
	observed, err := f.s.ScanObservation(context.Background(), f.file)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := f.s.find(f.file)
	transport, err := observationSnapshot(b, observed.Ref.Generation, observed.Observation.Facts)
	if err != nil {
		t.Fatal(err)
	}
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	source, err := catalog.InventoryFenceTx(context.Background(), view.Tx(), f.job)
	if err != nil {
		t.Fatal(err)
	}
	revision, _, err := catalog.ObservationRevision(source, transport)
	if err != nil {
		t.Fatal(err)
	}
	if revision == transport.Revision {
		t.Fatal("catalog revision aliased transport observation")
	}
	return revision
}
func TestScanObservationRejectsCallerInventedTransportRevision(t *testing.T) {
	f := newObservationFixture(t)
	value := scanFacts("forged")
	value.ScanRevision = "1:invented"
	if _, err := f.record(t, value, 0); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("invented transport scan revision accepted", err)
	}
}
