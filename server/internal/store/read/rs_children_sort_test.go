package read

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRSChildrenListeningOrderMatchesPublicOracle(t *testing.T) {
	f, r := rsChildrenFixture(t)
	for _, family := range []RSChildrenFamily{RSAlbumTracks, RSAudiobookFiles} {
		base := int64(3000) + 1000*int64(family)
		// Lowercase accented/script keys deliberately receive LOWER IDs than
		// their uppercase partners. Unicode folding would create false ID ties.
		for n, key := range []string{"Zulu", "alpha", "A\x00z", "a\x00a", "same", "SAME", "a\x00zz", "é\x00a", "retired-sentinel", "éclair", "Éclair", "ångström", "Ångström", "жук", "Жук", "αλφα", "Αλφα", "retired-sentinel", "東京", "大阪", "e\u0301clair"} {
			f.exec(t, `UPDATE catalog_entities SET disc_number=1,track_number=0,part_number=0,sort_key=? WHERE id=?`, key, base+int64(n+1))
		}
		in := rsChildrenInput(f, family)
		in.View.Limit = 7
		recipe, _ := rsChildrenRecipeFor(family)
		// The independent tiny writer oracle uses today's product order. Production
		// reads the complete capped set through its numeric index and sorts in Go.
		rows, err := f.conn.QueryContext(t.Context(), `SELECT id FROM catalog_entities WHERE `+recipe.predicate+` AND kind=? AND retired=0 ORDER BY COALESCE(disc_number,1),`+recipe.second+`,sort_key,id`, in.Parent, recipe.childKind)
		if err != nil {
			t.Fatal(err)
		}
		var want []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			want = append(want, id)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		var got []int64
		for n := 0; n < 30; n++ {
			p, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range p.Items {
				got = append(got, v.ID)
			}
			if p.Exhausted {
				break
			}
			if p.Next == nil || n == 29 {
				t.Fatal("lost complete sorted continuation")
			}
			in.After = p.Next
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("family%d got%v want%v", family, got, want)
		}
	}
}
func TestRSChildrenListeningCapAndProspectiveSortMemory(t *testing.T) {
	f, r := rsChildrenFixture(t)
	in := rsChildrenInput(f, RSAlbumTracks)
	f.exec(t, `UPDATE catalog_entities SET album_id=2001 WHERE id<2000 AND library_id=1`)
	p, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(p, RSChildrenPage{}) {
		t.Fatal("capped prefix published as ordered list", p, err)
	}
	f.exec(t, `UPDATE catalog_entities SET album_id=NULL WHERE id<2000 AND library_id=1`)
	f.exec(t, `UPDATE catalog_entities SET sort_key=? WHERE album_id=2001`, strings.Repeat("界", 6000))
	p, _, err = r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, governor.ErrSnapshotBudget) || !reflect.DeepEqual(p, RSChildrenPage{}) {
		t.Fatal("sort workspace exceeded output charge", p, err)
	}
}
func TestRSChildrenListeningRejectsOldOrderCursor(t *testing.T) {
	f, r := rsChildrenFixture(t)
	in := rsChildrenInput(f, RSAlbumTracks)
	page, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || page.Next == nil {
		t.Fatal(err)
	}
	in.After = page.Next
	in.After.orderVersion = 0
	page, _, err = r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, RSChildrenPage{}) {
		t.Fatal("three-key cursor reused for four-key order", page, err)
	}
}
