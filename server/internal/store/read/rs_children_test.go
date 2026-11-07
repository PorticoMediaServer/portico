package read

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func TestRSChildrenOrderedFamilies(t *testing.T) {
	f, r := rsChildrenFixture(t)
	for family := RSAlbumTracks; family <= RSArtistAlbums; family++ {
		t.Run(fmt.Sprint(family), func(t *testing.T) {
			in := rsChildrenInput(f, family)
			in.View.Limit = 7
			var got []int64
			var saved *RSChildrenCursor
			for n := 0; n < 30; n++ {
				page, result, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) > 7 || page.RawRows > MaxRaw || page.RawPositions > page.RawRows || len(result.LookupWindows) != 2 || result.LookupStatements > 10 || result.PublicationStatements != 7 {
					t.Fatalf("bounds page=%+v result=%+v", page, result)
				}
				if page.Pending == page.Exhausted || (page.Next == nil) != page.Exhausted {
					t.Fatal("ambiguous pending/exhaustion")
				}
				for _, item := range page.Items {
					got = append(got, item.ID)
					if item.ID == 4005 && (item.Disc.Present || !item.Track.Present || item.Track.Value != 0) {
						t.Fatal("NULL disc versus zero track lost")
					}
					if item.ID == 4008 && (!item.Disc.Present || item.Disc.Value != 1 || item.Track.Present) {
						t.Fatal("disc one versus NULL track lost")
					}
				}
				if page.Exhausted {
					break
				}
				if n == 29 {
					t.Fatal("continuation did not finish")
				}
				saved = page.Next
				in.After = page.Next
			}
			// Independent scalar oracle from the supplied facts. The deliberately
			// identical/reversed display titles play no part in accepted order.
			var want []int64
			for n := int64(1); n <= 90; n++ {
				if n%9 != 0 {
					want = append(want, 3000+1000*int64(family)+n)
				}
			}
			if family == RSShowSeasons {
				want = append(want, 2005)
			}
			if family == RSArtistAlbums {
				want = append(want, 2001)
			}
			key := func(id int64) (int64, int64) {
				if id == 2005 {
					return -10, 0
				}
				n := id % 1000
				switch family {
				case RSAlbumTracks, RSAudiobookFiles:
					disc := []int64{1, -1, 0, 1, 2}[n%5]
					number := []int64{0, 0, 1, 3}[n%4]
					return disc, number
				case RSShowSeasons:
					return (n * 37) % 91, 0
				case RSShowEpisodes, RSSeasonEpisodes:
					return n, 0
				default:
					return 0, 0
				}
			}
			slices.SortFunc(want, func(a, b int64) int {
				x, y := key(a)
				u, v := key(b)
				for _, pair := range [][2]int64{{x, u}, {y, v}, {a, b}} {
					if pair[0] < pair[1] {
						return -1
					}
					if pair[0] > pair[1] {
						return 1
					}
				}
				return 0
			})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ordered IDs\ngot %v\nwant%v", got, want)
			}
			if saved == nil {
				t.Fatal("fixture did not exercise continuation")
			}
			in.After = saved
			in.View.Limit++
			page, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, RSChildrenPage{}) {
				t.Fatal("query/limit cursor mismatch", err)
			}
		})
	}
}

func TestRSChildrenSparseRawAndNullContinuation(t *testing.T) {
	f, r := rsChildrenFixture(t)
	for _, family := range []RSChildrenFamily{RSAlbumTracks, RSAudiobookFiles, RSShowSeasons, RSShowEpisodes, RSSeasonEpisodes, RSArtistAlbums} {
		in := rsChildrenInput(f, family)
		recipe, _ := rsChildrenRecipeFor(family)
		f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id>=4000 AND id<10000`)
		page, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil || page.Exhausted || !page.Pending || page.RawPositions != MaxRaw || page.Next == nil {
			t.Fatalf("%s sparse: %+v %v", recipe.name, page, err)
		}
		// Sources 2005 and 2001 are also children of the show and artist. Other
		// families emit no output, and still advance past all hidden positions.
		if family != RSShowSeasons && family != RSArtistAlbums && len(page.Items) != 0 {
			t.Fatal("hidden output")
		}
		in.After = page.Next
		last, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil || !last.Exhausted || last.Next != nil {
			t.Fatal("sparse tail lost", err, last)
		}
	}
	// Force a real NULL-key anchor in the private raw kernel (the public
	// continuation normally consumes it while looking for eligible output).
	in := rsChildrenInput(f, RSShowEpisodes)
	in.After = &RSChildrenCursor{first: RSChildNumber{}, id: 11001}
	recipe, _ := rsChildrenRecipeFor(in.Family)
	var names []string
	_, err := rsReadChildren(t.Context(), func(name string, args ...any) ([][]any, error) { names = append(names, name); return nil, nil }, browseFence{}, in, recipe, nil)
	if err != nil || !reflect.DeepEqual(names, []string{rsChildrenName("show.tail_null"), rsChildrenName("show.next_null")}) {
		t.Fatal("NULL equality tail then non-NULL next", names, err)
	}
}

func TestRSChildrenNullTailUsesRealSeek(t *testing.T) {
	f, r := rsChildrenFixture(t)
	// More than a raw window of wrong-kind NULLs, followed by a real negative
	// episode number. Neither NULL->zero nor a positive initial sentinel works.
	f.exec(t, `UPDATE catalog_entities SET show_id=2004,episode_number=NULL WHERE id<=70`)
	f.exec(t, `UPDATE catalog_entities SET episode_number=-5 WHERE id=7003`)
	in := rsChildrenInput(f, RSShowEpisodes)
	page, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(page.Items) != 0 || page.RawRows != 64 || page.RawPositions != 64 || page.Next == nil || page.Next.first.Present || page.Exhausted {
		t.Fatalf("NULL raw window %+v %v", page, err)
	}
	in.After = page.Next
	page, _, err = r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(page.Items) != 40 || page.Items[0].ID != 7003 || page.Items[0].Episode.Value != -5 || page.RawRows > 64 {
		t.Fatalf("NULL->negative indexed stream %+v %v", page, err)
	}
}

func TestRSChildrenHierarchyAndQueryBinding(t *testing.T) {
	f, r := rsChildrenFixture(t)
	// Physical same-library edges alone do not prove that an episode's show
	// agrees with its selected season's show.
	f.exec(t, `UPDATE catalog_entities SET show_id=2004 WHERE id=8001`)
	in := rsChildrenInput(f, RSSeasonEpisodes)
	page, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(page.Items) == 0 || page.Items[0].ID != 8002 {
		t.Fatal("cross-show season member admitted", page, err)
	}
	in = rsChildrenInput(f, RSAlbumTracks)
	page, _, err = r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || page.Next == nil {
		t.Fatal(err)
	}
	other := rsChildrenInput(f, RSAudiobookFiles)
	other.After = page.Next
	page, _, err = r.window(t.Context(), foregroundRequest(), time.Second, f.output, other)
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, RSChildrenPage{}) {
		t.Fatal("another parent's continuation", err)
	}
}

func TestRSChildrenSourceAndAuthorityPublication(t *testing.T) {
	for _, mutation := range []struct {
		name, sql string
		want      error
	}{
		{"revoke", `UPDATE authorization_session_families SET revoked=1`, ErrDenied},
		{"retire-root", `UPDATE catalog_entities SET retired=1 WHERE id=2001`, ErrDenied},
		{"root-source", `UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=2001`, nil},
		{"membership", `UPDATE catalog_entities SET music_membership_revision=COALESCE(music_membership_revision,0)+1 WHERE id=2001`, nil},
		{"library-data", `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`, nil},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			f, r := rsChildrenFixture(t)
			in := rsChildrenInput(f, RSAlbumTracks)
			r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == 2 {
					f.exec(t, mutation.sql)
				}
			}}
			page, result, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if mutation.want != nil {
				if !errors.Is(err, mutation.want) || !reflect.DeepEqual(page, RSChildrenPage{}) {
					t.Fatal("late denial released page", err)
				}
			} else if err != nil || result.Attempts != 2 || len(page.Items) != 40 {
				t.Fatal("whole retry", result.Attempts, len(page.Items), err)
			}
		})
	}
}

func TestRSChildrenCursorFencesAndBoundedConflict(t *testing.T) {
	f, r := rsChildrenFixture(t)
	in := rsChildrenInput(f, RSAlbumTracks)
	page, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil {
		t.Fatal(err)
	}
	in.After = page.Next
	f.exec(t, `UPDATE catalog_entities SET music_membership_revision=2 WHERE id=2001`)
	page, result, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrStale) || !errors.Is(err, governor.ErrBusy) || result.Attempts != 2 || !reflect.DeepEqual(page, RSChildrenPage{}) {
		t.Fatal("stale source cursor", result, err)
	}
	in.After = nil
	r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(int) { f.exec(t, `UPDATE catalog_entities SET revision=revision+1 WHERE id=2001`) }}
	page, result, err = r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrStale) || result.Attempts != 2 || result.Refusal == nil || result.Refusal.Code != governor.Conflict || !reflect.DeepEqual(page, RSChildrenPage{}) {
		t.Fatal("conflict retry bound", result, err)
	}
	for _, window := range append(result.LookupWindows, result.PublicationWindows...) {
		if window.Deadline.After(result.LookupWindows[0].Deadline) {
			t.Fatal("retry extended deadline")
		}
	}
}

func TestRSChildrenDefaultRestrictionAndCancellation(t *testing.T) {
	f, r := rsChildrenFixture(t)
	in := rsChildrenInput(f, RSAlbumTracks)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	page, _, err := r.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, RSChildrenPage{}) {
		t.Fatal("restricted container enabled", err)
	}
	f.exec(t, `DELETE FROM profile_restrictions WHERE profile_id='p'`)
	ctx, cancel := context.WithCancel(t.Context())
	r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
		if phase == 2 {
			cancel()
		}
	}}
	page, _, err = r.window(ctx, foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(page, RSChildrenPage{}) {
		t.Fatal("canceled output", err)
	}
}

func TestRSChildrenLiveCustody(t *testing.T) {
	f := composeReadFixture(t, rsChildrenSource(t), append(browseDefinitions(), RSChildrenDefinitions()...), readfixture.LiveConfig(time.Now), false, rsChildrenFixtureHash(t))
	r, err := NewRSChildrenReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	in := rsChildrenInput(f, RSAlbumTracks)
	q, request, output := liveRequests("rs-children")
	page, _, err := r.Window(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || !reflect.DeepEqual(page, RSChildrenPage{}) {
		t.Fatal("public read without LIVE", err)
	}
	consumed := false
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, err := r.Window(ctx, request, time.Second, out, in)
		if err != nil {
			return err
		}
		if len(page.Items) != 40 {
			t.Fatal("LIVE child output")
		}
		assertLiveUsage(t, f, 1, 0)
		consumed = true
		return nil
	})
	if err != nil || !consumed {
		t.Fatal("LIVE consume", err)
	}
	assertLiveUsage(t, f, 0, 0)
}
