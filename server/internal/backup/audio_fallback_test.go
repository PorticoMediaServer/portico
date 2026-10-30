package backup

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestBackupCarriesHiddenCurrentAudioFallbackAndAllSlots(t *testing.T) {
	_, state, db := testService(t)
	c := catalogtest.New(t, db)
	lib := c.Library("audio", "Audio", "audiobook", "/audio")
	item := c.Movie(lib, "/audio/part.m4b", "Part", 2000)
	c.Exec(`UPDATE library_sources SET incarnation='root' WHERE library_id='audio'`)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'audio-object',id,?,'root','part.m4b','file-revision','{}',1000,1 FROM library_sources WHERE library_id='audio'`, item.Token)
	keys := []string{}
	for _, name := range []string{"embedded", "small", "large", "ambient"} {
		raw := []byte("audio fallback " + name)
		key := fmt.Sprintf("%x", sha256.Sum256(raw))
		keys = append(keys, key)
		folder := "artwork"
		if name == "embedded" {
			folder = "local-artwork"
		}
		path := filepath.Join(state, "Metadata", folder, key+".img")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if name != "embedded" {
			c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/png',2,2,?,'now')`, key, len(raw))
		}
	}
	c.Exec(`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/png',2,2,?,?,?,1920,0,0,1,'now')`, keys[0], keys[1], keys[2], keys[3])
	c.Exec(`INSERT INTO audio_file_metadata VALUES(?,'file-revision',1000,1,'known',?,'','')`, item.Token, keys[0])
	// The fallback is absent from selections, uploads, history and local carriers.
	copyTo := func() string {
		t.Helper()
		partial := t.TempDir()
		database := filepath.Join(partial, DatabaseName)
		if err := os.MkdirAll(filepath.Dir(database), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`VACUUM INTO ?`, database); err != nil {
			t.Fatal(err)
		}
		if _, err := copyStateFilesForBackup(context.Background(), state, partial, false); err != nil {
			t.Fatal(err)
		}
		return partial
	}
	partial := copyTo()
	for n, key := range keys {
		folder := "artwork"
		if n == 0 {
			folder = "local-artwork"
		}
		if got := fileHashOrEmpty(t, filepath.Join(partial, "Metadata", folder, key+".img")); got != key {
			t.Fatalf("backup lost hidden fallback %s/%s", folder, key)
		}
	}
	c.Exec(`UPDATE audio_file_metadata SET source_revision='stale' WHERE asset_id=?`, item.Token)
	partial = copyTo()
	for _, key := range keys[1:] {
		if _, err := os.Stat(filepath.Join(partial, "Metadata", "artwork", key+".img")); !os.IsNotExist(err) {
			t.Fatal("stale fallback retained rendition", err)
		}
	}
	if got := fileHashOrEmpty(t, filepath.Join(partial, "Metadata", "local-artwork", keys[0]+".img")); got != keys[0] {
		t.Fatal("raw local-artwork preservation changed")
	}
}
