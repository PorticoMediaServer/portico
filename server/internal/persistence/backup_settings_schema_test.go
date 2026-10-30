package persistence

import (
	"path/filepath"
	"testing"
)

func TestBackupProtectionRequiresRetainedSealedMaterial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, sealed := range []any{nil, []byte{}, make([]byte, 39)} {
		if _, err := db.Exec(`UPDATE backup_settings SET passphrase_protected=1,passphrase_sealed=? WHERE singleton=1`, sealed); err == nil {
			t.Fatal("protected backup settings accepted missing or truncated sealed material")
		}
	}
	if _, err := db.Exec(`UPDATE backup_settings SET passphrase_protected=1,passphrase_sealed=? WHERE singleton=1`, make([]byte, 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE backup_settings SET passphrase_protected=0 WHERE singleton=1`); err == nil {
		t.Fatal("disabling protection retained sealed material")
	}
	if _, err := db.Exec(`UPDATE backup_settings SET passphrase_protected=0,passphrase_sealed=NULL WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
}
