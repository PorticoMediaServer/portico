package read

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Both snapshot entries must retain mutation boundaries: promoted ReadForecasted
// would otherwise bypass an override of Read.
type rsChildrenBoundary struct {
	*governor.Snapshots
	mutate func(int)
	calls  int
}

func (s *rsChildrenBoundary) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	out, err := s.Snapshots.Read(ctx, r, b, fn)
	s.calls++
	s.mutate(s.calls)
	return out, err
}
func (s *rsChildrenBoundary) ReadForecasted(ctx context.Context, r governor.Request, b time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	out, err := s.Snapshots.ReadForecasted(ctx, r, b, f, fn)
	s.calls++
	s.mutate(s.calls)
	return out, err
}
func TestRSChildrenListeningSortKeyTieBreak(t *testing.T) {
	f, r := rsChildrenFixture(t)
	for _, family := range []RSChildrenFamily{RSAlbumTracks, RSAudiobookFiles} {
		if family == RSAlbumTracks {
			f.exec(t, `DELETE FROM catalog_entities WHERE id>4008 AND id<5000`)
		} else {
			f.exec(t, `DELETE FROM catalog_entities WHERE id>5008 AND id<6000`)
		}
		base := int64(3000) + 1000*int64(family)
		for n, key := range []string{"Zulu", "alpha", "A\x00z", "a\x00a", "same", "SAME", "a\x00zz", "é\x00a"} {
			f.exec(t, `UPDATE catalog_entities SET disc_number=1,track_number=0,part_number=0,sort_key=? WHERE id=?`, key, base+int64(n+1))
		}
		in := rsChildrenInput(f, family)
		in.View.Limit = 1
		recipe, _ := rsChildrenRecipeFor(family)
		// Independent product-order SQL oracle is confined to the tiny writer
		// fixture. Production keeps its numeric SEARCH index and sorts in Go.
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
		for n := 0; n < 100; n++ {
			p, cost, err := rsChildrenRead(t, r, f, in)
			if err != nil {
				t.Fatal(err)
			}
			if len(cost.LookupWindows) != 6 || cost.LookupWindows[2].NativeReservedBytes < 1 || cost.LookupWindows[2].NativeReservedBytes > 5 {
				t.Fatal("sort keys reserve actual largest UTF8/NUL point", cost)
			}
			for _, c := range p.Items {
				got = append(got, c.ID)
			}
			if p.Exhausted {
				break
			}
			in.After = p.Next
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("family%d got%v want%v", family, got, want)
		}
	}
}
func TestRSChildrenListeningCapAndByteCharge(t *testing.T) {
	f, r := rsChildrenFixture(t)
	// A complete membership beyond cap must refuse, never return an ordered prefix.
	f.exec(t, `UPDATE catalog_entities SET album_id=2001 WHERE id<2000 AND library_id=1`)
	p, _, err := rsChildrenRead(t, r, f, rsChildrenInput(f, RSAlbumTracks))
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(p, RSChildrenPage{}) {
		t.Fatal("overflow released prefix", p, err)
	}
	f.exec(t, `UPDATE catalog_entities SET album_id=NULL WHERE id<2000 AND library_id=1`)
	f.exec(t, `UPDATE catalog_entities SET sort_key=? WHERE album_id=2001`, strings.Repeat("界", 6000))
	p, _, err = rsChildrenRead(t, r, f, rsChildrenInput(f, RSAlbumTracks))
	if !errors.Is(err, governor.ErrSnapshotBudget) || !reflect.DeepEqual(p, RSChildrenPage{}) {
		t.Fatal("wide-key workspace exceeded grant", p, err)
	}
}
