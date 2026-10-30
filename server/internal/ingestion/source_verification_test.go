package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// A returning source owes exactly one walk, and that walk may only ever write
// what it can prove: a file absent from a readable parent under the source's own
// root identity. Everything else here is about the things that must never
// happen — a second walk, a mark made from stale evidence, and a walk that
// competes with a scan.

type verificationFixture struct {
	t       *testing.T
	db      *sql.DB
	path    string
	catalog *catalogtest.Catalog
	service *Service
	root    string
	library int64
	source  string
}

func newVerificationFixture(t *testing.T) *verificationFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "verify.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	root := filepath.Join(t.TempDir(), "library")
	if err = os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	catalog := catalogtest.New(t, db)
	library := catalog.Library("movies", "Movies", "movie", root)
	var source string
	if err = db.QueryRow(`SELECT id FROM library_sources WHERE library_id='movies'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	f := &verificationFixture{t: t, db: db, path: path, catalog: catalog, service: New(db, nil, assets.Probe{}), root: root, library: library, source: source}
	f.confirmRoot()
	return f
}

// confirmRoot gives the source the root identity a completed scan would have
// proved, which is what a per-file absence check is compared against.
func (f *verificationFixture) confirmRoot() {
	f.t.Helper()
	directory, err := os.Open(f.root)
	if err != nil {
		f.t.Fatal(err)
	}
	defer directory.Close()
	identity, err := storage.FileObjectIdentity(directory)
	if err != nil {
		f.t.Fatal(err)
	}
	f.exec(`UPDATE library_sources SET root_identity=?,identity_confirmed=1,health='healthy' WHERE id=?`, identity, f.source)
}

func (f *verificationFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// object records one catalogued file. present decides whether the file is
// really there; a path under a folder that does not exist is a parent the walk
// cannot read, which must never count as absence.
func (f *verificationFixture) object(id, relative string, present bool) {
	f.t.Helper()
	path := filepath.Join(f.root, relative)
	if present {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("media"), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	item := f.catalog.Movie(f.library, path, "Movie "+id, 2020)
	f.exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT ?,(SELECT id FROM library_sources WHERE library_id='movies'),?,(SELECT incarnation FROM library_sources WHERE library_id='movies'),?,'rev-1','{}',1000,1`, id, item.Token, relative)
}

// offline takes the source away. The trigger records the pending walk when it
// comes back, exactly as it does in production.
func (f *verificationFixture) offline() {
	f.exec(`UPDATE library_sources SET health='offline' WHERE id=?`, f.source)
}
func (f *verificationFixture) online() {
	f.exec(`UPDATE library_sources SET health='healthy' WHERE id=?`, f.source)
}

func (f *verificationFixture) row() (pending, started int, after, fence string, epoch int64) {
	f.t.Helper()
	err := f.db.QueryRow(`SELECT pending,started,after_object,fence_object,health_epoch FROM catalog_source_verification WHERE source_id=?`, f.source).
		Scan(&pending, &started, &after, &fence, &epoch)
	if err != nil {
		f.t.Fatal(err)
	}
	if after != "" {
		var decoded string
		if err := json.Unmarshal([]byte(after), &decoded); err != nil {
			f.t.Fatal(err)
		}
		after = decoded
	}
	return pending, started, after, fence, epoch
}

func (f *verificationFixture) state(id string) (string, string) {
	f.t.Helper()
	var state, since string
	if err := f.db.QueryRow(`SELECT state,missing_since FROM inventory_objects WHERE id=?`, id).Scan(&state, &since); err != nil {
		f.t.Fatal(err)
	}
	return state, since
}

// quantum runs exactly one bounded quantum and reports whether it advanced.
func (f *verificationFixture) quantum() bool {
	f.t.Helper()
	more, err := f.service.verifyOneSource(context.Background())
	if errors.Is(err, errVerificationPreempted) {
		return false
	}
	if err != nil {
		f.t.Fatalf("quantum: %v", err)
	}
	return more
}

// drain runs quanta until the walk closes and returns how many advanced.
func (f *verificationFixture) drain(max int) int {
	f.t.Helper()
	for turn := 0; turn < max; turn++ {
		more, err := f.service.verifyOneSource(context.Background())
		if errors.Is(err, errVerificationPreempted) {
			time.Sleep(time.Millisecond)
			continue
		}
		if err != nil {
			f.t.Fatal(err)
		}
		if !more {
			return turn + 1
		}
	}
	f.t.Fatalf("the walk did not close in %d quanta", max)
	return 0
}

func TestSourceVerificationMarksOnlyProvenAbsence(t *testing.T) {
	f := newVerificationFixture(t)
	f.object("a1", "Movies/present.mkv", true)
	f.object("a2", "Movies/gone.mkv", false)
	f.object("a3", "Unreadable/gone.mkv", false)
	f.offline()
	f.online()

	f.drain(8)

	if state, since := f.state("a1"); state != "available" || since != "" {
		t.Fatalf("a file that is there was changed: %q %q", state, since)
	}
	if state, since := f.state("a2"); state != "missing" || since == "" {
		t.Fatalf("a file proven absent was not marked: %q %q", state, since)
	}
	if state, _ := f.state("a3"); state != "available" {
		t.Fatalf("a file under an unreadable parent was marked %q", state)
	}
	var kind, job string
	if err := f.db.QueryRow(`SELECT kind,job_id FROM inventory_changes WHERE object_id='a2' ORDER BY sequence DESC LIMIT 1`).Scan(&kind, &job); err != nil {
		t.Fatal(err)
	}
	if kind != "missing" || !strings.HasPrefix(job, "source-verify:"+f.source+":") {
		t.Fatalf("missing evidence is %q by %q", kind, job)
	}
	pending, started, after, fence, epoch := f.row()
	if pending != 0 || started != 0 || after != "" || fence != "" || epoch == 0 {
		t.Fatalf("closed walk left pending=%d started=%d after=%q fence=%q epoch=%d", pending, started, after, fence, epoch)
	}
}

func TestSourceVerificationFlapsCoalesceAndKeepTheCursor(t *testing.T) {
	previous := sourceVerificationWindow
	sourceVerificationWindow = 1
	defer func() { sourceVerificationWindow = previous }()

	f := newVerificationFixture(t)
	f.object("a1", "Movies/one.mkv", true)
	f.object("a2", "Movies/two.mkv", true)
	f.object("a3", "Movies/three.mkv", true)
	f.offline()
	f.online()

	if !f.quantum() {
		t.Fatal("the first quantum did not advance")
	}
	pending, _, after, fence, base := f.row()
	if pending != 1 || after != "a1" || fence != "a3" {
		t.Fatalf("first quantum left pending=%d after=%q fence=%q", pending, after, fence)
	}

	// Two more flaps while the walk is in flight. Each advances the health epoch
	// — the fence a stale proof must fail — and none may restart the cursor or
	// move the walk's captured end.
	f.offline()
	f.online()
	f.offline()
	f.online()
	pending, started, after, fence, epoch := f.row()
	if pending != 1 || started != 1 || after != "a1" || fence != "a3" || epoch < base+2 {
		t.Fatalf("flaps left pending=%d started=%d after=%q fence=%q epoch=%d (was %d)", pending, started, after, fence, epoch, base)
	}
	var rows int
	if err := f.db.QueryRow(`SELECT count(*) FROM catalog_source_verification WHERE source_id=?`, f.source).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("a source holds %d verification rows: %v", rows, err)
	}

	f.drain(8)
	if pending, _, after, _, _ = f.row(); pending != 0 || after != "" {
		t.Fatalf("the walk did not close: pending=%d after=%q", pending, after)
	}
}

func TestSourceVerificationPausesWhileTheSourceIsAway(t *testing.T) {
	previous := sourceVerificationWindow
	sourceVerificationWindow = 1
	defer func() { sourceVerificationWindow = previous }()

	f := newVerificationFixture(t)
	f.object("a1", "Movies/one.mkv", true)
	f.object("a2", "Movies/two.mkv", true)
	f.offline()
	f.online()
	if !f.quantum() {
		t.Fatal("the first quantum did not advance")
	}
	if _, _, after, _, _ := f.row(); after != "a1" {
		t.Fatalf("first quantum left after=%q", after)
	}

	f.offline()
	if f.quantum() {
		t.Fatal("an offline source was verified")
	}
	if _, _, after, _, _ := f.row(); after != "a1" {
		t.Fatalf("an offline source moved its cursor to %q", after)
	}

	f.online()
	f.drain(8)
	if pending, _, after, _, _ := f.row(); pending != 0 || after != "" {
		t.Fatalf("the walk did not resume and close: pending=%d after=%q", pending, after)
	}
}

func TestSourceVerificationRefusesProofFromAnOlderHealthEpoch(t *testing.T) {
	f := newVerificationFixture(t)
	f.object("a1", "Movies/gone.mkv", false)
	f.offline()
	f.online()

	walk, ok, err := f.service.verificationTarget(context.Background())
	if err != nil || !ok {
		t.Fatalf("no walk claimed: %v %v", ok, err)
	}
	// The source leaves and comes back between the proof and its acceptance.
	f.offline()
	f.online()

	err = f.service.acceptVerification(context.Background(), walk, "", verificationRow{id: "a1", path: "Movies/gone.mkv", revision: "rev-1"})
	if err == nil {
		t.Fatal("a proof from an older health epoch was accepted")
	}
	if state, _ := f.state("a1"); state != "available" {
		t.Fatalf("a stale proof marked the file %q", state)
	}
	if pending, _, after, _, _ := f.row(); pending != 1 || after != "" {
		t.Fatalf("the refused proof moved the row: pending=%d after=%q", pending, after)
	}
}

func TestSourceVerificationWaitsForScansAndRunningWork(t *testing.T) {
	f := newVerificationFixture(t)
	f.object("a1", "Movies/one.mkv", true)
	f.offline()
	f.online()

	// The real queue path, so this is the scheduler the server actually uses and
	// not a hand-written job row.
	job, err := f.service.QueueContext(context.Background(), "movies", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.quantum() {
		t.Fatal("a queued scan did not preempt verification")
	}
	if _, _, after, _, _ := f.row(); after != "" {
		t.Fatalf("verification moved while a scan was queued: after=%q", after)
	}
	f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
	if f.quantum() {
		t.Fatal("a running scan did not preempt verification")
	}

	// No scan row, but this process is already running a scan quantum.
	if err = f.service.Control(context.Background(), job.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	f.service.mu.Lock()
	f.service.active["scan-120"] = func() {}
	f.service.mu.Unlock()
	if f.quantum() {
		t.Fatal("a running quantum did not preempt verification")
	}
	f.service.mu.Lock()
	delete(f.service.active, "scan-120")
	f.service.mu.Unlock()

	f.drain(4)
	if pending, _, _, _, _ := f.row(); pending != 0 {
		t.Fatalf("verification did not run once the scan gave up the source: pending=%d", pending)
	}
}

func TestSourceVerificationResetsOnConfigurationChange(t *testing.T) {
	previous := sourceVerificationWindow
	sourceVerificationWindow = 1
	defer func() { sourceVerificationWindow = previous }()

	f := newVerificationFixture(t)
	f.object("a1", "Movies/one.mkv", true)
	f.object("a2", "Movies/two.mkv", true)
	f.offline()
	f.online()
	if !f.quantum() {
		t.Fatal("the first quantum did not advance")
	}
	if _, _, after, _, _ := f.row(); after != "a1" {
		t.Fatalf("first quantum left after=%q", after)
	}

	// A new root is a new configuration lifetime: the old cursor cannot name an
	// object of the source that exists now.
	f.exec(`UPDATE library_sources SET generation=generation+1,incarnation='replacement-root' WHERE id=?`, f.source)
	pending, started, after, fence, epoch := f.row()
	if pending != 0 || started != 0 || after != "" || fence != "" || epoch == 0 {
		t.Fatalf("a configuration change left pending=%d started=%d after=%q fence=%q epoch=%d", pending, started, after, fence, epoch)
	}
	if f.quantum() {
		t.Fatal("a retired configuration lifetime was walked")
	}
}

func TestSourceVerificationResumesAfterReopen(t *testing.T) {
	previous := sourceVerificationWindow
	sourceVerificationWindow = 1
	defer func() { sourceVerificationWindow = previous }()

	f := newVerificationFixture(t)
	f.object("a1", "Movies/one.mkv", true)
	f.object("a2", "Movies/two.mkv", false)
	f.object("a3", "Movies/three.mkv", true)
	f.offline()
	f.online()
	if !f.quantum() {
		t.Fatal("the first quantum did not advance")
	}
	if _, _, after, _, _ := f.row(); after != "a1" {
		t.Fatalf("first quantum left after=%q", after)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := persistence.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f.db, f.service = db, New(db, nil, assets.Probe{})

	// Two objects remain, so the first resumed quantum must start after the file
	// the earlier process proved present.
	if !f.quantum() {
		t.Fatal("the resumed walk did not advance")
	}
	if _, _, after, _, _ := f.row(); after != "a2" {
		t.Fatalf("the resumed walk started at %q", after)
	}
	f.drain(8)
	if state, _ := f.state("a2"); state != "missing" {
		t.Fatalf("the resumed walk left the absent file %q", state)
	}
	if pending, _, _, _, _ := f.row(); pending != 0 {
		t.Fatalf("the resumed walk did not close: pending=%d", pending)
	}
}

// secondLibrary adds one more library and source on its own real folder, with the
// root identity a completed scan would have proved.
func (f *verificationFixture) secondLibrary(id string) (string, string) {
	f.t.Helper()
	root := filepath.Join(f.t.TempDir(), id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.catalog.Library(id, id, "movie", root)
	var source string
	if err := f.db.QueryRow(`SELECT id FROM library_sources WHERE library_id=?`, id).Scan(&source); err != nil {
		f.t.Fatal(err)
	}
	directory, err := os.Open(root)
	if err != nil {
		f.t.Fatal(err)
	}
	defer directory.Close()
	identity, err := storage.FileObjectIdentity(directory)
	if err != nil {
		f.t.Fatal(err)
	}
	f.exec(`UPDATE library_sources SET root_identity=?,identity_confirmed=1,health='healthy' WHERE id=?`, identity, source)
	return source, root
}

func (f *verificationFixture) sourceRow(source string) (pending, started int, after, fence string, epoch int64) {
	f.t.Helper()
	err := f.db.QueryRow(`SELECT pending,started,after_object,fence_object,health_epoch FROM catalog_source_verification WHERE source_id=?`, source).
		Scan(&pending, &started, &after, &fence, &epoch)
	if err != nil {
		f.t.Fatal(err)
	}
	return pending, started, after, fence, epoch
}

// One source that cannot be walked must never hold another source's walk behind
// it: an offline folder and a provider with no absence proof both stay pending
// while the healthy local folder is verified.
func TestSourceVerificationSkipsSourcesItCannotWalk(t *testing.T) {
	f := newVerificationFixture(t)
	other, otherRoot := f.secondLibrary("shows")

	// "movies" sorts first and is pending, but is offline.
	f.object("a1", "Movies/one.mkv", true)
	f.offline()
	f.online()
	f.offline()

	gone := f.catalog.Movie(f.catalog.Handle("shows"), filepath.Join(otherRoot, "gone.mkv"), "Gone", 2020)
	f.exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT 'b1',?,?,incarnation,'gone.mkv','rev-1','{}',1,1 FROM library_sources WHERE id=?`, other, gone.Token, other)
	f.offline()
	f.exec(`UPDATE library_sources SET health='offline' WHERE id=?`, other)
	f.exec(`UPDATE library_sources SET health='healthy' WHERE id=?`, other)

	f.drain(4)
	if pending, _, _, _, _ := f.sourceRow(other); pending != 0 {
		t.Fatalf("the healthy source behind an offline one was not verified: pending=%d", pending)
	}
	if pending, _, _, _, _ := f.row(); pending != 1 {
		t.Fatalf("the offline source's walk was not left pending: pending=%d", pending)
	}

	// A provider source with no absence proof keeps its pending row and is
	// reported, but it is not closed as if it had been walked.
	f.exec(`UPDATE library_sources SET kind='remote' WHERE id=?`, f.source)
	f.online()
	if f.quantum() {
		t.Fatal("unprovable provider was advanced")
	}
	pending, started, after, fence, _ := f.row()
	if pending != 1 || started != 0 || after != "" || fence != "" {
		t.Fatalf("an unprovable provider walk was closed: pending=%d started=%d after=%q fence=%q", pending, started, after, fence)
	}
}

func TestSourceVerificationLargeSourceMeasurement(t *testing.T) {
	if os.Getenv("PORTICO_SOURCE_VERIFICATION_SCALE") == "" {
		t.Skip("set PORTICO_SOURCE_VERIFICATION_SCALE=1 to measure a large walk")
	}
	objects := 100_000
	if raw := os.Getenv("PORTICO_SOURCE_VERIFICATION_OBJECTS"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &objects); err != nil {
			t.Fatalf("objects: %v", err)
		}
	}
	f := newVerificationFixture(t)
	// Use the production quantum and real present files. Hardlinks keep the
	// fixture's data bytes small while every catalogued name gets a real
	// root/parent/leaf check. Fixture creation is outside the measured interval.
	folder := filepath.Join(f.root, "Movies")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(f.root, "present-seed")
	if err := os.WriteFile(seed, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= objects; i++ {
		if i%50_000 == 0 {
			seed = filepath.Join(f.root, fmt.Sprintf("present-seed-%d", i))
			if err := os.WriteFile(seed, []byte("media"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Link(seed, filepath.Join(folder, fmt.Sprintf("file-%08d.mkv", i))); err != nil {
			t.Fatal(err)
		}
	}
	token := f.catalog.Movie(f.library, filepath.Join(f.root, "measure", "seed.mkv"), "Seed", 2020).Token
	f.exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?)
 INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT printf('obj-%08d',i),(SELECT id FROM library_sources WHERE library_id='movies'),?,(SELECT incarnation FROM library_sources WHERE library_id='movies'),
 printf('Movies/file-%08d.mkv',i),'rev-1','{}',1000,1 FROM n`, objects, token)
	f.offline()
	f.online()

	before := dbwork.WriteGate().Stats().ByClass["maintenance"]
	started := time.Now()
	turns := f.drain(objects)
	elapsed := time.Since(started)
	held := dbwork.WriteGate().Stats().ByClass["maintenance"].HeldMilli - before.HeldMilli
	share := 0.0
	if elapsed > 0 {
		share = 100 * float64(held) / float64(elapsed.Milliseconds())
	}
	t.Logf("source verification: %d objects, %d quanta, %s wall (%.1fus/object, %.2fms/quantum), %dms maintenance writer hold (%.2f%% of wall)",
		objects, turns, elapsed.Round(time.Millisecond), float64(elapsed.Microseconds())/float64(objects), float64(elapsed.Microseconds())/1000/float64(max(turns, 1)), held, share)

	// The other half of the cost: files that really are gone, each one its own
	// bounded maintenance transaction through the existing single-file writer. It
	// runs on a second source so its numbers are not mixed with the read pass.
	gone := min(objects, 2000)
	other, otherRoot := f.secondLibrary("shows")
	if err := os.MkdirAll(filepath.Join(otherRoot, "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	otherToken := f.catalog.Movie(f.catalog.Handle("shows"), filepath.Join(otherRoot, "gone", "seed.mkv"), "Seed", 2020).Token
	f.exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT printf('gone-%08d',i),?,?,incarnation,printf('gone/file-%08d.mkv',i),'rev-1','{}',1000,1
 FROM library_sources, (WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?) SELECT i FROM n)
 WHERE library_sources.id=?`, other, otherToken, gone, other)
	f.exec(`UPDATE library_sources SET health='offline' WHERE id=?`, other)
	f.exec(`UPDATE library_sources SET health='healthy' WHERE id=?`, other)

	// Measure the same interactive point writer idle and beside verification.
	idleStop := startVerificationUserWrites(f.db)
	time.Sleep(2 * time.Second)
	idle := idleStop()
	userStop := startVerificationUserWrites(f.db)
	dbwork.ProfileStatements(true)
	defer dbwork.ProfileStatements(false)
	before = dbwork.WriteGate().Stats().ByClass["maintenance"]
	started = time.Now()
	turns = f.drain(gone * 10)
	elapsed = time.Since(started)
	held = dbwork.WriteGate().Stats().ByClass["maintenance"].HeldMilli - before.HeldMilli
	share = 0.0
	if elapsed > 0 {
		share = 100 * float64(held) / float64(elapsed.Milliseconds())
	}
	t.Logf("source verification, proven missing: %d files, %d quanta, %s wall (%.2fms/file), %dms maintenance writer hold (%.2f%% of wall)",
		gone, turns, elapsed.Round(time.Millisecond), float64(elapsed.Microseconds())/1000/float64(gone), held, share)
	loaded := userStop()
	if idle.err != nil || loaded.err != nil {
		t.Fatalf("user writes idle=%v loaded=%v", idle.err, loaded.err)
	}
	t.Logf("interactive point writes: idle n=%d p50=%s p99=%s; with verification n=%d p50=%s p99=%s", len(idle.latency), idle.percentile(50), idle.percentile(99), len(loaded.latency), loaded.percentile(50), loaded.percentile(99))
	if share > 20 {
		t.Fatalf("verification writer share %.2f%% exceeds20%%", share)
	}
	for i, entry := range dbwork.StatementProfile() {
		if i >= 12 {
			break
		}
		t.Logf("missing profile %.1fms / %d calls: %s", entry.Millis(), entry.Count, entry.Shape)
	}

}

func TestSourceVerificationIncludesEmptyObjectID(t *testing.T) {
	f := newVerificationFixture(t)
	f.object("", "Movies/empty.mkv", true)
	f.offline()
	f.online()
	f.drain(4)
	if pending, _, _, _, _ := f.row(); pending != 0 {
		t.Fatal("empty object ID kept walk pending")
	}
	if state, _ := f.state(""); state != "available" {
		t.Fatalf("present empty ID became %s", state)
	}
}

type verificationUserSample struct {
	latency []time.Duration
	err     error
}

func (s verificationUserSample) percentile(percent int) time.Duration {
	if len(s.latency) == 0 {
		return 0
	}
	sort.Slice(s.latency, func(i, j int) bool { return s.latency[i] < s.latency[j] })
	return s.latency[max(0, (len(s.latency)*percent+99)/100-1)]
}
func startVerificationUserWrites(db *sql.DB) func() verificationUserSample {
	stop := make(chan struct{})
	done := make(chan verificationUserSample, 1)
	go func() {
		out := verificationUserSample{}
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				done <- out
				return
			case <-tick.C:
				start := time.Now()
				err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
					_, err := tx.Exec(`UPDATE admin_revision SET revision=revision+1 WHERE id=1`)
					return err
				})
				if err != nil {
					out.err = err
					done <- out
					return
				}
				out.latency = append(out.latency, time.Since(start))
			}
		}
	}()
	return func() verificationUserSample { close(stop); return <-done }
}

func TestSourceVerificationTargetRotatesPendingSources(t *testing.T) {
	f := newVerificationFixture(t)
	other, _ := f.secondLibrary("shows")
	f.offline()
	f.online()
	f.exec(`UPDATE library_sources SET health='offline' WHERE id=?`, other)
	f.exec(`UPDATE library_sources SET health='healthy' WHERE id=?`, other)
	first, ok, err := f.service.verificationTarget(context.Background())
	if err != nil || !ok {
		t.Fatalf("first target %v %v", ok, err)
	}
	second, ok, err := f.service.verificationTarget(context.Background())
	if err != nil || !ok || second.source == first.source {
		t.Fatalf("pending source monopolised claim: %q -> %q (%v)", first.source, second.source, err)
	}
}
