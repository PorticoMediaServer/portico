package catalog

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestLibraryScanPolicyPreservesStoredEpisodeMode(t *testing.T) {
	for _, mode := range []string{"provider_then_made", "provider", "off"} {
		t.Run(mode, func(t *testing.T) {
			c := catalogtest.Open(t)
			c.Library("tv", "Shows", "tv", "/shows")
			if mode != "provider_then_made" {
				// A fixture for the independent mode writer; ordinary scan writes
				// must preserve its current value, including an explicit off.
				c.Exec(`UPDATE library_scan_policies SET episode_thumbnail_mode=?,revision=revision+1 WHERE library_id='tv'`, mode)
			}
			ctx := context.Background()
			s := New(c.DB)
			read := func() ScanPolicy {
				t.Helper()
				p, err := s.ScanPolicy(ctx, "tv")
				if err != nil {
					t.Fatal(err)
				}
				var stored string
				if err := c.DB.QueryRow(`SELECT episode_thumbnail_mode FROM library_scan_policies WHERE library_id='tv'`).Scan(&stored); err != nil || stored != mode {
					t.Fatalf("independent mode changed: %q, want %q; %v", stored, mode, err)
				}
				return p
			}
			write := func(p ScanPolicy) error {
				return dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
					return ApplyScanPolicyTx(ctx, tx, "tv", p)
				})
			}
			before := read()
			complete := ScanPolicy{Tier: "complete", Trickplay: TrickplaySettings{IntervalSeconds: 5, TileWidth: 320, MaxTiles: 2160}}
			if err := write(complete); err != nil {
				t.Fatal(err)
			}
			saved := read()
			if saved.Revision != before.Revision+1 || saved.Tier != "complete" || saved.Trickplay != complete.Trickplay || !saved.Allows("chapter_images") || !saved.Allows("trickplay") || !saved.Allows("loudness") {
				t.Fatalf("scan change did not take effect: before=%+v saved=%+v", before, saved)
			}
			if err := write(complete); err != nil {
				t.Fatal(err)
			}
			if got := read(); !reflect.DeepEqual(got, saved) {
				t.Fatalf("no-op scan write changed policy: %+v, want %+v", got, saved)
			}
			invalid := complete
			invalid.Trickplay.TileWidth = 63
			if err := write(invalid); !errors.Is(err, ErrAdminQuery) {
				t.Fatalf("invalid scan geometry accepted: %v", err)
			}
			if got := read(); !reflect.DeepEqual(got, saved) {
				t.Fatalf("rejected scan write changed policy: %+v, want %+v", got, saved)
			}
			files := ScanPolicy{Tier: "file_list_only", Trickplay: complete.Trickplay}
			rollback := errors.New("rollback ordinary scan change")
			err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				if err := ApplyScanPolicyTx(ctx, tx, "tv", files); err != nil {
					return err
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if got := read(); !reflect.DeepEqual(got, saved) {
				t.Fatalf("rolled-back scan write changed policy: %+v, want %+v", got, saved)
			}
			if err := write(files); err != nil {
				t.Fatal(err)
			}
			if got := read(); got.Revision != saved.Revision+1 || got.Tier != "file_list_only" || len(got.Operations) != 0 {
				t.Fatalf("file-list change did not take effect: %+v", got)
			}
		})
	}
}
