package read

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func iaAudioListeningAdd(t *testing.T, f *browseFixture, id, parent, kind int64, column string, key string) {
	t.Helper()
	public := make([]byte, 16)
	binary.LittleEndian.PutUint64(public, uint64(id))
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, id, public, kind)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,`+column+`,disc_number,track_number,part_number,admission_own_age) VALUES(?,?,1,?,'tie',?,'tie',?,'tie',0,?,?,?,1,1,1,12)`, id, public, kind, key, key, public, public, parent)
}

func TestIAAudioListeningOrderMatchesNativeOracle(t *testing.T) {
	for _, book := range []bool{false, true} {
		t.Run(map[bool]string{false: "album", true: "book"}[book], func(t *testing.T) {
			f, r := newIAAudioFixture(t)
			parent, kind, column, position := int64(2002), int64(7), "album_id", "track_number"
			if book {
				parent, kind, column, position = 2005, 9, "book_id", "part_number"
			}
			for n, key := range []string{"z", "A", "a", "a\x00ZZ", "a\x00b", "É", "é"} {
				iaAudioListeningAdd(t, f, int64(3000+n), parent, kind, column, key)
			}
			// Retired rows remain in the raw order and advance its cursor; they
			// must not win disclosure or manufacture a duplicate continuation.
			f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=3002`)
			f.exec(t, `UPDATE catalog_entities SET disc_number=NULL,`+position+`=NULL WHERE id=3006`)
			query := `SELECT id FROM catalog_entities WHERE ` + column + `=? AND kind=? AND retired=0 ORDER BY COALESCE(disc_number,1),COALESCE(` + position + `,0),sort_key COLLATE NOCASE,id`
			rows, err := f.conn.QueryContext(t.Context(), query, parent, kind)
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
			err = errors.Join(rows.Err(), rows.Close())
			if err != nil {
				t.Fatal(err)
			}
			in := IAAudioInput{View: f.input, Entity: parent}
			in.View.Kind, in.View.Limit = kind-1, 1
			var got []int64
			for n := 0; n < len(want)+2; n++ {
				page, _, err := iaAudioRead(t, f, r, in)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range page.Members {
					got = append(got, m.ID)
				}
				if page.Exhausted {
					if page.Next != nil {
						t.Fatal("terminal raw order retained a cursor")
					}
					break
				}
				if page.Next == nil || page.Next.orderVersion != iaAudioListeningOrderVersion || page.Next.limit != 1 {
					t.Fatal("missing four-key continuation", page)
				}
				in.After = page.Next
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("listening ties differ from native NOCASE oracle", got, want)
			}
		})
	}
}

func TestIAAudioListeningRejectsOldOrderAndLimitCursor(t *testing.T) {
	f, r := newIAAudioFixture(t)
	in := IAAudioInput{View: f.input, Entity: 2002}
	in.View.Kind, in.View.Limit = 6, 1
	page, _, err := iaAudioRead(t, f, r, in)
	if err != nil || page.Next == nil {
		t.Fatal(page, err)
	}
	for _, old := range []bool{false, true} {
		cursor := *page.Next
		in.After = &cursor
		if old {
			in.After.orderVersion = 0
		} else {
			in.View.Limit = 2
		}
		out, _, err := iaAudioRead(t, f, r, in)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(out, IAAudioPage{}) {
			t.Fatal("old/mismatched order cursor disclosed output", out, err)
		}
		in.View.Limit = 1
	}
}

func TestIAAudioListeningCompleteCapRefusesPrefix(t *testing.T) {
	for _, book := range []bool{false, true} {
		t.Run(map[bool]string{false: "album", true: "book"}[book], func(t *testing.T) {
			f, r := newIAAudioFixture(t)
			parent, kind, column, existing := int64(2002), int64(7), "album_id", 4
			if book {
				parent, kind, column, existing = 2005, 9, "book_id", 2
			}
			for n := 0; n < IAAudioListeningChildCap+1-existing; n++ {
				iaAudioListeningAdd(t, f, int64(4000+n), parent, kind, column, "cap")
			}
			in := IAAudioInput{View: f.input, Entity: parent}
			in.View.Kind, in.View.Limit = kind-1, 1
			page, _, err := iaAudioRead(t, f, r, in)
			if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, IAAudioPage{}) {
				t.Fatal("over-cap cohort published a sorted prefix", page, err)
			}
		})
	}
}

func TestIAAudioListeningIndependentSortCustody(t *testing.T) {
	for _, mode := range []string{"metadata", "keys", "legal"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newIAAudioFixture(t)
			f.exec(t, `UPDATE catalog_entities SET sort_key=? WHERE id=2003`, strings.Repeat("é", 1024))
			_, request, _ := liveRequests("ia-listening-sort")
			output := int64(8 << 20)
			if mode == "metadata" {
				output = 512
			} else if mode == "keys" {
				output = 8192
			}
			var result []iaAudioListeningRow
			s := &iaAudioSnapshots{extra: []iaAudioCalls{{}}, copyLimit: 2 << 20}
			_, err := f.snapshots.ReadForecasted(t.Context(), request, time.Second, governor.SnapshotForecast{NativeRowBytes: f.snapshots.NativeRowReservation(), MaxQueries: 16}, func(scope *governor.ReadScope) error {
				q := func(name string, args ...any) ([][]any, error) { return scope.Query(readStatementID(name), args...) }
				s.scope = scope
				var err error
				result, err = s.listeningRows(context.Background(), q, 2002, 1, 7, output)
				return err
			})
			if mode == "legal" {
				if err != nil || len(result) != 4 {
					t.Fatal("funded complete order refused", len(result), err)
				}
			} else if !errors.Is(err, governor.ErrSnapshotBudget) || result != nil || mode == "metadata" && s.extra[0].n != 0 {
				t.Fatal("sort allocation escaped its independent custody", mode, result, s.extra[0].n, err)
			}
		})
	}
}
