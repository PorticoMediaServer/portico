package read

import "testing"

func TestRBHomeFixedNewServerVideoDomain(t *testing.T) {
	for _, kind := range []string{"music", "audiobook"} {
		homeFixedCase(t, kind, func(t *testing.T) {
			f, r := homeFixedFixture(t, func(f *browseFixture) {
				f.exec(t, `UPDATE libraries SET kind=? WHERE id='movies'`, kind)
				work, leaf := int64(6), int64(7)
				if kind == "audiobook" {
					work, leaf = 8, 9
				}
				if kind == "music" {
					homeFixedEntitySeed(t, f, 1999, 5, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
				}
				var parent any
				if kind == "music" {
					parent = int64(1999)
				}
				homeFixedEntitySeed(t, f, 2000, work, parent, nil, nil, "2026-10-01T00:00:00.000000000Z")
				homeFixedEntitySeed(t, f, 3000, leaf, int64(2000), nil, nil, "2026-10-01T00:00:00.000000000Z")
				homeFixedAssetSeed(t, f, 3000)
			})
			page, cost, e := homeFixedRead(t, f, r, "new_on_server")
			if e != nil || !page.Exact || !page.Exhausted || page.Pending || len(page.Items) != 0 || page.RawPositions != 0 {
				t.Fatal("audio entered video shelf", page, cost, e)
			}
			if kind == "music" {
				page, cost, e = homeFixedRead(t, f, r, "listen_next")
				if e != nil || !page.Exact || len(page.Items) != 1 || page.Items[0].Work.ID != 2000 {
					t.Fatal("audio recent fallback lost", page, cost, e)
				}
			}
			// An unrelated audio library cannot make the video shelf hit its 32-row cap.
			leaf := int64(7)
			if kind == "audiobook" {
				leaf = 9
			}
			for id := int64(4000); id < 4033; id++ {
				homeFixedEntitySeed(t, f, id, leaf, int64(2000), nil, nil, "2026-10-01T00:00:00.000000000Z")
			}
			page, cost, e = homeFixedRead(t, f, r, "new_on_server")
			if e != nil || !page.Exact || page.Pending || len(page.Items) != 0 || page.RawPositions != 0 {
				t.Fatal("audio consumed video budget", page, cost, e)
			}
		})
	}
}
