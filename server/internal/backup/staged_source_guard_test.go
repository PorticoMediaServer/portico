package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestPortableFileRefusalPreservesExistingStagedRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p, state := policyFixture(t)
	ctx := context.Background()
	password := "pending restore preservation fixture phrase"
	if _, err := p.Update(ctx, SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &password}); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(state, BackupsDir, id, encryptedPayload))
	if err != nil {
		t.Fatal(err)
	}
	source := Source{BackupID: id}
	corrupt := bytes.Clone(raw)
	corrupt[0] ^= 1
	unsupported := bytes.Clone(raw)
	unsupported[len(envelopeMagic)-1] = 0x7f
	tests := []struct {
		name string
		raw  []byte
	}{
		{"corrupted-magic", corrupt},
		{"unsupported-version", unsupported},
		{"truncated-magic", raw[:8]},
		{"truncated-version", raw[:len(envelopeMagic)-1]},
		{"truncated-kdf-salt", raw[:len(envelopeMagic)+kdfSaltBytes-1]},
		{"unrecognized-content", bytes.Repeat([]byte("unknown backup bytes"), 8)},
		{"truncated-sqlite-header", []byte("SQLite format 3\x00")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Each refusal begins with a genuinely validated, fully staged candidate,
			// not a fabricated marker or an absent destination.
			if result, err := s.StageWithPassphrase(ctx, source, password); err != nil || !result.Staged {
				t.Fatal("valid pending restore setup", err)
			}
			staged := filepath.Join(state, RestoreStagedDir)
			if _, err := os.Stat(filepath.Join(staged, RestoreMarkerFile)); err != nil {
				t.Fatal(err)
			}
			if _, err := persistence.InspectCandidate(ctx, staged, DatabaseName); err != nil {
				t.Fatal("invalid staged fixture", err)
			}
			beforeStage := restoreGuardWitness(t, staged)
			beforeLive := restoreGuardWitness(t, filepath.Join(state, "Database"), filepath.Join(state, "Keys"))
			file := filepath.Join(t.TempDir(), "selected.db")
			if err := os.WriteFile(file, test.raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.StageWithPassphrase(ctx, Source{Path: file}, password); err == nil {
				t.Error("invalid portable file accepted")
			}
			if !reflect.DeepEqual(beforeStage, restoreGuardWitness(t, staged)) {
				t.Error("refusal changed the existing staged marker/candidate")
			}
			if !reflect.DeepEqual(beforeLive, restoreGuardWitness(t, filepath.Join(state, "Database"), filepath.Join(state, "Keys"))) {
				t.Error("refusal changed live database/Keys")
			}
			if _, err := os.Stat(filepath.Join(state, ".staged-complete")); !os.IsNotExist(err) {
				t.Error("refusal created a staged-complete candidate", err)
			}
			assertNoScratch(t, state, filepath.Join(state, BackupsDir))
		})
	}
	// Raw SQLite is supported by content, even with an encrypted-file suffix.
	if _, err = s.StageWithPassphrase(ctx, source, password); err != nil {
		t.Fatal(err)
	}
	rawDB := filepath.Join(t.TempDir(), "database.pba")
	if _, err = copyOneFile(filepath.Join(state, RestoreStagedDir, DatabaseName), rawDB, DatabaseName); err != nil {
		t.Fatal(err)
	}
	beforeLive := restoreGuardWitness(t, filepath.Join(state, "Database"), filepath.Join(state, "Keys"))
	if result, err := s.Stage(ctx, Source{Path: rawDB}); err != nil || !result.Staged {
		t.Fatal("supported raw SQLite restore refused", err)
	}
	if !reflect.DeepEqual(beforeLive, restoreGuardWitness(t, filepath.Join(state, "Database"), filepath.Join(state, "Keys"))) {
		t.Fatal("raw SQLite staging changed live state")
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}

// Hash only the isolated fixture's exact paths, modes and bytes. Never copy
// live user data or key material into evidence or failure messages.
func restoreGuardWitness(t *testing.T, roots ...string) map[string]struct {
	Mode fs.FileMode
	Hash [32]byte
} {
	t.Helper()
	out := make(map[string]struct {
		Mode fs.FileMode
		Hash [32]byte
	})
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			value := struct {
				Mode fs.FileMode
				Hash [32]byte
			}{Mode: info.Mode()}
			if info.Mode().IsRegular() {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				hash := sha256.New()
				_, copyErr := io.Copy(hash, file)
				closeErr := file.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
				copy(value.Hash[:], hash.Sum(nil))
			}
			out[path] = value
			return nil
		})
		if err != nil {
			t.Fatal("fixture witness unavailable", err)
		}
	}
	return out
}
