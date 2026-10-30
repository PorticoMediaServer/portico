package remotesources

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// The selected-artwork facts are a fixture, not a production metadata reader.
// Actor, target, source, profile and publication checks use the real DB paths.
func episodeReadFixture(t *testing.T) (observationFixture, EpisodeAuthorityResolver, EpisodeAuthorityRequest, *episodeFactsFixture) {
	t.Helper()
	f := newObservationFixture(t)
	// This unit fixture supplies known measured range support. Actual source
	// setup qualification is exercised by the controlled provider fixtures.
	if _, err := f.s.db.Exec(`UPDATE remote_sources SET range_support='conditional'`); err != nil {
		t.Fatal(err)
	}
	// Ranged thumbnail decode is heavy remote analysis, so the source needs its
	// own recorded consent even for an explicit one-off action. Save it through
	// the real owner/CAS writer: an absent profile never supplies consent.
	identities, owner, _ := observationOwner(t, f)
	listing, err := SourceReadPreset("list")
	if err != nil {
		t.Fatal(err)
	}
	consent := true
	writer, err := dbwork.Begin(context.Background(), f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ApplySourceReadProfileTx(context.Background(), writer.Tx(), identities, owner, SourceReadProfileChange{SourceID: "remote", Generation: "1", ExpectedRevision: 0, Profile: listing, AdvancedMediaAnalysis: &consent}); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.record(t, scanFacts("episode-source"), 0); err != nil {
		t.Fatal(err)
	}
	revision := f.catalogObservationRevision(t)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES('asset',?,16,23,'mkv','h264','aac',1920,1080,60)`, []any{f.file}},
		{`INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head) SELECT X'00112233445566778899AABBCCDDEEFF',id,4,'Episode','episode','e' FROM catalog_libraries WHERE library_id=?`, []any{f.library}},
		{`INSERT INTO catalog_asset_links(entity_id,asset_id) SELECT e.id,a.id FROM catalog_entities e CROSS JOIN catalog_assets a WHERE a.token='asset'`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) SELECT 'object',id,'asset',incarnation,'film.mkv',?,'{}',16,23,'available' FROM library_sources WHERE id=?`, []any{revision, f.library}},
	} {
		if _, err := f.s.db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	var episode string
	if err := f.s.db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE kind=4`).Scan(&episode); err != nil {
		t.Fatal(err)
	}
	facts := &episodeFactsFixture{mode: "off", artworkRevision: "artwork-1"}
	resolver := EpisodeAuthorityResolver{Sources: f.s, Identities: identities, Episodes: facts}
	request := EpisodeAuthorityRequest{Target: EpisodeTarget{LibraryID: f.library, LibrarySourceID: f.library, ObjectID: "object", SourceRevision: revision, EpisodeID: episode}, Purpose: EpisodeExplicitOneEpisode, Owner: owner, Operation: "episode_thumbnail", CostClass: "decode"}
	return f, resolver, request, facts
}

func TestEpisodeReadSourceNeverUsesUnqualifiedWebDAVAsRandomInput(t *testing.T) {
	for _, support := range []string{"unknown", "unsupported"} {
		t.Run(support, func(t *testing.T) {
			f, resolver, request, _ := episodeReadFixture(t)
			if _, err := f.s.db.Exec(`UPDATE remote_sources SET range_support=?`, support); err != nil {
				t.Fatal(err)
			}
			view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			defer view.Rollback()
			source, err := resolver.ResolveEpisodeReadSourceTx(context.Background(), view.Tx(), request, func() {})
			want := ErrEpisodeAuthority
			if support == "unsupported" {
				want = storage.ErrRemoteRange
			}
			if source != nil || !errors.Is(err, want) {
				t.Fatal("unqualified WebDAV granted ranged decode", source, err)
			}
			if len(f.s.serveLeases) != 0 {
				t.Fatal("refusal opened source")
			}
		})
	}
}

func TestEpisodeReadSourceCaptureRetainsPurposeWithoutOpening(t *testing.T) {
	for _, mode := range []string{"off", "provider", "provider_then_made"} {
		t.Run(mode, func(t *testing.T) {
			f, resolver, request, facts := episodeReadFixture(t)
			facts.mode = mode
			ctx := context.Background()
			view, err := dbwork.BeginSnapshot(ctx, f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			source, err := resolver.ResolveEpisodeReadSourceTx(ctx, view.Tx(), request, func() { t.Error("permission failure became changed-file notice") })
			view.Rollback()
			if err != nil || source == nil {
				t.Fatal("explicit known mode with recorded source consent", err)
			}
			defer source.Close()
			if len(f.s.serveLeases) != 0 || source.remote == nil || source.remote.processGeneration != "" {
				t.Fatal("DB-only capture invented a physical process")
			}
			// Explicit source authority survives known automatic-art/mode changes.
			facts.mode, facts.artworkRevision = "provider", "artwork-2"
			facts.still, facts.providerResolved, facts.generationNeeded = true, false, false
			if _, err = f.s.db.Exec(`UPDATE library_scan_policies SET revision=revision+1 WHERE library_id=?`, f.library); err != nil {
				t.Fatal(err)
			}
			if err = source.Authorize(ctx, "episode_thumbnail", "decode", true); err != nil {
				t.Fatal("automatic facts became explicit permission", err)
			}
			if err = source.Authorize(ctx, "episode_thumbnail", "decode", false); !errors.Is(err, ErrEpisodeAuthority) {
				t.Fatal("captured purpose changed", err)
			}
			if err = source.Authorize(ctx, "episode_thumbnail", "decode", true); err == nil {
				t.Fatal("failed purpose check revived")
			}
		})
	}
}
