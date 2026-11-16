package metadata_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// thumb is the hash of the episode picture a title shows, or "".
func thumb(detail db.ItemDetail, role db.ImageRole) string {
	for _, image := range detail.Images {
		if image.Role == role {
			return image.FileHash
		}
	}
	return ""
}

// Episodes show the provider's still and seasons the provider's poster (definition area 4,
// rules 39, 40 and 46); an episode the provider has no still for gets a picture made from its
// video, once, and a still that comes later is shown instead of the made one.
func TestEpisodesAndSeasonsGetTheProvidersPicturesThenMadeOnes(t *testing.T) {
	f := newFixture(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "Breaking Bad/Season 01/Breaking Bad - S01E02.mkv",
		"Breaking Bad/Season 09/Breaking Bad - S09E01.mkv")
	f.s.Pictures = &pictureStore{put: map[string][]byte{}}
	// A picture made before the show was matched, as for a show matched by hand later.
	before, err := f.d.EpisodesWithoutPicture(ctx, f.lib.PublicID, 0)
	if err != nil || len(before) != 3 {
		t.Fatalf("episodes without a picture before the lookup: %d, %v; want 3", len(before), err)
	}
	pilot := before[0]
	if err := f.d.AddMadePicture(ctx, pilot.ItemID, pilot.FileID, "made-pilot"); err != nil {
		t.Fatal(err)
	}
	f.match()
	if _, err := f.s.FetchPictures(ctx, f.lib.ID, 100); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Pilot", "Cat's in the Bag..."} {
		if got := thumb(f.title(db.KindEpisode, title), db.ImageThumb); got == "" || got == "made-pilot" {
			t.Errorf("%s shows picture %q; want the provider's still", title, got)
		}
	}
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Kinds: []db.ItemKind{db.KindSeason}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	posters := map[string]bool{}
	for _, season := range page.Items {
		posters[season.Title] = thumb(db.ItemDetail{Item: season}, db.ImagePoster) != ""
	}
	if !posters["Season 1"] || posters["Season 9"] {
		t.Errorf("season posters: %v; want Season 1's from the provider, none for Season 9, which it does not know", posters)
	}
	// Only the episode the provider has no still for is left to make a picture for.
	left, err := f.d.EpisodesWithoutPicture(ctx, f.lib.PublicID, 0)
	if err != nil || len(left) != 1 || left[0].Season != 9 {
		t.Fatalf("episodes left without a picture: %+v, %v; want S09E01 alone", left, err)
	}
	if err := f.d.AddMadePicture(ctx, left[0].ItemID, left[0].FileID, "made-s09e01"); err != nil {
		t.Fatal(err)
	}
	if got := thumb(f.title(db.KindEpisode, "Episode 1"), db.ImageThumb); got != "made-s09e01" {
		t.Errorf("S09E01 shows %q; want the made picture", got)
	}
	// A file no picture could be made from is not tried again.
	f2 := newFixture(t, db.LibraryShows, "Unknown Show/Season 01/Unknown Show - S01E01.mkv")
	failed, err := f2.d.EpisodesWithoutPicture(ctx, f2.lib.PublicID, 0)
	if err != nil || len(failed) != 1 {
		t.Fatal(failed, err)
	}
	if err := f2.d.AddMadePicture(ctx, failed[0].ItemID, failed[0].FileID, ""); err != nil {
		t.Fatal(err)
	}
	if again, err := f2.d.EpisodesWithoutPicture(ctx, f2.lib.PublicID, 0); err != nil || len(again) != 0 {
		t.Errorf("a file tried and failed is listed again: %+v, %v", again, err)
	}
}
