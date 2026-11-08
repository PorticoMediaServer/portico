package read

import "testing"

func TestRBHomeFixedAlbumLatestBeforeAvailability(t *testing.T) {
	homeFixedCase(t, "unavailable-latest", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			f.exec(t, `UPDATE libraries SET kind='music' WHERE id='movies'`)
			homeFixedEntitySeed(t, f, 1999, 5, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedEntitySeed(t, f, 2000, 6, int64(1999), nil, nil, "2026-10-01T00:00:00.000000000Z")
			for id := int64(3000); id < 3003; id++ {
				homeFixedEntitySeed(t, f, id, 7, int64(2000), nil, nil, "2026-10-01T00:00:00.000000000Z")
				homeFixedAssetSeed(t, f, id)
				f.exec(t, `UPDATE catalog_entities SET track_number=? WHERE id=?`, id-2999, id)
			}
			homeFixedStateSeed(t, f, 3000, 5000, "in_progress")
			homeFixedStateSeed(t, f, 3002, 0, "ended")
			f.exec(t, `UPDATE personal_items SET activity_at='2026-11-03T12:00:00.000000000Z' WHERE item_id=3002`)
			f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=3002`)
		})
		assertRecent := func() {
			t.Helper()
			p, c, e := homeFixedRead(t, f, r, "listen_next")
			if e != nil || !p.Exact || p.Pending || len(p.Items) != 1 || p.Items[0].Work.ID != 2000 || p.Items[0].Target.ID != 2000 || p.Items[0].ActivityAt != "" || p.Items[0].ResumePosition != 0 {
				t.Fatal("older activity revived instead of recent fallback", p, c, e)
			}
		}
		assertRecent() // The newer ended final track means no album continuation.
		f.exec(t, `UPDATE personal_items SET activity_state='in_progress' WHERE item_id=3002`)
		assertRecent() // The current track is unavailable; older progress stays obsolete.
		f.exec(t, `UPDATE personal_items SET activity_state='ended' WHERE item_id=3002`)
		f.exec(t, `UPDATE catalog_entities SET track_number=CASE id WHEN 3002 THEN 2 WHEN 3001 THEN 3 END WHERE id IN(3001,3002)`)
		p, c, e := homeFixedRead(t, f, r, "listen_next")
		if e != nil || !p.Exact || len(p.Items) != 1 || p.Items[0].Target.ID != 3001 || p.Items[0].ActivityAt != "2026-11-03T12:00:00.000000000Z" {
			t.Fatal("ended unavailable track lost its available successor", p, c, e)
		}
		f.exec(t, `UPDATE catalog_assets SET available=1 WHERE id=3002`)
		f.exec(t, `UPDATE personal_items SET activity_state='in_progress' WHERE item_id=3002`)
		p, c, e = homeFixedRead(t, f, r, "listen_next")
		if e != nil || !p.Exact || len(p.Items) != 1 || p.Items[0].Target.ID != 3002 {
			t.Fatal("current available track lost", p, c, e)
		}
	})
}
