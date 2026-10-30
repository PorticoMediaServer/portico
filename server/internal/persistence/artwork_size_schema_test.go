package persistence

import (
	"path/filepath"
	"testing"
)

func TestMetadataArtworkSizeUsesExistingSettingsAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var edge, revision, localLimit int64
	if err := db.QueryRow(`SELECT artwork_max_edge,revision,local_artwork_limit_bytes FROM metadata_runtime_settings WHERE singleton=1`).Scan(&edge, &revision, &localLimit); err != nil {
		t.Fatal(err)
	}
	if edge != 1920 || revision != 1 || localLimit != 2147483648 {
		t.Fatalf("Metadata defaults changed: edge=%d revision=%d local limit=%d", edge, revision, localLimit)
	}
	for _, value := range []int{1280, 1920, 2560} {
		if _, err := db.Exec(`UPDATE metadata_runtime_settings SET artwork_max_edge=? WHERE singleton=1`, value); err != nil {
			t.Fatalf("rejected artwork edge %d: %v", value, err)
		}
	}
	for _, value := range []any{nil, 0, 1279, 1281, 1921, 2561, 3840, "automatic"} {
		if _, err := db.Exec(`UPDATE metadata_runtime_settings SET artwork_max_edge=? WHERE singleton=1`, value); err == nil {
			t.Fatalf("accepted invalid artwork edge %v", value)
		}
	}
}
