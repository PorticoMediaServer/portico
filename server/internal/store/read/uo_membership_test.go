package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func newUOMembershipFixture(t *testing.T, live bool) (*browseFixture, *ForegroundReader) {
	t.Helper()
	defs := append(browseDefinitions(), UOMembershipDefinitions()...)
	var f *browseFixture
	if live {
		f = composeReadFixture(t, newForegroundSource(t), defs, readfixture.LiveConfig(time.Now), false)
	} else {
		f = composeBrowseFixture(t, newForegroundSource(t), defs)
	}
	r, err := NewForegroundReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func uoMembershipRead(t *testing.T, f *browseFixture, r *ForegroundReader, in MembershipInput) (MembershipPage, BrowseReadResult, error) {
	t.Helper()
	return r.membershipWindow(t.Context(), foregroundRequest(), time.Second, f.output, in)
}

func uoMembershipIDs(page MembershipPage) []int64 {
	var ids []int64
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestUOMembershipCrossProfileIsolation(t *testing.T) {
	f, r := newUOMembershipFixture(t, false)
	f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id BETWEEN 1 AND 6`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,watchlisted) SELECT 'q',public_id,id,library_id,1 FROM catalog_entities WHERE id BETWEEN 201 AND 206`)
	in := MembershipInput{View: f.input, Flag: UOMembershipWatchlist}
	page, result, err := uoMembershipRead(t, f, r, in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := uoMembershipIDs(page), []int64{1, 2, 3, 4, 5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profile p window leaked another profile: %v want %v", got, want)
	}
	if page.RawRows != 6 || page.RawPositions != 6 || !page.Exhausted || page.Next != nil || page.Statements != 14 {
		t.Fatalf("unbounded or continuation-loss window %+v", page)
	}
	if result.LookupStatements != 7 || result.PublicationStatements != 7 {
		t.Fatalf("unexpected membership work %+v", result)
	}
	// Favorites and not-interested are separate closed statements.
	for _, flag := range []UOMembershipFlag{UOMembershipFavorite, UOMembershipNotInterested} {
		in.Flag = flag
		if page, _, err = uoMembershipRead(t, f, r, in); err != nil || len(page.Items) != 0 || !page.Exhausted {
			t.Fatalf("flag %d borrowed watchlist membership: %+v %v", flag, page, err)
		}
	}
}

func TestUOMembershipHiddenSubjectsKeepRawContinuation(t *testing.T) {
	f, r := newUOMembershipFixture(t, false)
	// 64 raw positions whose subjects are all retired. Every one of them must
	// consume a raw position and leave an explicit empty, non-terminal page
	// instead of a zero-match answer.
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id BETWEEN 121 AND 184`)
	f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id BETWEEN 121 AND 184`)
	in := MembershipInput{View: f.input, Flag: UOMembershipWatchlist}
	in.View.Limit = 10
	page, _, err := uoMembershipRead(t, f, r, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.Exhausted || page.Next == nil || page.RawRows != MaxRaw || page.RawPositions != MaxRaw {
		t.Fatalf("empty batch lost raw continuation %+v", page)
	}
	tail, _, err := uoMembershipRead(t, f, r, MembershipInput{View: in.View, Flag: in.Flag, After: page.Next})
	if err != nil {
		t.Fatal(err)
	}
	if len(tail.Items) != 0 || !tail.Exhausted || tail.Next != nil || tail.RawPositions != 0 {
		t.Fatalf("hidden remainder did not terminate %+v", tail)
	}
	// Retired, other-library, other-kind and age-restricted subjects stay hidden
	// while the remaining authorized membership is still delivered.
	f.exec(t, `UPDATE personal_items SET watchlisted=0 WHERE profile_id='p'`)
	f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id IN (2,9,10,1040,3)`)
	// Re-kind subject 3 and its entity together; the composite FK is deferred
	// until commit so the pair changes in one step.
	f.exec(t, `PRAGMA defer_foreign_keys=ON`)
	f.exec(t, `BEGIN`)
	f.exec(t, `UPDATE catalog_subjects SET kind=2 WHERE id=3`)
	f.exec(t, `UPDATE catalog_entities SET kind=2 WHERE id=3`)
	f.exec(t, `COMMIT`)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',4,0)`)
	page, _, err = uoMembershipRead(t, f, r, MembershipInput{View: f.input, Flag: UOMembershipWatchlist})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := uoMembershipIDs(page), []int64{2}; !reflect.DeepEqual(got, want) || !page.Exhausted {
		t.Fatalf("hidden subject escaped %v want %v", got, want)
	}
}

func TestUOMembershipDeletionBetweenPages(t *testing.T) {
	f, r := newUOMembershipFixture(t, false)
	f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id BETWEEN 1 AND 20`)
	in := MembershipInput{View: f.input, Flag: UOMembershipWatchlist}
	in.View.Limit = 6
	first, _, err := uoMembershipRead(t, f, r, in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := uoMembershipIDs(first), []int64{1, 2, 3, 4, 5, 6}; !reflect.DeepEqual(got, want) || first.Exhausted || first.Next == nil {
		t.Fatalf("first page %v %+v", got, first)
	}
	// Delete the continuation anchor itself and the rows between the pages, and
	// hide two later subjects. The public-ID keyset must resume strictly after
	// the deleted anchor without duplicates, gaps in the surviving keys or a
	// stolen extra raw batch.
	f.exec(t, `DELETE FROM personal_items WHERE profile_id='p' AND item_id BETWEEN 6 AND 12`)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id IN (13,14)`)
	second, _, err := uoMembershipRead(t, f, r, MembershipInput{View: in.View, Flag: in.Flag, After: first.Next})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := uoMembershipIDs(second), []int64{15, 16, 17, 19, 20}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted page anchor broke the keyset %v want %v", got, want)
	}
	if !second.Exhausted || second.Next != nil || second.RawPositions != 8 || second.RawRows != 8 {
		t.Fatalf("tail did not terminate %+v", second)
	}
}

func TestUOMembershipCursorAndInvalidInput(t *testing.T) {
	f, r := newUOMembershipFixture(t, false)
	f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id BETWEEN 1 AND 5`)
	in := MembershipInput{View: f.input, Flag: UOMembershipWatchlist}
	in.View.Limit = 2
	first, _, err := uoMembershipRead(t, f, r, in)
	if err != nil || first.Next == nil {
		t.Fatal(err, first)
	}
	// A cursor from another closed flag cannot silently skip this stream.
	if page, _, err := uoMembershipRead(t, f, r, MembershipInput{View: in.View, Flag: UOMembershipFavorite, After: first.Next}); !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, MembershipPage{}) {
		t.Fatalf("foreign flag cursor escaped %+v %v", page, err)
	}
	// A changed library fence must invalidate the private keyset rather than
	// silently re-seek under new authority.
	f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
	if page, _, err := uoMembershipRead(t, f, r, MembershipInput{View: in.View, Flag: in.Flag, After: first.Next}); !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, MembershipPage{}) {
		t.Fatalf("fence drift escaped %+v %v", page, err)
	}
	f.exec(t, `UPDATE libraries SET data_revision=data_revision-1 WHERE id='movies'`)
	for _, invalid := range []MembershipInput{
		{View: f.input, Flag: 0},
		{View: f.input, Flag: 4},
		{View: func() BrowseInput { v := f.input; v.After = &BrowseCursor{}; return v }(), Flag: UOMembershipWatchlist},
		{View: func() BrowseInput { v := f.input; v.Descending = true; return v }(), Flag: UOMembershipWatchlist},
		{View: func() BrowseInput { v := f.input; v.KeepArticles = true; return v }(), Flag: UOMembershipWatchlist},
	} {
		if page, result, err := uoMembershipRead(t, f, r, invalid); !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(page, MembershipPage{}) {
			t.Fatalf("invalid membership input ran %+v %+v %v", invalid, page, err)
		}
	}
	short := foregroundRequest()
	short.Budget.RawRows = MaxRaw - 1
	if page, result, err := r.membershipWindow(t.Context(), short, time.Second, f.output, in); !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(page, MembershipPage{}) {
		t.Fatalf("unfunded raw window ran %+v %v", page, err)
	}
}

func TestUOMembershipPublication(t *testing.T) {
	for _, scenario := range []string{"revoked", "unversioned-membership", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			f, r := newUOMembershipFixture(t, false)
			f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id BETWEEN 1 AND 8`)
			boundary := &browseBoundarySnapshots{Snapshots: f.snapshots}
			switch scenario {
			case "revoked":
				boundary.afterBusiness = func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }
			case "unversioned-membership":
				boundary.afterBusiness = func() {
					f.exec(t, `UPDATE personal_items SET watchlisted=0 WHERE profile_id='p' AND item_id=2`)
					f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id=10`)
				}
			case "canceled":
				boundary.cancelLookup = true
			}
			r.snapshots = boundary
			in := MembershipInput{View: f.input, Flag: UOMembershipWatchlist}
			page, result, err := uoMembershipRead(t, f, r, in)
			if scenario == "unversioned-membership" {
				// Membership does not bump a library revision, so the exact raw
				// rows must be compared and retried inside the original deadline.
				if err != nil || result.Attempts != 2 {
					t.Fatalf("exact membership was not retried %+v %+v %v", page, result, err)
				}
				if got, want := uoMembershipIDs(page), []int64{1, 3, 4, 5, 6, 7, 8, 10}; !reflect.DeepEqual(got, want) {
					t.Fatalf("stale membership published %v want %v", got, want)
				}
				return
			}
			want := ErrDenied
			if scenario == "canceled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(page, MembershipPage{}) {
				t.Fatalf("publication leaked %+v: %v", page, err)
			}
		})
	}
}

func TestUOMembershipLiveCustody(t *testing.T) {
	f, r := newUOMembershipFixture(t, true)
	f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id BETWEEN 1 AND 3`)
	q, request, output := liveRequests("uo-membership")
	in := MembershipInput{View: f.input, Flag: UOMembershipWatchlist}
	page, result, err := r.MembershipWindow(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(page, MembershipPage{}) {
		t.Fatal("unadmitted membership read ran", err)
	}
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, err := r.MembershipWindow(ctx, request, time.Second, out, in)
		if err != nil || len(page.Items) != 3 {
			t.Fatal("live membership read", page, err)
		}
		assertLiveUsage(t, f, 1, 0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}
