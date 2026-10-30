package mediaartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func seal(t *testing.T, s *Store, data string) Object {
	t.Helper()
	w, err := s.Begin(128)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	if _, err = w.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	o, err := w.Seal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func TestClosedArtifactIdentityAndLease(t *testing.T) {
	s := fixture(t)
	o := seal(t, s, "closed-media")
	sum := sha256.Sum256([]byte("closed-media"))
	if o.Digest != hex.EncodeToString(sum[:]) || o.Size != 12 {
		t.Fatal("wrong closed byte identity", o)
	}
	repeated := seal(t, s, "closed-media")
	if repeated != o {
		t.Fatal("identical closed bytes changed identity")
	}
	reader, err := s.Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Remove(o); !errors.Is(err, ErrLeased) {
		t.Fatal("collector interrupted read lease", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "closed-media" {
		t.Fatal("store close destroyed reader", err)
	}
	reader.Close()
	reader.Close()
}
func TestClosedArtifactAbortLimitAndCancellation(t *testing.T) {
	s := fixture(t)
	w, err := s.Begin(3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("four")); !errors.Is(err, ErrLimit) {
		t.Fatal("output limit ignored", err)
	}
	if _, err = w.Seal(context.Background()); !errors.Is(err, ErrLimit) {
		t.Fatal("limited output sealed", err)
	}
	w, err = s.Begin(10)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("bytes"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = w.Seal(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled output sealed", err)
	}
	staged, err := os.ReadDir(s.staging)
	if err != nil || len(staged) != 0 {
		t.Fatal("aborted staging remains", err)
	}
	objects, err := os.ReadDir(s.objects)
	if err != nil || len(objects) != 0 {
		t.Fatal("incomplete output became closed", err)
	}
}
func TestClosedArtifactRejectsAlterationAndCollectsAfterRelease(t *testing.T) {
	s := fixture(t)
	o := seal(t, s, "original")
	reader, err := s.Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	path := filepath.Join(s.objects, o.Digest)
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Open(context.Background(), o); !errors.Is(err, ErrIdentity) || r != nil {
		t.Fatal("closed URI accepted changed bytes", err)
	}
	w, err := s.Begin(100)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("original"))
	if _, err = w.Seal(context.Background()); !errors.Is(err, ErrIdentity) {
		t.Fatal("conflicting existing content silently overwritten", err)
	}
	if err = s.Remove(o); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unleased object not collected", err)
	}
}

func TestClosedArtifactDirectoryDurabilityFailureRetry(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "new-cache")
	injected := errors.New("directory sync failed")
	calls := []string{}
	store, err := newStore(root, func(path string) error {
		calls = append(calls, path)
		if path == parent {
			return injected
		}
		return syncDirectory(path)
	})
	if store != nil || !errors.Is(err, injected) {
		t.Fatal("failed parent entry sync accepted cache", err)
	}
	if len(calls) != 4 || calls[0] != filepath.Join(root, "staging") || calls[1] != filepath.Join(root, "objects") || calls[2] != root || calls[3] != parent {
		t.Fatal("creation durability ordering", calls)
	}
	calls = nil
	store, err = newStore(root, func(path string) error { calls = append(calls, path); return syncDirectory(path) })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	parentSeen := false
	for _, path := range calls {
		if path == parent {
			parentSeen = true
		}
	}
	if !parentSeen || calls[len(calls)-1] != filepath.VolumeName(root)+string(filepath.Separator) {
		t.Fatal("existing retry skipped uncertain ancestry", calls)
	}
	object := seal(t, store, "durable")
	read, err := store.Open(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	read.Close()
}

// Inspect answers what New would say about a folder that is already there,
// and leaves it exactly as it found it.
func TestInspectAgreesWithNewWithoutCreatingAnything(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "store")
	if err = Inspect(root); err != nil {
		t.Fatalf("a store that does not exist yet was refused: %v", err)
	}
	if _, err = os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("inspecting created the store: %v", err)
	}
	if _, err = New(root); err != nil {
		t.Fatal(err)
	}
	if err = Inspect(root); err != nil {
		t.Fatalf("a store New made was refused: %v", err)
	}
	for name, spoil := range map[string]func(root string) error{
		"the store is a file": func(root string) error { return os.WriteFile(root, []byte("x"), 0600) },
		"its objects folder is a file": func(root string) error {
			if err := os.Mkdir(root, 0700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(root, "objects"), []byte("x"), 0600)
		},
		"its staging folder is a link": func(root string) error {
			if err := os.Mkdir(root, 0700); err != nil {
				return err
			}
			return os.Symlink(parent, filepath.Join(root, "staging"))
		},
		"the store is a link": func(root string) error { return os.Symlink(filepath.Join(parent, "store"), root) },
	} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(parent, "spoiled-"+filepath.Base(t.Name()))
			if err := spoil(root); err != nil {
				t.Fatal(err)
			}
			if err := Inspect(root); !errors.Is(err, ErrPrivate) {
				t.Fatalf("inspection accepted it: %v", err)
			}
			if _, err := New(root); err == nil {
				t.Fatal("the fixture is one New accepts; it proves nothing")
			}
		})
	}
}

func TestArtifactStoreFreshStartupRestartAndPublication(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "subtitle-store")
	store, err := New(root)
	if err != nil {
		t.Fatalf("fresh startup: %v", err)
	}
	object := seal(t, store, "subtitle bytes")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = New(root)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer store.Close()
	reader, err := store.Open(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(data) != "subtitle bytes" {
		t.Fatalf("restarted object: %q %v", data, err)
	}
	if next := seal(t, store, "new subtitle bytes"); next == object {
		t.Fatal("restart publication reused different bytes")
	}
}
