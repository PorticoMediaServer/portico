package access

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dirsync"
)

func keyCreationSteps() invitationKeyIO {
	return invitationKeyIO{(*os.File).Write, (*os.File).Sync, (*os.File).Close, os.Link, dirsync.Sync}
}

func assertKeyFiles(t *testing.T, state string, installed bool) []byte {
	t.Helper()
	entries, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	if installed {
		want = 1
	}
	if len(entries) != want {
		t.Fatalf("temporary files remain: %v", entries)
	}
	if !installed {
		return nil
	}
	path := filepath.Join(state, "invitation-link.key")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe installed key: %v", info.Mode())
	}
	key, err := os.ReadFile(path)
	if err != nil || len(key) != 32 {
		t.Fatalf("incomplete installed key: length=%d error=%v", len(key), err)
	}
	return key
}

func TestInvitationKeyCreationFailuresLeaveRestartableState(t *testing.T) {
	for _, boundary := range []string{"short-write", "failed-write", "sync", "close", "install", "directory-sync"} {
		t.Run(boundary, func(t *testing.T) {
			state := t.TempDir()
			steps := keyCreationSteps()
			injected := syscall.ENOSPC
			switch boundary {
			case "short-write":
				steps.write = func(f *os.File, key []byte) (int, error) { return f.Write(key[:7]) }
			case "failed-write":
				steps.write = func(f *os.File, key []byte) (int, error) { n, _ := f.Write(key[:7]); return n, injected }
			case "sync":
				steps.syncFile = func(*os.File) error { return injected }
			case "close":
				steps.closeFile = func(f *os.File) error { return errors.Join(f.Close(), injected) }
			case "install":
				steps.install = func(string, string) error { return injected }
			case "directory-sync":
				steps.syncDirectory = func(string) error { return injected }
			}
			store := &Store{invitationKey: []byte("unchanged")}
			err := store.loadInvitationKey(state, steps)
			want := error(injected)
			if boundary == "short-write" {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) {
				t.Fatalf("failure not returned: %v", err)
			}
			if string(store.invitationKey) != "unchanged" {
				t.Fatal("failed load changed active key")
			}
			installed := boundary == "directory-sync"
			before := assertKeyFiles(t, state, installed)
			restarted := &Store{}
			if err := restarted.LoadInvitationKey(state); err != nil {
				t.Fatal(err)
			}
			after := assertKeyFiles(t, state, true)
			if installed && !bytes.Equal(before, after) {
				t.Fatal("restart replaced a complete key after directory sync failure")
			}
			if !bytes.Equal(restarted.invitationKey, after) {
				t.Fatal("restart loaded another key")
			}
		})
	}
}

func TestInvitationKeyCreationPublishesOnlyAfterClose(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, "invitation-link.key")
	steps := keyCreationSteps()
	var order []string
	absent := func() {
		t.Helper()
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("final key published early: %v", err)
		}
	}
	steps.write = func(f *os.File, key []byte) (int, error) {
		absent()
		if filepath.Dir(f.Name()) != state {
			t.Fatal("temporary key is on another filesystem")
		}
		info, err := f.Stat()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("temporary key is not private")
		}
		order = append(order, "write")
		return f.Write(key)
	}
	steps.syncFile = func(f *os.File) error { absent(); order = append(order, "sync"); return f.Sync() }
	steps.closeFile = func(f *os.File) error { absent(); order = append(order, "close"); return f.Close() }
	steps.install = func(temp, final string) error {
		absent()
		order = append(order, "install")
		raw, err := os.ReadFile(temp)
		if err != nil || len(raw) != 32 {
			t.Fatal("incomplete temporary key")
		}
		return os.Link(temp, final)
	}
	steps.syncDirectory = func(dir string) error {
		order = append(order, "directory-sync")
		assertKeyFiles(t, state, true)
		return dirsync.Sync(dir)
	}
	store := &Store{}
	if err := store.loadInvitationKey(state, steps); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "write,sync,close,install,directory-sync" {
		t.Fatalf("wrong publication order: %s", got)
	}
	assertKeyFiles(t, state, true)
}

func TestInvitationKeyConcurrentInstallUsesTheWinner(t *testing.T) {
	const creators = 16
	state := t.TempDir()
	ready := make(chan struct{}, creators)
	release := make(chan struct{})
	var wg sync.WaitGroup
	stores := make([]Store, creators)
	errs := make([]error, creators)
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			steps := keyCreationSteps()
			steps.install = func(temp, final string) error { ready <- struct{}{}; <-release; return os.Link(temp, final) }
			errs[i] = stores[i].loadInvitationKey(state, steps)
		}(i)
	}
	for range creators {
		<-ready
	}
	close(release)
	wg.Wait()
	key := assertKeyFiles(t, state, true)
	for i := range stores {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if !bytes.Equal(stores[i].invitationKey, key) {
			t.Fatal("creator did not reuse the winner")
		}
	}
}

func TestInvitationKeyNeverReplacesMalformedEstablishedOrConcurrentWinner(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "established", true: "concurrent"}[concurrent], func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, "invitation-link.key")
			malformed := []byte("incomplete")
			steps := keyCreationSteps()
			if concurrent {
				steps.install = func(temp, final string) error {
					if err := os.WriteFile(final, malformed, 0600); err != nil {
						return err
					}
					return os.Link(temp, final)
				}
			} else if err := os.WriteFile(path, malformed, 0600); err != nil {
				t.Fatal(err)
			}
			if err := (&Store{}).loadInvitationKey(state, steps); err == nil {
				t.Fatal("malformed key accepted")
			}
			raw, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, malformed) {
				t.Fatal("malformed key was silently replaced")
			}
			entries, _ := os.ReadDir(state)
			if len(entries) != 1 {
				t.Fatal("temporary files remain")
			}
		})
	}
}

func TestInvitationKeyRejectsUnsafeEstablishedFiles(t *testing.T) {
	for _, kind := range []string{"empty", "public", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, "invitation-link.key")
			switch kind {
			case "empty":
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.WriteFile(path, make([]byte, 32), 0644); err != nil {
					t.Fatal(err)
				}
				// The refusal control must remain public even under a private umask.
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, make([]byte, 32), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := (&Store{}).LoadInvitationKey(state); err == nil {
				t.Fatal("unsafe established key accepted")
			}
			entries, _ := os.ReadDir(state)
			if len(entries) != 1 {
				t.Fatal("unsafe key triggered replacement")
			}
		})
	}
}

// Exit from an actual creation boundary, bypassing defers as a terminated
// process would. The parent starts a fresh loader against exactly those files.
func TestInvitationKeyInterruptedProcessRestart(t *testing.T) {
	if boundary := os.Getenv("PORTICO_TEST_KEY_INTERRUPT"); boundary != "" {
		steps := keyCreationSteps()
		switch boundary {
		case "partial-write":
			steps.write = func(f *os.File, key []byte) (int, error) { f.Write(key[:7]); os.Exit(73); return 0, nil }
		case "after-sync":
			steps.syncFile = func(f *os.File) error {
				if err := f.Sync(); err != nil {
					return err
				}
				os.Exit(73)
				return nil
			}
		case "after-close":
			steps.closeFile = func(f *os.File) error {
				if err := f.Close(); err != nil {
					return err
				}
				os.Exit(73)
				return nil
			}
		case "after-install":
			steps.install = func(temp, final string) error {
				if err := os.Link(temp, final); err != nil {
					return err
				}
				os.Exit(73)
				return nil
			}
		}
		if err := (&Store{}).loadInvitationKey(os.Getenv("PORTICO_TEST_KEY_STATE"), steps); err != nil {
			t.Fatal(err)
		}
		t.Fatal("interruption not reached")
	}
	for _, boundary := range []string{"partial-write", "after-sync", "after-close", "after-install"} {
		t.Run(boundary, func(t *testing.T) {
			state := t.TempDir()
			command := exec.Command(os.Args[0], "-test.run=^TestInvitationKeyInterruptedProcessRestart$")
			command.Env = append(os.Environ(), "PORTICO_TEST_KEY_INTERRUPT="+boundary, "PORTICO_TEST_KEY_STATE="+state)
			err := command.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("did not interrupt at %s: %v", boundary, err)
			}
			path := filepath.Join(state, "invitation-link.key")
			before, err := os.ReadFile(path)
			if boundary == "after-install" {
				if err != nil || len(before) != 32 {
					t.Fatal("installed key is incomplete")
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("interruption left final key: %v", err)
			}
			restarted := &Store{}
			if err := restarted.LoadInvitationKey(state); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || len(after) != 32 {
				t.Fatal("restart failed to establish a complete key")
			}
			if boundary == "after-install" && !bytes.Equal(before, after) {
				t.Fatal("restart replaced installed key")
			}
			// A killed process cannot run cleanup. Its private orphan is ignored,
			// never loaded as a key or deleted by a concurrent live initializer.
			orphans, _ := filepath.Glob(filepath.Join(state, ".invitation-link-key-*"))
			if len(orphans) != 1 {
				t.Fatalf("expected one interrupted private temporary: %d", len(orphans))
			}
			info, err := os.Stat(orphans[0])
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("interrupted temporary was not private")
			}
			if err := os.Remove(orphans[0]); err != nil {
				t.Fatal(err)
			}
			assertKeyFiles(t, state, true)
		})
	}
}
