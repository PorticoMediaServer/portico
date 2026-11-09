package read

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// rbHomePreparePublishableOracle is an independent plain-SQL oracle for the
// published composed-date order. It never calls the reader: it discovers works
// from the raw catalogue, treats a leaf as playable only when its own asset link
// exists, groups by the public newest-work pointer and applies the published
// descending composed date / descending public-id tie order. Unplayable works
// are absent from the result, exactly like the composed shelf.
func rbHomePreparePublishableOracle(t *testing.T, f *browseFixture, libraries []int64, wantKinds map[int64]bool, limit int) []int64 {
	t.Helper()
	allowed := map[int64]bool{}
	for _, id := range libraries {
		allowed[id] = true
	}
	works := map[int64]string{}
	publics := map[int64][]byte{}
	rows, err := f.conn.QueryContext(t.Context(), `
		SELECT e.id,e.library_id,e.kind,e.public_id,e.show_id,e.album_id,e.book_id,e.retired,e.admission_own_age,e.added_at,
		       EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=e.id)
		FROM catalog_entities e`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, library, kind, retired, playable int64
		var public []byte
		var show, album, book, age sqlNullInt
		var added sqlNullText
		if err := rows.Scan(&id, &library, &kind, &public, &show, &album, &book, &retired, &age, &added, &playable); err != nil {
			t.Fatal(err)
		}
		publics[id] = public
		if !allowed[library] || retired != 0 || playable == 0 || !added.ok {
			continue
		}
		if !age.ok || age.value > 12 {
			continue
		}
		work, workKind := id, kind
		switch kind {
		case 4:
			if !show.ok {
				continue
			}
			work, workKind = show.value, 2
		case 7:
			if !album.ok {
				continue
			}
			work, workKind = album.value, 6
		case 9:
			if !book.ok {
				continue
			}
			work, workKind = book.value, 8
		case 1:
		default:
			continue
		}
		if len(wantKinds) != 0 && !wantKinds[workKind] {
			continue
		}
		if current, ok := works[work]; !ok || added.text > current {
			works[work] = added.text
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(works))
	for id := range works {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool {
		x, y := works[ids[a]], works[ids[b]]
		if x != y {
			return x > y
		}
		return strings.Compare(entityid.Encode(publics[ids[a]]), entityid.Encode(publics[ids[b]])) > 0
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids
}

// rbHomePrepareContentOracle is the independent unrestricted-show oracle. A
// movie keeps its newest playable added_at; an unrestricted show uses the show
// row's maintained last_content_added_at exactly like homeFixedRecent's
// homeFixedStamp(work.Row[0][17]) (NULL is the empty stamp and sorts last). A
// show only produces a card when at least one of its episodes is playable.
func rbHomePrepareContentOracle(t *testing.T, f *browseFixture, libraries []int64, limit int) []int64 {
	t.Helper()
	allowed := map[int64]bool{}
	for _, id := range libraries {
		allowed[id] = true
	}
	published := map[int64]string{}
	publics := map[int64][]byte{}
	rows, err := f.conn.QueryContext(t.Context(), `
		SELECT e.id,e.library_id,e.kind,e.public_id,e.retired,e.show_id,e.added_at,e.last_content_added_at,
		       EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=e.id)
		FROM catalog_entities e`)
	if err != nil {
		t.Fatal(err)
	}
	type showFact struct {
		date string
		ok   bool
	}
	shows := map[int64]showFact{}
	type leaf struct {
		id       int64
		work     int64
		workKind int64
		date     string
	}
	var leaves []leaf
	for rows.Next() {
		var id, library, kind, retired, playable int64
		var public []byte
		var show sqlNullInt
		var added, content sqlNullText
		if err := rows.Scan(&id, &library, &kind, &public, &retired, &show, &added, &content, &playable); err != nil {
			t.Fatal(err)
		}
		publics[id] = public
		if !allowed[library] || retired != 0 {
			continue
		}
		if kind == 2 {
			shows[id] = showFact{date: content.text, ok: content.ok}
			continue
		}
		if playable == 0 {
			continue
		}
		switch kind {
		case 1:
			if !added.ok {
				continue
			}
			leaves = append(leaves, leaf{id: id, work: id, workKind: 1, date: added.text})
		case 4:
			if !show.ok {
				continue
			}
			leaves = append(leaves, leaf{id: id, work: show.value, workKind: 2})
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, l := range leaves {
		if l.workKind == 1 {
			if current, ok := published[l.work]; !ok || l.date > current {
				published[l.work] = l.date
			}
			continue
		}
		show, ok := shows[l.work]
		if !ok {
			continue
		}
		// The show's own row is the date source; the leaf only proves a card.
		if _, ok := published[l.work]; !ok {
			published[l.work] = show.date
		}
	}
	ids := make([]int64, 0, len(published))
	for id := range published {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool {
		x, y := published[ids[a]], published[ids[b]]
		if x != y {
			if x == "" {
				return false
			}
			if y == "" {
				return true
			}
			return x > y
		}
		return strings.Compare(entityid.Encode(publics[ids[a]]), entityid.Encode(publics[ids[b]])) > 0
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids
}

func rbHomePrepareCompareIDs(t *testing.T, got []HomeFixedCard, want []int64, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: published %d works, oracle %d", label, len(got), len(want))
	}
	for n := range want {
		if got[n].Work.ID != want[n] {
			t.Fatalf("%s: row %d got %d want %d", label, n, got[n].Work.ID, want[n])
		}
	}
}

// rbHomePrepareMovieSeed writes one schema-legal movie. The caller decides
// whether it also gets a playable asset.
func rbHomePrepareMovieSeed(t *testing.T, f *browseFixture, id int64, date string) {
	t.Helper()
	rbHomePrepareEntitySeed(t, f, id, 1, 1, nil, nil, nil, nil, nil, date)
}

func rbHomePrepareAudioSeed(t *testing.T, f *browseFixture, album, song, library int64, date string, playable bool) {
	t.Helper()
	rbHomePrepareEntitySeed(t, f, album, library, 6, nil, nil, nil, nil, nil, date)
	rbHomePrepareEntitySeed(t, f, song, library, 7, int64(album), nil, nil, int64(album), nil, date)
	if playable {
		rbHomePrepareAssetSeed(t, f, song, library, "source-music", "inc-music")
	}
}

// TestRBHomePrepareComposedCut is the BLOCKER-2 regression: the frontier must be
// proved over COMPOSED dates (works that actually yield a card), never over the
// newest leaf dates of candidates that may have no resolvable binding.
func TestRBHomePrepareComposedCut(t *testing.T) {
	homeFixedCase(t, "movies_150_newest_unplayable", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			for n := int64(0); n < 300; n++ {
				id := 2000 + n
				date := base.Add(time.Duration(n) * time.Minute).Format(rbHomePrepareStamp)
				rbHomePrepareMovieSeed(t, f, id, date)
				// The 150 newest admitted movies have no resolvable binding. They
				// must not consume the published row, and the walk must continue
				// until 100 card-producing works are proved.
				if n < 150 {
					rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
				}
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || !page.Exhausted || page.Pending || len(page.Items) != 100 {
			t.Fatalf("composed cut was not proved: %+v %v", page, err)
		}
		want := rbHomePreparePublishableOracle(t, f, []int64{1}, map[int64]bool{1: true, 2: true}, 100)
		rbHomePrepareCompareIDs(t, page.Items, want, "movies")
		for _, card := range page.Items {
			if card.Work.ID >= 2150 {
				t.Fatalf("unplayable newest work %d was published", card.Work.ID)
			}
		}
	})

	homeFixedCase(t, "listen_next_150_newest_unplayable", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareLibrarySeed(t, f, "music", 3, "music")
			rbHomePrepareMemberLibraries(t, f, "movies", "music")
			base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			for n := int64(0); n < 250; n++ {
				date := base.Add(time.Duration(n) * time.Minute).Format(rbHomePrepareStamp)
				// Only the 100 oldest albums have a playable track; the recent
				// fallback must not stop at the newest (unplayable) 150.
				rbHomePrepareAudioSeed(t, f, 2000+n, 4000+n, 3, date, n < 100)
			}
		})
		job := rbHomePrepareBegin(t, r, f, "listen_next")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || !page.Exhausted || page.Pending || len(page.Items) != 100 {
			t.Fatalf("listen_next composed cut was not proved: %+v %v", page, err)
		}
		want := rbHomePreparePublishableOracle(t, f, []int64{3}, map[int64]bool{6: true, 8: true}, 100)
		rbHomePrepareCompareIDs(t, page.Items, want, "albums")
	})

	homeFixedCase(t, "composed_date_beats_leaf_date", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareLibrarySeed(t, f, "shows", 3, "tv")
			rbHomePrepareMemberLibraries(t, f, "movies", "shows")
			// The old two-work case passed even on the pre-fix leaf frontier. Make
			// the 100th COMPOSED date the cut: 98 movies are newer than both
			// shows, one playable movie (2098) sits at 11-02, and the two shows'
			// composed dates straddle it. Show 2150 (composed 11-05) publishes;
			// show 2100 is the LAST work: its newest leaf is an unplayable episode
			// at 11-10, but its only playable leaf is 11-01, so its composed date
			// is 11-01 and it ranks below movie 2098 (11-02). A frontier that
			// ordered by leaf dates would instead put show 2100 at 11-10 and drop
			// movie 2098 from the published set.
			base := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
			for n := int64(0); n < 98; n++ {
				id := 2000 + n
				rbHomePrepareMovieSeed(t, f, id, base.Add(time.Duration(n)*time.Minute).Format(rbHomePrepareStamp))
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
			rbHomePrepareMovieSeed(t, f, 2098, "2026-11-02T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2098, 1, "fixed-source", "fixed-inc")
			rbHomePrepareEntitySeed(t, f, 2100, 3, 2, nil, nil, nil, nil, nil, "2026-11-01T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2150, 3, 2, nil, nil, nil, nil, nil, "2026-11-05T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3000, 3, 4, nil, int64(2100), nil, nil, nil, "2026-11-10T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3001, 3, 4, nil, int64(2100), nil, nil, nil, "2026-11-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 3001, 3, "source-shows", "inc-shows")
			rbHomePrepareEntitySeed(t, f, 3002, 3, 4, nil, int64(2150), nil, nil, nil, "2026-11-05T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 3002, 3, "source-shows", "inc-shows")
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || page.Pending || len(page.Items) != 100 {
			t.Fatalf("composed cut shelf: %+v %v", page, err)
		}
		want := rbHomePreparePublishableOracle(t, f, []int64{1, 3}, map[int64]bool{1: true, 2: true}, 100)
		rbHomePrepareCompareIDs(t, page.Items, want, "composed cut")
		if page.Items[98].Work.ID != 2150 || page.Items[99].Work.ID != 2098 {
			t.Fatalf("the 100th composed date did not decide the boundary: %+v", page.Items[97:])
		}
		if page.Items[98].Added != "2026-11-05T00:00:00.000000000Z" || page.Items[99].Added != "2026-11-02T00:00:00.000000000Z" {
			t.Fatalf("boundary cards used leaf dates: %+v", page.Items[98:])
		}
		for _, card := range page.Items {
			if card.Work.ID == 2100 {
				t.Fatalf("show 2100's unplayable newest leaf pushed it into the cut: %+v", card)
			}
		}
	})
}

// TestRBHomePrepareTiedInstantFrontier binds the two strict comparisons in the
// frontier. One instant holds 128 playable works read in the first windows and
// an unplayable cluster (higher public ids) plus 30 more playable works that
// are only read after the frontier asks for the next stream window.
//
// The strict `cut > upper` at rb_home_prepare.go's frontier is what keeps the
// walk reading: after the first 128 are published, the unread window still has
// leaf date == cut, and equality alone proves nothing because the unseen
// candidates could outrank by public id. Changing `>` to `>=` would complete
// there and drop the 30 later playables. The belowCut `<` is the other half:
// after the later window is read, a candidate whose leaf date equals the cut
// must still be hydrated; changing `<` to `<=` would skip those same 30 works.
// The 70 unplayable works at the same instant carry higher public ids than the
// later playables, so they are read first in that window and must never be
// published.
func TestRBHomePrepareTiedInstantFrontier(t *testing.T) {
	homeFixedCase(t, "unread_tied_window_changes_page", func(t *testing.T) {
		const instant = "2026-10-01T00:00:00.000000000Z"
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			// Two playable libraries are read before selection starts. The two
			// empty libraries and the final cluster library exhaust one quantum's
			// four-stream budget so the cluster stays unstarted for the frontier.
			rbHomePrepareLibrarySeed(t, f, "za-empty", 4, "movie")
			rbHomePrepareLibrarySeed(t, f, "zb-empty", 5, "movie")
			rbHomePrepareLibrarySeed(t, f, "zlibrary", 6, "movie")
			f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,generation,identity_confirmed) VALUES('source-other','other','/media','/media','inc-other',3,1)`)
			rbHomePrepareMemberLibraries(t, f, "movies", "other", "za-empty", "zb-empty", "zlibrary")
			for n := int64(0); n < 64; n++ {
				rbHomePrepareEntitySeed(t, f, 2000+n, 1, 1, nil, nil, nil, nil, nil, instant)
				rbHomePrepareAssetSeed(t, f, 2000+n, 1, "fixed-source", "fixed-inc")
				rbHomePrepareEntitySeed(t, f, 2100+n, 2, 1, nil, nil, nil, nil, nil, instant)
				rbHomePrepareAssetSeed(t, f, 2100+n, 2, "source-other", "inc-other")
			}
			// The unread window: 70 unplayable works with the highest public ids,
			// then 30 playable works that must displace the oldest first-window
			// cards because the whole set ties on the composed instant.
			for n := int64(0); n < 70; n++ {
				rbHomePrepareEntitySeed(t, f, 4000+n, 6, 1, nil, nil, nil, nil, nil, instant)
			}
			for n := int64(0); n < 30; n++ {
				rbHomePrepareEntitySeed(t, f, 3000+n, 6, 1, nil, nil, nil, nil, nil, instant)
				rbHomePrepareAssetSeed(t, f, 3000+n, 6, "source-zlibrary", "inc-zlibrary")
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || !page.Exhausted || page.Pending || len(page.Items) != 100 {
			t.Fatalf("tied frontier was not proved: %+v %v", page, err)
		}
		want := rbHomePreparePublishableOracle(t, f, []int64{1, 2, 6}, map[int64]bool{1: true, 2: true}, 100)
		rbHomePrepareCompareIDs(t, page.Items, want, "tied frontier")
		// Public ids compare in their encoded (base64url) form, not numerically,
		// but the later 3000-block still ranks above every first-window work, so
		// all 30 must be in the page. An early stop at `cut >= upper` would miss
		// them; a `belowCut <=` skip would miss them the same way.
		present := map[int64]bool{}
		for _, card := range page.Items {
			present[card.Work.ID] = true
			if card.Work.ID >= 4000 {
				t.Fatalf("unplayable tied work %d was published", card.Work.ID)
			}
		}
		for n := int64(0); n < 30; n++ {
			if !present[3000+n] {
				t.Fatalf("later tied work %d was dropped from the page", 3000+n)
			}
		}
	})
}

// TestRBHomePrepareUnrestrictedContentCut exercises the UNRESTRICTED TV source:
// the show stream is catalog_entities_content_added, and a show's composed date
// is its own maintained last_content_added_at, never an episode's DateAdded.
// The fixture's default profile is age-restricted at 12, so this is the only
// test that reaches buildStreams' ContentAdded branch.
func TestRBHomePrepareUnrestrictedContentCut(t *testing.T) {
	homeFixedCase(t, "content_added_cut_ignores_episode_dates", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			f.exec(t, `UPDATE profile_restrictions SET maximum_age=-1, allow_unrated=1 WHERE profile_id='p'`)
			rbHomePrepareLibrarySeed(t, f, "shows", 3, "tv")
			rbHomePrepareMemberLibraries(t, f, "movies", "shows")
			base := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
			for n := int64(0); n < 98; n++ {
				id := 2000 + n
				rbHomePrepareMovieSeed(t, f, id, base.Add(time.Duration(n)*time.Minute).Format(rbHomePrepareStamp))
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
			rbHomePrepareMovieSeed(t, f, 2098, "2026-11-03T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2098, 1, "fixed-source", "fixed-inc")
			// Show 2100's content chronology is 11-02, older than its playable
			// episode (11-01 is the playable one; 11-10 is unplayable) and older
			// than its newest episode. Show 2150's content date is 11-05.
			rbHomePrepareEntitySeed(t, f, 2100, 3, 2, nil, nil, nil, nil, nil, "2026-11-02T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2200, 3, 3, int64(2100), int64(2100), nil, nil, nil, "2026-11-02T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3000, 3, 4, int64(2200), int64(2100), int64(2200), nil, nil, "2026-11-10T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3001, 3, 4, int64(2200), int64(2100), int64(2200), nil, nil, "2026-11-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 3001, 3, "source-shows", "inc-shows")
			rbHomePrepareEntitySeed(t, f, 2150, 3, 2, nil, nil, nil, nil, nil, "2026-11-05T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2250, 3, 3, int64(2150), int64(2150), nil, nil, nil, "2026-11-05T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3002, 3, 4, int64(2250), int64(2150), int64(2250), nil, nil, "2026-11-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 3002, 3, "source-shows", "inc-shows")
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		done := rbHomePrepareRun(t, job)
		if done.Restarts != 0 {
			t.Fatalf("unrestricted content walk restarted: %+v", done)
		}
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || page.Pending || len(page.Items) != 100 {
			t.Fatalf("unrestricted content cut: %+v %v", page, err)
		}
		want := rbHomePrepareContentOracle(t, f, []int64{1, 3}, 100)
		rbHomePrepareCompareIDs(t, page.Items, want, "unrestricted content")
		// The 100th composed date is movie 2098 (11-03); show 2150's content date
		// (11-05) publishes above it, and show 2100 (11-02) is out. An episode
		// DateAdded frontier would invert exactly this pair.
		if page.Items[98].Work.ID != 2150 || page.Items[99].Work.ID != 2098 {
			t.Fatalf("episode dates drove the unrestricted cut: %+v", page.Items[97:])
		}
		for _, card := range page.Items {
			if card.Work.ID == 2100 {
				t.Fatalf("show 2100 was dated by its newest episode: %+v", card)
			}
		}
	})
}

// TestRBHomePrepareActualDateUnrestrictedMissingContent pins the parity nit:
// legacy homeFixedRecent stamps an unrestricted show from the show's own
// last_content_added_at and homeFixedStamp(nil) is the empty stamp, which sorts
// last. The column is nullable and no trigger forces it, so actualDate must
// return the empty stamp instead of the newest playable leaf.
func TestRBHomePrepareActualDateUnrestrictedMissingContent(t *testing.T) {
	unrestricted := func() *RBHomePreparePreparation {
		return &RBHomePreparePreparation{
			row: "new_on_server",
			entities: map[int64]*rbHomePrepareEntity{
				2000: {ID: 2000, Kind: 2, Library: 3, Available: true},
				3000: {ID: 3000, Kind: 4, Library: 3, Available: true, HasAdded: true, Added: "2026-11-05T00:00:00.000000000Z"},
			},
			childrenOf: map[int64][]int64{2000: {3000}},
			entry: &rbHomePrepareEntry{scope: rbHomePrepareScope{Libraries: []rbHomePrepareLibrary{
				{NativeID: 3, Fence: browseFence{maximumAge: -1, allowUnrated: 1}},
			}}},
		}
	}
	if got := unrestricted().actualDate(2000); got != "" {
		t.Fatalf("unrestricted show without last_content_added_at returned %q, want the empty stamp", got)
	}
	// A restricted viewer keeps the newest admitted leaf, and a maintained
	// content date still wins when the column is present.
	p := unrestricted()
	p.entry.scope.Libraries[0].Fence = browseFence{maximumAge: 12, allowUnrated: 0}
	if got := p.actualDate(2000); got != "2026-11-05T00:00:00.000000000Z" {
		t.Fatalf("restricted show lost its newest leaf date: %q", got)
	}
	p = unrestricted()
	show := p.entities[2000]
	show.HasContentAdded = true
	show.ContentAdded = "2026-11-02T00:00:00.000000000Z"
	if got := p.actualDate(2000); got != show.ContentAdded {
		t.Fatalf("unrestricted show did not use its content chronology: %q", got)
	}
}

// TestRBHomePrepareRestartBudgetPerJob proves the F-04 invalidation budget is
// held on the JOB, not recomputed by each Advance call. A caller that keeps
// polling may restart at most twice for the whole job.
func TestRBHomePrepareRestartBudgetPerJob(t *testing.T) {
	homeFixedCase(t, "third_content_restart_refuses", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			for n := int64(0); n < 60; n++ {
				id := 2000 + n
				date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute).Format(rbHomePrepareStamp)
				rbHomePrepareMovieSeed(t, f, id, date)
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		// One bounded poll cannot finish the sixty-work selection, so each later
		// revision move forces a content restart without completing the job.
		if advance, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); err != nil || advance.Ready {
			t.Fatalf("first poll: %v %+v", err, advance)
		}
		for poll := 0; poll < rbHomePrepareRestartLimit; poll++ {
			f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
			advance, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second)
			if err != nil || advance.Ready || advance.Restarts != 1 {
				t.Fatalf("poll %d did not restart once: %v %+v", poll+1, err, advance)
			}
			if job.entry.restarts != poll+1 {
				t.Fatalf("poll %d kept %d job-wide restarts", poll+1, job.entry.restarts)
			}
		}
		// The two job-wide restarts are spent; a third content change is refused
		// instead of being restarted again by a fresh per-call counter.
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		advance, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second)
		if !errors.Is(err, ErrStale) || advance.Ready {
			t.Fatalf("third restart was not refused: %v %+v", err, advance)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("over-budget restart kept its lease or bytes")
		}
	})
}

// TestRBHomePreparePersonalScope matches legacy Home for personal rows outside
// the viewer's library scope: the row is not part of the shelf, not a denial.
func TestRBHomePreparePersonalScope(t *testing.T) {
	homeFixedCase(t, "out_of_scope_personal_row_is_ignored", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareMovieSeed(t, f, 2000, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2000, 1, "fixed-source", "fixed-inc")
			rbHomePrepareStateSeed(t, f, 2000, 5000, "in_progress")
			// A second, playable movie with its own progress row deliberately
			// names the out-of-scope "Other" library (native 2). Legacy iterates
			// visible membership, so that row is not part of the shelf and is not
			// a denial for the whole row.
			rbHomePrepareMovieSeed(t, f, 2001, "2026-10-02T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2001, 1, "fixed-source", "fixed-inc")
			f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,resume_position,resume_unit,activity_at,activity_state,resume_playback_id) SELECT 'p',public_id,id,2,5000,0,'2026-11-02T12:00:00.000000000Z','in_progress','prep-play' FROM catalog_entities WHERE id=2001`)
		})
		job := rbHomePrepareBegin(t, r, f, "continue")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || page.Pending || len(page.Items) != 1 || page.Items[0].Work.ID != 2000 {
			t.Fatalf("out-of-scope personal row changed the continue shelf: %+v %v", page, err)
		}
	})
}

// TestRBHomePrepareCapRefusals covers every named capacity boundary: each is a
// pending/refusal, never a truncated Exact page.
func TestRBHomePrepareCapRefusals(t *testing.T) {
	homeFixedCase(t, "more_than_16_libraries", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(*browseFixture) {})
		ids := make([]string, 0, rbHomePrepareLibraryCap+1)
		for n := 0; n <= rbHomePrepareLibraryCap; n++ {
			ids = append(ids, fmt.Sprintf(`"lib%d"`, n))
		}
		f.exec(t, `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='a'`, "["+strings.Join(ids, ",")+"]")
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Pending || page.Exact || page.PendingReason != "library_membership_preparation" {
			t.Fatalf("library census was not refused: %+v %v", page, err)
		}
	})

	homeFixedCase(t, "more_than_4096_candidates", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			// Undated candidates cannot form the initial leaf-date batch, so the
			// stream must reach its own cap before any hydration.
			for n := int64(0); n <= rbHomePrepareSelectionCap; n++ {
				rbHomePrepareMovieSeed(t, f, 50000+n, "2026-10-01T00:00:00.000000000Z")
			}
			f.exec(t, `UPDATE catalog_entities SET added_at=NULL,last_content_added_at=NULL WHERE id>=50000`)
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Pending || page.Exact || page.PendingReason != "home_prepare_selection_cap" {
			t.Fatalf("candidate cap was not refused: %+v %v", page, err)
		}
	})

	homeFixedCase(t, "more_than_128_children", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareLibrarySeed(t, f, "shows", 3, "tv")
			rbHomePrepareMemberLibraries(t, f, "movies", "shows")
			rbHomePrepareEntitySeed(t, f, 2000, 3, 2, nil, nil, nil, nil, nil, "2026-11-04T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2100, 3, 3, int64(2000), int64(2000), nil, nil, nil, "2026-11-04T00:00:00.000000000Z")
			for n := int64(0); n <= rbHomePrepareChildWindow; n++ {
				rbHomePrepareEntitySeed(t, f, 3000+n, 3, 4, int64(2100), int64(2000), int64(2100), nil, nil, "2026-11-01T00:00:00.000000000Z")
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Pending || page.Exact || page.PendingReason != "home_prepare_child_window" {
			t.Fatalf("child window was not refused: %+v %v", page, err)
		}
	})

	homeFixedCase(t, "retained_byte_ceiling", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			for n := int64(0); n < 200; n++ {
				date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute).Format(rbHomePrepareStamp)
				rbHomePrepareMovieSeed(t, f, 2000+n, date)
			}
		})
		job, err := r.Begin(t.Context(), 1, 32<<10, RBHomePrepareInput{View: f.input, RowID: "new_on_server"})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Pending || page.Exact || page.PendingReason != "home_prepare_capacity" {
			t.Fatalf("retained workspace was not charged: %+v %v", page, err)
		}
	})

	homeFixedCase(t, "octet_preflight_refusal", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			rbHomePrepareMovieSeed(t, f, 2000, "2026-10-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 2000, 1, "fixed-source", "fixed-inc")
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		request := rbHomePrepareRequest()
		request.Need[governor.MemoryBytes] = r.fixed.native.pool.NativeRowReservation() + 512
		if _, err := job.Advance(t.Context(), request, time.Second); !errors.Is(err, governor.ErrSnapshotBudget) {
			t.Fatalf("same-snapshot octet preflight did not refuse: %v", err)
		}
		if job.entry.complete || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("refused preflight retained a page or bytes")
		}
	})

	homeFixedCase(t, "revocation_denied", func(t *testing.T) {
		f, r := rbHomePrepareFixture(t, func(f *browseFixture) {
			for n := int64(0); n < 12; n++ {
				id := 2000 + n
				date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute).Format(rbHomePrepareStamp)
				rbHomePrepareMovieSeed(t, f, id, date)
				rbHomePrepareAssetSeed(t, f, id, 1, "fixed-source", "fixed-inc")
			}
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		if _, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["other"]' WHERE account_id='a'`)
		if _, err := job.Advance(t.Context(), rbHomePrepareRequest(), time.Second); !errors.Is(err, ErrDenied) {
			t.Fatalf("revoked library scope did not deny: %v", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied preparation retained its lease or bytes")
		}
	})
}
