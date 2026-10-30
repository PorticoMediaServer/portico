package playback

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func preparationFixture(t *testing.T, name, contents string) (*storage.Client, SourcePreparationInput) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	c := storage.New(binary)
	c.Timeout = 2 * time.Second
	c.Guard = func(p string) error {
		if p != path {
			return storage.ErrPlaybackSource
		}
		return nil
	}
	// Only the owned synthetic fixture is qualified. Tests may deliberately break
	// its no-mutation rule; this is not a policy for arbitrary production mounts.
	c.VersionPolicy = func(p string) (storage.LocalVersionPolicy, error) {
		if p != path {
			return storage.LocalVersionPolicy{}, storage.ErrPlaybackSource
		}
		return storage.LocalVersionPolicy{Scope: "root", StableObjectIDs: true, RevisionTracksWrites: true, NoInPlaceMutation: true}, nil
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(2 * time.Second)
		for c.Supervisor.Active() != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if c.Supervisor.Active() != 0 {
			t.Error("source preparation leaked a storage helper")
		}
	})
	return c, SourcePreparationInput{Fence: SourcePreparationFence{PlaybackID: "playback", PresentationID: "presentation", OwnershipRevision: 1, DesiredRevision: 2, Generation: 3, PolicyRevision: 4}, Selection: SourcePreparationSelection{Kind: "local_file", ItemID: "item", AssetID: "asset", LibraryID: "lib", RootID: "root", Path: path, InventorySize: info.Size(), InventoryModifiedNS: info.ModTime().UnixNano()}}
}

func TestSourcePreparationLocalReadAndLifetime(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("local version adapter unsupported")
	}
	c, in := preparationFixture(t, "source.bin", "0123456789")
	prepareCtx, cancelPrepare := context.WithCancel(context.Background())
	source, err := NewStrongInputPreparer(c, nil).PrepareInput(prepareCtx, in)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	cancelPrepare() // acquisition deadline does not own the successful resource
	current := source.Metadata()
	if current.Input != in || current.Reference.Kind != StrongSourceReference || current.InitialEvidence.ObservationInterval != nil {
		t.Fatal("current input contract lost selection or strong evidence")
	}
	meta := source.strong.Metadata()
	if meta.Fence != in.Fence || meta.Access != "finite_random" || meta.FactsStatus != "unknown" || meta.Size != 10 || meta.Version.ID() == "" {
		t.Fatalf("wrong prepared facts: %+v", meta)
	}
	meta.Fence.Generation = 999
	if source.Metadata().Input.Fence.Generation != 3 {
		t.Fatal("metadata copy mutated resource fence")
	}
	if err := source.strong.ValidateDescriptorDependency(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader, err := source.strong.OpenRange(context.Background(), 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(data) != "234" {
		t.Fatalf("actual local range %q %v", data, err)
	}
	file, err := source.strong.OpenDescriptor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := source.strong.ValidateDescriptor(context.Background(), file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if _, err := source.strong.OpenRange(context.Background(), 9, 2); err == nil {
		t.Fatal("out-of-bounds extent accepted")
	}
	source.Close()
	if _, err := source.strong.OpenRange(context.Background(), 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed source reopened: %v", err)
	}
}

func TestSourcePreparationRejectsStaleLocalSelection(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("local version adapter unsupported")
	}
	c, in := preparationFixture(t, "source.bin", "0123456789")
	preparer := NewStrongInputPreparer(c, nil)
	for _, mutate := range []func(*SourcePreparationInput){
		func(i *SourcePreparationInput) { i.Selection.InventorySize = 9 },
		func(i *SourcePreparationInput) { i.Selection.InventoryModifiedNS++ },
		func(i *SourcePreparationInput) { i.Selection.RootID = "other-root" },
		func(i *SourcePreparationInput) { i.Selection.ExpectedSourceVersionID = "stale" },
	} {
		stale := in
		mutate(&stale)
		if s, err := preparer.PrepareInput(context.Background(), stale); !errors.Is(err, mediasource.ErrSourceChanged) {
			if s != nil {
				s.Close()
			}
			t.Fatalf("stale selection accepted: %v", err)
		}
	}
	source, err := preparer.PrepareInput(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	replacement := in.Selection.Path + ".replacement"
	if err := os.WriteFile(replacement, []byte("abcdefghij"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(0, in.Selection.InventoryModifiedNS)
	if err := os.Chtimes(replacement, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, in.Selection.Path); err != nil {
		t.Fatal(err)
	}
	if reader, err := source.strong.OpenRange(context.Background(), 0, 1); !errors.Is(err, mediasource.ErrSourceChanged) {
		if reader != nil {
			reader.Close()
		}
		t.Fatalf("same-size/restored-mtime replacement accepted: %v", err)
	}
}

func TestSourcePreparationSTRMConditionalReadPolicyAndObject(t *testing.T) {
	// The fixture owns an ephemeral listener, independent of other packages.
	// Server bytes are in memory, never downloaded media.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conditional atomic.Int32
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != "" {
			conditional.Add(1)
			if r.Header.Get("If-Match") != `"version-1"` {
				t.Error("wrong validator")
			}
		}
		w.Header().Set("ETag", `"version-1"`)
		data := "0123456789"
		if r.URL.Path == "/media/b" {
			data = "abcdefghij"
		}
		switch r.Header.Get("Range") {
		case "bytes=0-0":
			w.Header().Set("Content-Range", "bytes 0-0/10")
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(206)
			io.WriteString(w, data[:1])
		case "bytes=2-4":
			w.Header().Set("Content-Range", "bytes 2-4/10")
			w.Header().Set("Content-Length", "3")
			w.WriteHeader(206)
			if r.URL.Path == "/media/stall" {
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			io.WriteString(w, data[2:5])
		default:
			t.Error("unexpected range")
			w.WriteHeader(400)
		}
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("fixture listener exit: %v", err)
		}
	})
	root := "http://" + listener.Addr().String()
	c, in := preparationFixture(t, "source.strm", root+"/media/a\n")
	in.Selection.Kind = "strm"
	in.Selection.NetworkPolicyRevision = 1
	db, err := persistence.Open(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	catalogLibrary(t, db, "lib", "Movies", "movie", "/test")
	remote := NewRemote(db, c, assets.Probe{})
	if _, err := remote.SetRoots(context.Background(), "lib", []string{root + "/media/"}); err != nil {
		t.Fatal(err)
	}
	preparer := NewStrongInputPreparer(nil, remote) // Remote owns its actual descriptor storage adapter.
	source, err := preparer.PrepareInput(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	current := source.Metadata()
	if current.Input != in || current.Reference.Kind != StrongSourceReference || current.InitialEvidence.ObservationInterval != nil {
		t.Fatal("current input contract lost selection or strong evidence")
	}
	meta := source.strong.Metadata()
	if meta.Version.Evidence().Kind != mediasource.StrongETag || meta.Size != 10 || meta.DescriptorDigest == "" || meta.DescriptorVersion.ID() == "" || meta.FactsStatus != "unknown" {
		t.Fatalf("incorrect remote source facts: %+v", meta)
	}
	if err := source.strong.ValidateDescriptorDependency(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader, err := source.strong.OpenRange(context.Background(), 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(data) != "234" || conditional.Load() == 0 {
		t.Fatalf("conditional actual bytes %q %v", data, err)
	}
	staleDescriptor := in
	staleDescriptor.Selection.ExpectedDescriptorVersionID = "stale-descriptor"
	if s, err := preparer.PrepareInput(context.Background(), staleDescriptor); !errors.Is(err, mediasource.ErrSourceChanged) {
		if s != nil {
			s.Close()
		}
		t.Fatalf("stale descriptor version accepted: %v", err)
	}
	stale := in
	stale.Selection.NetworkPolicyRevision = 0
	if s, err := preparer.PrepareInput(context.Background(), stale); !errors.Is(err, ErrSourcePolicyChanged) {
		if s != nil {
			s.Close()
		}
		t.Fatalf("stale policy accepted: %v", err)
	}
	// New descriptor targets another resource with the SAME strong ETag and size.
	// Those values do not prove resource identity; expected old version must fail.
	if err := os.WriteFile(in.Selection.Path, []byte(root+"/media/b\n"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(in.Selection.Path)
	if err != nil {
		t.Fatal(err)
	}
	in.Selection.InventorySize = info.Size()
	in.Selection.InventoryModifiedNS = info.ModTime().UnixNano()
	if err := source.strong.ValidateDescriptorDependency(context.Background()); !errors.Is(err, mediasource.ErrSourceChanged) {
		t.Fatalf("changed descriptor dependency accepted: %v", err)
	}
	in.Selection.ExpectedSourceVersionID = meta.Version.ID()
	if s, err := preparer.PrepareInput(context.Background(), in); !errors.Is(err, mediasource.ErrSourceChanged) {
		if s != nil {
			s.Close()
		}
		t.Fatalf("cross-resource ETag collision accepted: %v", err)
	}
	// A stalled body proves resource Close cancels actual in-flight HTTP reading.
	if err := os.WriteFile(in.Selection.Path, []byte(root+"/media/stall\n"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(in.Selection.Path)
	if err != nil {
		t.Fatal(err)
	}
	in.Selection.InventorySize, in.Selection.InventoryModifiedNS = info.Size(), info.ModTime().UnixNano()
	in.Selection.ExpectedSourceVersionID = ""
	stalled, err := preparer.PrepareInput(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	body, err := stalled.strong.OpenRange(readCtx, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	stalled.Close()
	_, err = io.ReadAll(body)
	body.Close()
	if err == nil || readCtx.Err() != nil {
		t.Fatalf("resource Close failed to promptly cancel body: %v", err)
	}
	if strings.Contains(meta.Version.Evidence().Object, root) {
		t.Fatal("protected locator leaked into source identity")
	}
}
