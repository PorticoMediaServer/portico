package metadata

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type thumbnailEpisodeProvider struct {
	*episodeFixtureProvider
	still string
}

func (p *thumbnailEpisodeProvider) ScreenEpisode(ctx context.Context, show string, season, episode int, order, language string) (metadataprovider.ScreenRecord, error) {
	r, err := p.episodeFixtureProvider.ScreenEpisode(ctx, show, season, episode, order, language)
	r.StillPath = p.still
	return r, err
}
func readThumbnailFacts(t *testing.T, s *Service, library, episode string) EpisodeThumbnailState {
	t.Helper()
	g, err := dbwork.BeginSnapshot(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Rollback()
	out, err := s.episodeThumbnailArtworkStateTx(context.Background(), g.Tx(), library, episode)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestEpisodeThumbnailStateUsesAcceptedProviderAbsenceAndPresence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, still := range []string{"", "/episode.jpg", "https://untrusted.invalid/image.jpg"} {
		t.Run(still, func(t *testing.T) {
			s, db, p, _, _, settle := episodeFreshnessFixture(t)
			s.screenProviders["tmdb"] = &thumbnailEpisodeProvider{p, still}
			settle()
			var episode string
			if err := db.QueryRow(`SELECT pid(e.public_id) FROM catalog_entities e JOIN catalog_episodes ep ON ep.entity_id=e.id WHERE ep.number=1`).Scan(&episode); err != nil {
				t.Fatal(err)
			}
			out := readThumbnailFacts(t, s, "tv", episode)
			if len(out.ArtworkRevision) != 64 || !out.GenerationNeeded {
				t.Fatalf("missing actual artwork revision/need: %+v", out)
			}
			wantResolved := still == "" || still == "/episode.jpg"
			if out.ProviderResolved != wantResolved || out.ProviderStill != (still == "/episode.jpg") {
				t.Fatalf("incorrect accepted-provider facts: %+v", out)
			}
		})
	}
}
func TestEpisodeThumbnailStateNeverInfersAbsenceFromPendingRetryOrMissingPublication(t *testing.T) {
	s, db, _, _, _, settle := episodeFreshnessFixture(t)
	settle()
	var episode string
	var entity int64
	if err := db.QueryRow(`SELECT pid(e.public_id),e.id FROM catalog_entities e JOIN catalog_episodes ep ON ep.entity_id=e.id WHERE ep.number=1`).Scan(&episode, &entity); err != nil {
		t.Fatal(err)
	}
	original := readThumbnailFacts(t, s, "tv", episode)
	if !original.ProviderResolved || original.ProviderStill {
		t.Fatal("fixture lacks accepted absence", original)
	}
	for _, change := range []struct {
		status, problem, publication string
		refresh                      bool
	}{
		{"pending", "", "", false}, {"retry", "provider_unavailable", "", false}, {"matched", "", "", true}, {"matched", "", "missing-publication", false},
	} {
		var accepted string
		if err := db.QueryRow(`SELECT accepted_publication FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, entity).Scan(&accepted); err != nil {
			t.Fatal(err)
		}
		publication := accepted
		if change.publication != "" {
			publication = change.publication
		}
		if _, err := db.Exec(`UPDATE screen_metadata_work SET status=?,error=?,force_refresh=?,accepted_publication=? WHERE target_kind='item' AND target_id=?`, change.status, change.problem, change.refresh, publication, entity); err != nil {
			t.Fatal(err)
		}
		out := readThumbnailFacts(t, s, "tv", episode)
		if out.ProviderResolved || out.ArtworkRevision == original.ArtworkRevision {
			t.Fatalf("unknown provider became absence: %+v", out)
		}
		if _, err := db.Exec(`UPDATE screen_metadata_work SET status='matched',error='',force_refresh=0,accepted_publication=? WHERE target_kind='item' AND target_id=?`, accepted, entity); err != nil {
			t.Fatal(err)
		}
	}
}
func TestEpisodeThumbnailStateKeepsSelectedArtSeparateFromProviderPresence(t *testing.T) {
	s, c, target, _ := episodePublicationFixture(t)
	c.Exec(`UPDATE library_metadata_agents SET agent='local' WHERE library_id='episodes'`)
	before := readThumbnailFacts(t, s, "episodes", target.ID)
	if !before.ProviderResolved || before.ProviderStill || !before.GenerationNeeded {
		t.Fatalf("local absence wrong: %+v", before)
	}
	picture := preparePublication(t, s, func(context.Context, *sql.Tx) error { return nil })
	_, err := s.publishEpisodeThumbnail(context.Background(), capturePublication(t, s, target), picture, &thumbnailPublicationAuthority{}, true, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	after := readThumbnailFacts(t, s, "episodes", target.ID)
	if !after.ProviderResolved || after.ProviderStill || after.GenerationNeeded || after.ArtworkRevision == before.ArtworkRevision {
		t.Fatalf("selected generated art misreported: %+v", after)
	}
}
