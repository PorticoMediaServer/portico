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
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func newUOActivityFixture(t *testing.T, live bool) (*browseFixture, *ForegroundReader) {
	t.Helper()
	defs := append(browseDefinitions(), UOActivityDefinitions()...)
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

func uoActivityRead(t *testing.T, f *browseFixture, r *ForegroundReader, in UOActivityInput) (UOActivityPage, BrowseReadResult, error) {
	t.Helper()
	return r.activityWindow(t.Context(), foregroundRequest(), time.Second, f.output, in)
}

func uoActivityIDs(page UOActivityPage) []int64 {
	var ids []int64
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

// uoActivityOracle is plain SQL over the fixture, independent of the read
// recipe, the fence helper and the page decoder.
func uoActivityOracle(t *testing.T, f *browseFixture, column string) []int64 {
	t.Helper()
	rows, err := f.conn.QueryContext(t.Context(), `SELECT p.item_id FROM personal_items AS p JOIN catalog_entities AS e ON e.id=p.item_id WHERE p.profile_id='p' AND p.activity_visible=1 AND p.`+column+` IS NOT NULL AND e.library_id=1 AND e.kind=1 AND e.retired=0 ORDER BY p.`+column+` DESC,p.item_public_id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func uoHistoryHead(t *testing.T, f *browseFixture, activity, cutoff int64) {
	t.Helper()
	var member, lifetime string
	if err := f.conn.QueryRowContext(t.Context(), `SELECT incarnation FROM direct_memberships WHERE account_id='a'`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if err := f.conn.QueryRowContext(t.Context(), `SELECT lifetime FROM direct_profiles WHERE id='p'`).Scan(&lifetime); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO personal_activity_revisions(profile_id,revision,activity_generation,recommendation_generation,history_visible_from_id,action_sequence,policy_authority,policy_account_id,policy_profile_id,policy_member_incarnation,policy_profile_lifetime,history_policy_generation) VALUES('p',1,?,1,?,0,'local','a','p',?,?,1) ON CONFLICT(profile_id) DO UPDATE SET activity_generation=excluded.activity_generation,history_visible_from_id=excluded.history_visible_from_id`, activity, cutoff, member, lifetime)
}

func uoHistoryRow(t *testing.T, f *browseFixture, occurred, library string, kind int64, public []byte, generation, completed int64) {
	t.Helper()
	var member, lifetime string
	if err := f.conn.QueryRowContext(t.Context(), `SELECT incarnation FROM direct_memberships WHERE account_id='a'`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if err := f.conn.QueryRowContext(t.Context(), `SELECT lifetime FROM direct_profiles WHERE id='p'`).Scan(&lifetime); err != nil {
		t.Fatal(err)
	}
	// The fixture's catalogue public ids are the little-endian subject id, so the
	// declared (subject_id,item_public_id) pair must name the same subject or the
	// native composite foreign key refuses the row.
	var subject any
	if len(public) == 16 {
		subject = int64(binary.LittleEndian.Uint64(public))
	}
	f.exec(t, `INSERT INTO play_history(occurred_at,event_kind,account_key,profile_id,playback_id,occurrence_generation,accepted_sequence,item_public_id,item_kind,library_public_id,position,played_ms,completed,subject_id,activity_generation,membership_incarnation,profile_lifetime) VALUES(?,?,?,?,?,0,?,?,?,?,?,?,?,?,?,?,?)`,
		occurred, "ended", "a", "p", "playback-"+occurred, int64(1), public, kind, library, int64(10), int64(1000), completed, subject, generation, member, lifetime)
}

func TestUOActivityActorItemTimeBoundedTraversal(t *testing.T) {
	f, r := newUOActivityFixture(t, false)
	// Profile p owns the fixture stream. Profile q's newer row must never leak.
	f.exec(t, `UPDATE personal_items SET last_played_at=activity_at WHERE profile_id='p'`)
	f.exec(t, `UPDATE personal_items SET last_completed_at=activity_at WHERE profile_id='p' AND item_id%4=0`)
	for _, family := range []struct {
		kind   UOActivityFamily
		column string
	}{
		{UOActivityProgress, "activity_at"},
		{UOActivityRecent, "last_played_at"},
		{UOActivityCompleted, "last_completed_at"},
	} {
		in := UOActivityInput{View: f.input, Family: family.kind}
		in.View.Limit = 40
		var got []int64
		for pages := 0; ; pages++ {
			if pages > 40 {
				t.Fatal("cursor did not terminate")
			}
			page, result, err := uoActivityRead(t, f, r, in)
			if err != nil || result.Refusal != nil {
				t.Fatalf("%s: %v %+v", family.kind.activityName(), err, result)
			}
			if page.RawRows > MaxRaw+1 || page.RawPositions > MaxRaw || len(page.Items) > in.View.Limit || page.Statements > 24 {
				t.Fatalf("%s unbounded window %+v", family.kind.activityName(), page)
			}
			got = append(got, uoActivityIDs(page)...)
			if page.Exhausted {
				break
			}
			if page.Next == nil {
				t.Fatalf("%s lost continuation %+v", family.kind.activityName(), page)
			}
			in.After = page.Next
		}
		if want := uoActivityOracle(t, f, family.column); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s traversal %v want oracle %v", family.kind.activityName(), got, want)
		}
	}
}

func TestUOActivityCrossProfileAndHeadGeneration(t *testing.T) {
	f, r := newUOActivityFixture(t, false)
	in := UOActivityInput{View: f.input, Family: UOActivityProgress}
	page, _, err := uoActivityRead(t, f, r, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == 1 && item.At == "2026-11-06T12:00:00.000000000Z" {
			t.Fatal("another profile's personal row was returned")
		}
	}
	// A clear generation hides every current row stamped with the old one, and a
	// cursor from the old head is reset rather than continued.
	uoHistoryHead(t, f, 2, 0)
	f.exec(t, `UPDATE personal_items SET activity_generation=2 WHERE profile_id='p'`)
	first, _, err := uoActivityRead(t, f, r, in)
	if err != nil || len(first.Items) == 0 || !first.Head.Exists || first.Head.ActivityGeneration != 2 {
		t.Fatalf("current generation empty %+v %v", first, err)
	}
	uoHistoryHead(t, f, 3, 0)
	page, _, err = uoActivityRead(t, f, r, UOActivityInput{View: f.input, Family: UOActivityProgress, After: first.Next})
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, UOActivityPage{}) {
		t.Fatalf("stale head cursor released %+v %v", page, err)
	}
}

func TestUOActivityHistoryCurrentLifetimeAndClearBoundary(t *testing.T) {
	f, r := newUOActivityFixture(t, false)
	uoHistoryHead(t, f, 2, 0)
	public := func(id byte) []byte { return []byte{id, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0} }
	uoHistoryRow(t, f, "2026-11-05T12:00:00.000000000Z", "movies", 1, public(1), 2, 0)
	uoHistoryRow(t, f, "2026-11-05T11:00:00.000000000Z", "movies", 1, public(2), 2, 1)
	uoHistoryRow(t, f, "2026-11-05T10:00:00.000000000Z", "movies", 1, public(3), 1, 0) // older generation
	uoHistoryRow(t, f, "2026-11-05T09:00:00.000000000Z", "movies", 1, public(4), 2, 0) // retired lifetime
	uoHistoryRow(t, f, "2026-11-05T08:00:00.000000000Z", "other", 1, public(5), 2, 0)  // other library
	uoHistoryRow(t, f, "2026-11-05T07:00:00.000000000Z", "movies", 2, public(6), 2, 0) // other kind
	uoHistoryRow(t, f, "2026-11-05T06:00:00.000000000Z", "movies", 1, nil, 2, 0)       // missing identity
	f.exec(t, `UPDATE play_history SET profile_lifetime=lower(hex(zeroblob(16))) WHERE item_public_id=?`, public(4))
	in := UOActivityInput{View: f.input, Family: UOActivityHistory}
	in.View.Limit = 40
	page, _, err := uoActivityRead(t, f, r, in)
	if err != nil {
		t.Fatal(err)
	}
	var occurrences []int64
	for _, item := range page.Items {
		occurrences = append(occurrences, item.OccurrenceID)
	}
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id FROM play_history WHERE profile_id='p' AND membership_incarnation=(SELECT incarnation FROM direct_memberships WHERE account_id='a') AND profile_lifetime=(SELECT lifetime FROM direct_profiles WHERE id='p') AND library_public_id='movies' AND item_kind=1 AND item_public_id IS NOT NULL AND activity_generation=2 ORDER BY occurred_at DESC,id DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var want []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if !reflect.DeepEqual(occurrences, want) || len(want) != 2 || !page.Exhausted {
		t.Fatalf("history %v want %v page %+v", occurrences, want, page)
	}
	// The exact accepted fields survive; nullable item identity never invents one.
	if page.Items[0].BrowseItem.ID != 1 || page.Items[0].PlayedMS != 1000 || page.Items[0].Completed {
		t.Fatalf("history item fields %+v", page.Items[0])
	}
	if page.Items[1].Completed != true {
		t.Fatalf("completion lost %+v", page.Items[1])
	}
	// A cursor issued under the old head must reset when the clear boundary
	// moves, and the older retained occurrence is then hidden.
	in.View.Limit = 1
	first, _, err := uoActivityRead(t, f, r, in)
	if err != nil || first.Next == nil {
		t.Fatalf("missing history continuation %+v %v", first, err)
	}
	in.View.Limit = 40
	uoHistoryHead(t, f, 2, 2)
	if page, _, err := uoActivityRead(t, f, r, UOActivityInput{View: f.input, Family: UOActivityHistory, After: first.Next}); !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, UOActivityPage{}) {
		t.Fatalf("stale history cursor released %+v %v", page, err)
	}
	next, _, err := uoActivityRead(t, f, r, in)
	if err != nil || len(next.Items) != 1 || next.Items[0].OccurrenceID != 2 || !next.Head.Exists || next.Head.HistoryCutoff != 2 {
		t.Fatalf("clear boundary %+v %v", next, err)
	}
}

func TestUOActivityNullableRatingAndHostileTitle(t *testing.T) {
	f, r := newUOActivityFixture(t, false)
	in := UOActivityInput{View: f.input, Family: UOActivityProgress}
	in.View.Limit = 3
	// rating NULL is unrated; an explicit zero is a real zero rating.
	f.exec(t, `UPDATE personal_items SET rating=NULL WHERE profile_id='p' AND item_id=1`)
	f.exec(t, `UPDATE personal_items SET rating=0 WHERE profile_id='p' AND item_id=2`)
	page, _, err := uoActivityRead(t, f, r, in)
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ID != 1 || page.Items[0].Rating != nil || page.Items[1].ID != 2 || page.Items[1].Rating == nil || *page.Items[1].Rating != 0 {
		t.Fatalf("nullable rating collapsed %+v", page.Items[:2])
	}
	// A hostile title is refused by the same-snapshot actual-byte reservation,
	// never copied and then trimmed.
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=2`, strings.Repeat("x", 5<<20))
	page, _, err = uoActivityRead(t, f, r, in)
	if !errors.Is(err, governor.ErrSnapshotBudget) || !reflect.DeepEqual(page, UOActivityPage{}) {
		t.Fatalf("hostile title released %+v %v", page, err)
	}
}

func TestUOActivityEffectiveWatchedRefusalAndFold(t *testing.T) {
	f, r := newUOActivityFixture(t, false)
	in := UOActivityInput{View: f.input, Family: UOActivityProgress, EffectiveWatched: true}
	page, result, err := r.UOActivityWindow(t.Context(), foregroundRequest(), time.Second, f.output, in)
	var pending *UOProducerPending
	if !errors.As(err, &pending) || pending.Task != "UO-05" || !errors.Is(err, ErrUOSourceNotLanded) || result.LookupStatements != 0 || !reflect.DeepEqual(page, UOActivityPage{}) {
		t.Fatalf("effective watched did not name its missing producer %+v %+v %v", page, result, err)
	}
	public := [16]byte{1}
	fact := func(subject int64, generation, action int64, watched bool, public [16]byte) uoActivityWatchedFact {
		return uoActivityWatchedFact{subject: subject, public: public, generation: generation, action: action, watched: watched}
	}
	leaf := fact(1, 2, 5, false, public)
	ancestors := []uoActivityAncestorIntent{
		{uoActivityWatchedFact: fact(2, 2, 7, true, [16]byte{2}), membership: 1, watermark: 3},
		{uoActivityWatchedFact: fact(3, 1, 9, true, [16]byte{3}), membership: 1, watermark: 3},
	}
	got, err := foldUOActivityWatched(1, public, 2, leaf, ancestors)
	if err != nil || !got.watched || got.subject != 2 || got.action != 7 {
		t.Fatalf("effective watched fold %+v %v", got, err)
	}
	if _, err := foldUOActivityWatched(1, public, 2, leaf, []uoActivityAncestorIntent{ancestors[0], ancestors[0]}); err == nil {
		t.Fatal("duplicate ancestor identity accepted")
	}
	ancestors[0] = uoActivityAncestorIntent{uoActivityWatchedFact: fact(4, 2, 5, true, [16]byte{4}), membership: 4, watermark: 3}
	if got, err := foldUOActivityWatched(1, public, 2, leaf, ancestors); err != nil || got.watched || got.action != 5 {
		t.Fatalf("ineligible member or tied action folded %+v %v", got, err)
	}
}

func TestUOActivityInvalidInput(t *testing.T) {
	f, r := newUOActivityFixture(t, false)
	for _, in := range []UOActivityInput{
		{View: f.input, Family: 0},
		{View: f.input, Family: 5},
		{View: func() BrowseInput { v := f.input; v.After = &BrowseCursor{}; return v }(), Family: UOActivityProgress},
		{View: func() BrowseInput { v := f.input; v.Descending = true; return v }(), Family: UOActivityProgress},
	} {
		page, result, err := uoActivityRead(t, f, r, in)
		if !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(page, UOActivityPage{}) {
			t.Fatalf("invalid activity ran %+v %+v %v", in, page, err)
		}
	}
	short := foregroundRequest()
	short.Budget.RawRows = MaxRaw - 1
	if page, result, err := r.activityWindow(t.Context(), short, time.Second, f.output, UOActivityInput{View: f.input, Family: UOActivityProgress}); !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(page, UOActivityPage{}) {
		t.Fatalf("unfunded raw window ran %+v %v", page, err)
	}
}

func TestUOActivityLiveCustody(t *testing.T) {
	f, r := newUOActivityFixture(t, true)
	q, request, output := liveRequests("uo-activity")
	in := UOActivityInput{View: f.input, Family: UOActivityProgress}
	in.View.Limit = 5
	if page, result, err := r.UOActivityWindow(t.Context(), request, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(page, UOActivityPage{}) {
		t.Fatal("unadmitted activity read ran", err)
	}
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, err := r.UOActivityWindow(ctx, request, time.Second, out, in)
		if err != nil || len(page.Items) != 5 {
			t.Fatal("live activity read", page, err)
		}
		assertLiveUsage(t, f, 1, 0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}
