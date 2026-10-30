package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestObservedLocalInputActualAcquisitionAndContinuity(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("actual directory handle implementation required")
	}
	root := t.TempDir()
	path := filepath.Join(root, "ordinary.bin")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := storage.New(binary)
	c.Timeout = time.Second
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	var released atomic.Int32
	resolver := func(_ context.Context, in SourcePreparationInput) (*storage.RootLease, error) {
		if in.Selection.Path != path || in.Selection.RootID != "root" {
			return nil, storage.ErrRootLease
		}
		// A real fixture FD exercises the required handoff, not the production root
		// registry's authorization or managed-mount incarnation mechanism.
		fd, err := os.Open(root)
		if err != nil {
			return nil, err
		}
		var once sync.Once
		return &storage.RootLease{Directory: fd, RegistrationID: "real-fixture-root", RootPath: root, RelativePath: "ordinary.bin", Lifetime: lifetime, Release: func() { once.Do(func() { fd.Close(); released.Add(1) }) }}, nil
	}
	preparer := NewObservedLocalInputPreparer(c, resolver)
	in := workerInput()
	in.Selection.Path = path
	setup, cancelSetup := context.WithCancel(context.Background())
	p, err := preparer.PrepareInput(setup, in)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cancelSetup()
	m := p.Metadata()
	if m.Input != in || m.Reference.Kind != ObservedSourceReference || m.InitialObservation == nil || m.InitialObservation.Observation.ObjectBindingID == "" || m.FactsStatus != "unknown" {
		t.Fatal("missing observed acquisition facts")
	}
	if _, strong := p.StrongVersion(); strong || c.VersionPolicy != nil {
		t.Fatal("ordinary file required or fabricated immutable evidence")
	}
	out, err := p.ReadExtent(context.Background(), 2, 3)
	if err != nil || string(out.Bytes) != "234" || out.Evidence.Reference != m.Reference {
		t.Fatal("actual extent/current reference invalid", out, err)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		_, err = p.Validate(context.Background())
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("registration cancellation did not reach input")
		}
	}
	if out, err := p.ReadExtent(context.Background(), 0, 1); err == nil || len(out.Bytes) != 0 {
		t.Fatal("lost root acquisition returned bytes")
	}
	p.Close()
	p.Close()
	deadline = time.Now().Add(2 * time.Second)
	for c.Supervisor.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if c.Supervisor.Active() != 0 || released.Load() != 1 {
		t.Fatal("actual helper or root borrow leaked")
	}
}
func TestObservedLocalInputDeniesMissingAuthorityAndPins(t *testing.T) {
	calls := 0
	resolver := func(context.Context, SourcePreparationInput) (*storage.RootLease, error) {
		calls++
		return nil, storage.ErrRootLease
	}
	p := NewObservedLocalInputPreparer(storage.New("not-started"), resolver)
	for _, descriptor := range []bool{false, true} {
		in := workerInput()
		if descriptor {
			in.Selection.ExpectedDescriptorVersionID = "strict"
		} else {
			in.Selection.ExpectedSourceVersionID = "strict"
		}
		if result, err := p.PrepareInput(context.Background(), in); result != nil || !errors.Is(err, mediasource.ErrSourceChanged) {
			t.Fatal("observed mode accepted strict pin")
		}
	}
	if calls != 0 {
		t.Fatal("strict pin initiated resolver IO")
	}
	p = NewObservedLocalInputPreparer(storage.New("not-started"), nil)
	if result, err := p.PrepareInput(context.Background(), workerInput()); result != nil || !errors.Is(err, storage.ErrRootLease) {
		t.Fatal("missing resolver admitted input")
	}
}
