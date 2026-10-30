//go:build !windows

package mediaartifact

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPhysicalReaderOutlivesStoreAndDuplicatedDescriptor(t *testing.T) {
	s := fixture(t)
	object := seal(t, s, "actual-reader")
	other, e := New(s.root)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	r, e := s.Open(context.Background(), object)
	if e != nil {
		t.Fatal(e)
	}
	// A duplicate is the same open-file description, as for helper inheritance.
	fd, e := syscall.Dup(int(r.file.Fd()))
	if e != nil {
		t.Fatal(e)
	}
	duplicate := os.NewFile(uintptr(fd), "duplicate")
	defer duplicate.Close()
	_, _ = io.ReadAll(r)
	_ = r.Close()
	_ = s.Close()
	if e = other.Remove(object); !errors.Is(e, ErrLeased) {
		t.Fatalf("EOF/logical close released a real descriptor: %v", e)
	}
	_ = duplicate.Close()
	if e = other.Remove(object); e != nil {
		t.Fatal(e)
	}
}
func TestPhysicalReaderHeldByChild(t *testing.T) {
	s := fixture(t)
	object := seal(t, s, "child-reader")
	r, e := s.Open(context.Background(), object)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInheritedArtifactChild$")
	cmd.Env = append(os.Environ(), "PORTICO_ARTIFACT_TEST_CHILD=1")
	cmd.ExtraFiles = []*os.File{r.file}
	input, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { input.Close(); _ = cmd.Wait() }()
	var ready [1]byte
	if _, e = io.ReadFull(output, ready[:]); e != nil {
		t.Fatal(e)
	}
	r.Close()
	other, e := New(s.root)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	if e = other.Remove(object); !errors.Is(e, ErrLeased) {
		t.Fatalf("child's actual FD ignored: %v", e)
	}
	input.Close()
	if e = cmd.Wait(); e != nil {
		t.Fatal(e)
	}
	if e = other.Remove(object); e != nil {
		t.Fatal(e)
	}
}
func TestInheritedArtifactChild(t *testing.T) {
	if os.Getenv("PORTICO_ARTIFACT_TEST_CHILD") != "1" {
		return
	}
	f := os.NewFile(3, "inherited")
	if _, e := f.Stat(); e != nil {
		os.Exit(3)
	}
	_, _ = os.Stdout.Write([]byte{1})
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = f.Close()
	os.Exit(0)
}
func TestRetainedWriterExcludesRecoveryAcrossStores(t *testing.T) {
	s := fixture(t)
	other, e := New(s.root)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	key := strings.Repeat("a", 64) + ".1"
	w, e := s.BeginRetained(key, 100)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Retain()
	_, _ = w.Write([]byte("prefix-tail"))
	if _, e = other.ResumeRetained(context.Background(), key, 6, 100); !errors.Is(e, ErrLeased) {
		t.Fatal("concurrent recovery", e)
	}
	if e = other.RemoveRetained(key); !errors.Is(e, ErrLeased) {
		t.Fatal("concurrent deletion", e)
	}
	if e = w.Retain(); e != nil {
		t.Fatal(e)
	}
	recovered, e := other.ResumeRetained(context.Background(), key, 6, 100)
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.Retain()
	object, e := recovered.Checkpoint()
	if e != nil || object.Size != 6 {
		t.Fatal(object, e)
	}
	if _, e = recovered.Seal(context.Background()); e != nil {
		t.Fatal(e)
	}
	r, e := s.Open(context.Background(), object)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	b, e := io.ReadAll(r)
	if e != nil || string(b) != "prefix" {
		t.Fatal(string(b), e)
	}
}
func TestFailedRetainedSealPreservesCheckpoint(t *testing.T) {
	s := fixture(t)
	key := strings.Repeat("b", 64) + ".1"
	w, e := s.BeginRetained(key, 100)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = w.Write([]byte("recoverable"))
	cp, e := w.Checkpoint()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = w.Seal(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = w.Retain(); e != nil {
		t.Fatal(e)
	}
	if !s.RetainedExists(key) {
		t.Fatal("failed seal discarded durable bytes")
	}
	recovered, e := s.ResumeRetained(context.Background(), key, cp.Size, 100)
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.Retain()
	o, e := recovered.Seal(context.Background())
	if e != nil || o != cp {
		t.Fatal(o, e)
	}
}
func TestRecoveryNeverTruncatesLinkedImmutableObject(t *testing.T) {
	s := fixture(t)
	object := seal(t, s, "immutable-media")
	key := strings.Repeat("c", 64) + ".1"
	path := filepath.Join(s.objects, object.Digest)
	if e := os.Link(path, filepath.Join(s.staging, "capture-"+key)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ResumeRetained(context.Background(), key, 3, 100); !errors.Is(e, ErrIdentity) {
		t.Fatal("linked immutable object admitted", e)
	}
	st, e := os.Stat(path)
	if e != nil || st.Size() != object.Size || st.Mode().Perm()&0222 != 0 {
		t.Fatal("immutable object changed", e)
	}
}

// A queued SCM_RIGHTS message owns a reference even before its receiver has
// installed the descriptor. EOF, sender close and client death are not retirement.
func TestTransferredReaderSurvivesQueuedRightsAndSenderClose(t *testing.T) {
	s := fixture(t)
	object := seal(t, s, "transferred-reader")
	r, err := s.Open(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(pair[0])
	defer syscall.Close(pair[1])
	if err = syscall.Sendmsg(pair[0], []byte{1}, syscall.UnixRights(int(r.file.Fd())), nil, 0); err != nil {
		t.Fatal(err)
	}
	r.Close()
	other, err := New(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err = other.Remove(object); !errors.Is(err, ErrLeased) {
		t.Fatalf("queued descriptor ignored: %v", err)
	}
	data, control := make([]byte, 1), make([]byte, syscall.CmsgSpace(4))
	_, count, _, _, err := syscall.Recvmsg(pair[1], data, control, 0)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := syscall.ParseSocketControlMessage(control[:count])
	if err != nil || len(messages) != 1 {
		t.Fatal(messages, err)
	}
	fds, err := syscall.ParseUnixRights(&messages[0])
	if err != nil || len(fds) != 1 {
		t.Fatal(fds, err)
	}
	received := os.NewFile(uintptr(fds[0]), "received-reader")
	defer received.Close()
	if err = other.Remove(object); !errors.Is(err, ErrLeased) {
		t.Fatalf("received descriptor ignored: %v", err)
	}
	received.Close()
	if err = other.Remove(object); err != nil {
		t.Fatal(err)
	}
}

func TestStoreClosePreservesRetainedCaptureButAbortsDisposableWork(t *testing.T) {
	s := fixture(t)
	key := strings.Repeat("d", 64) + ".1"
	retained, e := s.BeginRetained(key, 100)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = retained.Write([]byte("recover-after-shutdown")); e != nil {
		t.Fatal(e)
	}
	cp, e := retained.Checkpoint()
	if e != nil {
		t.Fatal(e)
	}
	disposable, e := s.Begin(100)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = disposable.Write([]byte("discard")); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(disposable.path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("disposable staging survived", e)
	}
	reopened, e := New(s.root)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	recovered, e := reopened.ResumeRetained(context.Background(), key, cp.Size, 100)
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.Retain()
	got, e := recovered.Seal(context.Background())
	if e != nil || got != cp {
		t.Fatal("shutdown destroyed recovery identity", got, cp, e)
	}
}
