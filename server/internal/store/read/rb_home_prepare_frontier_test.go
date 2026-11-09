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
			// Show 2000 has the newest leaf (an unplayable episode at 11-10) but
			// only an older playable leaf (11-01). Show 2100's playable leaf
			// (11-05) is older by leaf date yet newer by composed date, so it must
			// publish first.
			rbHomePrepareEntitySeed(t, f, 2000, 3, 2, nil, nil, nil, nil, nil, "2026-11-10T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 2100, 3, 2, nil, nil, nil, nil, nil, "2026-11-05T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3000, 3, 4, nil, int64(2000), nil, nil, nil, "2026-11-10T00:00:00.000000000Z")
			rbHomePrepareEntitySeed(t, f, 3001, 3, 4, nil, int64(2000), nil, nil, nil, "2026-11-01T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 3001, 3, "source-shows", "inc-shows")
			rbHomePrepareEntitySeed(t, f, 3002, 3, 4, nil, int64(2100), nil, nil, nil, "2026-11-05T00:00:00.000000000Z")
			rbHomePrepareAssetSeed(t, f, 3002, 3, "source-shows", "inc-shows")
		})
		job := rbHomePrepareBegin(t, r, f, "new_on_server")
		defer job.Close()
		rbHomePrepareRun(t, job)
		page, _, err := rbHomePrepareRead(t, f, job)
		if err != nil || !page.Exact || page.Pending || len(page.Items) != 2 {
			t.Fatalf("boundary shelf: %+v %v", page, err)
		}
		want := rbHomePreparePublishableOracle(t, f, []int64{1, 3}, map[int64]bool{1: true, 2: true}, 100)
		rbHomePrepareCompareIDs(t, page.Items, want, "boundary")
		if page.Items[0].Work.ID != 2100 || page.Items[1].Work.ID != 2000 {
			t.Fatalf("published order ignored composed dates: %+v", page.Items)
		}
		if page.Items[1].Added != "2026-11-01T00:00:00.000000000Z" {
			t.Fatalf("older playable leaf did not fix the composed date: %+v", page.Items[1])
		}
	})
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
