package db_test

import (
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Search's genres, for every way a profile can be limited: each genre listed has films or shows
// the profile may see, its count is the size of that genre's list for the profile, its cover is
// a backdrop of one of those titles and stays the same between reads, and no genre is left out.
func TestGenresShowOnlyWhatTheProfileMaySee(t *testing.T) {
	l := newLibrary(t, 1500)
	// The seed gives titles posters only; here every poster is a backdrop.
	if err := l.d.ExecForTest(ctx, `UPDATE images SET role = ? WHERE role = ?`, db.ImageBackdrop, db.ImagePoster); err != nil {
		t.Fatal(err)
	}
	video := []db.ItemKind{db.KindMovie, db.KindShow}
	for n, v := range l.viewers {
		plain, err := l.d.Genres(ctx, v, "", false)
		if err != nil {
			t.Fatal(err)
		}
		counted, err := l.d.Genres(ctx, v, "", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(plain) == 0 || len(plain) != len(counted) {
			t.Fatalf("viewer %d: %d genres, %d with counts", n, len(plain), len(counted))
		}
		listed := map[string]bool{}
		for i, g := range counted {
			listed[g.PublicID] = true
			if p := plain[i]; p.PublicID != g.PublicID || p.CoverHash != g.CoverHash || p.Count != 0 {
				t.Fatalf("viewer %d: the plain read differs: %+v and %+v", n, p, g)
			}
			titles := everything(t, l.d, v, db.ItemQuery{Tag: g.PublicID, Kinds: video, Limit: 200})
			if len(titles) == 0 || g.Count != len(titles) {
				t.Fatalf("viewer %d, genre %s: count %d, the list holds %d", n, g.Name, g.Count, len(titles))
			}
			var backdrops []string
			for _, it := range titles {
				for _, im := range it.Images {
					if im.Role == db.ImageBackdrop {
						backdrops = append(backdrops, im.FileHash)
					}
				}
			}
			if g.CoverHash == "" || !slices.Contains(backdrops, g.CoverHash) {
				t.Fatalf("viewer %d, genre %s: cover %q is not a backdrop of a title it may see", n, g.Name, g.CoverHash)
			}
		}
		// No genre with a title the profile may see is left out.
		all, err := l.d.Genres(ctx, l.all, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range all {
			if !listed[g.PublicID] && len(everything(t, l.d, v, db.ItemQuery{Tag: g.PublicID, Kinds: video, Limit: 1})) > 0 {
				t.Fatalf("viewer %d: genre %s is left out", n, g.Name)
			}
		}
	}
}
