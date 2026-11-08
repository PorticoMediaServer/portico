package writer

import (
	"context"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestMPPhysicalHelper(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "mp-physical-helper" {
		fmt.Print("joined")
		os.Exit(0)
	}
}
func mpPhysicalFixture(t *testing.T) (*mpPhysicalSupervisor, *mpPhysicalExecution) {
	t.Helper()
	s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMPPhysicalHelper$", "--", "mp-physical-helper")
	e, err := s.start(mpPhysicalKey{"media", "execution", "stop", 2, 2}, &storage.Supervisor{}, cmd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, e
}
func TestMPPhysicalJoinRequiresEveryOwner(t *testing.T) {
	for _, owner := range []string{"wait", "reader", "window"} {
		t.Run(owner, func(t *testing.T) {
			_, e := mpPhysicalFixture(t)
			var release func()
			switch owner {
			case "wait":
				actual := e.retired
				e.retired = make(chan struct{})
				release = func() { <-actual; close(e.retired) }
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
			cancel()
			if o, err := e.join(ctx); o != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(o, err)
			}
			release()
			o, err := e.join(t.Context())
			if err != nil || o == nil || o.digest == [32]byte{} {
				t.Fatal(o, err)
			}
			if _, err = e.readers.Acquire(); err == nil {
				t.Fatal("reader reopened after join")
			}
			if _, err = e.windows.Acquire(); err == nil {
				t.Fatal("window reopened after join")
			}
		})
	}
}
func TestMPPhysicalCleanupAndTerminalFailure(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			dir := t.TempDir()
			if !missing {
				if err := os.WriteFile(filepath.Join(dir, "owned"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = root.Close() })
			s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestMPPhysicalHelper$", "--", "mp-physical-helper")
			e, err := s.start(mpPhysicalKey{"media", "execution", "stop", 2, 2}, &storage.Supervisor{}, cmd, root, []string{"owned"})
			if err != nil {
				t.Fatal(err)
			}
			o, err := e.join(t.Context())
			if missing {
				if o != nil || err == nil {
					t.Fatal(o, err)
				}
			} else if err != nil || o == nil {
				t.Fatal(o, err)
			}
		})
	}
	_, e := mpPhysicalFixture(t)
	e.owner.revoke()
	if o, err := e.join(t.Context()); o != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(o, err)
	}
	s, err := newMPPhysicalSupervisor(&Writer{}, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	e, err = s.start(mpPhysicalKey{"bad", "bad-execution", "stop", 1, 1}, &storage.Supervisor{}, exec.Command(filepath.Join(t.TempDir(), "missing")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o, err := e.join(t.Context()); o != nil || err == nil {
		t.Fatal("no-start minted proof", o, err)
	}
}
func TestMPPhysicalNativeBinding(t *testing.T) {
	f := mpWorkerFixture(t, true)
	if _, err := f.run(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.db.ExecContext(t.Context(), `INSERT INTO configuration(key,value) VALUES('id',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, f.cap.scope.serverID); err != nil {
		t.Fatal(err)
	}
	// Capture the new point in the complete installed union, against actual F.
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
	// Unqualified component frame, not a new runnable 705 recipe.
	turn := mgSourceTestTurn(t, f.e, MPQueueRepeat, mpPhysicalDefinitions())
	turn.transaction.descriptor.ID = 705
	s, err := newMPPhysicalSupervisor(f.w, t.Context(), f.cap.scope.serverID)
	if err != nil {
		t.Fatal(err)
	}
	key := mpPhysicalKey{"mp-media", "mp-execution", "mp-stop", 2, 2}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMPPhysicalHelper$", "--", "mp-physical-helper")
	e, err := s.start(key, &storage.Supervisor{}, cmd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	o, err := e.join(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := consumeMPPhysicalObservation(t.Context(), turn, s, o, key); err != nil || got.digest != o.digest {
		t.Fatal(got, err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE playback_sessions SET mp_execution_id='replaced' WHERE id='mp-media'`); err != nil {
		t.Fatal(err)
	}
	if got, err := consumeMPPhysicalObservation(t.Context(), turn, s, o, key); !errors.Is(err, ErrDisclosureDenied) || got != (mpPhysicalValue{}) {
		t.Fatal(got, err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE playback_sessions SET mp_execution_id='mp-execution' WHERE id='mp-media'`); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"forged", "copy", "execution", "generation", "request", "revision", "supervisor", "reopen"} {
		t.Run(kind, func(t *testing.T) {
			proof, k, owner := o, key, s
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
			case "request":
				k.request = "other"
			case "revision":
				k.revision++
			case "supervisor", "reopen":
				owner, err = newMPPhysicalSupervisor(f.w, t.Context(), s.server)
				if err != nil {
					t.Fatal(err)
				}
			}
			if got, err := consumeMPPhysicalObservation(t.Context(), turn, owner, proof, k); err == nil || got != (mpPhysicalValue{}) {
				t.Fatal(got, err)
			}
		})
	}
	if got, err := consumeMPPhysicalObservation(t.Context(), turn, s, o, key); err == nil || got != (mpPhysicalValue{}) {
		t.Fatal("replaced supervisor remained current", got, err)
	}

}
