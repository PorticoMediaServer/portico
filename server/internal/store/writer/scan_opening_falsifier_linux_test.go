package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Opening/ownership only: no create/claim command, source permit, profile,
// replacement executor or full issuer is constructed by these falsifiers.
type scanOpeningCrashProof struct {
	Original effectiveRawJournal
	InUse    int
	Open     int
}

func scanOpeningFalsifierSource(t *testing.T, revision int64) (string, effectiveRawJournal) {
	t.Helper()
	w, path := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), "INSERT INTO search_revision(singleton,revision) VALUES(1,?)", revision); err != nil {
		t.Fatal(err)
	}
	original, err := readEffectiveJournal(t.Context(), w.db, effectiveJournalTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	return path, original // frozen BEFORE the candidate opening runs
}

func requireScanOpeningJournal(t *testing.T, conn *sql.Conn, original effectiveRawJournal) {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
	settings, err := sqliteconn.Assert(t.Context(), conn, cfg)
	want := sqliteconn.Settings{Engine: "3.53.4", JournalMode: "wal", ForeignKeys: 1, Synchronous: 2, CacheSize: -1024, TempStore: 1}
	if err != nil || !reflect.DeepEqual(settings, want) {
		t.Fatal("opening changed actual native policy", settings, err)
	}
	actual, err := readEffectiveJournal(t.Context(), conn, effectiveJournalTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(original, actual); err != nil {
		t.Fatal("opening changed original raw state", err)
	}
	var integrity string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	rows, err := conn.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("opening changed native FK integrity", rows.Err())
	}
}

func scanOpeningFalsifierDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func TestScanOpeningFalsifierCloseRetainsNativeClaim(t *testing.T) {
	path, original := scanOpeningFalsifierSource(t, 733)
	owner := scanOpeningOwner(t, path)
	copyOfOwner := *owner
	if value, err := openScanStore(t.Context(), &copyOfOwner, path, 1024); value != nil || !errors.Is(err, stateownership.ErrInvalid) {
		t.Fatal("copied native owner opened a store", err)
	}
	old, err := owner.ClaimStore()
	if err != nil {
		t.Fatal(err)
	}
	alias := *old
	if err := old.Close(); err != nil || !errors.Is(old.Lifetime().Err(), context.Canceled) {
		t.Fatal("store release failed to revoke old lifetime", err)
	}
	o := openScanFixture(t, path, owner)
	if err := alias.Close(); err != nil {
		t.Fatal(err)
	}
	if value, err := openScanStore(t.Context(), owner, path, 1024); value != nil || !errors.Is(err, stateownership.ErrBusy) {
		t.Fatal("old claim alias released the NEW actual store claim", err)
	}
	conn, err := o.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	requireScanOpeningJournal(t, conn, original)
	if err := owner.Close(); !errors.Is(err, stateownership.ErrBusy) {
		t.Fatal("native owner close lost physical holding", err)
	}
	select {
	case <-o.ownership.Lifetime().Done():
	default:
		t.Fatal("native revocation did not synchronously close borrowed lifetime")
	}
	select {
	case <-o.lifetime.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("opening lifetime did not observe native-owner revocation")
	}
	if err := o.close(); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("physical pool close did not retain its outstanding native borrow", err)
	}
	if stats := o.executor.db.Stats(); stats.OpenConnections != 1 || stats.InUse != 1 {
		t.Fatal("missing actual retained physical SQL connection", stats)
	}
	probeScanOpeningNativeOwner(t, path, true)
	requireScanOpeningJournal(t, conn, original) // already-owned read drain only
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if o.executor.db.Stats().OpenConnections != 0 {
		t.Fatal("physical connection did not join")
	}
	probeScanOpeningNativeOwner(t, path, true) // explicit opening still owes release
	if err := o.close(); err != nil {
		t.Fatal(err)
	}
	if err := o.close(); err != nil {
		t.Fatal("repeated opening close changed completed release", err)
	}
	probeScanOpeningNativeOwner(t, path, false)
}

func TestScanOpeningFalsifierIdleCanonicalReplacement(t *testing.T) {
	path, original := scanOpeningFalsifierSource(t, 733)
	replacement, _ := scanOpeningFalsifierSource(t, 977)
	owner := scanOpeningOwner(t, path)
	o := openScanFixture(t, path, owner)
	if o.executor.db.Stats().OpenConnections != 1 {
		t.Fatal("idle physical bypass was not established")
	}
	identity, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	replacementDigest := scanOpeningFalsifierDigest(t, path)
	// Same existing admission fixture, not a production Inventory recipe/grant.
	now := time.Now().Add(time.Hour)
	o.executor.now = func() time.Time { return now }
	custody := &testCustody{}
	tx, err := o.executor.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if tx != nil {
		_, _ = tx.rollback(nil)
		t.Fatal("idle native connection bypassed canonical-file replacement fence")
	}
	if !errors.Is(err, ErrClosed) || custody.checks != 1 || custody.returns != 1 || custody.fences != 1 || o.executor.db.Stats().InUse != 0 {
		t.Fatal("replacement did not fail before native BEGIN with retired custody", err, custody)
	}
	if got := scanOpeningFalsifierDigest(t, path); got != replacementDigest {
		t.Fatal("refused opening wrote the replacement database")
	}
	if err := os.Rename(path, replacement); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".original", path); err != nil {
		t.Fatal(err)
	}
	restored, err := os.Stat(path)
	if err != nil || !os.SameFile(identity, restored) {
		t.Fatal("original native identity was not restored", err)
	}
	if err := o.binding.check(); !errors.Is(err, ErrClosed) {
		t.Fatal("restoring the original inode resurrected file binding", err)
	}
	o.executor.db.SetMaxIdleConns(0)
	// The real failed-BEGIN path quarantines and closes this pool. Its binding
	// stays poisoned, and disposal must not be mistaken for a new Connect.
	if !o.executor.fenced || o.executor.db.Stats().OpenConnections != 0 {
		t.Fatal("failed BEGIN did not quarantine and join the original pool")
	}
	if err := o.executor.db.PingContext(t.Context()); err == nil {
		t.Fatal("quarantined pool resurrected after original inode restoration")
	}
	if err := o.close(); err != nil {
		t.Fatal(err)
	}
	next := openScanFixture(t, path, owner)
	conn, err := next.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	requireScanOpeningJournal(t, conn, original)
}

func TestScanOpeningFalsifierReplacementDuringConnect(t *testing.T) {
	path, original := scanOpeningFalsifierSource(t, 733)
	replacement, _ := scanOpeningFalsifierSource(t, 977)
	owner := scanOpeningOwner(t, path)
	o := openScanFixture(t, path, owner)
	if err := o.executor.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, o.executor.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	// Reuse W's existing test connector on the SAME opening/executor/binding.
	// Only its test-only post-native callback changes the path between checks.
	fault := &faultConnector{}
	fault.Connector = trackedNativeConnector{Connector: connector, owner: fault}
	var once sync.Once
	var swapErr error
	fault.afterConnect = func() error {
		once.Do(func() {
			swapErr = os.Rename(path, path+".original")
			if swapErr == nil {
				swapErr = os.Rename(replacement, path)
			}
		})
		return swapErr
	}
	digest := scanOpeningFalsifierDigest(t, replacement)
	o.executor.openPool(fault, nil)
	if err := o.executor.db.PingContext(t.Context()); !errors.Is(err, ErrClosed) || swapErr != nil {
		t.Fatal("post-native file identity change was accepted", err, swapErr)
	}
	if !fault.nativeClosed.Load() || fault.nativeCloses.Load() != 1 || o.executor.db.Stats().OpenConnections != 0 {
		t.Fatal("refused physical Connect leaked native ownership", fault.nativeCloses.Load(), o.executor.db.Stats())
	}
	if got := scanOpeningFalsifierDigest(t, path); got != digest {
		t.Fatal("failed replacement Connect altered the substituted canonical file")
	}
	if err := os.Rename(path, replacement); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".original", path); err != nil {
		t.Fatal(err)
	}
	if err := o.executor.db.PingContext(t.Context()); !errors.Is(err, ErrClosed) || fault.nativeCloses.Load() != 1 {
		t.Fatal("post-Connect fault was not permanent before another native open", err)
	}
	if err := o.close(); err != nil {
		t.Fatal(err)
	}
	next := openScanFixture(t, path, owner)
	conn, err := next.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	requireScanOpeningJournal(t, conn, original)
}

// Preserve the actual getter through the existing close-counting test seam.
// The successful unchanged-wrapper control below makes this forwarding load
// bearing; no device/inode value is invented by the test.
func (c *trackedNativeConn) SQLiteMainFileIdentity() (uint64, uint64, error) {
	getter, ok := c.testDriverConn.(interface {
		SQLiteMainFileIdentity() (uint64, uint64, error)
	})
	if !ok {
		return 0, 0, ErrInvalid
	}
	return getter.SQLiteMainFileIdentity()
}

func TestScanOpeningFalsifierSwapRestoreAroundNativeOpen(t *testing.T) {
	path, original := scanOpeningFalsifierSource(t, 733)
	replacement, _ := scanOpeningFalsifierSource(t, 991)
	owner := scanOpeningOwner(t, path)
	o := openScanFixture(t, path, owner)
	if err := o.executor.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, o.executor.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	fault := &faultConnector{}
	fault.Connector = trackedNativeConnector{Connector: connector, owner: fault}
	o.executor.openPool(fault, nil)
	// A valid open through this EXACT wrapper must work first. Missing native
	// attestation forwarding must not masquerade as detection of the swap.
	control, err := o.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal("unchanged native wrapper was not accepted", err)
	}
	requireScanOpeningJournal(t, control, original)
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	o.executor.db.SetMaxIdleConns(0)
	if o.executor.db.Stats().OpenConnections != 0 {
		t.Fatal("control physical connection did not join before scheduling swap")
	}
	identity, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	var swapErr error
	fault.beforeConnect = func() error {
		before++
		swapErr = os.Rename(path, path+".original")
		if swapErr == nil {
			swapErr = os.Rename(replacement, path)
		}
		return swapErr
	}
	fault.afterConnect = func() error {
		after++
		if swapErr == nil {
			swapErr = os.Rename(path, replacement)
		}
		if swapErr == nil {
			swapErr = os.Rename(path+".original", path)
		}
		return swapErr
	}
	conn, openErr := o.executor.db.Conn(t.Context())
	if swapErr != nil || before != 1 || after != 1 {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("did not reach the declared check-A/open-B/restore-A schedule", before, after, swapErr, openErr)
	}
	restored, err := os.Stat(path)
	if err != nil || !os.SameFile(identity, restored) {
		t.Fatal("schedule did not restore the exact original native inode", err)
	}
	if conn != nil || openErr == nil {
		var revision int64
		var readErr error
		if conn != nil {
			readErr = conn.QueryRowContext(t.Context(), "SELECT revision FROM search_revision WHERE singleton=1").Scan(&revision)
			_ = conn.Close()
		}
		t.Fatal("foreign held SQLite file accepted after pathname restoration", revision, readErr, openErr)
	}
	if o.executor.db.Stats().OpenConnections != 0 {
		t.Fatal("refused swap retained a native connection")
	}
	if err := o.close(); err != nil {
		t.Fatal(err)
	}
	next := openScanFixture(t, path, owner)
	conn, err = next.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	requireScanOpeningJournal(t, conn, original)
}

func TestScanOpeningFalsifierHardlinkAliases(t *testing.T) {
	path, _ := scanOpeningFalsifierSource(t, 733)
	alias := filepath.Join(t.TempDir(), "alias.sqlite")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(alias)
	if err != nil || !os.SameFile(first, second) {
		t.Fatal("test did not establish one physical database through two names", err)
	}
	digest := scanOpeningFalsifierDigest(t, path)
	owner := scanOpeningOwner(t, path)
	otherOwner := scanOpeningOwner(t, alias) // genuinely separate native server.lock
	a, firstErr := openScanStore(t.Context(), owner, path, 1024)
	if a != nil {
		defer a.close()
	}
	b, secondErr := openScanStore(t.Context(), otherOwner, alias, 1024)
	if b != nil {
		defer b.close()
	}
	if a != nil || b != nil || firstErr == nil || secondErr == nil {
		t.Fatal("hardlink aliases accepted native store openings", a != nil, b != nil, firstErr, secondErr)
	}
	if got := scanOpeningFalsifierDigest(t, path); got != digest {
		t.Fatal("hardlink refusal altered the original canonical database")
	}
	// Startup refusal must release each of its own claims, even across owners.
	for _, current := range []*stateownership.Lock{owner, otherOwner} {
		claim, err := current.ClaimStore()
		if err != nil {
			t.Fatal("hardlink refusal stranded native store claim", err)
		}
		if err := claim.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func crashScanOpeningFixture(t *testing.T, witness *crashWitness) {
	if witness.Case != (crashCase{"scan_opening", "physical_borrow", "opening_retained", ""}) {
		t.Fatal("unsupported concrete opening crash cell", witness.Case)
	}
	path, original := scanOpeningFalsifierSource(t, 733)
	owner := scanOpeningOwner(t, path)
	o := openScanFixture(t, path, owner)
	conn, err := o.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	requireScanOpeningJournal(t, conn, original)
	if err := owner.Close(); !errors.Is(err, stateownership.ErrBusy) {
		t.Fatal(err)
	}
	if err := o.close(); !errors.Is(err, ErrResolveRequired) || o.lifetime.Err() == nil {
		t.Fatal("opening crash lacks revoked but retained physical custody", err)
	}
	stats := o.executor.db.Stats()
	if stats.InUse != 1 || stats.OpenConnections != 1 {
		t.Fatal("opening crash has no actual outstanding physical borrow", stats)
	}
	witness.Path = path
	witness.Opening = &scanOpeningCrashProof{Original: original, InUse: stats.InUse, Open: stats.OpenConnections}
	crashPoint(t, witness, "opening_retained")
	t.Fatal("opening kill boundary was not reached")
}

func TestScanOpeningFalsifierKilledBorrowReopen(t *testing.T) {
	var lockIdentity os.FileInfo
	witness := runCrashChild(t, crashCase{"scan_opening", "physical_borrow", "opening_retained", ""}, func(w crashWitness) {
		if w.Opening == nil || w.Opening.InUse != 1 || w.Opening.Open != 1 {
			t.Fatal("missing actual opening custody witness")
		}
		probeScanOpeningNativeOwner(t, w.Path, true)
		var err error
		lockIdentity, err = os.Stat(filepath.Join(filepath.Dir(w.Path), "server.lock"))
		if err != nil {
			t.Fatal(err)
		}
	})
	retained, err := os.Stat(filepath.Join(filepath.Dir(witness.Path), "server.lock"))
	if err != nil || !os.SameFile(lockIdentity, retained) {
		t.Fatal("restart replaced the native lock inode", err)
	}
	owner := scanOpeningOwner(t, witness.Path) // only AFTER real SIGKILL/Wait
	o := openScanFixture(t, witness.Path, owner)
	conn, err := o.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	requireScanOpeningJournal(t, conn, witness.Opening.Original)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	probeScanOpeningNativeOwner(t, witness.Path, false)
}

type scanOpeningProbe struct {
	Held, Acquired bool
	Temp           string
}

func probeScanOpeningNativeOwner(t *testing.T, path string, held bool) {
	t.Helper()
	scratch := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScanOpeningFalsifierProbeChild$", "-test.count=1", "-test.timeout=8s")
	cmd.Env = append(os.Environ(), "PORTICO_SCAN_OPENING_PROBE="+path, "TMPDIR="+scratch, "GOTMPDIR="+scratch)
	output, err := cmd.CombinedOutput() // joins the real competing process
	if err != nil {
		t.Fatalf("opening native competitor failed: %v\n%s", err, output)
	}
	var report scanOpeningProbe
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&report); err != nil {
		t.Fatal("missing independent native competitor result", err)
	}
	relative, err := filepath.Rel(scratch, report.Temp)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) || report.Held != held || report.Acquired == held {
		t.Fatal("native competitor disproved opening custody/containment", held, report, err)
	}
}

func TestScanOpeningFalsifierProbeChild(t *testing.T) {
	path := os.Getenv("PORTICO_SCAN_OPENING_PROBE")
	if path == "" {
		return
	}
	if os.Getenv("TMPDIR") == "" || os.Getenv("TMPDIR") != os.Getenv("GOTMPDIR") {
		t.Fatal("native competitor temp variables are not both scoped")
	}
	report := scanOpeningProbe{Temp: t.TempDir()}
	owner, err := stateownership.Acquire(filepath.Join(filepath.Dir(path), "server.lock"))
	if owner != nil && err == nil {
		report.Acquired = true
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
	} else if owner == nil && errors.Is(err, stateownership.ErrHeld) {
		report.Held = true
	} else {
		t.Fatal("unexpected native ownership probe error", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		t.Fatal(err)
	}
}
