//go:build linux || darwin

package remotesources

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestEpisodeLocalSourceUsesConfinedActualScanIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"unchanged", "object-replaced", "root-replaced"} {
		t.Run(change, func(t *testing.T) {
			f, resolver, request, _ := episodeReadFixture(t)
			helper, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			client := storage.New(helper)
			client.Timeout = 5 * time.Second
			managed, err := mounts.New(f.s.db, t.TempDir(), "", helper, client)
			if err != nil {
				t.Fatal(err)
			}
			f.s.mounts, resolver.LocalStorage = managed, client
			root := filepath.Dir(f.file)
			payload := []byte("original-episode")
			if err = os.WriteFile(f.file, payload, 0600); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			observedRoot, err := client.InspectRoot(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := client.Stat(ctx, "episode-local-scan", f.file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.db.Exec(`UPDATE library_sources SET kind='local',root_identity=? WHERE id=?`, storage.RootIdentity(observedRoot), f.library); err != nil {
				t.Fatal(err)
			}
			selected, err := catalog.New(f.s.db).LibrarySource(ctx, f.library)
			if err != nil {
				t.Fatal(err)
			}
			request.Target.SourceRevision, _, err = catalog.ObservationRevision(selected, observed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.db.Exec(`UPDATE inventory_objects SET revision=?,size=?,modified_ns=? WHERE id='object'`, request.Target.SourceRevision, observed.Size, observed.ModifiedNS); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.db.Exec(`UPDATE catalog_assets SET size=?,modified_ns=? WHERE token='asset'`, observed.Size, observed.ModifiedNS); err != nil {
				t.Fatal(err)
			}
			view, err := dbwork.BeginSnapshot(ctx, f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			source, err := resolver.ResolveEpisodeReadSourceTx(ctx, view.Tx(), request, func() {})
			view.Rollback()
			if err != nil || source == nil {
				t.Fatal("actual local source capture", err)
			}
			t.Cleanup(func() {
				source.Close()
				deadline := time.Now().Add(3 * time.Second)
				for client.Supervisor.Active() != 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if client.Supervisor.Active() != 0 {
					t.Error("owned confined helpers not physically drained")
				}
			})
			switch change {
			case "object-replaced":
				err = os.Rename(f.file, f.file+".original")
			case "root-replaced":
				err = os.Rename(root, root+".original")
				if err == nil {
					err = os.Mkdir(root, 0700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if change != "unchanged" {
				if err = os.WriteFile(f.file, bytes.Repeat([]byte("x"), len(payload)), 0600); err != nil {
					t.Fatal(err)
				}
				mtime := time.Unix(0, observed.ModifiedNS)
				if err = os.Chtimes(f.file, mtime, mtime); err != nil {
					t.Fatal(err)
				}
			}
			input, err := source.Open(ctx)
			if change != "unchanged" {
				if input != nil {
					input.Close()
					t.Fatal("replacement acquired an input")
				}
				if !errors.Is(err, storage.ErrRemoteChanged) {
					t.Fatal("equal-size/time replacement was admitted", err)
				}
				return
			}
			if err != nil {
				t.Fatal("unchanged actual confined local input", err)
			}
			got, err := input.ReadExtent(ctx, 0, int64(len(payload)))
			input.Close()
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatal("confined actual bytes", len(got), err)
			}
			publication, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
			if err != nil {
				t.Fatal(err)
			}
			defer publication.Rollback()
			if err = source.CheckTx(ctx, publication.Tx()); err != nil {
				t.Fatal("local child close retired publication authority", err)
			}
		})
	}
}
