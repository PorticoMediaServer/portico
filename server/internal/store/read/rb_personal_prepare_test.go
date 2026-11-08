package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

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
	return governor.Request{Key: governor.JobKey{Domain: 394, ID: "rb-personal-prepare-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
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
// the same fixture. It never calls the production comparator or recipes.
func rbPersonalPrepareWant(t *testing.T, f *browseFixture, order PersonalOrder, desc, keep bool) []int64 {
	t.Helper()
	column, condition, index := "p.rating", "p.rating>0", "personal_current_rating"
	if order == PersonalPlayCountOrder {
		column, condition, index = "p.play_count", "p.play_count>0", "personal_current_plays"
	}
	if order == PersonalPlayedOrder {
		column, condition, index = "p.last_played_at", "p.last_played_at>''", "personal_current_recent"
	}
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	valued := `SELECT e.id FROM personal_items AS p INDEXED BY ` + index + ` JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id
WHERE p.profile_id='p' AND e.library_id=1 AND e.kind=1 AND e.retired=0 AND ` + condition + `
ORDER BY ` + column + ` ` + direction + `,e.id ` + direction
	title := "e.sort_key"
	if keep {
		title = "e.sort_key_keep_articles"
	}
	rest := `SELECT e.id FROM catalog_entities AS e LEFT JOIN personal_items AS p ON p.profile_id='p' AND p.item_id=e.id AND p.item_public_id=e.public_id
WHERE e.library_id=1 AND e.kind=1 AND e.retired=0 AND COALESCE(` + condition + `,0)=0
ORDER BY ` + title + ` COLLATE NOCASE ASC,e.id ASC`
	valuedIDs := rbPersonalPrepareQueryIDs(t, f, valued)
	restIDs := rbPersonalPrepareQueryIDs(t, f, rest)
	if desc {
		return append(valuedIDs, restIDs...)
	}
	return append(restIDs, valuedIDs...)
}

func rbPersonalPrepareQueryIDs(t *testing.T, f *browseFixture, query string) []int64 {
	t.Helper()
	rows, err := f.conn.QueryContext(t.Context(), query)
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
			t.Fatal("page total is not the complete admitted set", page.Total, want)
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
					want := rbPersonalPrepareWant(t, f, order, desc, keep)
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
		want := rbPersonalPrepareWant(t, f, in.Order, true, false)
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
		if slices.Contains(valued, int64(1)) {
			t.Fatal("zero play count entered the valued run")
		}
		// The other profile's large count and rating never leak in.
		if slices.Contains(got[:len(valued)], int64(1)) {
			t.Fatal("another profile's values leaked into the valued run")
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
			want := rbPersonalPrepareWant(t, f, in.Order, false, keep)
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
			if len(run) != 71 || !slices.Equal(run, sorted) {
				t.Fatal("equal-title run is not in entity-ID order", keep, len(run))
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
		if !done.Ready || done.RawRows != valuedRaw+restRaw {
			t.Fatalf("rejected rows did not remain in the raw stream: %+v want %d", done, valuedRaw+restRaw)
		}
		want := rbPersonalPrepareWant(t, f, in.Order, true, false)
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
	p := &RBPersonalPreparation{staged: make([]rbPersonalValuedKey, 0, 4), stagedCap: 128, stageIDs: make([]int64, 0, rbPersonalPrepareRaw)}
	if err := p.appendStaged(rbPersonalValuedKey{id: 1}); err != nil {
		t.Fatal(err)
	}
	if err := p.appendStaged(rbPersonalValuedKey{id: 2}); err != nil {
		t.Fatal(err)
	}
	if err := p.appendStaged(rbPersonalValuedKey{id: 3}); !errors.Is(err, errRBPersonalPrepareWorkspace) || len(p.staged) != 2 {
		t.Fatal("workspace grew past its charge before refusing", err, len(p.staged))
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
		want := rbPersonalPrepareWant(t, f, in.Order, true, false)
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
