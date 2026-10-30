package metadata

import (
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"testing"
)

// A candidate preview is already a thumbnail (inside 400x400,
// the same bounded object a selection's size=thumbnail serves), so a grid of
// candidates never downloads full images; and every candidate carries a votes
// field, null when the provider publishes no count (as for an upload).
func TestCandidatePreviewIsAThumbnailAndPublishesVotes(t *testing.T) {
	s, db := editorFixture(t)
	ctx := context.Background()
	target := artworkFixtureTarget(t, db, 1, "movies")
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if state, err = s.UploadArtwork(ctx, target, "poster", "", state.Revision, editorJPEG(t, 1200, 1800), editorActor(), allowAll); err != nil {
		t.Fatal(err)
	}
	var candidate ArtworkCandidate
	for _, c := range state.Artwork.Candidates {
		if c.Provider == "upload" {
			candidate = c
		}
	}
	if candidate.ID == "" || candidate.PreviewURL == "" {
		t.Fatalf("no upload candidate with a preview: %+v", state.Artwork.Candidates)
	}
	if candidate.Votes != nil {
		t.Fatalf("an upload has no provider vote count: %d", *candidate.Votes)
	}
	f, _, err := s.PreviewArtwork(ctx, target, candidate.ID, ArtworkSmall)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if config.Width > artworkSmallEdge || config.Height > artworkSmallEdge {
		t.Fatalf("candidate preview is %dx%d, not a thumbnail", config.Width, config.Height)
	}
}

// A provider's vote count (TMDB vote_count) round-trips to the candidate list;
// a provider without one stays null.
func TestCandidateVotesRoundTrip(t *testing.T) {
	s, db := editorFixture(t)
	ctx := context.Background()
	target := artworkFixtureTarget(t, db, 1, "movies")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fence, err := artworkFence(ctx, tx, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = insertArtworkCandidateVotes(ctx, tx, target, "poster", "", "tmdb", "/a.jpg", "https://image.tmdb.org/t/p/original/a.jpg", "en", "TMDB", fence, "2026-09-23T00:00:00Z", 7.5, 37); err != nil {
		t.Fatal(err)
	}
	if _, err = insertArtworkCandidate(ctx, tx, target, "poster", "", "tvdb", "9", "https://artworks.thetvdb.com/9.jpg", "en", "TVDB", fence, "2026-09-23T00:00:00Z", 5); err != nil {
		t.Fatal(err)
	}
	state, err := readArtworkState(ctx, tx, target)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]*int64{}
	for _, c := range state.Candidates {
		seen[c.Provider] = c.Votes
	}
	if seen["tmdb"] == nil || *seen["tmdb"] != 37 || seen["tvdb"] != nil {
		t.Fatalf("votes: tmdb %v tvdb %v", seen["tmdb"], seen["tvdb"])
	}
	_ = s
}
