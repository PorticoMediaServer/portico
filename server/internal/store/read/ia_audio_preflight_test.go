package read

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Exercise the production entity/link point adapter with the real snapshot
// pool, grants, byte points and payload decoders. Keep Go-copy custody generous
// while independently varying the native variable-row forecast.
func TestIAAudioPreflightBeforePayload(t *testing.T) {
	for _, link := range []bool{false, true} {
		for _, bound := range []int64{1024, 16 << 10} {
			name := "entity"
			if link {
				name = "link"
			}
			if bound == 1024 {
				name += "/refuse"
			} else {
				name += "/decode"
			}
			t.Run(name, func(t *testing.T) {
				f, _ := newIAAudioFixture(t)
				payload := strings.Repeat("é", 1024) // 2048 actual UTF-8 octets, not rune count
				point, columns, args := "entity", []int{1, 2, 3, 14, 18}, []any{int64(2002), int64(1)}
				if link {
					f.exec(t, `UPDATE catalog_asset_links SET edition=? WHERE entity_id=2003 AND asset_id=1`, payload)
					point, columns, args = "link", []int{2, 7}, []any{int64(2003), int64(1), int64(1)}
				} else {
					f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=2002`, payload)
				}
				_, request, _ := liveRequests("ia-audio-preflight")
				request.Need[governor.MemoryBytes] = bound + (2 << 20)
				var row []any
				_, err := f.snapshots.ReadForecasted(t.Context(), request, time.Second, governor.SnapshotForecast{NativeRowBytes: bound, MaxQueries: 2}, func(scope *governor.ReadScope) error {
					s := &iaAudioSnapshots{scope: scope, extra: []iaAudioCalls{{}}, copyLimit: 2 << 20}
					var e error
					row, e = s.point(point, columns, args...)
					return e
				})
				if bound == 1024 {
					if !errors.Is(err, governor.ErrSnapshotBudget) || row != nil {
						t.Fatal("oversize payload escaped its native point preflight", row, err)
					}
					return
				}
				if err != nil {
					t.Fatal("legal measured payload refused", err)
				}
				column := 3 // entity title
				if link {
					column = 2 // link edition
				}
				if row[column] != payload {
					t.Fatal("legal payload was truncated")
				}
			})
		}
	}
}
