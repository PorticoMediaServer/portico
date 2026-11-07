package read

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// The real snapshot pool, grants, byte points and payload decoders remain in
// use. Only this trusted fixture's native variable-row forecast is narrowed.
// A large Go-copy/output budget must not let direct payload Query bypass it.
type iaAudioNativeBound struct {
	*governor.Snapshots
	bound int64
}

func (s *iaAudioNativeBound) NativeRowReservation() int64 { return s.bound }

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
				f, r := newIAAudioFixture(t)
				payload := strings.Repeat("é", 1024) // 2048 actual UTF-8 octets, not rune count
				in := IAAudioInput{View: f.input, Entity: 2002}
				in.View.Kind = 6
				if link {
					in.Entity, in.View.Kind = 2003, 7
					f.exec(t, `UPDATE catalog_asset_links SET edition=? WHERE entity_id=2003 AND asset_id=1`, payload)
				} else {
					f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=2002`, payload)
				}
				r.snapshots = &iaAudioNativeBound{Snapshots: f.snapshots, bound: bound}
				page, _, err := iaAudioRead(t, f, r, in)
				if bound == 1024 {
					if !errors.Is(err, governor.ErrSnapshotBudget) || len(page.Members) != 0 || len(page.Links) != 0 || page.Entity.ID != 0 {
						t.Fatal("oversize payload escaped its native point preflight", page, err)
					}
					return
				}
				if err != nil {
					t.Fatal("legal measured payload refused", err)
				}
				if !link && page.Entity.Title != payload {
					t.Fatal("legal root payload was truncated")
				}
				if link {
					found := false
					for _, v := range page.Links {
						if v.Asset == 1 && v.Edition == payload {
							found = true
						}
					}
					if !found {
						t.Fatal("legal complete link payload was truncated")
					}
				}
			})
		}
	}
}
