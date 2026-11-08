package read

import (
	"testing"
	"testing/synctest"
)

// Exact inclusive boundary of the raw catalog_entities membership budget. The
// raw count includes every source row; composition refuses the 33rd position
// rather than filtering first.
func TestRBHomeFixedMembershipBounds(t *testing.T) {
	t.Run("entities-32", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f, r := homeFixedFixture(t, func(f *browseFixture) {
				for id := int64(2000); id < 2032; id++ {
					homeFixedEntitySeed(t, f, id, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
					homeFixedAssetSeed(t, f, id)
				}
			})
			p, c, e := homeFixedRead(t, f, r, "new_on_server")
			if e != nil || !p.Exact || !p.Exhausted || p.Pending || len(p.Items) != 32 || p.RawPositions != 32 {
				t.Fatalf("32-entity membership: %+v %+v %v", p, c, e)
			}
			works := map[int64]bool{}
			for _, card := range p.Items {
				if card.Work.Kind != 1 || card.Work.Library != 1 || card.Target.Kind != 1 || card.Target.Library != 1 || card.Target.ID != card.Work.ID || card.Target.PublicID != card.Work.PublicID {
					t.Fatalf("unexpected movie target: %+v", card)
				}
				if works[card.Work.ID] {
					t.Fatalf("duplicate work id %d", card.Work.ID)
				}
				works[card.Work.ID] = true
			}
			if len(works) != 32 {
				t.Fatalf("distinct works = %d", len(works))
			}
		})
	})
	t.Run("entities-33", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f, r := homeFixedFixture(t, func(f *browseFixture) {
				for id := int64(2000); id < 2033; id++ {
					homeFixedEntitySeed(t, f, id, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
					homeFixedAssetSeed(t, f, id)
				}
			})
			p, c, e := homeFixedRead(t, f, r, "new_on_server")
			if e != nil || !p.Pending || p.Exact || p.Exhausted || len(p.Items) != 0 || p.RawPositions != 33 || p.PendingReason != "complete_membership_budget" {
				t.Fatalf("33-entity membership: %+v %+v %v", p, c, e)
			}
		})
	})
}
