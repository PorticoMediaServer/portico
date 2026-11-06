package sqliteconn

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
)

// These are the published ownership and ExistingFile primitives, not W's
// file-bound opening/full issuer. Test code explicitly retains/releases a
// physical borrow; it installs no connector, lock or authority implementation.
type ownershipLifetimeReport struct {
	Step, Temp, Error string
	PID               int
	Acquired          bool
	Facts             [4]int64
}

type ownershipLifetimeProcess struct {
	cmd     *exec.Cmd
	ctx     context.Context
	cancel  context.CancelFunc
	input   io.WriteCloser
	pipe    *os.File
	decoder *json.Decoder
	output  bytes.Buffer
	joined  bool
	scratch string
}

func startOwnershipLifetimeProcess(t *testing.T, path, mode string) *ownershipLifetimeProcess {
	t.Helper()
	scratch := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	p := &ownershipLifetimeProcess{ctx: ctx, cancel: cancel, scratch: scratch}
	read, write, err := os.Pipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	p.pipe, p.decoder = read, json.NewDecoder(read)
	p.cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnershipLifetimeChild$", "-test.count=1", "-test.timeout=15s")
	p.cmd.Env = append(os.Environ(), "PORTICO_OWNERSHIP_LIFETIME="+mode, "PORTICO_OWNERSHIP_DATABASE="+path,
		"TMPDIR="+scratch, "GOTMPDIR="+scratch)
	p.cmd.ExtraFiles = []*os.File{write}
	p.cmd.Stdout, p.cmd.Stderr = &p.output, &p.output
	p.input, err = p.cmd.StdinPipe()
	if err != nil {
		_ = read.Close()
		_ = write.Close()
		cancel()
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		_ = p.input.Close()
		_ = read.Close()
		_ = write.Close()
		cancel()
		t.Fatal(err)
	}
	_ = write.Close()
	t.Cleanup(func() {
		if !p.joined {
			_ = p.cmd.Process.Kill()
			_ = p.wait()
		}
	})
	return p
}

func (p *ownershipLifetimeProcess) wait() error {
	err := p.cmd.Wait()
	p.joined = true
	_ = p.input.Close()
	_ = p.pipe.Close()
	p.cancel()
	return err
}

func (p *ownershipLifetimeProcess) report(t *testing.T) ownershipLifetimeReport {
	t.Helper()
	type response struct {
		value ownershipLifetimeReport
		err   error
	}
	ready := make(chan response, 1)
	go func() {
		var r response
		r.err = p.decoder.Decode(&r.value)
		ready <- r
	}()
	var r response
	select {
	case r = <-ready:
	case <-p.ctx.Done():
		r.err = p.ctx.Err()
	}
	if r.err != nil {
		_ = p.cmd.Process.Kill()
		_ = p.wait()
		t.Fatalf("ownership child did not witness its boundary: %v\n%s", r.err, p.output.String())
	}
	relative, err := filepath.Rel(p.scratch, r.value.Temp)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) || r.value.PID != p.cmd.Process.Pid {
		t.Fatal("child escaped owned scratch/process", r.value, err)
	}
	return r.value
}

func probeOwnershipLifetime(t *testing.T, path string, held bool) {
	t.Helper()
	p := startOwnershipLifetimeProcess(t, path, "probe")
	r := p.report(t)
	if err := p.wait(); err != nil {
		t.Fatalf("competing process failed: %v\n%s", err, p.output.String())
	}
	wantError := ""
	if held {
		wantError = "held"
	}
	if r.Step != "probe" || r.Acquired == held || r.Error != wantError || r.Facts != ([4]int64{}) {
		t.Fatal("native competitor disproved ownership lifetime", held, r)
	}
}

func ownershipLifetimeFacts(t *testing.T, conn *sql.Conn, cfg Config) [4]int64 {
	t.Helper()
	settings, err := Assert(t.Context(), conn, cfg)
	want := Settings{Engine: "3.53.4", JournalMode: "wal", ForeignKeys: 1, Synchronous: 1, CacheSize: -1024, TempStore: 1}
	if err != nil || !reflect.DeepEqual(settings, want) {
		t.Fatal("physical opening policy changed", settings, err)
	}
	var facts [4]int64
	if err := conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM parent), (SELECT count(*) FROM child), (SELECT min(id) FROM parent), (SELECT min(parent_id) FROM child)`).Scan(&facts[0], &facts[1], &facts[2], &facts[3]); err != nil {
		t.Fatal(err)
	}
	// Literal facts originate in fixture's SQL input, not a candidate projection.
	if facts != ([4]int64{1, 1, 1, 1}) {
		t.Fatal("original native fixture facts changed", facts)
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
		t.Fatal("native foreign-key integrity changed", rows.Err())
	}
	return facts
}

func TestOwnershipLifetimeClosingRetainsBorrowedConnection(t *testing.T) {
	path, db, cfg := fixture(t)
	lockPath := filepath.Join(filepath.Dir(path), "server.lock")
	owner, err := stateownership.Acquire(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	copyOfOwner := *owner
	if lease, err := copyOfOwner.Borrow(); lease != nil || !errors.Is(err, stateownership.ErrInvalid) {
		if lease != nil {
			_ = lease.Close()
		}
		t.Fatal("copied owner minted a borrow", err)
	}
	if err := copyOfOwner.Close(); !errors.Is(err, stateownership.ErrInvalid) {
		t.Fatal("copied owner closed native ownership", err)
	}
	var zero stateownership.Lock
	if lease, err := zero.Borrow(); lease != nil || !errors.Is(err, stateownership.ErrInvalid) {
		t.Fatal("zero owner minted a borrow", err)
	}
	first, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	physical, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = physical.Close() })
	conn := assertConn(t, db, cfg)
	t.Cleanup(func() { _ = conn.Close() })
	ownershipLifetimeFacts(t, conn, cfg)
	if err := owner.Close(); !errors.Is(err, stateownership.ErrBusy) {
		t.Fatal("owner close did not retain physical borrowers", err)
	}
	if lease, err := owner.Borrow(); lease != nil || !errors.Is(err, stateownership.ErrClosing) {
		t.Fatal("closing owner minted a borrow", err)
	}
	if err := physical.Check(); !errors.Is(err, stateownership.ErrClosing) {
		t.Fatal("closing owner failed to revoke ingress", err)
	}
	if got, err := physical.LockPath(); got != "" || !errors.Is(err, stateownership.ErrClosing) {
		t.Fatal("closing owner returned scope", got, err)
	}
	alias := *first
	if err := alias.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	probeOwnershipLifetime(t, path, true) // aliases must not retire physical
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if stats := db.Stats(); stats.OpenConnections != 1 || stats.InUse != 1 {
		t.Fatal("test did not retain an actual physical connection", stats)
	}
	ownershipLifetimeFacts(t, conn, cfg) // already-owned native work can drain
	probeOwnershipLifetime(t, path, true)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if db.Stats().OpenConnections != 0 {
		t.Fatal("physical connection did not join before release")
	}
	probeOwnershipLifetime(t, path, true) // test owner still owes its release
	if err := physical.Close(); err != nil {
		t.Fatal(err)
	}
	probeOwnershipLifetime(t, path, false)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnershipLifetimeLockReplacementIsPermanent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	lockPath := filepath.Join(filepath.Dir(path), "server.lock")
	owner, err := stateownership.Acquire(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	lease, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	original, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lockPath, lockPath+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := lease.Check(); !errors.Is(err, stateownership.ErrChanged) {
		t.Fatal("replaced native lock path was accepted", err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lockPath+".original", lockPath); err != nil {
		t.Fatal(err)
	}
	restored, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(original, restored) {
		t.Fatal("test failed to restore the exact original inode", err)
	}
	if err := lease.Check(); !errors.Is(err, stateownership.ErrChanged) {
		t.Fatal("restoring original inode resurrected a lease", err)
	}
	if got, err := lease.LockPath(); got != "" || !errors.Is(err, stateownership.ErrChanged) {
		t.Fatal("restored path resurrected scope", got, err)
	}
	if next, err := owner.Borrow(); next != nil || !errors.Is(err, stateownership.ErrChanged) {
		t.Fatal("restored path resurrected owner ingress", err)
	}
	probeOwnershipLifetime(t, path, true)
	if err := owner.Close(); !errors.Is(err, stateownership.ErrBusy) {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	probeOwnershipLifetime(t, path, false)
}

func TestOwnershipLifetimeKilledBorrowerRestartsExistingFile(t *testing.T) {
	path, db, _ := fixture(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(filepath.Dir(path), "server.lock")
	p := startOwnershipLifetimeProcess(t, path, "hold")
	r := p.report(t)
	if r.Step != "physical_borrow_after_close" || !r.Acquired || r.Error != "" || r.Facts != ([4]int64{1, 1, 1, 1}) {
		t.Fatal("child did not reach actual retained native borrow", r)
	}
	originalLock, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	probeOwnershipLifetime(t, path, true)
	if p.ctx.Err() != nil {
		t.Fatal("watchdog expired before the intended kill")
	}
	if err := p.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	err = p.wait() // actual process exit/join precedes any new custody
	status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if err == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("not an actual joined SIGKILL: %v, %v\n%s", status, err, p.output.String())
	}
	retainedLock, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(originalLock, retainedLock) {
		t.Fatal("kill/restart replaced the retained lock pathname", err)
	}
	p = startOwnershipLifetimeProcess(t, path, "reopen")
	r = p.report(t)
	if err := p.wait(); err != nil {
		t.Fatalf("new-process restart failed: %v\n%s", err, p.output.String())
	}
	if r.Step != "reopened" || !r.Acquired || r.Error != "" || r.Facts != ([4]int64{1, 1, 1, 1}) {
		t.Fatal("restart lost original native facts", r)
	}
	probeOwnershipLifetime(t, path, false)
	// ExistingFile is immutable native-open policy, not a one-time path Stat.
	// The successful restart has physically joined before moving its database.
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	p = startOwnershipLifetimeProcess(t, path, "missing")
	r = p.report(t)
	if err := p.wait(); err != nil {
		t.Fatalf("missing-file restart control failed: %v\n%s", err, p.output.String())
	}
	if r.Step != "missing_refused" || !r.Acquired || r.Error != "cantopen" || r.Facts != ([4]int64{}) {
		t.Fatal("missing file was not refused by native existing-only open", r)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("restart manufactured a missing database/sidecar", suffix, err)
		}
	}
	if err := os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	p = startOwnershipLifetimeProcess(t, path, "reopen")
	r = p.report(t)
	if err := p.wait(); err != nil || r.Step != "reopened" || r.Facts != ([4]int64{1, 1, 1, 1}) {
		t.Fatalf("restored original file did not reopen: %+v %v\n%s", r, err, p.output.String())
	}
}

func TestOwnershipLifetimeChild(t *testing.T) {
	mode := os.Getenv("PORTICO_OWNERSHIP_LIFETIME")
	if mode == "" {
		return
	}
	path := os.Getenv("PORTICO_OWNERSHIP_DATABASE")
	if os.Getenv("TMPDIR") == "" || os.Getenv("TMPDIR") != os.Getenv("GOTMPDIR") {
		t.Fatal("child temp variables were not both scoped")
	}
	message := ownershipLifetimeReport{PID: os.Getpid(), Temp: t.TempDir()}
	pipe := os.NewFile(3, "ownership-boundary")
	if pipe == nil {
		t.Fatal("missing private boundary pipe")
	}
	defer pipe.Close()
	owner, err := stateownership.Acquire(filepath.Join(filepath.Dir(path), "server.lock"))
	if mode == "probe" {
		message.Step = "probe"
		if owner != nil && err == nil {
			message.Acquired = true
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
		} else if owner == nil && errors.Is(err, stateownership.ErrHeld) {
			message.Error = "held"
		} else {
			t.Fatal("native contender failed unexpectedly", err)
		}
		if err := json.NewEncoder(pipe).Encode(message); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err != nil || owner == nil {
		t.Fatal("child did not acquire actual native ownership", err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()
	message.Acquired = true
	lease, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	}()
	cfg := Config{Role: Writer, Connections: 1, CacheKiB: 1024, Synchronous: Normal}
	db, _, err := Open(t.Context(), path, cfg, ExistingFile)
	if mode == "missing" {
		var code interface{ Code() int }
		if db != nil || !errors.As(err, &code) || code.Code()&255 != 14 {
			if db != nil {
				_ = db.Close()
			}
			t.Fatal("existing-only open did not refuse missing file", err)
		}
		message.Step, message.Error = "missing_refused", "cantopen"
	} else {
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		conn := assertConn(t, db, cfg)
		defer conn.Close()
		message.Facts = ownershipLifetimeFacts(t, conn, cfg)
		switch mode {
		case "hold":
			if err := owner.Close(); !errors.Is(err, stateownership.ErrBusy) {
				t.Fatal("child did not retain physical custody", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if stats := db.Stats(); stats.OpenConnections != 1 || stats.InUse != 1 {
				t.Fatal("child has no retained physical connection", stats)
			}
			message.Facts = ownershipLifetimeFacts(t, conn, cfg)
			message.Step = "physical_borrow_after_close"
		case "reopen":
			message.Step = "reopened"
		default:
			t.Fatal("unsupported ownership child mode", mode)
		}
	}
	if err := json.NewEncoder(pipe).Encode(message); err != nil {
		t.Fatal(err)
	}
	if mode == "hold" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		t.Fatal("parent failed to kill the retained physical borrower")
	}
}
