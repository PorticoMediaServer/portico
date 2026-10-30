package compactcatalog_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// Exercise both real workers against the production baseline. In particular,
// a provider claiming/completing after the P2-52 pass must survive domain43's
// later turn. No copied triggers or reimplemented P2-52 effects form the oracle.
func TestSourceLibraryScreenArtworkSingleOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, event := range []string{"scan", "configuration", "source"} {
		for _, interleave := range []string{"owner-then-claim", "owner-then-complete", "catalog-first"} {
			t.Run(event+"/"+interleave, func(t *testing.T) {
				ctx := context.Background()
				c := catalogtest.Open(t)
				library := c.Library("lib", "Films", "movie", "/films")
				item := c.Movie(library, "/films/film.mkv", "Film", 2000)
				c.Drain()
				s := metadata.New(c.DB, "")
				scalar := func(q string, args ...any) int64 {
					t.Helper()
					var n int64
					if err := c.DB.QueryRow(q, args...).Scan(&n); err != nil {
						t.Fatal(err)
					}
					return n
				}
				settle := func() {
					t.Helper()
					for i := 0; i < 20; i++ {
						if scalar(`SELECT count(*) FROM screen_metadata_fanout WHERE pending=1`) == 0 {
							return
						}
						if err := s.ScreenFanoutStep(ctx); err != nil {
							t.Fatal(err)
						}
					}
					t.Fatal("screen owner did not settle")
				}
				settle()
				c.Exec(`UPDATE screen_metadata_work SET status='matched',lease='',lease_until='' WHERE target_kind='item' AND target_id=?`, item.ID)
				generation := scalar(`SELECT generation FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, item.ID)
				markerGeneration := scalar(`SELECT COALESCE((SELECT generation FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'),0)`)
				switch event {
				case "scan":
					c.Exec(`UPDATE library_scan_policies SET revision=revision+1 WHERE library_id='lib'`)
				case "configuration":
					c.Exec(`UPDATE library_configuration SET metadata_revision=metadata_revision+1,revision=revision+1 WHERE library_id='lib'`)
				case "source":
					c.Exec(`UPDATE library_sources SET configured_root='/films-new' WHERE library_id='lib'`)
				}
				if scalar(`SELECT generation FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`) <= markerGeneration {
					// Source/scan triggers can also advance metadata configuration.
					// P2-52 coalesces those markers into one target walk.
					t.Fatal("source edit did not accept screen work")
				}
				if scalar(`SELECT count(*) FROM catalog_upstream_sources WHERE domain=43 AND (flags&48)<>0`) != 0 {
					t.Fatal("duplicate library screen/artwork owner queued")
				}
				if interleave == "catalog-first" {
					c.Drain()
					if scalar(`SELECT generation FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, item.ID) != generation {
						t.Fatal("catalogue worker duplicated screen wake")
					}
				}
				settle()
				if scalar(`SELECT generation FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, item.ID) != generation+1 {
					t.Fatal("one edit woke target more than once")
				}
				if scalar(`SELECT count(*) FROM screen_metadata_work WHERE target_kind='item' AND target_id=? AND status='pending' AND lease=''`, item.ID) != 1 {
					t.Fatal("screen owner lost wake/lease semantics")
				}
				status, lease := "pending", "intervening-lease"
				if interleave == "owner-then-complete" {
					status, lease = "matched", ""
				}
				c.Exec(`UPDATE screen_metadata_work SET status=?,lease=?,lease_until='future' WHERE target_kind='item' AND target_id=?`, status, lease, item.ID)
				c.Exec(`DELETE FROM artwork_dirty`)
				c.Drain()
				settle()
				if got := scalar(`SELECT count(*) FROM screen_metadata_work WHERE target_kind='item' AND target_id=? AND status=? AND lease=? AND generation=?`, item.ID, status, lease, generation+1); got != 1 {
					t.Fatal("second worker reset intervening claim/completion")
				}
				if scalar(`SELECT count(*) FROM artwork_dirty`) != 0 {
					t.Fatal("second library worker repeated consumed artwork invalidation")
				}
				ready, err := compactcatalog.PublicationSourcesReady(ctx, c.DB)
				if err != nil || !ready {
					t.Fatalf("source-head readiness after owner settled: %v %v", ready, err)
				}
			})
		}
	}
}

func TestSourceLibraryFanoutCountsDoNotBecomeContentAcceptance(t *testing.T) {
	ctx := context.Background()
	c := catalogtest.Open(t)
	library := c.Library("lib", "Films", "movie", "/films")
	c.Movie(library, "/films/film.mkv", "Film", 2000)
	c.Drain()
	before, err := compactcatalog.CaptureFacetProducerFence(ctx, c.DB)
	if err != nil {
		t.Fatal(err)
	}
	// This is actual accepted policy work, still pending in P2-52. Current live
	// policy/config/scan checks protect providers; a source-head marker is absent.
	c.Exec(`UPDATE library_scan_policies SET revision=revision+1 WHERE library_id='lib'`)
	ready, err := compactcatalog.PublicationSourcesReady(ctx, c.DB)
	if err != nil || !ready {
		t.Fatalf("asynchronous screen count mistaken for source acceptance: %v %v", ready, err)
	}
	after, err := compactcatalog.CaptureFacetProducerFence(ctx, c.DB)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("screen work changed accepted facet image: %s", fmt.Sprint(before, after))
	}
}
