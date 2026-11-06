package read

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestForegroundSQLCreditClassCapsAndContinuations(t *testing.T) {
	for _, department := range []int64{2, 4} {
		t.Run(fmt.Sprint(department), func(t *testing.T) {
			f, r := newForegroundFixture(t)
			f.exec(t, `UPDATE catalog_credits SET department_id=?`, department)
			var castCount, crewCount int
			// Inspect the actual registered SQL results before Detail can reduce them.
			_, e := f.snapshots.Read(t.Context(), foregroundRequest(), time.Second, func(scope *governor.ReadScope) error {
				cast, err := scope.Query(readStatementID("foreground.credits.cast"), int64(1), int64(1), int64(-1), int64(2))
				if err != nil {
					return err
				}
				crew, err := scope.Query(readStatementID("foreground.credits.crew"), int64(1), int64(1), int64(-1), int64(2))
				if err != nil {
					return err
				}
				castCount, crewCount = len(cast), len(crew)
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			if department == 2 && (castCount != 100 || crewCount != 0) || department == 4 && (castCount != 0 || crewCount != 50) {
				t.Fatalf("SQL class bounds cast%d crew%d", castCount, crewCount)
			}
			in := TitleInput{View: f.input, Item: 1}
			var castOrd, crewOrd []int64
			for window := 0; window < 5; window++ {
				d, result, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
				if err != nil {
					t.Fatal(err)
				}
				if len(d.Cast) > 100 || len(d.Crew) > 50 || result.LookupStatements != 13 || result.PublicationStatements != 6 {
					t.Fatal("class/window bounds", result.LookupStatements, result.PublicationStatements)
				}
				for _, c := range d.Cast {
					castOrd = append(castOrd, c.Ord)
				}
				for _, c := range d.Crew {
					crewOrd = append(crewOrd, c.Ord)
				}
				if d.ChildrenExhausted && d.CreditsExhausted {
					break
				}
				if d.Next == nil {
					t.Fatal("missing class continuation")
				}
				in.After = d.Next
			}
			want := make([]int64, 150)
			for n := range want {
				want[n] = int64(n)
			}
			if department == 2 && (!reflect.DeepEqual(castOrd, want) || len(crewOrd) != 0) || department == 4 && (!reflect.DeepEqual(crewOrd, want) || len(castOrd) != 0) {
				t.Fatal("class continuation lost or duplicated credits")
			}
		})
	}
}

// Every case changes only the cited library root/origin fact. Mutation occurs
// after each attempt's last business window, never via a middle-window fence.
func TestReadPublicationRejectsLibraryRootAndOriginDrift(t *testing.T) {
	for _, recipe := range []string{"detail", "playback", "continue", "browse"} {
		for _, fact := range []string{"root", "bridge-root", "origin-revision", "origin-incarnation"} {
			t.Run(recipe+"/"+fact, func(t *testing.T) {
				f, r := newForegroundFixture(t)
				var origin []byte
				if e := f.conn.QueryRowContext(t.Context(), `SELECT playback_origin_incarnation FROM libraries WHERE id='movies'`).Scan(&origin); e != nil {
					t.Fatal(e)
				}
				windows := 2
				if recipe == "detail" {
					windows = 4
				} else if recipe == "playback" {
					windows = 3
				}
				observed := &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
					if phase%windows != windows-1 {
						return
					}
					switch fact {
					case "root":
						f.exec(t, `UPDATE libraries SET root=? WHERE id='movies'`, fmt.Sprintf("/replacement-%d", phase))
					case "bridge-root":
						f.exec(t, `UPDATE catalog_libraries SET root=? WHERE id=1`, fmt.Sprintf("/bridge-%d", phase))
					case "origin-revision":
						f.exec(t, `UPDATE libraries SET playback_origin_revision=playback_origin_revision+1 WHERE id='movies'`)
					case "origin-incarnation":
						origin = bytes.Clone(origin)
						origin[15] ^= byte(phase)
						f.exec(t, `UPDATE libraries SET playback_origin_incarnation=? WHERE id='movies'`, origin)
					}
				}}
				r.snapshots = observed
				f.reader.snapshots = observed
				var value, zero any
				var result BrowseReadResult
				var e error
				switch recipe {
				case "detail":
					var d TitleDetail
					d, result, e = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
					value, zero = d, TitleDetail{}
				case "playback":
					var d PlaybackFile
					d, result, e = r.playback(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
					value, zero = d, PlaybackFile{}
				case "continue":
					var d ContinuePage
					d, result, e = r.continueWatching(t.Context(), foregroundRequest(), time.Second, f.output, ContinueInput{View: f.input})
					value, zero = d, ContinuePage{}
				case "browse":
					var d BrowsePage
					d, result, e = f.window(t, t.Context(), f.input)
					value, zero = d, BrowsePage{}
				}
				if !errors.Is(e, ErrStale) || !reflect.DeepEqual(value, zero) {
					t.Fatalf("%s drift released payload: %v", fact, e)
				}
				if recipe == "detail" || recipe == "playback" {
					if result.Attempts != 2 || result.Refusal == nil || result.Refusal.Code != governor.Conflict {
						t.Fatalf("missing typed retry refusal: %+v", result)
					}
				}
			})
		}
	}
}

func TestForegroundLibraryRootABARetriesBeforePublication(t *testing.T) {
	f, r := newForegroundFixture(t)
	observed := &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
		if phase == 1 {
			f.exec(t, `UPDATE libraries SET root='/replacement',playback_origin_revision=playback_origin_revision+1 WHERE id='movies'`)
		}
		if phase == 2 {
			f.exec(t, `UPDATE libraries SET root='/media',playback_origin_revision=playback_origin_revision+1 WHERE id='movies'`)
		}
	}}
	r.snapshots = observed
	d, result, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
	if e != nil || d.File == nil || result.Attempts != 2 {
		t.Fatalf("root ABA was not discarded and retried: attempts%d err%v", result.Attempts, e)
	}
}

func TestItemValuePreservesBrowseIdentityError(t *testing.T) {
	for _, identity := range []any{nil, []byte{}, make([]byte, 15), "not a blob"} {
		item, e := itemValue([]any{int64(1), "key", identity, "Title", int64(0), int64(1), nil})
		if e == nil || e.Error() != "invalid public identity" || errors.Is(e, ErrUnqualified) || item != (BrowseItem{}) {
			t.Fatalf("browse identity error changed: %+v %v", item, e)
		}
	}
}

func TestForegroundCreditsWithoutActingLabel(t *testing.T) {
	f, r := newForegroundFixture(t)
	f.exec(t, `UPDATE catalog_credits SET department_id=4`)
	f.exec(t, `DELETE FROM catalog_credit_labels WHERE id=2`)
	d, result, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1})
	if e != nil || len(d.Cast) != 0 || len(d.Crew) != 50 || d.CreditsExhausted || result.LookupStatements != 12 {
		t.Fatalf("missing Acting label lost crew: cast%d crew%d SQL%d %v", len(d.Cast), len(d.Crew), result.LookupStatements, e)
	}
}
