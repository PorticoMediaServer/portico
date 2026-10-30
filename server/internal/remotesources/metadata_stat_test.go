package remotesources

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSourceMetadataCaptureRechecksCurrentSourceAndProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []struct{ name, sql string }{
		{"root", `UPDATE remote_sources SET root=root||'/replacement'`},
		{"kind", `UPDATE remote_sources SET kind='rclone'`},
		{"generation", `UPDATE remote_sources SET generation=generation+1`},
		{"removed", `UPDATE remote_sources SET removed=1`},
		{"profile", `INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second) VALUES('remote',1,'basic',0,4,4,0)`},
	} {
		t.Run(change.name, func(t *testing.T) {
			f := newObservationFixture(t)
			ctx := context.Background()
			b, _, ok := f.s.find(f.file)
			if !ok {
				t.Fatal("fixture has no source")
			}
			view, err := dbwork.BeginSnapshot(ctx, f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			captured, err := captureSourceMetadataTx(ctx, view.Tx(), b)
			view.Rollback()
			if err != nil {
				t.Fatal(err)
			}
			if err = f.s.checkSourceMetadata(ctx, captured); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.db.Exec(change.sql); err != nil {
				t.Fatal(err)
			}
			if err = f.s.checkSourceMetadata(ctx, captured); err == nil {
				t.Fatal("metadata capture admitted changed source facts")
			}
		})
	}
}

func TestSourceStatDoesNotFallBackWithoutRetainedRuntime(t *testing.T) {
	f := newObservationFixture(t)
	value, err := f.s.Stat(context.Background(), f.file)
	if !errors.Is(err, storage.ErrRemoteOffline) || value != (storage.Snapshot{}) || len(f.s.serveLeases) != 0 {
		t.Fatal("missing process lifetime admitted alternate provider I/O", value, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = f.s.Stat(ctx, f.file); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled stat acquired provider work", err)
	}
}
