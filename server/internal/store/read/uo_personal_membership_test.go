package read

import "testing"

func TestUOPersonalMembershipSparseRawWindows(t *testing.T) {
	f := newUOPersonalReadSource(t)
	q := uoPersonalNativeQuery(t, f)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,watchlisted,favorite,rating,not_interested) SELECT 'p',public_id,id,library_id,1,1,0,1 FROM catalog_subjects WHERE id<=200`)
	// Keep personal membership but hide current catalogue source eligibility.
	f.exec(t, `UPDATE catalog_entities SET retired=CASE WHEN id<=70 THEN 1 ELSE 0 END WHERE id<=200`)
	fence := browseFence{profile: "p", libraryID: 1, kind: 1, maximumAge: -1, allowUnrated: 1}
	for _, m := range []uoPersonalMembership{uoMembershipMyList, uoMembershipFavorite, uoMembershipRating, uoMembershipNotInterested} {
		first, e := readUOPersonalMembership(q, fence, m, 0, nil, 40)
		if e != nil || first.exhausted || first.next == nil || len(first.items) != 0 || first.rawPositions != 64 {
			t.Fatal("hidden raw tail", m, first, e)
		}
		seen := map[int64]bool{}
		next := first.next
		for n := 0; n < 30; n++ {
			page, e := readUOPersonalMembership(q, fence, m, 0, next, 7)
			if e != nil || len(page.items) > 7 || page.rawRows > 64 {
				t.Fatal(m, page, e)
			}
			for _, item := range page.items {
				if seen[item.subject] || item.subject <= 70 {
					t.Fatal("duplicate/hidden item", item)
				}
				seen[item.subject] = true
			}
			if page.exhausted {
				break
			}
			if page.next == nil || n == 29 {
				t.Fatal("missing raw cursor")
			}
			next = page.next
		}
		if len(seen) != 130 {
			t.Fatal("membership lost positions", m, len(seen))
		}
	}
}
func TestUOPersonalMembershipQueryBindingAndRatingZero(t *testing.T) {
	f := newUOPersonalReadSource(t)
	q := uoPersonalNativeQuery(t, f)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating) SELECT 'p',public_id,id,library_id,0 FROM catalog_subjects WHERE id=1`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,watchlisted) SELECT 'p',public_id,id,library_id,1 FROM catalog_subjects WHERE id=1040`)
	fence := browseFence{profile: "p", libraryID: 1, kind: 1, maximumAge: -1, allowUnrated: 1}
	p, e := readUOPersonalMembership(q, fence, uoMembershipRating, 0, nil, 40)
	if e != nil || len(p.items) != 1 || !p.exhausted {
		t.Fatal(p, e)
	}
	p, e = readUOPersonalMembership(q, fence, uoMembershipMyList, 0, nil, 40)
	if e != nil || len(p.items) != 0 || !p.exhausted {
		t.Fatal("rating0 invented watchlist", p, e)
	}
	stale := &uoPersonalMembershipCursor{fence: fence, membership: uoMembershipFavorite}
	if _, e = readUOPersonalMembership(q, fence, uoMembershipMyList, 0, stale, 40); e != ErrStale {
		t.Fatal(e)
	}
}

func TestUOPersonalMembershipRefusesUnsupportedContainerRestriction(t *testing.T) {
	q := func(string, ...any) ([][]any, error) {
		t.Fatal("unsupported restriction entered data recipe")
		return nil, nil
	}
	f := browseFence{container: 1, maximumAge: 12, allowUnrated: 1}
	p, e := readUOPersonalMembership(q, f, uoMembershipMyList, 0, nil, 40)
	if e != ErrUnqualified || len(p.items) != 0 || p.next != nil {
		t.Fatal(p, e)
	}
}
