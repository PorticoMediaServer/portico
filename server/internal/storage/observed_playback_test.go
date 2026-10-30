package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func observedStorageFixture(t *testing.T) (*Client, *RootLease, string, *atomic.Int32, context.CancelFunc) {
	t.Helper()
	if !observedPlaybackSupported {
		t.Skip("real Unix directory adapter required")
	}
	root := t.TempDir()
	path := filepath.Join(root, "source.bin")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	// A real fixture directory descriptor stands in only for the registration
	// provider's borrowed FD. Production registration is a separately owned gate.
	fd, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	lifetime, cancel := context.WithCancel(context.Background())
	count := &atomic.Int32{}
	var once sync.Once
	lease := &RootLease{Directory: fd, RegistrationID: "fixture-registration", RootPath: root, RelativePath: "source.bin", Lifetime: lifetime, Release: func() { once.Do(func() { fd.Close(); count.Add(1) }) }}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	c.Timeout = time.Second
	t.Cleanup(func() { cancel(); waitReaderExit(t, c); lease.Close() })
	return c, lease, path, count, cancel
}
func TestObservedStorageActualReadAndRootRename(t *testing.T) {
	c, l, _, released, _ := observedStorageFixture(t)
	setup, cancel := context.WithCancel(context.Background())
	reader, err := c.OpenObservedPlayback(setup, l)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	cancel()
	if c.VersionPolicy != nil {
		t.Fatal("ordinary source unexpectedly qualified immutable")
	}
	o, err := reader.Observe(context.Background())
	if err != nil || !o.SizeKnown || o.Size != 10 || o.RootBindingID == "" || o.ObjectBindingID == "" {
		t.Fatal("missing actual object evidence", o, err)
	}
	moved := l.RootPath + "-moved"
	if err := os.Rename(l.RootPath, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(moved) })
	data, err := reader.ReadExtent(context.Background(), 2, 3)
	if err != nil || string(data) != "234" {
		t.Fatal("held root/file was replaced by namespace reopen", string(data), err)
	}
	if released.Load() != 0 {
		t.Fatal("root lease released while helper retained source")
	}
	reader.Close()
	reader.Close()
	waitReaderExit(t, c)
	if released.Load() != 1 {
		t.Fatal("root borrow not released at actual exit")
	}
}
func TestObservedStorageRejectsDifferentRootAndSymlinkEscape(t *testing.T) {
	for _, kind := range []string{"replaced root", "relative escape", "symlink escape"} {
		t.Run(kind, func(t *testing.T) {
			c, l, _, released, _ := observedStorageFixture(t)
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "source.bin"), []byte("abcdefghij"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "replaced root":
				l.RootPath = outside
			case "relative escape":
				l.RelativePath = "../source.bin"
			case "symlink escape":
				if err := os.Symlink(filepath.Join(outside, "source.bin"), filepath.Join(l.RootPath, "escape")); err != nil {
					t.Fatal(err)
				}
				l.RelativePath = "escape"
			}
			reader, err := c.OpenObservedPlayback(context.Background(), l)
			if err == nil || reader != nil {
				if reader != nil {
					reader.Close()
				}
				t.Fatal("unregistered/outside object accepted")
			}
			waitReaderExit(t, c)
			if released.Load() != 1 {
				t.Fatal("rejected lease leaked")
			}
		})
	}
}
func TestObservedStorageChangeAndRegistrationCancellation(t *testing.T) {
	for _, kind := range []string{"overwrite", "registration loss"} {
		t.Run(kind, func(t *testing.T) {
			c, l, path, released, cancel := observedStorageFixture(t)
			reader, err := c.OpenObservedPlayback(context.Background(), l)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if kind == "overwrite" {
				if err := os.WriteFile(path, []byte("abcdefghij"), 0600); err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(1, 0)
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			if data, err := reader.ReadExtent(context.Background(), 0, 2); err == nil || len(data) != 0 {
				t.Fatal("lost source returned bytes")
			}
			reader.Close()
			waitReaderExit(t, c)
			if released.Load() != 1 {
				t.Fatal("root lifetime leaked")
			}
		})
	}
}
func TestObservedStorageProtocolAndMissingLease(t *testing.T) {
	for _, frame := range [][]byte{nil, {0, 0, 0, 0}, {0xff, 0xff, 0xff, 0xff}, {0, 0, 0, 2, '{', '}'}} {
		if _, err := readObservedResponse(bytes.NewReader(frame)); err == nil {
			t.Fatal("malformed frame accepted")
		}
	}
	var frame bytes.Buffer
	var h [4]byte
	b := []byte(`{"Unknown":true}`)
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	frame.Write(h[:])
	frame.Write(b)
	if _, err := readObservedResponse(&frame); err == nil {
		t.Fatal("unknown protocol fields accepted")
	}
	c := New("not-started")
	if r, err := c.OpenObservedPlayback(context.Background(), nil); r != nil || !errors.Is(err, ErrRootLease) || c.Supervisor.Active() != 0 {
		t.Fatal("missing root lease admitted")
	}
}
func TestObservedSupervisorOwnedExitAcknowledgment(t *testing.T) {
	c, l, _, released, _ := observedStorageFixture(t)
	entered := make(chan struct{})
	finishConsume := make(chan struct{})
	var finishOnce sync.Once
	t.Cleanup(func() { finishOnce.Do(func() { close(finishConsume) }) })
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	body, _ := json.Marshal(request{Operation: "root", Path: l.RootPath})
	cmd.Stdin = bytes.NewReader(body)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Supervisor.RunOwned(ctx, "playback:owned-exit", cmd, func(r io.Reader) error {
			var b [1]byte
			if _, err := r.Read(b[:]); err != nil {
				return err
			}
			close(entered)
			<-finishConsume
			return errors.New("consumer finished")
		}, l.Release)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("helper did not reach controlled consumer")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation failed to return")
	}
	if released.Load() != 0 || c.Supervisor.Active() != 1 {
		t.Fatal("cancellation released ownership before Wait")
	}
	finishOnce.Do(func() { close(finishConsume) })
	waitReaderExit(t, c)
	if released.Load() != 1 || cmd.ProcessState == nil {
		t.Fatal("missing actual Wait acknowledgment")
	}
}
func TestObservedSupervisorOwnedNoStart(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		s := &Supervisor{}
		ctx, cancel := context.WithCancel(context.Background())
		if cancelFirst {
			cancel()
		}
		var calls atomic.Int32
		err := s.RunOwned(ctx, "playback:no-start", exec.Command("/portico-no-such-owned-helper"), func(io.Reader) error { t.Error("failed start consumed output"); return nil }, func() { calls.Add(1) })
		cancel()
		if err == nil || calls.Load() != 1 || s.Active() != 0 {
			t.Fatal("no-start cleanup not exact")
		}
	}
}

func TestObservedStorageOwnerCancellationPublicationBoundary(t *testing.T) {
	for _, beforeAdmission := range []bool{true, false} {
		owner, cancelOwner := context.WithCancel(context.Background())
		// Deliberately leave derived life uncancelled to model an AfterFunc callback
		// that has not run. The owner context alone must fence admission/publication.
		life, cancelLife := context.WithCancel(context.Background())
		input, writer := io.Pipe()
		reader := &ObservedPlaybackReader{owner: owner, life: life, cancel: cancelLife, input: writer, calls: make(chan observedCall), timeout: time.Second}
		responderDone := make(chan struct{})
		if beforeAdmission {
			cancelOwner()
			close(responderDone)
		} else {
			go func() {
				defer close(responderDone)
				select {
				case call := <-reader.calls:
					observation := ObservedFileObservation{RootBindingID: "root", ObjectBindingID: "object", SizeKnown: true, Size: 1}
					cancelOwner()
					call.done <- observedCallResult{response: observedResponse{Observation: &observation, After: &observation, Data: []byte("x")}}
				case <-life.Done():
				}
			}()
		}
		data, err := reader.ReadExtent(context.Background(), 0, 1)
		if !errors.Is(err, ErrObservedSourceLost) || len(data) != 0 || life.Err() == nil {
			t.Error("owner cancellation depended on asynchronous propagation", data, err)
		}
		reader.Close()
		cancelOwner()
		input.Close()
		select {
		case <-responderDone:
		case <-time.After(time.Second):
			t.Fatal("response fixture leaked")
		}
	}
}
