package subtitles_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func TestInfoSubtitlePublicationPreservesOfferedProjection(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("s", "Subtitles", "movie", "/s")
	old := c.Movie(lib, "/s/oracle.mkv", "Oracle", 2026)
	next := c.Movie(lib, "/s/source.mkv", "Source", 2026)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, next.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	input := &subtitles.Inventory{Status: "known", SourceEvidence: "evidence-1", TimingKnown: true, OriginUS: 100}
	for i := 0; i < 25; i++ {
		track := subtitles.Track{Origin: "embedded", StreamIndex: i, Format: []string{"vtt", "ass", "pgs"}[i%3], Language: "en", Title: fmt.Sprintf("Track %02d", i), Default: i == 0, Forced: i == 1, HearingImpaired: i%2 == 0}
		if i == 2 {
			track.Language = "invalid_#"
		}
		if i == 3 {
			track.Title = strings.Repeat("wide", 100)
		}
		if i == 4 {
			track.Reason = "unsupported"
		}
		input.Tracks = append(input.Tracks, track)
	}
	type projection struct {
		Deleted                                   bool
		Format, Language, Title, Origin, Renderer string
		Default, Forced                           bool
		Evidence                                  string
		Hearing                                   bool
		Revision, Facts                           int64
	}
	read := func(item int64) []projection {
		rows, err := c.DB.Query(`SELECT r.deleted,v.format,v.language,v.title,v.origin,rr.renderer,rr.is_default,rr.is_forced,rr.source_evidence,rr.hearing_impaired,r.discovery_revision,v.source_facts_revision FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision JOIN subtitle_render_revisions rr ON rr.resource_id=v.resource_id AND rr.revision=v.revision WHERE r.item_id=? AND r.discovered=1 ORDER BY v.title,v.format,v.language,r.deleted`, item)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []projection{}
		for rows.Next() {
			var p projection
			if err := rows.Scan(&p.Deleted, &p.Format, &p.Language, &p.Title, &p.Origin, &p.Renderer, &p.Default, &p.Forced, &p.Evidence, &p.Hearing, &p.Revision, &p.Facts); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			input = &subtitles.Inventory{Status: "known", SourceEvidence: "evidence-2", Tracks: input.Tracks[:2]}
		}
		// Retain the old complete projection as an independent semantic oracle.
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return subtitles.Persist(tx, old.Token, size, modified, input)
		})
		c.Drain()
		dbwork.ResetPlans()
		check := func(ctx context.Context, tx *sql.Tx) error {
			var valid bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_assets WHERE id=? AND token=? AND size=? AND modified_ns=?)`, next.Asset, next.Token, size, modified).Scan(&valid)
			if err != nil {
				return err
			}
			if !valid {
				return compactcatalog.ErrInfoInputDrift
			}
			return nil
		}
		receipt, err := compactcatalog.PublishInfoSource(context.Background(), c.DB, check, func(ctx context.Context, tx *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
			var epoch int64
			if err := tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=?`, next.Asset).Scan(&epoch); err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			families, err := subtitles.BuildInfoInput(ctx, tx, next.Asset, next.Token, size, modified, input)
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			var mask int64
			for _, family := range families {
				mask |= 1 << uint(family.Number-1)
			}
			spec := compactcatalog.InfoInputSpec{AssetID: next.Asset, Token: next.Token, ExpectedEpoch: epoch, FamilyMask: mask, Fingerprint: [32]byte{byte(pass + 1)}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{byte(pass + 1)}}}}
			return spec, families, nil
		})
		if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
			t.Fatal("accept", receipt, err)
		}
		c.Drain()
		got, want := read(next.ID), read(old.ID)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("projection pass%d\ngot%+v\nwant%+v", pass, got, want)
		}
		stats := dbwork.Plans()
		plan := dbwork.DefaultPlan(dbwork.ClassBackgroundMedia)
		if stats.Measured == 0 || stats.Overruns != 0 || stats.Largest.Rows > plan.Rows || stats.Largest.Pages > plan.Pages || stats.Largest.VMOps > plan.VMOps {
			t.Fatalf("source projection exceeded actual plan: %+v", stats)
		}
		t.Logf("pass%d: rows/pages/VM %d/%d/%d", pass, stats.Largest.Rows, stats.Largest.Pages, stats.Largest.VMOps)
	}
}
