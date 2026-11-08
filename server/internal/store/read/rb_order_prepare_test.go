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

// BEGIN_RB_ORDER_PREPARE_JOURNAL
func newRBOrderPrepareSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	// Equal keys (A/a) and prefix keys (a/aa/ab) that straddle 64-row windows,
	// plus a preserved article-policy variant. Rejected rows (retired, age) stay
	// in the raw stream.
	f.exec(t, `UPDATE catalog_entities SET
		sort_key=CASE id%6 WHEN 0 THEN 'A' WHEN 1 THEN 'a' WHEN 2 THEN 'aa' WHEN 3 THEN 'ab' WHEN 4 THEN 'A' ELSE '' END,
		sort_key_keep_articles=CASE id%6 WHEN 0 THEN 'The A' WHEN 1 THEN 'the a' WHEN 2 THEN 'The Aa' WHEN 3 THEN 'The Ab' WHEN 4 THEN 'the A' ELSE '' END
		WHERE library_id=1`)
	for i := range f.facts {
		if f.facts[i].id == 1040 {
			continue
		}
		switch f.facts[i].id % 6 {
		case 0:
			f.facts[i].key, f.facts[i].keep = "A", "The A"
		case 1:
			f.facts[i].key, f.facts[i].keep = "a", "the a"
		case 2:
			f.facts[i].key, f.facts[i].keep = "aa", "The Aa"
		case 3:
			f.facts[i].key, f.facts[i].keep = "ab", "The Ab"
		case 4:
			f.facts[i].key, f.facts[i].keep = "A", "the A"
		default:
			f.facts[i].key, f.facts[i].keep = "", ""
		}
	}
	return f
}

// END_RB_ORDER_PREPARE_JOURNAL

func rbOrderPrepareFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_order_prepare_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_ORDER_PREPARE_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_ORDER_PREPARE_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}

func rbOrderPrepareConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	// Separate PHASE2 logical test profile. Prep 16MiB is included in non-LIVE
	// demand; M grows 20MiB to fund it plus its 20% contingency. This arithmetic
	// is not a whole-server/2GB physical qualification.
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

func newRBOrderPrepareFixture(t *testing.T) (*browseFixture, *RBOrderPrepareReader) {
	t.Helper()
	defs := append(RBOrderPrepareDefinitions(), RBPersonalTitleFilterDefinitions()...)
	f := composeReadFixture(t, newRBOrderPrepareSource(t), defs, rbOrderPrepareConfig(), false, rbOrderPrepareFixtureHash(t))
	r, err := NewRBOrderPrepareReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rbOrderPrepareRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 396, ID: "rb-order-prepare-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}

func rbOrderPrepareRun(t *testing.T, job *RBOrderPreparation) RBOrderPrepareAdvance {
	t.Helper()
	var last RBOrderPrepareAdvance
	for n := 0; n < 64; n++ {
		advance, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second)
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
	t.Fatal("preparation did not finish")
	return last
}

func rbOrderPrepareRead(t *testing.T, f *browseFixture, job *RBOrderPreparation, view BrowseInput, after *RBOrderPrepareCursor, limit int) (RBOrderPreparePage, BrowseReadResult, error) {
	t.Helper()
	live, request, output := liveRequests(fmt.Sprintf("rb-order-prepare-page-%d", afterPosition(after)))
	var page RBOrderPreparePage
	var cost BrowseReadResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var err error
		page, cost, err = job.Page(ctx, request, time.Second, out, view, after, limit)
		return err
	})
	if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
		t.Fatal("ordered page retained LIVE custody", u)
	}
	return page, cost, err
}

func afterPosition(after *RBOrderPrepareCursor) int {
	if after == nil {
		return 0
	}
	return after.position
}

func TestRBOrderPrepareCompleteTraversal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		for _, key := range []RBOrderKey{RBOrderByID, RBOrderByTitle} {
			for _, desc := range []bool{false, true} {
				in := RBOrderPrepareInput{View: f.input, Key: key}
				in.View.Descending = desc
				job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, in)
				if err != nil {
					t.Fatal(err)
				}
				done := rbOrderPrepareRun(t, job)
				if !done.Ready || done.RawRows < 257 {
					t.Fatal("preparation did not exhaust the complete raw stream", done)
				}
				var got []int64
				var after *RBOrderPrepareCursor
				for pageNumber := 0; pageNumber < 20; pageNumber++ {
					page, cost, err := rbOrderPrepareRead(t, f, job, in.View, after, 200)
					if err != nil {
						t.Fatalf("page %d key=%d desc=%v: %v", pageNumber, key, desc, err)
					}
					if cost.PublicationStatements < 5 || page.Statements != cost.PublicationStatements {
						t.Fatal("missing fresh publication evidence", page, cost)
					}
					got = append(got, page.IDs...)
					if page.Exhausted {
						if page.After != nil {
							t.Fatal("terminal page has a cursor")
						}
						after = nil
						break
					}
					if page.After == nil || len(page.IDs) == 0 {
						t.Fatal("unfinished page has no continuation", page)
					}
					after = page.After
				}
				if after != nil {
					t.Fatal("pages never exhausted")
				}
				want := rbOrderPrepareWant(t, f, key, desc)
				if len(got) != len(want) {
					t.Fatal("wrong complete membership", len(got), len(want))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("wrong ordered ID key=%d desc=%v i=%d got=%d want=%d", key, desc, i, got[i], want[i])
					}
				}
				if err := job.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

func rbOrderPrepareWant(t *testing.T, f *browseFixture, key RBOrderKey, desc bool) []int64 {
	t.Helper()
	var want []int64
	for _, fact := range f.facts {
		if fact.retired || fact.id == 1040 {
			continue
		}
		want = append(want, fact.id)
	}
	if key == RBOrderByTitle {
		slices.SortFunc(want, func(a, b int64) int {
			ka, kb := rbOrderPrepareFactKey(f, a, false), rbOrderPrepareFactKey(f, b, false)
			if c := strings.Compare(rbPersonalASCIIFold(ka), rbPersonalASCIIFold(kb)); c != 0 {
				return c
			}
			return compareInt64(a, b)
		})
	} else {
		slices.Sort(want)
	}
	if desc {
		slices.Reverse(want)
	}
	return want
}

func rbOrderPrepareFactKey(f *browseFixture, id int64, keep bool) string {
	for _, fact := range f.facts {
		if fact.id == id {
			if keep {
				return fact.keep
			}
			return fact.key
		}
	}
	return ""
}

func TestRBOrderPrepareEqualAndPrefixKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		for _, keep := range []bool{false, true} {
			in := RBOrderPrepareInput{View: f.input, Key: RBOrderByTitle}
			in.View.KeepArticles = keep
			job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, in)
			if err != nil {
				t.Fatal(err)
			}
			rbOrderPrepareRun(t, job)
			var got []int64
			var after *RBOrderPrepareCursor
			for n := 0; n < 80; n++ {
				page, _, err := rbOrderPrepareRead(t, f, job, in.View, after, 13)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, page.IDs...)
				if page.Exhausted {
					break
				}
				if page.After == nil {
					t.Fatal("lost cursor across an equal/prefix key run")
				}
				after = page.After
			}
			want := rbOrderPrepareWant(t, f, RBOrderByTitle, false)
			if len(got) != len(want) {
				t.Fatal("equal/prefix stream lost or duplicated rows", keep, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatal("equal/prefix order changed", keep, i, got[i], want[i])
				}
			}
			if err := job.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestRBOrderPrepareSparseRawContinuation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, RBOrderPrepareInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		done := rbOrderPrepareRun(t, job)
		rawFacts := 0
		for _, fact := range f.facts {
			if fact.id != 1040 {
				rawFacts++
			}
		}
		if !done.Ready || done.RawRows != rawFacts {
			t.Fatalf("rejected rows did not remain in the raw stream: %+v want %d", done, rawFacts)
		}
		page, _, err := rbOrderPrepareRead(t, f, job, f.input, nil, 200)
		if err != nil || page.Total < 257 {
			t.Fatalf("sparse continuation total: %+v %v", page, err)
		}
		var total int64
		var after *RBOrderPrepareCursor
		for n := 0; n < 20; n++ {
			if n > 0 {
				page, _, err = rbOrderPrepareRead(t, f, job, f.input, after, 200)
				if err != nil {
					t.Fatal(err)
				}
			}
			total += int64(len(page.IDs))
			if page.Exhausted {
				break
			}
			after = page.After
		}
		if total != 977 || total >= int64(done.RawRows) {
			t.Fatal("sparse raw window truncated the eligible set", total, done.RawRows)
		}
	})
}

func TestRBOrderPrepareCapacityBeforeAppend(t *testing.T) {
	workspace := rbOrderWorkspace{rows: make([]rbOrderSourceKey, 0, 2)}
	if err := workspace.add(rbOrderSourceKey{id: 1}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.add(rbOrderSourceKey{id: 2}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.add(rbOrderSourceKey{id: 3}); !errors.Is(err, errRBOrderWorkspace) || len(workspace.rows) != 2 {
		t.Fatal("workspace grew past capacity before refusing", err, len(workspace.rows))
	}

	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		job, err := r.BeginRBOrderPrepare(t.Context(), 1, 8192, RBOrderPrepareInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		var failure error
		for n := 0; n < 32 && failure == nil; n++ {
			advance, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second)
			if err != nil {
				failure = err
				break
			}
			if advance.Ready {
				t.Fatal("undersized admission published a truncated set")
			}
		}
		if !errors.Is(failure, errRBPreparationCapacity) {
			t.Fatal("capacity refusal missing", failure)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("capacity refusal retained a partial set or bytes")
		}
		if _, _, err := rbOrderPrepareRead(t, f, job, f.input, nil, 40); err == nil {
			t.Fatal("closed preparation still paged")
		}
	})
}

func TestRBOrderPrepareCrossPollCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		job, err := r.BeginRBOrderPrepare(ctx, 1, 1<<20, RBOrderPrepareInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled preparation poll continued", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("cross-poll cancellation retained values or bytes")
		}
	})
}

func rbOrderPrepareWriter(class governor.Class, now time.Time) governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 395, ID: "rb-order-prepare-publication"}, Owner: 2, Class: class, Writer: true, MaxHold: 25 * time.Millisecond, Deadline: now.Add(time.Minute), Budget: governor.UnitBudget{RawRows: 1, VMOps: 1, Pages: 1}, Need: governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}, Footprint: []governor.Scope{{Library: 1}}}
}

func TestRBOrderPrepareFirstHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, RBOrderPrepareInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		rbOrderPrepareRun(t, job)
		if d := f.governor.Try(rbOrderPrepareWriter(governor.Background, time.Now())); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.Conflict {
			t.Fatal("ready preparation released the parked writer before first handoff", d)
		}
		first, _, err := rbOrderPrepareRead(t, f, job, f.input, nil, 40)
		if err != nil || len(first.IDs) != 40 || first.Exhausted {
			t.Fatal("first handoff", first, err)
		}
		d := f.governor.Try(rbOrderPrepareWriter(governor.Background, time.Now()))
		if d.Grant == nil {
			t.Fatal("publication did not resume after first handoff", d.Refusal)
		}
		if err := f.authority.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		second, _, err := rbOrderPrepareRead(t, f, job, f.input, first.After, 40)
		if err != nil || len(second.IDs) != 40 || second.IDs[0] <= first.IDs[len(first.IDs)-1] {
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

func TestRBOrderPrepareRestartFromInvalidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, RBOrderPrepareInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		if _, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		original := job.entry.lease.Deadline()
		bytes := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		done := rbOrderPrepareRun(t, job)
		if !done.Ready || done.Restarts != 1 {
			t.Fatalf("content invalidation did not restart exactly once: %+v", done)
		}
		if !job.entry.lease.Deadline().Equal(original) || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != bytes {
			t.Fatal("restart renewed the deadline or re-admitted physical bytes")
		}
		got := make([]int64, 0, 977)
		var after *RBOrderPrepareCursor
		for n := 0; n < 20; n++ {
			page, _, err := rbOrderPrepareRead(t, f, job, f.input, after, 200)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, page.IDs...)
			if page.Exhausted {
				break
			}
			after = page.After
		}
		want := rbOrderPrepareWant(t, f, RBOrderByID, false)
		if len(got) != len(want) {
			t.Fatal("restart published a truncated or duplicated set", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatal("restart published mixed content", i, got[i], want[i])
			}
		}
	})
}

func TestRBOrderPrepareForegroundEditDenied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, RBOrderPrepareInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		d := f.governor.Try(rbOrderPrepareWriter(governor.Foreground, time.Now()))
		if d.Grant == nil {
			t.Fatal(d.Refusal)
		}
		if err := f.authority.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second); !errors.Is(err, governor.ErrPreparationStale) {
			t.Fatal("security change restarted instead of denying", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied preparation retained values or bytes")
		}
	})
}

func TestRBOrderPrepareOriginalDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, RBOrderPrepareInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(governor.PreparationLifetime + time.Second)
		if _, err := job.Advance(t.Context(), rbOrderPrepareRequest(), time.Second); !errors.Is(err, governor.ErrPreparationExpired) {
			t.Fatal("restart outlived the original deadline", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("expired preparation retained values or bytes")
		}
	})
}

func TestRBOrderPrepareSameSnapshotPreflightRefusal(t *testing.T) {
	p := &RBOrderPreparation{view: BrowseInput{Library: 1, Kind: 1}, key: RBOrderByTitle, copyLimit: 1 << 20, nativeLimit: 8 << 20, entry: &rbPreparedEntry{fence: browseFence{profile: "p"}}}
	if p.after != nil {
		t.Fatal("unexpected cursor")
	}
	var calls []string
	query := func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		if !strings.HasSuffix(name, ".bytes") {
			t.Fatal("payload requested before the byte preflight")
		}
		return [][]any{{int64(1), int64(1), int64(3 << 20)}}, nil
	}
	rows, err := p.readTitleRows(query, p.view)
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatal("octet preflight did not refuse before payload", rows, err, calls)
	}
	calls = nil
	p.copied = 0
	query = func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		return [][]any{{int64(1), int64(1), int64(9 << 20)}}, nil
	}
	rows, err = p.readTitleRows(query, p.view)
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatal("native row bound was not enforced", rows, err, calls)
	}
}

func TestRBOrderPreparePageDriftAndDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, RBOrderPrepareInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		rbOrderPrepareRun(t, job)
		first, _, err := rbOrderPrepareRead(t, f, job, f.input, nil, 40)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		if _, _, err := rbOrderPrepareRead(t, f, job, f.input, first.After, 40); !errors.Is(err, ErrStale) {
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

func TestRBOrderPreparePageForeignCursorRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBOrderPrepareFixture(t)
		begin := func(key RBOrderKey) *RBOrderPreparation {
			t.Helper()
			job, err := r.BeginRBOrderPrepare(t.Context(), 1, 1<<20, RBOrderPrepareInput{View: f.input, Key: key})
			if err != nil {
				t.Fatal(err)
			}
			rbOrderPrepareRun(t, job)
			return job
		}
		firstPage := func(job *RBOrderPreparation) *RBOrderPrepareCursor {
			t.Helper()
			page, _, err := rbOrderPrepareRead(t, f, job, f.input, nil, 40)
			if err != nil || page.After == nil {
				t.Fatalf("first page: %+v %v", page, err)
			}
			return page.After
		}
		// A cursor naming another order source is refused, never replayed. One
		// preparation per owner is funded at a time, so the foreign cursor is
		// captured, its job retired, and the visiting job begins afterwards.
		byTitle := begin(RBOrderByTitle)
		foreign := *firstPage(byTitle)
		if err := byTitle.Close(); err != nil {
			t.Fatal(err)
		}
		byID := begin(RBOrderByID)
		if _, _, err := rbOrderPrepareRead(t, f, byID, f.input, &foreign, 40); !errors.Is(err, ErrStale) {
			t.Fatal("page accepted a foreign-query cursor", err)
		}
		if !byID.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("foreign-query cursor retained values or bytes")
		}
		// A cursor carrying a fence this publication never observed is refused
		// even for its own source, so a stale token/horizon cannot page values.
		job := begin(RBOrderByID)
		foreign = *firstPage(job)
		foreign.fence.horizon = "2000-01-01T00:00:00Z"
		if _, _, err := rbOrderPrepareRead(t, f, job, f.input, &foreign, 40); !errors.Is(err, ErrStale) {
			t.Fatal("page accepted a foreign-fence cursor", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("foreign-fence cursor retained values or bytes")
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRBOrderPrepareTitleOrderMatchesSQLiteNoCase(t *testing.T) {
	// The expectation below comes from SQLite's own `ORDER BY ... COLLATE
	// NOCASE, id`, never from the production fold. Keys without an embedded NUL
	// are the reachable domain: sqlite3_strnicmp stops comparing at a NUL.
	f := newBrowseFixture(t)
	f.exec(t, `CREATE TEMP TABLE rb_order_oracle(id INTEGER PRIMARY KEY, key TEXT NOT NULL)`)
	keys := []string{"", " ", "A", "a", "AA", "aa", "ab", "ab c", "The A", "the a", "z", "Z", "~", "\u007f", "a!", "a0", "a_", "a\u00e9", "A\u00e9", "a\u0301", "\u00e9", "\u00c9", "\u00df", "\u03c3", "\u03a3", "\u4e2d", "\ufb01", "\U0001f600"}
	for i, key := range keys {
		f.exec(t, `INSERT INTO rb_order_oracle(id,key) VALUES(?,?)`, int64(i+1), key)
	}
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id, key FROM rb_order_oracle ORDER BY key COLLATE NOCASE, id`)
	if err != nil {
		t.Fatal(err)
	}
	var want []rbOrderSourceKey
	for rows.Next() {
		var row rbOrderSourceKey
		if err = rows.Scan(&row.id, &row.key); err != nil {
			t.Fatal(err)
		}
		want = append(want, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Clone(want)
	slices.SortFunc(got, func(a, b rbOrderSourceKey) int {
		return rbOrderCompare(a, b, RBOrderByTitle)
	})
	if !slices.Equal(got, want) {
		t.Fatal("Go title order differs from SQLite NOCASE", got, want)
	}
}
