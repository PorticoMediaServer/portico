package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// A scan quantum can be refused before it does any work: a WAL reset pauses
// reader admission, and dbwork answers dbwork.ErrReadPressure for a read that
// arrives during the pause; the writer gate refuses an ordinary writer when its
// user ticket pool is full (dbwork.ErrWriterCapacity); and storage may be busy
// with another operation. None of these describe the source, and none changed
// anything, so the run must keep its durable cursor and pending work and be
// revisited on the loop's ordinary schedule instead of being failed.
//
// These tests drive the production processQuantum, the real claim transaction,
// the real readiness fence, the real failure bookkeeping and the real writer
// gate. The WAL-pressure test injects the production sentinel at the remote
// adapter seam and is labelled as such: it proves the typed-refusal boundary,
// not gate capacity. The capacity test produces its refusal with the public
// dbwork gate itself. The fatal-negative tests use errors the database really
// raises.
const remoteRootIdentity = "remote-root-identity"

// refusalRemote is a minimal remote adapter: it publishes no files and answers
// only the calls an empty-source scan makes. It can refuse from CheckScan in
// two ways: with the dbwork WAL-pressure sentinel, or with the real refusal of
// its own saturated writer gate.
type refusalRemote struct {
	storage.RemoteSources
	db         *sql.DB
	sourceID   string
	generation string
	root       string
	pressure   atomic.Bool
	gate       *dbwork.Gate
	gateWanted atomic.Bool
	gateRefuse atomic.Bool
}

func (r *refusalRemote) Handles(path string) bool { return path == r.root }

func (r *refusalRemote) InspectRoot(_ context.Context, path string) (storage.Snapshot, error) {
	return storage.Snapshot{Path: path, Name: path, Directory: true, ObjectIdentity: remoteRootIdentity}, nil
}

func (r *refusalRemote) CheckScan(ctx context.Context, job, root string) error {
	if r.pressure.Load() {
		return dbwork.ErrReadPressure
	}
	if r.gateWanted.Load() {
		// An ordinary writer asks its gate for the writer turn while the pool is
		// full; the gate refuses it before it begins.
		refused, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		release, err := r.gate.Acquire(refused, dbwork.ClassInteractive)
		if err == nil {
			release()
		} else if errors.Is(err, dbwork.ErrWriterCapacity) {
			r.gateRefuse.Store(true)
		}
		if err != nil {
			return err
		}
	}
	// The same durable generation fence the real adapter writes at first
	// contact; later phases validate it before publishing.
	if _, err := dbwork.ExecWrite(ctx, r.db, dbwork.ClassBackgroundMedia, `INSERT OR IGNORE INTO remote_scan_generations(job_id,source_id,generation) VALUES(?,?,?)`, job, r.sourceID, r.generation); err != nil {
		return err
	}
	var expected string
	if err := r.db.QueryRowContext(ctx, `SELECT generation FROM remote_scan_generations WHERE job_id=? AND source_id=?`, job, r.sourceID).Scan(&expected); err != nil {
		return err
	}
	if expected != r.generation {
		return storage.ErrRemoteChanged
	}
	return nil
}

func (r *refusalRemote) ValidateScan(ctx context.Context, tx *sql.Tx, job, root string) error {
	var expected, current string
	var removed bool
	if err := tx.QueryRowContext(ctx, `SELECT g.generation,CAST(s.generation AS TEXT),s.removed FROM remote_scan_generations g JOIN remote_sources s ON s.id=g.source_id WHERE g.job_id=? AND s.id=?`, job, r.sourceID).Scan(&expected, &current, &removed); err != nil {
		return err
	}
	if removed || expected != current {
		return storage.ErrRemoteChanged
	}
	return nil
}

func (r *refusalRemote) ListInventoryPage(_ context.Context, _ string, directory, cursor string, _ int) (storage.RemotePage, error) {
	return storage.RemotePage{ID: "remote-dir", SourceID: r.sourceID, Directory: directory, Generation: r.generation, Cursor: cursor, Complete: true}, nil
}

func (r *refusalRemote) CommitPage(context.Context, *sql.Tx, storage.RemotePage) error {
	return nil
}

func (r *refusalRemote) VerifyInventoryDirectory(context.Context, string, string, string) error {
	return nil
}

// newRefusalFixture attaches an empty remote source to a real library.
func newRefusalFixture(t *testing.T) (*loopFixture, *refusalRemote) {
	t.Helper()
	f := newLoopFixture(t, "movie")
	root := filepath.Join(string(filepath.Separator), "remote", "library")
	remote := &refusalRemote{db: f.db, sourceID: f.source, generation: "1", root: root}
	f.exec(`INSERT INTO remote_sources(id,kind,name,root,generation) VALUES(?,?,?,?,1)`, f.source, "webdav", "Remote", root)
	f.exec(`UPDATE library_sources SET kind='remote',classification='network',configured_root=?,root=?,root_identity=?,remote_source_id=? WHERE id=?`, root, root, remoteRootIdentity, f.source, f.source)
	client := &storage.Client{Remote: remote}
	f.s.SetStorage(client)
	f.s.catalog.SetStorage(client)
	return f, remote
}

// saturatedGate holds the writer turn and fills every user ticket with a queued
// security fence (the highest class), each under a distinct owner so the
// over-share rule cannot make room. An ordinary writer is then refused, which is
// the pool's real capacity answer, not a synthetic error. Everything is
// released before the test ends.
func saturatedGate(t *testing.T) (*dbwork.Gate, func()) {
	t.Helper()
	gate := dbwork.NewGate()
	hold, err := gate.Acquire(context.Background(), dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	cancels := make([]context.CancelFunc, 0, dbwork.WriterQueueCapacity)
	var queued sync.WaitGroup
	for n := 0; n < dbwork.WriterQueueCapacity; n++ {
		owner := fmt.Sprintf("queued-owner-%d", n)
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		queued.Add(1)
		go func() {
			defer queued.Done()
			release, err := gate.Acquire(dbwork.WithOwner(ctx, owner), dbwork.ClassSecurityFence)
			if err == nil {
				release()
			}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for gate.Admission().UserQueued < dbwork.WriterQueueCapacity {
		if time.Now().After(deadline) {
			for _, cancel := range cancels {
				cancel()
			}
			queued.Wait()
			hold()
			t.Fatal("writer gate did not fill its user ticket pool")
		}
		time.Sleep(time.Millisecond)
	}
	releasePool := sync.OnceFunc(func() {
		for _, cancel := range cancels {
			cancel()
		}
		queued.Wait()
		hold()
		if gate.ActiveOrWaiting() {
			t.Error("saturated gate kept a ticket after cleanup")
		}
	})
	t.Cleanup(releasePool)
	return gate, releasePool
}

func TestQuantumKeepsRunOnGateWriterCapacityRefusal(t *testing.T) {
	f, remote := newRefusalFixture(t)
	var releasePool func()
	remote.gate, releasePool = saturatedGate(t)
	job := f.queue()
	remote.gateWanted.Store(true)
	if f.s.processQuantum(context.Background(), job.ID, f.library) {
		t.Fatal("a refused quantum reported durable progress")
	}
	if !remote.gateRefuse.Load() {
		t.Fatal("the writer gate did not refuse the readiness write")
	}
	if stats := remote.gate.Admission(); stats.Refused == 0 || stats.RefusedByClass[dbwork.ClassInteractive.String()] == 0 {
		t.Fatalf("capacity refusal not recorded by the gate: %+v", stats)
	}
	state, err := f.s.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "running" || state.Error != "" || state.Phase != "inventory" {
		t.Fatalf("gate capacity refusal failed the run: %+v", state)
	}
	var health, code string
	if err = f.db.QueryRow(`SELECT s.health,COALESCE(j.error,'') FROM library_sources s JOIN jobs j ON j.id=? WHERE s.id=?`, job.ID, f.source).Scan(&health, &code); err != nil {
		t.Fatal(err)
	}
	if health == "degraded" || health == "offline" || code != "" {
		t.Fatalf("capacity refusal recorded as a source failure: health=%q error=%q", health, code)
	}
	if n := f.count(`SELECT count(*) FROM inventory_source_active WHERE job_id=?`, job.ID); n != 1 {
		t.Fatal("refused quantum released its durable claim")
	}
	// The pool drains without intervention: the same run continues from its own
	// cursor instead of needing a new one.
	releasePool()
	remote.gateWanted.Store(false)
	f.finish(job)
	if n := f.count(`SELECT count(*) FROM inventory_source_active WHERE job_id=?`, job.ID); n != 0 {
		t.Fatal("finished run kept its claim")
	}
}

func TestQuantumKeepsRunOnAdapterReadPressureRefusal(t *testing.T) {
	f, remote := newRefusalFixture(t)
	job := f.queue()
	remote.pressure.Store(true)
	if f.s.processQuantum(context.Background(), job.ID, f.library) {
		t.Fatal("a refused quantum reported durable progress")
	}
	state, err := f.s.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "running" || state.Error != "" || state.Phase != "inventory" {
		t.Fatalf("precommit read pressure failed the run: %+v", state)
	}
	var health, code string
	if err = f.db.QueryRow(`SELECT s.health,COALESCE(j.error,'') FROM library_sources s JOIN jobs j ON j.id=? WHERE s.id=?`, job.ID, f.source).Scan(&health, &code); err != nil {
		t.Fatal(err)
	}
	if health == "degraded" || health == "offline" || code != "" {
		t.Fatalf("refusal recorded as a source failure: health=%q error=%q", health, code)
	}
	if n := f.count(`SELECT count(*) FROM inventory_source_active WHERE job_id=?`, job.ID); n != 1 {
		t.Fatal("refused quantum released its durable claim")
	}
	remote.pressure.Store(false)
	f.finish(job)
	if n := f.count(`SELECT count(*) FROM inventory_source_active WHERE job_id=?`, job.ID); n != 0 {
		t.Fatal("finished run kept its claim")
	}
}

func TestQuantumStillFailsOnRealConstraintRefusal(t *testing.T) {
	f := newLoopFixture(t, "movie")
	job := f.queue()
	// A real SQLITE_CONSTRAINT from the run's own transition into the extras
	// phase. Retrying cannot help, so it must still fail the scan.
	f.exec(`CREATE TRIGGER test_refuse_extras BEFORE UPDATE OF phase ON inventory_runs WHEN NEW.phase='extras' BEGIN SELECT RAISE(ABORT,'constraint failed'); END`)
	for n := 0; n < 40; n++ {
		f.s.processQuantum(context.Background(), job.ID, f.library)
		state, err := f.s.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "failed" {
			break
		}
	}
	state, err := f.s.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "failed" {
		t.Fatalf("constraint refusal left the run %q", state.Status)
	}
	var code, health string
	if err = f.db.QueryRow(`SELECT j.error,s.health FROM jobs j JOIN library_sources s ON s.id=? WHERE j.id=?`, f.source, job.ID).Scan(&code, &health); err != nil {
		t.Fatal(err)
	}
	if code != "scan_inventory_incomplete" || health != "degraded" {
		t.Fatalf("constraint failure recorded as %q/%q", code, health)
	}
}

func TestQuantumStillFailsOnUnknownPhase(t *testing.T) {
	f := newLoopFixture(t, "movie")
	job := f.queue()
	f.exec(`UPDATE inventory_runs SET phase='unexpected' WHERE job_id=?`, job.ID)
	if f.s.processQuantum(context.Background(), job.ID, f.library) {
		t.Fatal("unknown phase reported progress")
	}
	state, err := f.s.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "failed" {
		t.Fatalf("unknown phase left the run %q", state.Status)
	}
	var code string
	if err = f.db.QueryRow(`SELECT error FROM jobs WHERE id=?`, job.ID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != "scan_inventory_incomplete" {
		t.Fatalf("unknown phase failed as %q", code)
	}
}

func TestQuantumStillFailsOnUnavailableSource(t *testing.T) {
	f := newLoopFixture(t, "movie")
	job := f.queue()
	if err := os.RemoveAll(f.root); err != nil {
		t.Fatal(err)
	}
	if f.s.processQuantum(context.Background(), job.ID, f.library) {
		t.Fatal("unavailable source reported progress")
	}
	state, err := f.s.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "failed" || state.Error != sourceUnavailable {
		t.Fatalf("unavailable source was not failed: %+v", state)
	}
	var health string
	if err = f.db.QueryRow(`SELECT health FROM library_sources WHERE id=?`, f.source).Scan(&health); err != nil {
		t.Fatal(err)
	}
	if health != "offline" {
		t.Fatalf("unavailable source health %q", health)
	}
}
