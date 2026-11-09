package read

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_PERSONAL_PREPARE_JOURNAL
func newRBPersonalPrepareSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	// A preserved article-policy variant, plus one equal-title run longer than
	// a raw window so the equality tail and next-key seek are exercised.
	f.exec(t, `UPDATE catalog_entities SET sort_key_keep_articles='The '||sort_key`)
	f.exec(t, `UPDATE catalog_entities SET sort_key='same',sort_key_keep_articles='The Same' WHERE id BETWEEN 400 AND 470`)
	// Three hundred rows with current personal facts: values, explicit zeroes,
	// empty times, hidden-activity rows and two large counts. Rows above 300
	// have no personal row at all, which is the missing case.
	f.exec(t, `DELETE FROM personal_items WHERE profile_id='p' AND item_id BETWEEN 1 AND 200`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating,play_count,last_played_at,activity_visible)
SELECT 'p',public_id,id,library_id,
 CASE WHEN id=1 OR id%23=0 THEN 0 ELSE (id%10)+1 END,
 CASE WHEN id=1 THEN 9223372036854775806 WHEN id=2 THEN 9223372036854775807 WHEN id%17=0 THEN 0 ELSE (id*3)%19+1 END,
 CASE WHEN id%19=0 THEN '' ELSE printf('2026-%02d-%02dT00:00:00Z',1+(id%12),1+(id%28)) END,
 CASE WHEN id%13=0 THEN 0 ELSE 1 END
FROM catalog_entities WHERE library_id=1 AND kind=1 AND id BETWEEN 1 AND 300`)
	// Another profile's values must never leak into this profile's order.
	f.exec(t, `UPDATE personal_items SET rating=10,play_count=100,last_played_at='2035-01-01T00:00:00Z' WHERE profile_id='q'`)
	return f
}

// END_RB_PERSONAL_PREPARE_JOURNAL

// rbPersonalPrepareAddViewer seeds a second local viewer outside the hashed
// fixture journal, so adding it does not alter the recorded statement plans. It
// returns that viewer's request anchor.
func rbPersonalPrepareAddViewer(t *testing.T, f *browseFixture, profile, account, token, device, family string, maximumAge, allowUnrated int64) BrowseInput {
	t.Helper()
	f.exec(t, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,x'00',?)`, account, account, profile)
	f.exec(t, `INSERT INTO direct_memberships(account_id,role,allowed_libraries) VALUES(?,'member','["movies"]')`, account)
	f.exec(t, `INSERT INTO direct_profiles(id,account_id,name) VALUES(?,?,?)`, profile, account, profile)
	f.exec(t, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?,?,?,?,'test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`, device, account, device, device)
	f.exec(t, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) SELECT ?,'s',?,?,'local','member',1,'2026-11-07T00:00:00Z',1,?,m.incarnation,p.lifetime,x'00800000000000000000000000000003',0 FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id=? AND p.id=?`, family, account, profile, device, account, profile)
	f.exec(t, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,1,'2026-11-07T00:00:00Z')`, token, family)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES(?,?,?)`, profile, maximumAge, allowUnrated)
	in := f.input
	in.TokenHash = token
	return in
}

func rbPersonalPrepareFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_personal_prepare_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_PERSONAL_PREPARE_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_PERSONAL_PREPARE_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}

func rbPersonalPrepareConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	cfg.Foreground.AssignedBytes += 20 << 20
	cfg.Foreground.EffectiveBytes += 20 << 20
	cfg.Foreground.NonBrowseBytes += 16 << 20
	protected := cfg.Partitions[governor.Foreground][governor.MemoryBytes]
	cfg.Partitions[governor.Foreground][governor.MemoryBytes] += 80 << 20
	cfg.Partitions[governor.Foreground][governor.WALBytes] += 1 << 20
	cfg.Partitions[governor.Background] = governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: protected}
	return cfg
}

func newRBPersonalPrepareFixture(t *testing.T) (*browseFixture, *RBPersonalPrepareReader) {
	t.Helper()
	defs := append(RBPersonalPrepareDefinitions(), RBPersonalTitleFilterDefinitions()...)
	f := composeReadFixture(t, newRBPersonalPrepareSource(t), defs, rbPersonalPrepareConfig(), false, rbPersonalPrepareFixtureHash(t))
	r, err := NewRBPersonalPrepareReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rbPersonalPrepareRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 394, ID: "rb-personal-prepare-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundReadBytes}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}

func rbPersonalPrepareRun(t *testing.T, job *RBPersonalPreparation) RBPersonalPrepareAdvance {
	t.Helper()
	var last RBPersonalPrepareAdvance
	for n := 0; n < 128; n++ {
		advance, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		last.Restarts += advance.Restarts
		last.RawRows += advance.RawRows
		last.Statements += advance.Statements
		if advance.Ready {
			last.Ready = true
			return last
		}
	}
	t.Fatal("personal preparation did not finish")
	return last
}

func rbPersonalPrepareRead(t *testing.T, f *browseFixture, job *RBPersonalPreparation, view BrowseInput, after *RBPersonalPrepareCursor, limit int) (RBPersonalPreparePage, BrowseReadResult, error) {
	t.Helper()
	live, request, output := liveRequests(fmt.Sprintf("rb-personal-prepare-page-%d", rbPersonalPreparePosition(after)))
	var page RBPersonalPreparePage
	var cost BrowseReadResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var err error
		page, cost, err = job.Page(ctx, request, time.Second, out, view, after, limit)
		return err
	})
	if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
		t.Fatal("personal page retained LIVE custody", u)
	}
	return page, cost, err
}

func rbPersonalPreparePosition(after *RBPersonalPrepareCursor) int {
	if after == nil {
		return 0
	}
	return after.position
}

// rbPersonalPrepareWant derives the complete expected order from plain SQL over
// the same fixture. It never calls the production comparator or recipes. It
// binds the viewer profile explicitly and applies the same own-age/unrated
// admission rule as the public predicate.
func rbPersonalPrepareWant(t *testing.T, f *browseFixture, profile string, order PersonalOrder, desc, keep bool) []int64 {
	t.Helper()
	column, condition, index := "p.rating", "p.rating>0", "personal_current_rating"
	if order == PersonalPlayCountOrder {
		column, condition, index = "p.play_count", "p.play_count>0", "personal_current_plays"
	}
	if order == PersonalPlayedOrder {
		column, condition, index = "p.last_played_at", "p.last_played_at>''", "personal_current_recent"
	}
	maxAge, allowUnrated := int64(-1), int64(1)
	if err := f.conn.QueryRowContext(t.Context(), `SELECT COALESCE(maximum_age,-1),COALESCE(allow_unrated,1) FROM profile_restrictions WHERE profile_id=?`, profile).Scan(&maxAge, &allowUnrated); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	admission := fmt.Sprintf(`(e.admission_own_age IS NOT NULL AND (%d < 0 OR e.admission_own_age<=%d) OR %d<>0 AND e.admission_own_age IS NULL)`, maxAge, maxAge, allowUnrated)
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	valued := `SELECT e.id FROM personal_items AS p INDEXED BY ` + index + ` JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id
WHERE p.profile_id=? AND e.library_id=1 AND e.kind=1 AND e.retired=0 AND ` + admission + ` AND ` + condition + `
ORDER BY ` + column + ` ` + direction + `,e.id ` + direction
	title := "e.sort_key"
	if keep {
		title = "e.sort_key_keep_articles"
	}
	rest := `SELECT e.id FROM catalog_entities AS e LEFT JOIN personal_items AS p ON p.profile_id=? AND p.item_id=e.id AND p.item_public_id=e.public_id
WHERE e.library_id=1 AND e.kind=1 AND e.retired=0 AND ` + admission + ` AND COALESCE(` + condition + `,0)=0
ORDER BY ` + title + ` COLLATE NOCASE ASC,e.id ASC`
	valuedIDs := rbPersonalPrepareQueryIDs(t, f, valued, profile)
	restIDs := rbPersonalPrepareQueryIDs(t, f, rest, profile)
	if desc {
		return append(valuedIDs, restIDs...)
	}
	return append(restIDs, valuedIDs...)
}

func rbPersonalPrepareQueryIDs(t *testing.T, f *browseFixture, query string, args ...any) []int64 {
	t.Helper()
	rows, err := f.conn.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func rbPersonalPrepareAllPages(t *testing.T, f *browseFixture, job *RBPersonalPreparation, in RBPersonalPrepareInput, want int, limit int) []int64 {
	t.Helper()
	var got []int64
	var after *RBPersonalPrepareCursor
	for pageNumber := 0; pageNumber < 500; pageNumber++ {
		page, cost, err := rbPersonalPrepareRead(t, f, job, in.View, after, limit)
		if err != nil {
			t.Fatalf("page %d order=%s desc=%v keep=%v: %v", pageNumber, in.Order, in.View.Descending, in.View.KeepArticles, err)
		}
		if cost.PublicationStatements < 5 || page.Statements != cost.PublicationStatements {
			t.Fatal("missing fresh publication evidence", page, cost)
		}
		if page.Total != int64(want) {
			t.Fatalf("page total is not the complete admitted set %d want %d order=%s desc=%v keep=%v", page.Total, want, in.Order, in.View.Descending, in.View.KeepArticles)
		}
		got = append(got, page.IDs...)
		if page.Exhausted {
			if page.After != nil {
				t.Fatal("terminal page has a cursor")
			}
			return got
		}
		if page.After == nil || len(page.IDs) == 0 {
			t.Fatal("unfinished page has no continuation", page)
		}
		after = page.After
	}
	t.Fatal("pages never exhausted")
	return got
}

func TestRBPersonalPrepareCompleteTraversal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		for _, order := range []PersonalOrder{PersonalRatingOrder, PersonalPlayedOrder, PersonalPlayCountOrder} {
			for _, desc := range []bool{false, true} {
				for _, keep := range []bool{false, true} {
					in := RBPersonalPrepareInput{View: f.input, Order: order}
					in.View.Descending = desc
					in.View.KeepArticles = keep
					job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
					if err != nil {
						t.Fatal(err)
					}
					done := rbPersonalPrepareRun(t, job)
					if !done.Ready || done.RawRows < 257 {
						t.Fatal("preparation did not exhaust the complete raw stream", done)
					}
					want := rbPersonalPrepareWant(t, f, "p", order, desc, keep)
					if len(want) < 257 {
						t.Fatal("test source is not a large personal set", len(want))
					}
					got := rbPersonalPrepareAllPages(t, f, job, in, len(want), 37)
					if !slices.Equal(got, want) {
						for i := range min(len(got), len(want)) {
							if got[i] != want[i] {
								t.Fatalf("order=%s desc=%v keep=%v i=%d got=%d want=%d", order, desc, keep, i, got[i], want[i])
							}
						}
						t.Fatal("wrong complete membership", order, desc, keep, len(got), len(want))
					}
					if err := job.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	})
}

func TestRBPersonalPrepareMissingZeroAndLargeIntegers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalPlayCountOrder}
		in.View.Descending = true
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbPersonalPrepareRun(t, job)
		want := rbPersonalPrepareWant(t, f, "p", in.Order, true, false)
		got := rbPersonalPrepareAllPages(t, f, job, in, len(want), 200)
		if !slices.Equal(got, want) {
			t.Fatal("missing/zero/large-integer order differs")
		}
		if len(want) < 2 || want[0] != 2 || want[1] != 1 {
			t.Fatalf("large 64-bit counts did not order first: %v", want[:min(3, len(want))])
		}
		// Explicit zeroes and missing personal rows are both the title-default
		// remainder, never the valued run.
		valued := rbPersonalPrepareQueryIDs(t, f, `SELECT e.id FROM personal_items AS p JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id WHERE p.profile_id='p' AND e.library_id=1 AND e.kind=1 AND e.retired=0 AND p.play_count>0`)
		zero := rbPersonalPrepareQueryIDs(t, f, `SELECT e.id FROM personal_items AS p JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id WHERE p.profile_id='p' AND e.library_id=1 AND e.kind=1 AND e.retired=0 AND p.play_count=0`)
		if len(zero) == 0 {
			t.Fatal("fixture has no zero play counts")
		}
		for _, id := range zero {
			if slices.Contains(got[:len(valued)], id) {
				t.Fatal("zero play count entered the valued run", id)
			}
		}
		// The valued prefix is exactly this profile's positive counts: no other
		// profile's row and no zero/missing row may take a valued place.
		gotValued, wantValued := slices.Clone(got[:len(valued)]), slices.Clone(valued)
		slices.Sort(gotValued)
		slices.Sort(wantValued)
		if !slices.Equal(gotValued, wantValued) {
			t.Fatalf("valued run is not this profile's current values: got %d want %d", len(gotValued), len(wantValued))
		}
	})
}

func TestRBPersonalPrepareEqualTitleRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		for _, keep := range []bool{false, true} {
			in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
			in.View.KeepArticles = keep
			job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
			if err != nil {
				t.Fatal(err)
			}
			rbPersonalPrepareRun(t, job)
			want := rbPersonalPrepareWant(t, f, "p", in.Order, false, keep)
			got := rbPersonalPrepareAllPages(t, f, job, in, len(want), 13)
			if !slices.Equal(got, want) {
				t.Fatal("equal/prefix title run changed across windows", keep)
			}
			var run []int64
			for _, id := range got {
				if id >= 400 && id <= 470 {
					run = append(run, id)
				}
			}
			sorted := slices.Clone(run)
			slices.Sort(sorted)
			wantRun := rbPersonalPrepareCount(t, f, `SELECT count(*) FROM catalog_entities WHERE library_id=1 AND kind=1 AND retired=0 AND id BETWEEN 400 AND 470`)
			if len(run) != wantRun || !slices.Equal(run, sorted) {
				t.Fatal("equal-title run is not in entity-ID order", keep, len(run), wantRun)
			}
			if err := job.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestRBPersonalPrepareSparseRawContinuation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id BETWEEN 250 AND 270`)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		in.View.Descending = true
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		done := rbPersonalPrepareRun(t, job)
		valuedRaw := rbPersonalPrepareCount(t, f, `SELECT count(*) FROM personal_items AS p JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id WHERE p.profile_id='p' AND e.library_id=1 AND e.kind=1 AND p.rating>0`)
		restRaw := rbPersonalPrepareCount(t, f, `SELECT count(*) FROM catalog_entities WHERE library_id=1 AND kind=1`)
		// Every raw row is fetched (the lower bound) and the only repeats are the
		// one-row lookahead each bounded window re-reads (the upper bound).
		if !done.Ready || done.RawRows < valuedRaw+restRaw || done.RawRows > valuedRaw+restRaw+done.Statements {
			t.Fatalf("rejected rows did not remain in the raw stream: %+v want [%d,%d]", done, valuedRaw+restRaw, valuedRaw+restRaw+done.Statements)
		}
		want := rbPersonalPrepareWant(t, f, "p", in.Order, true, false)
		got := rbPersonalPrepareAllPages(t, f, job, in, len(want), 200)
		if !slices.Equal(got, want) || len(want) >= int(done.RawRows) {
			t.Fatal("sparse raw windows truncated or padded the eligible set", len(got), len(want), done.RawRows)
		}
	})
}

func rbPersonalPrepareCount(t *testing.T, f *browseFixture, query string) int {
	t.Helper()
	var n int
	if err := f.conn.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRBPersonalPrepareCapacityBeforeAppend(t *testing.T) {
	// The fixed split charges the key-header array once and each value's text
	// before append. A value whose text would cross W refuses without a partial
	// write; nothing is copied or grown to make it fit.
	p := &RBPersonalPreparation{staged: make([]rbPersonalValuedKey, 0, 4), stagedCap: 128, stageIDs: make([]int64, 0, rbPersonalPrepareRaw)}
	if err := p.appendStaged(rbPersonalValuedKey{id: 1, text: strings.Repeat("a", 60)}); err != nil {
		t.Fatal(err)
	}
	if err := p.appendStaged(rbPersonalValuedKey{id: 2, text: strings.Repeat("b", 60)}); err != nil {
		t.Fatal(err)
	}
	if err := p.appendStaged(rbPersonalValuedKey{id: 3, text: strings.Repeat("c", 20)}); !errors.Is(err, errRBPersonalPrepareWorkspace) || len(p.staged) != 2 || p.stagedBytes != 120 {
		t.Fatal("workspace grew past its charge before refusing", err, len(p.staged), p.stagedBytes)
	}

	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 8192, in)
		if err != nil {
			t.Fatal(err)
		}
		var failure error
		for n := 0; n < 32 && failure == nil; n++ {
			advance, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second)
			if err != nil {
				failure = err
				break
			}
			if advance.Ready {
				t.Fatal("undersized admission published a truncated set")
			}
		}
		if !errors.Is(failure, errRBPersonalPrepareWorkspace) && !errors.Is(failure, errRBPreparationCapacity) {
			t.Fatal("capacity refusal missing", failure)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("capacity refusal retained a partial set or bytes")
		}
		if _, _, err := rbPersonalPrepareRead(t, f, job, f.input, nil, 40); err == nil {
			t.Fatal("closed preparation still paged")
		}
	})
}

func TestRBPersonalPrepareSameSnapshotPreflightRefusal(t *testing.T) {
	p := &RBPersonalPreparation{view: BrowseInput{Library: 1, Kind: 1}, order: PersonalPlayedOrder, copyLimit: 1 << 20, nativeLimit: 8 << 20, entry: &rbPreparedEntry{fence: browseFence{profile: "p"}}}
	var calls []string
	query := func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		if !strings.HasSuffix(name, ".bytes") {
			t.Fatal("payload requested before the byte preflight")
		}
		return [][]any{{int64(1), int64(9 << 20)}}, nil
	}
	rows, err := p.readRecentRows(query, p.view, "p", "rb.personal_prepare.recent.first")
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatal("recent octet preflight did not refuse before payload", rows, err, calls)
	}

	calls = nil
	p.copied = 0
	query = func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		if !strings.HasSuffix(name, ".bytes") {
			t.Fatal("title payload requested before the byte preflight")
		}
		return [][]any{{int64(1), int64(1), int64(3 << 20)}}, nil
	}
	rows, err = p.readRestRows(query, p.view, "p")
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatal("title octet preflight did not refuse before payload", rows, err, calls)
	}
}

func rbPersonalPrepareWriter(class governor.Class, now time.Time) governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 393, ID: "rb-personal-prepare-publication"}, Owner: 2, Class: class, Writer: true, MaxHold: 25 * time.Millisecond, Deadline: now.Add(time.Minute), Budget: governor.UnitBudget{RawRows: 1, VMOps: 1, Pages: 1}, Need: governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}, Footprint: []governor.Scope{{Library: 1}}}
}

func TestRBPersonalPrepareFirstHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		rbPersonalPrepareRun(t, job)
		if d := f.governor.Try(rbPersonalPrepareWriter(governor.Background, time.Now())); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.Conflict {
			t.Fatal("ready preparation released the parked writer before first handoff", d)
		}
		first, _, err := rbPersonalPrepareRead(t, f, job, f.input, nil, 40)
		if err != nil || len(first.IDs) != 40 || first.Exhausted {
			t.Fatal("first handoff", first, err)
		}
		d := f.governor.Try(rbPersonalPrepareWriter(governor.Background, time.Now()))
		if d.Grant == nil {
			t.Fatal("publication did not resume after first handoff", d.Refusal)
		}
		if err := f.authority.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		second, _, err := rbPersonalPrepareRead(t, f, job, f.input, first.After, 40)
		if err != nil || len(second.IDs) != 40 || second.IDs[0] == first.IDs[len(first.IDs)-1] {
			t.Fatal("later handoff replayed or skipped values", second, err)
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		if f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("disposal retained physical bytes")
		}
	})
}

func TestRBPersonalPrepareRestartFromInvalidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalPlayCountOrder}
		in.View.Descending = true
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		if _, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		original := job.entry.lease.Deadline()
		bytes := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		done := rbPersonalPrepareRun(t, job)
		if !done.Ready || done.Restarts != 1 {
			t.Fatalf("content invalidation did not restart exactly once: %+v", done)
		}
		if !job.entry.lease.Deadline().Equal(original) || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != bytes {
			t.Fatal("restart renewed the deadline or re-admitted physical bytes")
		}
		want := rbPersonalPrepareWant(t, f, "p", in.Order, true, false)
		got := rbPersonalPrepareAllPages(t, f, job, in, len(want), 200)
		if !slices.Equal(got, want) {
			t.Fatal("restart published a truncated, duplicated or mixed set", len(got), len(want))
		}
	})
}

func TestRBPersonalPrepareForegroundEditDenied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		d := f.governor.Try(rbPersonalPrepareWriter(governor.Foreground, time.Now()))
		if d.Grant == nil {
			t.Fatal(d.Refusal)
		}
		if err := f.authority.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second); !errors.Is(err, governor.ErrPreparationStale) {
			t.Fatal("security change restarted instead of denying", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied preparation retained values or bytes")
		}
	})
}

func TestRBPersonalPrepareOriginalDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(governor.PreparationLifetime + time.Second)
		if _, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second); !errors.Is(err, governor.ErrPreparationExpired) {
			t.Fatal("restart outlived the original deadline", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("expired preparation retained values or bytes")
		}
	})
}

func TestRBPersonalPreparePageDriftAndDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		rbPersonalPrepareRun(t, job)
		first, _, err := rbPersonalPrepareRead(t, f, job, f.input, nil, 40)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		if _, _, err := rbPersonalPrepareRead(t, f, job, f.input, first.After, 40); !errors.Is(err, ErrStale) {
			t.Fatal("page accepted a drifted fence", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("drifted page retained values or bytes")
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRBPersonalPreparePageForeignCursorRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		begin := func(order PersonalOrder) *RBPersonalPreparation {
			t.Helper()
			job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, RBPersonalPrepareInput{View: f.input, Order: order})
			if err != nil {
				t.Fatal(err)
			}
			rbPersonalPrepareRun(t, job)
			return job
		}
		byRating := begin(PersonalRatingOrder)
		page, _, err := rbPersonalPrepareRead(t, f, byRating, f.input, nil, 40)
		if err != nil || page.After == nil {
			t.Fatalf("first page: %+v %v", page, err)
		}
		foreign := *page.After
		if err := byRating.Close(); err != nil {
			t.Fatal(err)
		}
		byPlays := begin(PersonalPlayCountOrder)
		if _, _, err := rbPersonalPrepareRead(t, f, byPlays, f.input, &foreign, 40); !errors.Is(err, ErrStale) {
			t.Fatal("page accepted a foreign-order cursor", err)
		}
		if !byPlays.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("foreign-order cursor retained values or bytes")
		}
		if err := byPlays.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

// rbPersonalStepContext reports a cancellation after a fixed number of Err
// calls, so a window can fail mid-reduction without closing the entry.
type rbPersonalStepContext struct {
	context.Context
	calls, failAt int
}

func (c *rbPersonalStepContext) Err() error {
	c.calls++
	if c.calls >= c.failAt {
		return context.Canceled
	}
	return nil
}

// TestRBPersonalPrepareStagingAtomicPerWindow proves a mid-window error leaves
// no staged key behind while the raw cursor is unmoved, so the retry retains
// every ID exactly once.
func TestRBPersonalPrepareStagingAtomicPerWindow(t *testing.T) {
	p := &RBPersonalPreparation{
		order:     PersonalRatingOrder,
		view:      BrowseInput{Library: 1, Kind: 1},
		entry:     &rbPreparedEntry{fence: browseFence{maximumAge: -1, allowUnrated: 1}},
		staged:    make([]rbPersonalValuedKey, 0, 128),
		stagedCap: 1 << 20,
		stageIDs:  make([]int64, 0, rbPersonalPrepareRaw),
	}
	raw := make([][]any, rbPersonalPrepareRaw+1)
	for i := range raw {
		public := make([]byte, 16)
		public[0] = byte(i + 1)
		raw[i] = []any{int64(i + 1), int64(1), public, int64(0), int64(1), nil}
	}
	ctx := &rbPersonalStepContext{Context: t.Context(), failAt: 3}
	if err := p.reduceValuedWindow(ctx, raw); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-window cancellation was not returned: %v", err)
	}
	if len(p.staged) != 0 || p.stagedBytes != 0 || p.valuedAfter != nil {
		t.Fatalf("failed window kept a partial staged run or moved the cursor: %d %d %v", len(p.staged), p.stagedBytes, p.valuedAfter)
	}
	if err := p.reduceValuedWindow(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	if len(p.staged) != rbPersonalPrepareRaw {
		t.Fatalf("retry staged %d keys, want %d", len(p.staged), rbPersonalPrepareRaw)
	}
	seen := make(map[int64]struct{}, len(p.staged))
	for _, key := range p.staged {
		if _, duplicate := seen[key.id]; duplicate {
			t.Fatalf("retry duplicated staged id %d", key.id)
		}
		seen[key.id] = struct{}{}
	}
}

// rbPersonalPrepareHolding is the exact observable physical accounting of one
// fixed-split job. idsBytes and stagedBytes are the real allocation extents, not
// logical lengths, so a hidden copy/shrink/restore would show up immediately.
type rbPersonalPrepareHolding struct {
	payload, fence, idsBytes, stagedBytes int64
	idsCap, stagedCap                     int
	idsData, stagedData                   unsafe.Pointer
}

func rbPersonalPrepareCheckHolding(t *testing.T, job *RBPersonalPreparation) rbPersonalPrepareHolding {
	t.Helper()
	job.entry.cache.mu.Lock()
	defer job.entry.cache.mu.Unlock()
	if !job.entry.captured {
		t.Fatal("holding inspected before the fixed split was captured")
	}
	h := rbPersonalPrepareHolding{
		payload: job.entry.lease.PayloadBytes(),
		fence:   2 * rbPreparedFenceBytes(job.entry.fence),
		idsCap:  cap(job.entry.ids),
		idsData: unsafe.Pointer(unsafe.SliceData(job.entry.ids)),
	}
	h.idsBytes = int64(h.idsCap) * 8
	if job.staged != nil {
		h.stagedCap = cap(job.staged)
		h.stagedData = unsafe.Pointer(unsafe.SliceData(job.staged))
		h.stagedBytes = int64(cap(job.staged))*rbPersonalValuedKeyBytes + int64(cap(job.stageIDs))*8
		text := job.stagedBytes - h.stagedBytes
		if text < 0 {
			t.Fatalf("staged text accounting underflow: %d", text)
		}
		h.stagedBytes += text
	} else if job.stagedBytes != 0 || job.stageIDs != nil {
		t.Fatalf("staged accounting outlived its allocation: %d %v", job.stagedBytes, job.stageIDs)
	}
	if h.fence+h.idsBytes+h.stagedBytes > h.payload {
		t.Fatalf("physical peak exceeds the grant: fence=%d ids=%d staged=%d payload=%d", h.fence, h.idsBytes, h.stagedBytes, h.payload)
	}
	workspace := job.entry.workspace
	if workspace <= 0 {
		t.Fatal("fixed split has no workspace budget")
	}
	wantIDs := int((h.payload - workspace - h.fence) / 8)
	if h.idsCap != wantIDs {
		t.Fatalf("ids cap %d is not the fixed split cap_ids %d", h.idsCap, wantIDs)
	}
	if h.fence+h.idsBytes+workspace > h.payload {
		t.Fatalf("split exceeds the grant: fence=%d ids=%d W=%d payload=%d", h.fence, h.idsBytes, workspace, h.payload)
	}
	if h.stagedBytes > workspace {
		t.Fatalf("staged workspace %d exceeds W=%d", h.stagedBytes, workspace)
	}
	return h
}

// rbPersonalPrepareWindowStep runs exactly ONE admitted window so a test can
// observe the split between windows, where the old shrink-copy lived.
func rbPersonalPrepareWindowStep(t *testing.T, job *RBPersonalPreparation) (done bool) {
	t.Helper()
	var result RBPersonalPrepareAdvance
	if _, err := job.window(t.Context(), rbPersonalPrepareRequest(), time.Second, &result); err != nil {
		t.Fatal(err)
	}
	return job.complete
}

// TestRBPersonalPrepareAscendingFixedSplit is the reviewed case: an ascending
// personalRating walk retains the large unrated remainder first, then the first
// valued key allocates the workspace. The ID vector must keep its cap_ids and
// backing array from capture to Ready, and ids+staged+fences must stay <= the
// payload at every observed point. The old shrink-copy failed both.
func TestRBPersonalPrepareAscendingFixedSplit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		var first rbPersonalPrepareHolding
		var stagedCap int
		var stagedData unsafe.Pointer
		stagedSeen, ready := false, false
		for n := 0; n < 512 && !ready; n++ {
			ready = rbPersonalPrepareWindowStep(t, job)
			h := rbPersonalPrepareCheckHolding(t, job)
			if n == 0 {
				first = h
			} else if h.idsCap != first.idsCap || h.idsData != first.idsData {
				t.Fatalf("retained-ID vector changed after capture: cap %d->%d data %p->%p", first.idsCap, h.idsCap, first.idsData, h.idsData)
			}
			if job.staged != nil {
				if !stagedSeen {
					stagedSeen, stagedCap, stagedData = true, h.stagedCap, h.stagedData
					job.entry.cache.mu.Lock()
					rest := job.entry.total
					job.entry.cache.mu.Unlock()
					if rest <= 0 {
						t.Fatal("ascending valued workspace appeared before the unrated remainder was retained")
					}
				} else if h.stagedCap != stagedCap || h.stagedData != stagedData {
					t.Fatalf("staged workspace changed after allocation: cap %d->%d data %p->%p", stagedCap, h.stagedCap, stagedData, h.stagedData)
				}
			}
		}
		if !stagedSeen || !ready {
			t.Fatalf("ascending walk did not observe staging then readiness: staged=%v ready=%v", stagedSeen, ready)
		}
		final := rbPersonalPrepareCheckHolding(t, job)
		if final.idsCap != first.idsCap || final.idsData != first.idsData {
			t.Fatal("Ready copied or reallocated the retained-ID vector")
		}
		if job.staged != nil || job.stageIDs != nil {
			t.Fatal("ready preparation kept the staged workspace")
		}
		job.entry.cache.mu.Lock()
		total := job.entry.total
		job.entry.cache.mu.Unlock()
		if want := int64(len(rbPersonalPrepareWant(t, f, "p", in.Order, false, false))); total != want {
			t.Fatalf("ready total %d differs from the complete admitted set %d", total, want)
		}
	})
}

// TestRBPersonalPrepareDescendingFixedSplit covers valued-first ordering: the
// first window already stages keys, and the ID vector must be the fixed cap_ids
// (the old path shrank it to zero and then copied on every retain batch).
func TestRBPersonalPrepareDescendingFixedSplit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		in.View.Descending = true
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		var result RBPersonalPrepareAdvance
		if _, err := job.window(t.Context(), rbPersonalPrepareRequest(), time.Second, &result); err != nil {
			t.Fatal(err)
		}
		first := rbPersonalPrepareCheckHolding(t, job)
		if job.staged == nil || job.stageIDs == nil {
			t.Fatal("descending first window did not allocate the fixed valued workspace")
		}
		job.entry.cache.mu.Lock()
		total := job.entry.total
		job.entry.cache.mu.Unlock()
		if total != 0 {
			t.Fatal("descending rating prepared the remainder before the valued run")
		}
		stagedCap, stagedData := first.stagedCap, first.stagedData
		ready := false
		for n := 0; n < 128 && !ready; n++ {
			advance, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			h := rbPersonalPrepareCheckHolding(t, job)
			if h.idsCap != first.idsCap || h.idsData != first.idsData {
				t.Fatal("retained-ID vector changed after the fixed split")
			}
			if job.staged != nil && (h.stagedCap != stagedCap || h.stagedData != stagedData) {
				t.Fatal("staged workspace changed after its single allocation")
			}
			ready = advance.Ready
		}
		if !ready {
			t.Fatal("descending preparation did not finish")
		}
		if job.staged != nil || job.stageIDs != nil {
			t.Fatal("ready preparation kept the staged workspace")
		}
		job.entry.cache.mu.Lock()
		total = job.entry.total
		job.entry.cache.mu.Unlock()
		if want := int64(len(rbPersonalPrepareWant(t, f, "p", in.Order, true, false))); total != want {
			t.Fatalf("ready total %d differs from the complete admitted set %d", total, want)
		}
	})
}

// TestRBPersonalPrepareRestartFixedSplit invalidates content while the valued
// run is staged, then runs the remainder phase again. Attempt two must reuse the
// same cap_ids and W allocations, reset both lengths to zero, and stay inside
// the split through Ready.
func TestRBPersonalPrepareRestartFixedSplit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		var first rbPersonalPrepareHolding
		staged, stagedCap := false, 0
		for n := 0; n < 512 && !staged; n++ {
			if rbPersonalPrepareWindowStep(t, job) {
				t.Fatal("walk became ready before a restart was injected")
			}
			h := rbPersonalPrepareCheckHolding(t, job)
			if n == 0 {
				first = h
			} else if h.idsCap != first.idsCap || h.idsData != first.idsData {
				t.Fatal("retained-ID vector changed before the restart")
			}
			if job.staged != nil {
				staged, stagedCap = true, h.stagedCap
			}
		}
		if !staged {
			t.Fatal("restart was not injected inside the valued phase")
		}
		// The reset itself keeps both allocations and empties both lengths.
		job.restartAttempt()
		if job.staged == nil || len(job.staged) != 0 || job.stagedBytes != job.stagedFixedBytes() {
			t.Fatal("restart did not empty the staged length inside its fixed allocation")
		}
		job.entry.cache.mu.Lock()
		resetIDs, resetCap := len(job.entry.ids), cap(job.entry.ids)
		job.entry.cache.mu.Unlock()
		if resetIDs != 0 || resetCap != first.idsCap {
			t.Fatalf("restart did not reset the ID length inside cap_ids: len=%d cap=%d want %d", resetIDs, resetCap, first.idsCap)
		}
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		restarted, ready := false, false
		for n := 0; n < 128 && !ready; n++ {
			advance, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			h := rbPersonalPrepareCheckHolding(t, job)
			if h.idsCap != first.idsCap || h.idsData != first.idsData {
				t.Fatal("restart copied or reallocated the retained-ID vector")
			}
			if job.staged != nil && h.stagedCap != stagedCap {
				// W is allocated once for the whole job, including both attempts.
				t.Fatalf("restart resized the staged workspace: %d != %d", h.stagedCap, stagedCap)
			}
			if advance.Restarts > 0 && !restarted {
				restarted = true
				// Attempt two restarts at the remainder source; the reused W is
				// still allocated but empty.
				if job.staged == nil || len(job.staged) != 0 {
					t.Fatalf("restart kept attempt-one staged values: %d", len(job.staged))
				}
			}
			ready = advance.Ready
		}
		if !restarted || !ready {
			t.Fatalf("restart did not complete: restarted=%v ready=%v", restarted, ready)
		}
		if job.staged != nil || job.stageIDs != nil {
			t.Fatal("ready preparation kept the staged workspace after restart")
		}
		job.entry.cache.mu.Lock()
		total := job.entry.total
		job.entry.cache.mu.Unlock()
		if want := int64(len(rbPersonalPrepareWant(t, f, "p", in.Order, false, false))); total != want {
			t.Fatalf("restart published a truncated or mixed set: %d != %d", total, want)
		}
	})
}

// TestRBPersonalPrepareDropsWorkspaceOnCloseAndDenial proves a retired lease
// keeps neither the W region nor the reader's job slot, on both Close and the
// viewer-denial path.
func TestRBPersonalPrepareDropsWorkspaceOnCloseAndDenial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		stage := func(in RBPersonalPrepareInput) *RBPersonalPreparation {
			t.Helper()
			job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
			if err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 512; n++ {
				if rbPersonalPrepareWindowStep(t, job) {
					t.Fatal("walk became ready before the workspace was observed")
				}
				if job.staged != nil && len(job.staged) > 0 {
					return job
				}
			}
			t.Fatal("workspace was never staged")
			return nil
		}
		assertDropped := func(job *RBPersonalPreparation) {
			t.Helper()
			if job.staged != nil || job.stageIDs != nil || job.stagedBytes != 0 || job.stagedCap != 0 {
				t.Fatalf("job kept staged state after retirement: %v %v %d %d", job.staged, job.stageIDs, job.stagedBytes, job.stagedCap)
			}
			for _, held := range job.reader.jobs {
				if held == job {
					t.Fatal("reader slot still references the retired job")
				}
			}
			if !job.entry.closed {
				t.Fatal("retired job entry is still open")
			}
			if usage := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; usage != 0 {
				t.Fatalf("retired lease still holds %d bytes", usage)
			}
		}
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		job := stage(in)
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		assertDropped(job)

		job = stage(in)
		f.exec(t, `INSERT INTO viewer_revisions(profile_id,library_id,revision) VALUES('p','movies',1) ON CONFLICT(profile_id,library_id) DO UPDATE SET revision=revision+1`)
		if _, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second); !errors.Is(err, ErrDenied) {
			t.Fatal("viewer edit did not deny", err)
		}
		assertDropped(job)
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

// TestRBPersonalPrepareFixedSplitRefusals proves each half of the split refuses
// by its own named error without a truncated order or retained values.
func TestRBPersonalPrepareFixedSplitRefusals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A small W cannot hold the valued run: the workspace error, never a
		// truncated exact order.
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		in.View.Descending = true
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 40960, in)
		if err != nil {
			t.Fatal(err)
		}
		var failure error
		for n := 0; n < 128 && failure == nil; n++ {
			advance, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second)
			if err != nil {
				failure = err
				break
			}
			if advance.Ready {
				t.Fatal("undersized valued workspace published a truncated set")
			}
		}
		if !errors.Is(failure, errRBPersonalPrepareWorkspace) {
			t.Fatalf("valued run above W refused with %v", failure)
		}
		if !job.entry.closed || job.staged != nil || job.stageIDs != nil || job.entry.ids != nil {
			t.Fatal("workspace refusal kept a partial set")
		}
		if usage := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; usage != 0 {
			t.Fatalf("workspace refusal retained %d bytes", usage)
		}
		if _, _, err := rbPersonalPrepareRead(t, f, job, f.input, nil, 40); err == nil {
			t.Fatal("closed preparation still paged")
		}

		// A small cap_ids cannot hold the unrated remainder: the existing named
		// capacity error, never a truncated order.
		f, r = newRBPersonalPrepareFixture(t)
		job, err = r.BeginRBPersonalPrepare(t.Context(), 1, 12288, RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder})
		if err != nil {
			t.Fatal(err)
		}
		failure = nil
		for n := 0; n < 128 && failure == nil; n++ {
			advance, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second)
			if err != nil {
				failure = err
				break
			}
			if advance.Ready {
				t.Fatal("undersized ID vector published a truncated set")
			}
		}
		if !errors.Is(failure, errRBPreparationCapacity) {
			t.Fatalf("remainder above cap_ids refused with %v", failure)
		}
		if !job.entry.closed || job.staged != nil || job.stageIDs != nil || job.entry.ids != nil {
			t.Fatal("capacity refusal kept a partial set")
		}
		if usage := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; usage != 0 {
			t.Fatalf("capacity refusal retained %d bytes", usage)
		}
	})
}

// TestRBPersonalPrepareViewerEditDenied records the decided outcome for personal
// facts changing mid-walk: viewer_revision is part of the pinned authorization
// context, so the walk denies instead of restarting on stale personal values.
func TestRBPersonalPrepareViewerEditDenied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		in := RBPersonalPrepareInput{View: f.input, Order: PersonalRatingOrder}
		in.View.Descending = true
		job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		f.exec(t, `INSERT INTO viewer_revisions(profile_id,library_id,revision) VALUES('p','movies',1) ON CONFLICT(profile_id,library_id) DO UPDATE SET revision=revision+1`)
		if _, err := job.Advance(t.Context(), rbPersonalPrepareRequest(), time.Second); !errors.Is(err, ErrDenied) {
			t.Fatal("personal-value edit restarted instead of denying", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("viewer denial retained values or bytes")
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

// TestRBPersonalPrepareSecondViewerIsolation binds another viewer profile to the
// same library and proves each profile sees only its own personal rows, with the
// oracle applying the same own-age/unrated admission rule.
func TestRBPersonalPrepareSecondViewerIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBPersonalPrepareFixture(t)
		viewR := rbPersonalPrepareAddViewer(t, f, "r", "c", "token-r", "d-r", "f-r", 10, 0)
		// Profile r has different values from p.
		f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating,play_count,last_played_at,activity_visible)
SELECT 'r',public_id,id,library_id,(id%7)+1,0,'',1 FROM catalog_entities WHERE library_id=1 AND kind=1 AND id BETWEEN 1 AND 250`)
		// The same entity is age-restricted for r but visible to unrestricted p.
		f.exec(t, `UPDATE catalog_entities SET admission_own_age=99 WHERE id=120`)
		f.exec(t, `UPDATE personal_items SET rating=10 WHERE item_id=120 AND profile_id IN ('p','r')`)
		run := func(profile string, view BrowseInput) []int64 {
			t.Helper()
			in := RBPersonalPrepareInput{View: view, Order: PersonalRatingOrder}
			in.View.Descending = true
			job, err := r.BeginRBPersonalPrepare(t.Context(), 1, 4<<20, in)
			if err != nil {
				t.Fatal(err)
			}
			defer job.Close()
			done := rbPersonalPrepareRun(t, job)
			if !done.Ready {
				t.Fatalf("viewer %s did not finish: %+v", profile, done)
			}
			want := rbPersonalPrepareWant(t, f, profile, in.Order, true, false)
			got := rbPersonalPrepareAllPages(t, f, job, in, len(want), 40)
			if !slices.Equal(got, want) {
				t.Fatalf("viewer %s order differs from the profile-bound oracle: got %v want %v", profile, got, want)
			}
			return got
		}
		gotP, gotR := run("p", f.input), run("r", viewR)
		if !slices.Contains(gotP, int64(120)) {
			t.Fatal("unrestricted viewer lost a visible valued row")
		}
		if slices.Contains(gotR, int64(120)) {
			t.Fatal("age-restricted viewer admitted an own-age row above its maximum")
		}
		if slices.Equal(gotP, gotR) {
			t.Fatal("two viewers with different personal rows published the same order")
		}
	})
}
