package writer

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Test-only native process fixture. No storage/dbwork import or SQL function
// registration enters the writer test binary. Production composition supplies
// storage.Supervisor through the private port; its execution API is unchanged.
type mpPhysicalTestSupervisor struct{}

func (*mpPhysicalTestSupervisor) RunOwnedLinear(ctx context.Context, _ string, cmd *exec.Cmd, consume func(io.Reader) error, afterExit, afterRetired func()) error {
	retire := func() {
		if afterExit != nil {
			afterExit()
		}
		if afterRetired != nil {
			afterRetired()
		}
	}
	if err := ctx.Err(); err != nil {
		retire()
		return err
	}
	childprocess.Configure(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		retire()
		return err
	}
	if err = childprocess.Start(ctx, cmd); err != nil {
		stdout.Close()
		retire()
		return err
	}
	done := make(chan error, 1)
	go func() {
		readErr := consume(stdout)
		if readErr != nil {
			childprocess.Kill(cmd)
		}
		waitErr := cmd.Wait()
		stdout.Close()
		retire()
		done <- errors.Join(readErr, waitErr)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		childprocess.Kill(cmd)
		stdout.Close()
		return ctx.Err()
	}
}

func TestMPPhysicalHelper(t *testing.T) {
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) && os.Args[i+1] == "mp-physical-helper" {
			if i+3 < len(os.Args) {
				if err := os.WriteFile(os.Args[i+2], []byte("ready"), 0600); err != nil {
					os.Exit(2)
				}
				for {
					if _, err := os.Stat(os.Args[i+3]); err == nil {
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
			fmt.Print("joined")
			os.Exit(0)
		}
	}
}
func mpPhysicalCommand() *exec.Cmd {
	return exec.Command(os.Args[0], "-test.run=^TestMPPhysicalHelper$", "--", "mp-physical-helper")
}
func mpPhysicalBlockedCommand(t *testing.T) (*exec.Cmd, string, func()) {
	t.Helper()
	dir := t.TempDir()
	ready, gate := filepath.Join(dir, "ready"), filepath.Join(dir, "gate")
	release := func() {
		if err := os.WriteFile(gate, nil, 0600); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)
	cmd := mpPhysicalCommand()
	cmd.Args = append(cmd.Args, ready, gate)
	return cmd, ready, release
}
func mpPhysicalAwaitFile(t *testing.T, path string) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-tick.C:
		case <-timeout.C:
			t.Fatal("helper did not reach physical barrier")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}
func mpPhysicalFixture(t *testing.T) (*mpPhysicalSupervisor, *mpPhysicalExecution) {
	t.Helper()
	s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.start(mpPhysicalRoot{"media", "execution", 2}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, e
}

type mpPhysicalJoined struct {
	o   *mpPhysicalObservation
	err error
}

func mpPhysicalJoinAsync(ctx context.Context, e *mpPhysicalExecution) <-chan mpPhysicalJoined {
	done := make(chan mpPhysicalJoined, 1)
	go func() { o, err := e.join(ctx); done <- mpPhysicalJoined{o, err} }()
	return done
}
func mpPhysicalAssertHeld(t *testing.T, e *mpPhysicalExecution, done <-chan mpPhysicalJoined) {
	t.Helper()
	// This fires inside join before its first physical wait. The context stays
	// LIVE throughout the observation interval: a removed wait yields proof and
	// fails this test, rather than being hidden by pre-cancelled final redaction.
	select {
	case <-e.readers.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("join not entered")
	}
	select {
	case got := <-done:
		t.Fatal("proof escaped a withheld physical owner", got)
	case <-time.After(100 * time.Millisecond):
	}
}
func mpPhysicalAssertJoined(t *testing.T, done <-chan mpPhysicalJoined) *mpPhysicalObservation {
	t.Helper()
	select {
	case got := <-done:
		if got.err != nil || got.o == nil || got.o.digest == [32]byte{} {
			t.Fatal(got)
		}
		return got.o
	case <-time.After(5 * time.Second):
		t.Fatal("physical join did not finish")
	}
	return nil
}
func TestMPPhysicalJoinRequiresEveryOwner(t *testing.T) {
	for _, owner := range []string{"returned", "retired", "reader", "window"} {
		t.Run(owner, func(t *testing.T) {
			_, e := mpPhysicalFixture(t)
			<-e.returned
			<-e.retired
			var release func()
			switch owner {
			case "returned":
				e.returned = make(chan struct{})
				release = func() { close(e.returned) }
			case "retired":
				e.retired = make(chan struct{})
				release = func() { close(e.retired) }
			case "reader":
				l, err := e.readers.Acquire()
				if err != nil {
					t.Fatal(err)
				}
				release = l.Release
			case "window":
				l, err := e.windows.Acquire()
				if err != nil {
					t.Fatal(err)
				}
				release = l.Release
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := mpPhysicalJoinAsync(ctx, e)
			mpPhysicalAssertHeld(t, e, done)
			cancel()
			got := <-done
			if got.o != nil || !errors.Is(got.err, context.Canceled) {
				t.Fatal(got)
			}
			if e.joined {
				t.Fatal("cancelled blocked join minted physical truth")
			}
			release()
			mpPhysicalAssertJoined(t, mpPhysicalJoinAsync(t.Context(), e))
			if _, err := e.readers.Acquire(); err == nil {
				t.Fatal("reader reopened")
			}
			if _, err := e.windows.Acquire(); err == nil {
				t.Fatal("window reopened")
			}
		})
	}
}
func TestMPPhysicalWaitAndReplacementKeepLiveExclusion(t *testing.T) {
	s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	cmd, ready, release := mpPhysicalBlockedCommand(t)
	root := mpPhysicalRoot{"media", "execution", 2}
	e, err := s.start(root, &mpPhysicalTestSupervisor{}, cmd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mpPhysicalAwaitFile(t, ready)
	done := mpPhysicalJoinAsync(t.Context(), e)
	mpPhysicalAssertHeld(t, e, done)
	replacement, err := newMPPhysicalSupervisor(s.writer, t.Context(), s.server)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := replacement.start(root, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil); got != nil || !errors.Is(err, ErrConflict) {
		t.Fatal("replacement reused a live old occurrence", got, err)
	}
	release()
	got := <-done
	if got.o != nil || !errors.Is(got.err, ErrDisclosureDenied) {
		t.Fatal(got)
	}
	if len(s.registry.executions) != 0 {
		t.Fatal("revoked issuer retained an exited root")
	}
	successor, err := replacement.start(root, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o, err := e.join(t.Context()); o != nil || !errors.Is(err, ErrDisclosureDenied) || len(s.registry.executions) != 1 {
		t.Fatal("old issuer removed successor", o, err)
	}
	if _, err = successor.join(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func TestMPPhysicalCleanupRetryAndTerminalFailure(t *testing.T) {
	_, e := mpPhysicalFixture(t)
	e.owner.revoke()
	if o, err := e.join(t.Context()); o != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(o, err)
	}
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			dir := t.TempDir()
			if !missing {
				if err := os.WriteFile(filepath.Join(dir, "owned"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			files, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
			if err != nil {
				t.Fatal(err)
			}
			e, err := s.start(mpPhysicalRoot{"media", "execution", 2}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), files, []string{"owned"})
			if err != nil {
				t.Fatal(err)
			}
			if o, err := e.join(t.Context()); err != nil || o == nil {
				t.Fatal("already-absent owned file wedged exit", o, err)
			}
		})
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "first"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "blocked"), 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "blocked", "child")
	if err := os.WriteFile(child, nil, 0600); err != nil {
		t.Fatal(err)
	}
	files, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	e, err = s.start(mpPhysicalRoot{"media", "execution", 2}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), files, []string{"first", "blocked", "never-written"})
	if err != nil {
		t.Fatal(err)
	}
	if o, err := e.join(t.Context()); o != nil || err == nil || e.cleanupCursor != 1 {
		t.Fatal(o, err, e.cleanupCursor)
	}
	if err = os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if o, err := e.join(t.Context()); o == nil || err != nil {
		t.Fatal("partial cleanup could not resume", o, err)
	}
	s, err = newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	e, err = s.start(mpPhysicalRoot{"bad", "bad-execution", 1}, &mpPhysicalTestSupervisor{}, exec.Command(filepath.Join(t.TempDir(), "missing")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o, err := e.join(t.Context()); o != nil || err == nil {
		t.Fatal("no-start error exposed proof", o, err)
	}
}

// Real order: process launches against native prepared revision1/request="";
// MP-04 creates stop request/revision2; physical join knows only root identity.
func mpPhysicalNativeFixture(t *testing.T) (mpRepeatFixture, *mpPhysicalSupervisor, *mpPhysicalObservation, mpPhysicalKey) {
	t.Helper()
	f := mpWorkerFixture(t, true)
	if _, err := f.e.db.ExecContext(t.Context(), `INSERT INTO configuration(key,value) VALUES('id',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, f.cap.scope.serverID); err != nil {
		t.Fatal(err)
	}
	s, err := newMPPhysicalSupervisor(f.w, t.Context(), f.cap.scope.serverID)
	if err != nil {
		t.Fatal(err)
	}
	cmd, ready, release := mpPhysicalBlockedCommand(t)
	root := mpPhysicalRoot{"mp-media", "mp-execution", 2}
	e, err := s.start(root, &mpPhysicalTestSupervisor{}, cmd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mpPhysicalAwaitFile(t, ready)
	var rev int64
	var request string
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_revision,mp_retire_request_id FROM playback_sessions WHERE id='mp-media'`).Scan(&rev, &request); err != nil || rev != 1 || request != "" {
		t.Fatal("launch was not before stop", rev, request, err)
	}
	if _, err = f.run(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	release()
	o, err := e.join(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return f, s, o, mpPhysicalKey{root.media, root.execution, "mp-stop", root.generation, 2}
}
func mpPhysicalTurn(t *testing.T, f mpRepeatFixture) *commandTurn {
	t.Helper()
	turn := mgSourceTestTurn(t, f.e, MPQueueRepeat, mpPhysicalDefinitions())
	turn.transaction.descriptor.ID = 705
	return turn
}
func TestMPPhysicalNativeBinding(t *testing.T) {
	f, s, o, key := mpPhysicalNativeFixture(t)
	all := []stmt.Definition{}
	for _, entry := range f.w.effective.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	all = append(all, mpPhysicalDefinitions()...)
	conn, err := f.e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	policy := f.e.config
	policy.Synchronous = sqliteconn.Normal
	_ = mpWorkerCaptureFixture(t, conn, policy, all, "mp_worker_dispatch_test.go", "mp_worker_dispatch.go", "mp_worker_receipt.go", "mp_worker_receipt_statements.go", "mp_worker_result.go", "mp_queue_repeat.go", "mp_physical_observation.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	turn := mpPhysicalTurn(t, f)
	if got, err := consumeMPPhysicalObservation(t.Context(), turn, s, o, key); err != nil || got.digest != mpPhysicalRetirementDigest(o, key.request) {
		t.Fatal(got, err)
	}
	for _, cell := range []struct{ name, change, restore string }{
		{"state", `UPDATE playback_sessions SET state='prepared'`, `UPDATE playback_sessions SET state='stopped'`},
		{"grant", `UPDATE playback_sessions SET mp_grant_retired=0`, `UPDATE playback_sessions SET mp_grant_retired=1`},
		{"retired-generation", `UPDATE playback_sessions SET mp_retired_generation=1`, `UPDATE playback_sessions SET mp_retired_generation=2`},
		{"execution", `UPDATE playback_sessions SET mp_execution_id='other'`, `UPDATE playback_sessions SET mp_execution_id='mp-execution'`},
		{"request", `UPDATE playback_sessions SET mp_retire_request_id='other'`, `UPDATE playback_sessions SET mp_retire_request_id='mp-stop'`},
		{"revision", `UPDATE playback_sessions SET mp_revision=3`, `UPDATE playback_sessions SET mp_revision=2`},
		{"server", `UPDATE configuration SET value='other' WHERE key='id'`, ""},
	} {
		t.Run(cell.name, func(t *testing.T) {
			if _, err := turn.transaction.conn.ExecContext(t.Context(), cell.change); err != nil {
				t.Fatal(err)
			}
			if got, err := consumeMPPhysicalObservation(t.Context(), turn, s, o, key); !errors.Is(err, ErrDisclosureDenied) || got != (mpPhysicalValue{}) {
				t.Fatal(got, err)
			}
			if cell.restore != "" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), cell.restore); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE configuration SET value=? WHERE key='id'`, s.server); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, kind := range []string{"forged", "copy", "execution", "generation", "command", "executor"} {
		t.Run(kind, func(t *testing.T) {
			proof, k := o, key
			switch kind {
			case "forged":
				proof = &mpPhysicalObservation{}
			case "copy":
				copy := *o
				proof = &copy
			case "execution":
				k.execution = "other"
			case "generation":
				k.generation++
			case "command":
				turn.transaction.descriptor.ID = 754
				defer func() { turn.transaction.descriptor.ID = 705 }()
			case "executor":
				old := turn.transaction.writer
				turn.transaction.writer = &executor{}
				defer func() { turn.transaction.writer = old }()
			}
			if got, err := consumeMPPhysicalObservation(t.Context(), turn, s, proof, k); got != (mpPhysicalValue{}) || err == nil {
				t.Fatal(got, err)
			}
		})
	}
	foreign, err := s.start(mpPhysicalRoot{"foreign-media", "foreign-execution", 2}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	foreignProof, err := foreign.join(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := consumeMPPhysicalObservation(t.Context(), turn, s, foreignProof, key); got != (mpPhysicalValue{}) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("real foreign root proof accepted", got, err)
	}

	replacement, err := newMPPhysicalSupervisor(f.w, t.Context(), s.server)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []*mpPhysicalSupervisor{s, replacement} {
		if got, err := consumeMPPhysicalObservation(t.Context(), turn, owner, o, key); err == nil || got != (mpPhysicalValue{}) {
			t.Fatal(got, err)
		}
	}
}
func mpPhysicalNativeExit(t *testing.T, turn *commandTurn, v mpPhysicalValue) {
	t.Helper()
	k := v.consumption.key
	_, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE playback_sessions SET mp_revision=mp_revision+1,mp_exited_generation=generation,mp_exit_supervisor_incarnation=?,mp_exited_ms=101,mp_exit_request_id=mp_retire_request_id,mp_exit_digest=?,mp_cleanup_complete=1,mp_cleanup_cursor=? WHERE id=?`, v.supervisor[:], hex.EncodeToString(v.digest[:]), len(v.consumption.observation.execution.cleanup), k.media)
	if err != nil {
		t.Fatal(err)
	}
}
func TestMPPhysicalReleaseRequiresCommittedNativeExit(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "changed-after-seal", "unknown-before", "unknown-after"} {
		t.Run(mode, func(t *testing.T) {
			f, s, o, key := mpPhysicalNativeFixture(t)
			if strings.HasPrefix(mode, "unknown") {
				mpWorkerFaultPool(t, f, &faultConnector{replyError: true, afterCommit: mode == "unknown-after"})
			}
			turn := mpPhysicalTurn(t, f)
			v, err := consumeMPPhysicalObservation(t.Context(), turn, s, o, key)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := sealMPPhysicalRelease(t.Context(), turn, v); got != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("retired is not exited", got, err)
			}
			mpPhysicalNativeExit(t, turn, v)
			r, err := sealMPPhysicalRelease(t.Context(), turn, v)
			if err != nil {
				t.Fatal(err)
			}
			if err = releaseMPPhysicalAfterCommit(turn, r); !errors.Is(err, ErrConflict) {
				t.Fatal("pre-commit refund", err)
			}
			copy := *r
			if err = releaseMPPhysicalAfterCommit(turn, &copy); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("copied token", err)
			}
			if mode == "changed-after-seal" {
				if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE playback_sessions SET mp_exit_digest=?`, strings.Repeat("f", 64)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "rollback" {
				_, err = turn.transaction.rollback(nil)
			} else {
				_, err = turn.transaction.commit()
			}
			if mode == "commit" {
				if err != nil {
					t.Fatal(err)
				}
				if err = releaseMPPhysicalAfterCommit(turn, r); err != nil || len(s.registry.executions) != 0 {
					t.Fatal("committed slot not refunded", err, len(s.registry.executions))
				}
				successor, err := s.start(mpPhysicalRoot{key.media, "successor", key.generation}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = releaseMPPhysicalAfterCommit(turn, r); err != nil || len(s.registry.executions) != 1 {
					t.Fatal("repeat refund removed successor", err)
				}
				if _, err = successor.join(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				if strings.HasPrefix(mode, "unknown") && !errors.Is(err, ErrResolveRequired) {
					t.Fatal(err)
				}
				if mode == "changed-after-seal" && !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal("W did not recheck native exit before COMMIT", err)
				}
				if err = releaseMPPhysicalAfterCommit(turn, r); !errors.Is(err, ErrConflict) || len(s.registry.executions) != 1 {
					t.Fatal("non-commit refunded slot", mode, err, len(s.registry.executions))
				}
			}
		})
	}
}
func TestMPPhysicalCapacityReturnsAfterCommittedExit(t *testing.T) {
	f, s, o, key := mpPhysicalNativeFixture(t)
	// 127 other outstanding root debts, independent of this root's real native
	// consume/effect/seal/COMMIT. They grant no proof or database authority.
	for i := 0; i < 127; i++ {
		root := mpPhysicalRoot{fmt.Sprintf("held-%d", i), "held", 1}
		s.registry.executions[root.occurrence()] = &mpPhysicalExecution{owner: s, physical: root}
	}
	next := mpPhysicalRoot{"129th", "next", 1}
	if e, err := s.start(next, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil); e != nil || !errors.Is(err, ErrRefused) {
		t.Fatal(e, err)
	}
	turn := mpPhysicalTurn(t, f)
	v, err := consumeMPPhysicalObservation(t.Context(), turn, s, o, key)
	if err != nil {
		t.Fatal(err)
	}
	mpPhysicalNativeExit(t, turn, v)
	r, err := sealMPPhysicalRelease(t.Context(), turn, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = releaseMPPhysicalAfterCommit(turn, r); err != nil {
		t.Fatal(err)
	}
	e, err := s.start(next, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal("129th lifetime launch refused after committed release", err)
	}
	if _, err = e.join(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMPPhysicalNilSupervisorRefused(t *testing.T) {
	s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	var typedNil *mpPhysicalTestSupervisor
	for _, runner := range []mpPhysicalRunner{nil, typedNil} {
		if e, err := s.start(mpPhysicalRoot{"media", "execution", 2}, runner, mpPhysicalCommand(), nil, nil); e != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("nil supervisor admitted", e, err)
		}
	}
	if len(s.registry.executions) != 0 {
		t.Fatal("nil supervisor retained a root")
	}
}

func TestMPPhysicalFailedLaunchRefundsAfterEveryOwner(t *testing.T) {
	for _, owner := range []string{"returned", "retired", "reader", "window"} {
		t.Run(owner, func(t *testing.T) {
			s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
			if err != nil {
				t.Fatal(err)
			}
			root := mpPhysicalRoot{"media", "execution", 1}
			e, err := s.start(root, &mpPhysicalTestSupervisor{}, exec.Command(filepath.Join(t.TempDir(), "missing")), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			<-e.returned
			<-e.retired
			var release func()
			switch owner {
			case "returned":
				e.returned = make(chan struct{})
				release = func() { close(e.returned) }
			case "retired":
				e.retired = make(chan struct{})
				release = func() { close(e.retired) }
			case "reader":
				lease, err := e.readers.Acquire()
				if err != nil {
					t.Fatal(err)
				}
				release = lease.Release
			case "window":
				lease, err := e.windows.Acquire()
				if err != nil {
					t.Fatal(err)
				}
				release = lease.Release
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := mpPhysicalJoinAsync(ctx, e)
			mpPhysicalAssertHeld(t, e, done)
			cancel()
			got := <-done
			if got.o != nil || !errors.Is(got.err, context.Canceled) || len(s.registry.executions) != 1 {
				t.Fatal("unfinished failure refunded", got, len(s.registry.executions))
			}
			release()
			if o, err := e.join(t.Context()); o != nil || err == nil || len(s.registry.executions) != 0 {
				t.Fatal("terminal failure retained a slot or minted proof", o, err, len(s.registry.executions))
			}
			successor, err := s.start(root, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
			if err != nil {
				t.Fatal("same root did not recover", err)
			}
			if o, err := e.join(t.Context()); o != nil || err == nil || len(s.registry.executions) != 1 {
				t.Fatal("old failed join removed successor", o, err)
			}
			if _, err = successor.join(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMPPhysicalFailedLaunchesDoNotExhaustCapacity(t *testing.T) {
	s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	for i := 0; i < 128; i++ {
		root := mpPhysicalRoot{fmt.Sprintf("failed-%d", i), fmt.Sprintf("execution-%d", i), 1}
		e, err := s.start(root, &mpPhysicalTestSupervisor{}, exec.Command(missing), nil, nil)
		if err != nil {
			t.Fatal(i, err)
		}
		if o, err := e.join(t.Context()); o != nil || err == nil || len(s.registry.executions) != 0 {
			t.Fatal(i, o, err, len(s.registry.executions))
		}
	}
	e, err := s.start(mpPhysicalRoot{"recovered", "healthy", 1}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal("transient failures permanently exhausted writer", err)
	}
	if _, err = e.join(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type mpPhysicalPanickingRunner struct{ proceed <-chan struct{} }

func (r *mpPhysicalPanickingRunner) RunOwnedLinear(_ context.Context, _ string, _ *exec.Cmd, _ func(io.Reader) error, _ func(), afterRetired func()) error {
	defer afterRetired()
	<-r.proceed
	panic("mp physical runner test panic")
}

func TestMPPhysicalRunnerPanicRefundsAfterOwnersAndCleanup(t *testing.T) {
	s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "owned")
	if err = os.WriteFile(path, []byte("pending cleanup"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	proceed := make(chan struct{})
	root := mpPhysicalRoot{"media", "execution", 1}
	e, err := s.start(root, &mpPhysicalPanickingRunner{proceed}, mpPhysicalCommand(), files, []string{"owned"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := e.readers.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Release()
	window, err := e.windows.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer window.Release()
	close(proceed)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-e.returned:
	case <-ctx.Done():
		t.Fatal("recovered panic never published runner return", ctx.Err())
	}
	if e.runErr == nil {
		t.Fatal("recovered panic did not record a terminal error")
	}
	joinCtx, cancelJoin := context.WithCancel(ctx)
	done := mpPhysicalJoinAsync(joinCtx, e)
	mpPhysicalAssertHeld(t, e, done)
	cancelJoin()
	if got := <-done; got.o != nil || !errors.Is(got.err, context.Canceled) || len(s.registry.executions) != 1 {
		t.Fatal("panic refunded before owners finished", got, len(s.registry.executions))
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("panic cleaned files before owners finished", err)
	}
	reader.Release()
	window.Release()
	if o, err := e.join(ctx); o != nil || err == nil || !errors.Is(err, e.runErr) || len(s.registry.executions) != 0 {
		t.Fatal("panic retained a slot or minted proof", o, err, len(s.registry.executions))
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("panic refund skipped owned cleanup", err)
	}
	successor, err := s.start(root, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal("panic permanently excluded root", err)
	}
	if _, err = successor.join(ctx); err != nil {
		t.Fatal(err)
	}
}
