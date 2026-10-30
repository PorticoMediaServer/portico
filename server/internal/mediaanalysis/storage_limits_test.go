package mediaanalysis

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestOwnerAnalysisStorageLimitsApplyAtCheckpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := &Service{db: db, options: Options{MaxCacheBytes: 32 << 30, MaxGeneratedBytes: 256 << 20}}
	ctx := context.Background()
	if service.cacheLimit(ctx) != 32<<30 {
		t.Fatal("unset columns changed default")
	}
	_, err = db.Exec(`INSERT INTO server_settings(singleton,"analysisStorageGiB","analysisStageStorageMiB") VALUES(1,64,1024) ON CONFLICT(singleton) DO UPDATE SET "analysisStorageGiB"=64,"analysisStageStorageMiB"=1024`)
	if err != nil {
		t.Fatal(err)
	}
	if service.cacheLimit(ctx) != 64<<30 || service.stageLimit(ctx) != 1024<<20 {
		t.Fatal("owner limits not consumed")
	}
	_, err = db.Exec(`UPDATE server_settings SET "analysisStorageGiB"=128,"analysisStageStorageMiB"=2048 WHERE singleton=1`)
	if err != nil {
		t.Fatal(err)
	}
	if service.cacheLimit(ctx) != 128<<30 || service.stageLimit(ctx) != 2048<<20 {
		t.Fatal("hot change missed")
	}
}
