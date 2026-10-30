package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"sync"
	"testing"
)

// Err is first consulted by the copy loop, after resolveSource verifies the
// source. Mutate there to reproduce a source changing during restore without
// relying on a timing race or adding a production test hook.
type changingRestoreSource struct {
	context.Context
	once   sync.Once
	change func()
}

func (c *changingRestoreSource) Err() error {
	c.once.Do(c.change)
	return c.Context.Err()
}

func TestRestoreVerifiesCopiedBytesAgainstCapturedManifest(t *testing.T) {
	for _, byPath := range []bool{false, true} {
		t.Run(map[bool]string{false: "id", true: "path"}[byPath], func(t *testing.T) {
			svc, state, db := testService(t)
			if _, err := db.Exec(`INSERT INTO configuration(key,value) VALUES('copy_marker','COPY_ORIGINAL_VALUE')`); err != nil {
				t.Fatal(err)
			}
			id, err := svc.Create(t.Context(), KindManual)
			if err != nil {
				t.Fatal(err)
			}
			folder := storagepaths.Path(state, BackupsDir, id)
			source := Source{BackupID: id}
			if byPath {
				source = Source{Path: folder}
			}
			changed := false
			ctx := &changingRestoreSource{Context: t.Context(), change: func() {
				path := filepath.Join(folder, DatabaseName)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Count(raw, []byte("COPY_ORIGINAL_VALUE")) != 1 {
					t.Fatal("copy marker must be unique")
				}
				raw = bytes.Replace(raw, []byte("COPY_ORIGINAL_VALUE"), []byte("COPY_MODIFIED_VALUE"), 1)
				if err = writeTestFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				// Rewriting the source manifest too cannot change the expectations
				// already captured by this restore operation.
				manifest, err := readManifest(folder)
				if err != nil {
					t.Fatal(err)
				}
				entry, err := hashFile(t.Context(), folder, DatabaseName)
				if err != nil {
					t.Fatal(err)
				}
				for i := range manifest.Files {
					if manifest.Files[i].Name == DatabaseName {
						manifest.Files[i] = entry
					}
				}
				if err = writeManifest(folder, manifest); err != nil {
					t.Fatal(err)
				}
				changed = true
			}}
			if _, err = svc.Stage(ctx, source); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("changed source staged: %v", err)
			}
			if !changed {
				t.Fatal("source mutation hook was not exercised")
			}
			if _, err = os.Stat(storagepaths.Path(state, RestoreStagedDir, RestoreMarkerFile)); !os.IsNotExist(err) {
				t.Fatalf("invalid copy published a restore marker: %v", err)
			}
		})
	}
}
