package networking

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestFreshClaimKeysFailureCanRetryAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, failure := range []string{"key-directory", "state-directory", "identity-insert", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			state := t.TempDir()
			db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, runner := lifecycleRunner(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			refused := errors.New("injected directory flush failure")
			if failure == "identity-insert" {
				if _, err = db.Exec(`CREATE TRIGGER fail_fresh_identity BEFORE INSERT ON configuration WHEN NEW.key='id' BEGIN SELECT RAISE(ABORT,'injected identity insert failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			syncDir := func(path string) error {
				if failure == "key-directory" && path == storagepaths.Path(state, "networking-keys") {
					return refused
				}
				if failure == "state-directory" && path == state {
					return refused
				}
				if failure == "cancelled" && path == state {
					cancel()
				}
				return syncDirectory(path)
			}
			keys, err := openCurrentKeys(ctx, db, state, runner, freshKeySteps{syncDir, (*sql.Tx).Commit})
			if err == nil || keys != nil {
				t.Fatalf("failed creation accepted: %v", err)
			}
			if failure == "key-directory" || failure == "state-directory" {
				if !errors.Is(err, refused) {
					t.Fatalf("flush error suppressed: %v", err)
				}
			}
			entries, err := os.ReadDir(storagepaths.Path(state, "networking-keys"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("orphan key prevents retry: %v %v", entries, err)
			}
			var identities int
			if err = db.QueryRow(`SELECT count(*) FROM networking_server_identities`).Scan(&identities); err != nil || identities != 0 {
				t.Fatalf("failed identity persisted: %d %v", identities, err)
			}
			if failure == "identity-insert" {
				if _, err = db.Exec(`DROP TRIGGER fail_fresh_identity`); err != nil {
					t.Fatal(err)
				}
			}
			keys, err = OpenCurrentKeys(context.Background(), db, state, runner)
			if err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			keys.Close()
			entries, err = os.ReadDir(storagepaths.Path(state, "networking-keys"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("key registry: %v %v", entries, err)
			}
			path := storagepaths.Path(state, "networking-keys", entries[0].Name())
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = persistence.Open(storagepaths.Path(state, "server.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			keys, err = OpenCurrentKeys(context.Background(), db, state, runner)
			if err != nil {
				t.Fatalf("restart failed: %v", err)
			}
			keys.Close()
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("restart replaced key: %v", err)
			}
		})
	}
}

func TestFreshClaimKeysNeverDeleteUnmarkedExistingMaterial(t *testing.T) {
	state := t.TempDir()
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, runner := lifecycleRunner(t)
	directory := storagepaths.Path(state, "networking-keys")
	if err = os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "existing.ed25519")
	original := []byte("preserve preexisting unmarked key")
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if keys, err := OpenCurrentKeys(context.Background(), db, state, runner); err == nil || keys != nil {
		t.Fatal("adopted unmarked existing material")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("existing material changed: %v", err)
	}
}

// After a commit attempt the identity may be durable even when Commit reports
// failure, so the key created by that attempt must never be removed.
func TestFreshClaimKeysKeepKeyAfterAmbiguousCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := "rolled-back"
		if committed {
			name = "committed"
		}
		t.Run(name, func(t *testing.T) {
			state := t.TempDir()
			db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, runner := lifecycleRunner(t)
			refused := errors.New("injected ambiguous commit failure")
			commit := func(tx *sql.Tx) error {
				if committed {
					if err := tx.Commit(); err != nil {
						return err
					}
				}
				return refused
			}
			keys, err := openCurrentKeys(context.Background(), db, state, runner, freshKeySteps{syncDirectory, commit})
			if !errors.Is(err, refused) || keys != nil {
				t.Fatalf("ambiguous commit accepted: %v", err)
			}
			entries, err := os.ReadDir(storagepaths.Path(state, "networking-keys"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("possibly committed key removed: %v %v", entries, err)
			}
			keys, err = OpenCurrentKeys(context.Background(), db, state, runner)
			if committed {
				if err != nil {
					t.Fatalf("committed identity unusable: %v", err)
				}
				keys.Close()
				return
			}
			// Without a committed identity the retained key is unmarked state:
			// refused, never adopted or deleted.
			if err == nil || keys != nil {
				t.Fatal("unmarked retained key adopted")
			}
			if after, err := os.ReadDir(storagepaths.Path(state, "networking-keys")); err != nil || len(after) != 1 || after[0].Name() != entries[0].Name() {
				t.Fatalf("retained key changed: %v %v", after, err)
			}
		})
	}
}
