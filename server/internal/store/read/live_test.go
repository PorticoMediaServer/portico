package read

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// Explicit test composition, not a default profile or physical-fit claim.
func newLiveFixture(t *testing.T, clocks ...func() time.Time) (*browseFixture, *ForegroundReader) {
	t.Helper()
	now := time.Now
	if len(clocks) > 0 {
		now = clocks[0]
	}
	gc := readfixture.LiveConfig(now)
	f := composeReadFixture(t, newForegroundSource(t), HarnessDefinitions(), gc, false)
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func liveRequests(id string) (governor.ForegroundRequest, governor.Request, governor.Request) {
	entry := time.Now()
	q, request, output := readfixture.Requests(id, entry)
	q.ReadyDeadline = entry.Add(functionalReadTimeout)
	q.ProtectedDeadline = entry.Add(2 * functionalReadTimeout)
	request.Deadline, output.Deadline = q.ProtectedDeadline, q.ProtectedDeadline
	return q, request, output
}
func assertLiveUsage(t *testing.T, f *browseFixture, live int, readers int64) {
	t.Helper()
	if got := f.authority.ForegroundUsage(); got.Live != live || got.Waiters != 0 {
		t.Fatalf("live custody %+v want%d", got, live)
	}
	usage := f.authority.Usage()[governor.Foreground]
	if usage[governor.Reader] != readers || usage[governor.MemoryBytes] != int64(live)*((8<<20)+4096) || usage[governor.QueueBytes] != int64(live)*4096 {
		t.Fatalf("payload/metadata custody %+v live%d readers%d", usage, live, readers)
	}
}

func TestLivePublicReadersRequireAdmission(t *testing.T) {
	f, r := newLiveFixture(t)
	q, request, output := liveRequests("required")
	_ = q
	_ = output
	for _, recipe := range []string{"browse", "detail", "playback", "continue"} {
		var e error
		var result BrowseReadResult
		switch recipe {
		case "browse":
			var page BrowsePage
			page, result, e = f.reader.Window(t.Context(), request, time.Second, BrowseOutput{}, f.input)
			if !reflect.DeepEqual(page, BrowsePage{}) {
				t.Fatal("browse leak")
			}
		case "detail":
			var page TitleDetail
			page, result, e = r.Detail(t.Context(), request, time.Second, BrowseOutput{}, TitleInput{View: f.input, Item: 1})
			if !reflect.DeepEqual(page, TitleDetail{}) {
				t.Fatal("detail leak")
			}
		case "playback":
			var page PlaybackFile
			page, result, e = r.Playback(t.Context(), request, time.Second, BrowseOutput{}, TitleInput{View: f.input, Item: 1, Asset: 1})
			if !reflect.DeepEqual(page, PlaybackFile{}) {
				t.Fatal("playback leak")
			}
		case "continue":
			var page ContinuePage
			page, result, e = r.ContinueWatching(t.Context(), request, time.Second, BrowseOutput{}, ContinueInput{View: f.input})
			if !reflect.DeepEqual(page, ContinuePage{}) {
				t.Fatal("continue leak")
			}
		}
		if !errors.Is(e, ErrInput) || result.LookupStatements != 0 {
			t.Fatal(recipe, e)
		}
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestLiveAllRecipesKeepConsumerCustody(t *testing.T) {
	synctest.Test(t, testLiveAllRecipesKeepConsumerCustody)
}

func testLiveAllRecipesKeepConsumerCustody(t *testing.T) {
	f, r := newLiveFixture(t)
	for _, recipe := range []string{"browse", "detail", "playback", "continue"} {
		q, request, output := liveRequests(recipe)
		consumed := false
		result, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			var readResult BrowseReadResult
			var err error
			switch recipe {
			case "browse":
				var page BrowsePage
				page, readResult, err = f.reader.Window(ctx, request, time.Second, out, f.input)
				if err == nil && len(page.Items) != 40 {
					t.Fatal("browse content")
				}
			case "detail":
				var page TitleDetail
				page, readResult, err = r.Detail(ctx, request, time.Second, out, TitleInput{View: f.input, Item: 1, Asset: 1})
				if err == nil && (page.File == nil || len(page.Cast) != 100 || len(page.Crew) != 50) {
					t.Fatal("detail content")
				}
			case "playback":
				var page PlaybackFile
				page, readResult, err = r.Playback(ctx, request, time.Second, out, TitleInput{View: f.input, Item: 1, Asset: 1})
				if err == nil && page.Path != "/media/title.mkv" {
					t.Fatal("playback content")
				}
			case "continue":
				var page ContinuePage
				page, readResult, err = r.ContinueWatching(ctx, request, time.Second, out, ContinueInput{View: f.input})
				if err == nil && len(page.Items) != 40 {
					t.Fatal("continue content")
				}
			}
			if err != nil {
				return err
			}
			if readResult.AdmissionElapsed != out.live.admission || readResult.AdmissionElapsed < 0 || readResult.Elapsed < readResult.AdmissionElapsed {
				t.Fatal("admission accounting differs from the issued LIVE scope")
			}
			for _, w := range append(readResult.LookupWindows, readResult.PublicationWindows...) {
				if w.Ended.IsZero() || w.Deadline.After(q.ReadyDeadline) {
					t.Fatal("original ready deadline/physical return")
				}
			}
			assertLiveUsage(t, f, 1, 0) // All native windows ended; consumer still owns output.
			if err := f.authority.CheckForeground(out.live.live, q); err != nil {
				t.Fatal(err)
			}
			consumed = true
			return nil
		})
		if e != nil || !consumed || result.Refusal != nil || result.Elapsed < result.AdmissionElapsed {
			t.Fatal(recipe, result, e)
		}
		assertLiveUsage(t, f, 0, 0)
	}
}

func TestLiveCapacityHeldBySlowConsumers(t *testing.T) {
	synctest.Test(t, testLiveCapacityHeldBySlowConsumers)
}

func testLiveCapacityHeldBySlowConsumers(t *testing.T) {
	f, _ := newLiveFixture(t)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var wg sync.WaitGroup
	errorsOut := make(chan error, 4)
	for n := 0; n < 4; n++ {
		entered := make(chan struct{})
		q, request, output := liveRequests(fmt.Sprint(n))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
				page, _, e := f.reader.Window(ctx, request, time.Second, out, f.input)
				if e != nil {
					return e
				}
				if len(page.Items) != 40 {
					return errors.New("missing browse content")
				}
				close(entered)
				<-release // Models the owned encoder/client consumer, after native cleanup.
				return nil
			})
			errorsOut <- e
		}()
		synctest.Wait()
		select {
		case <-entered:
		case e := <-errorsOut:
			t.Fatal("slow consumer failed", e)
		default:
			t.Fatal("consumer did not start")
		}
	}
	assertLiveUsage(t, f, 4, 0)
	q, _, output := liveRequests("fifth")
	called := false
	result, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(context.Context, BrowseOutput) error { called = true; return nil })
	if !errors.Is(e, governor.ErrBusy) || result.Refusal == nil || called {
		t.Fatal("fifth request allocated output", result, e)
	}
	assertLiveUsage(t, f, 4, 0)
	close(release)
	wg.Wait()
	for n := 0; n < 4; n++ {
		if e := <-errorsOut; e != nil {
			t.Fatal(e)
		}
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestLiveErrorsPanicCancellationAndExpiryKeepCustody(t *testing.T) {
	synctest.Test(t, testLiveErrorsPanicCancellationAndExpiryKeepCustody)
}

func testLiveErrorsPanicCancellationAndExpiryKeepCustody(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancel", "expiry", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newLiveFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			q, request, output := liveRequests(mode)
			if mode == "expiry" {
				q.ReadyDeadline = time.Now().Add(20 * time.Millisecond)
				q.ProtectedDeadline = time.Now().Add(40 * time.Millisecond)
				output.Deadline = q.ProtectedDeadline
			}
			marker := errors.New("consumer fault")
			var e error
			var got any
			func() {
				defer func() { got = recover() }()
				_, e = ConsumeForeground(ctx, f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
					if mode == "expiry" {
						<-ctx.Done()
					} else {
						page, _, err := f.reader.Window(ctx, request, time.Second, out, f.input)
						if err != nil {
							return err
						}
						if len(page.Items) == 0 {
							t.Fatal("missing content")
						}
					}
					switch mode {
					case "cancel":
						cancel()
					case "revoke":
						f.governor.Cancel(out.live.live)
						if err := out.live.check(); !errors.Is(err, governor.ErrGrant) {
							t.Fatal("revoked LIVE usable", err)
						}
					}
					assertLiveUsage(t, f, 1, 0)
					if mode == "panic" {
						panic(marker)
					}
					if mode == "error" {
						return marker
					}
					return nil
				})
			}()
			if mode == "panic" {
				if got != marker {
					t.Fatal("primary panic lost", got)
				}
			} else if got != nil {
				t.Fatal(got)
			}
			if mode == "error" && !errors.Is(e, marker) || mode == "cancel" && !errors.Is(e, context.Canceled) || mode == "expiry" && !errors.Is(e, context.DeadlineExceeded) {
				t.Fatal("consumer outcome lost", mode, e)
			}
			assertLiveUsage(t, f, 0, 0)
		})
	}
}

func TestLiveExactBindingsAndEscapedScope(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, request, output := liveRequests("bindings")
	var escaped BrowseOutput
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		escaped = out
		for _, mutate := range []func(*governor.Request){func(r *governor.Request) { r.Owner++ }, func(r *governor.Request) { r.Key.ID += "-other" }, func(r *governor.Request) { r.Class = governor.Background }} {
			wrong := request
			mutate(&wrong)
			page, result, e := f.reader.Window(ctx, wrong, time.Second, out, f.input)
			if !errors.Is(e, governor.ErrGrant) || !reflect.DeepEqual(page, BrowsePage{}) || result.LookupStatements != 0 {
				t.Fatal("wrong route authority accepted", e)
			}
		}
		for _, mutate := range []func(*governor.ForegroundRequest){func(r *governor.ForegroundRequest) { r.Owner++ }, func(r *governor.ForegroundRequest) { r.Key.ID += "-other" }, func(r *governor.ForegroundRequest) { r.ReadyDeadline = r.ReadyDeadline.Add(time.Nanosecond) }, func(r *governor.ForegroundRequest) { r.ProtectedDeadline = r.ProtectedDeadline.Add(time.Nanosecond) }} {
			original := out.live.request
			mutate(&out.live.request)
			page, result, e := f.reader.Window(ctx, request, time.Second, out, f.input)
			out.live.request = original
			if !errors.Is(e, governor.ErrGrant) || !reflect.DeepEqual(page, BrowsePage{}) || result.LookupStatements != 0 {
				t.Fatal("LIVE tuple was not exact", e)
			}
		}
		page, _, e := f.reader.Window(ctx, request, time.Second, out, f.input)
		if e != nil || len(page.Items) != 40 {
			return errors.Join(e, errors.New("valid route failed"))
		}
		page, result, e := f.reader.Window(ctx, request, time.Second, out, f.input)
		if !errors.Is(e, governor.ErrGrant) || !reflect.DeepEqual(page, BrowsePage{}) || result.LookupStatements != 0 {
			t.Fatal("scope admitted a second payload", e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	page, result, e := f.reader.Window(t.Context(), request, time.Second, escaped, f.input)
	if !errors.Is(e, governor.ErrGrant) || !reflect.DeepEqual(page, BrowsePage{}) || result.LookupStatements != 0 {
		t.Fatal("retired output resurrected", e)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestLiveWaitingReservesNoPayloadAndKeepsOriginalReadyDeadline(t *testing.T) {
	synctest.Test(t, testLiveWaitingReservesNoPayloadAndKeepsOriginalReadyDeadline)
}

func testLiveWaitingReservesNoPayloadAndKeepsOriginalReadyDeadline(t *testing.T) {
	f, _ := newLiveFixture(t)
	hold, _, _ := liveRequests("holder")
	var grants []*governor.Grant
	for n := 0; n < 4; n++ {
		d := f.authority.AdmitForeground(t.Context(), hold)
		if d.Refusal != nil {
			t.Fatal(d.Refusal)
		}
		grants = append(grants, d.Grant)
	}
	defer func() {
		for _, grant := range grants {
			if grant != nil {
				f.governor.Cancel(grant)
				if e := f.authority.Retired(grant); e != nil {
					t.Error(e)
				}
			}
		}
	}()
	q, request, output := liveRequests("waiter")
	q.ReadyDeadline = time.Now().Add(250 * time.Millisecond)
	done := make(chan error, 1)
	entered := make(chan struct{})
	var consumption ForegroundConsumption
	go func() {
		var e error
		consumption, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			close(entered)
			page, result, e := f.reader.Window(ctx, request, time.Second, out, f.input)
			if e != nil {
				return e
			}
			if len(page.Items) != 40 {
				return errors.New("waiter content")
			}
			if result.Lookup.Deadline.After(q.ReadyDeadline) || result.Publication.Deadline.After(q.ReadyDeadline) || result.AdmissionElapsed != time.Millisecond || result.Elapsed < result.AdmissionElapsed {
				return errors.New("waiter renewed/omitted original ready budget")
			}
			return nil
		})
		done <- e
	}()
	// Observe the blocked waiter without racing a wall-clock polling loop
	// against its bounded admission/ready lifetime. Then advance virtual time
	// by one millisecond to retain a nonzero admission-elapsed witness.
	synctest.Wait()
	if got := f.authority.ForegroundUsage().Waiters; got != 1 {
		t.Fatalf("waiter did not queue: %d", got)
	}
	time.Sleep(time.Millisecond)

	select {
	case <-entered:
		t.Fatal("queued callback allocated payload")
	default:
	}
	if usage := f.authority.Usage()[governor.Foreground]; usage[governor.MemoryBytes] != 5*4096 || usage[governor.QueueBytes] != 5*4096 {
		t.Fatal("waiter charged payload or lost metadata", usage)
	}
	f.governor.Cancel(grants[0])
	if e := f.authority.Retired(grants[0]); e != nil {
		t.Fatal(e)
	}
	grants[0] = nil
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if consumption.AdmissionElapsed != time.Millisecond || consumption.Elapsed < consumption.AdmissionElapsed {
		t.Fatal("virtual admission wait was not charged exactly", consumption)
	}
	if f.authority.ForegroundUsage().Live != 3 || f.authority.ForegroundUsage().Waiters != 0 {
		t.Fatal("waiter/consumer custody leaked")
	}
}

type liveBlockedRead struct {
	*governor.Snapshots
	once             sync.Once
	entered, release chan struct{}
}

func (s *liveBlockedRead) Read(ctx context.Context, r governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	return s.Snapshots.Read(ctx, r, budget, func(scope *governor.ReadScope) error {
		e := read(scope)
		s.once.Do(func() { close(s.entered); <-s.release })
		return e
	})
}
func TestLiveConsumerReturnJoinsActiveReadBeforeRefund(t *testing.T) {
	synctest.Test(t, testLiveConsumerReturnJoinsActiveReadBeforeRefund)
}

func testLiveConsumerReturnJoinsActiveReadBeforeRefund(t *testing.T) {
	f, _ := newLiveFixture(t)
	observed := &liveBlockedRead{Snapshots: f.snapshots, entered: make(chan struct{}), release: make(chan struct{})}
	f.reader.snapshots = observed
	q, request, output := liveRequests("join")
	done := make(chan error, 1)
	readDone := make(chan error, 1)
	scopeOut := make(chan BrowseOutput, 1)
	go func() {
		_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			go func() { _, _, e := f.reader.Window(ctx, request, time.Second, out, f.input); readDone <- e }()
			<-observed.entered
			scopeOut <- out
			return nil // Driver still must join the already active read.
		})
		done <- e
	}()
	out := <-scopeOut
	// The consumer has returned and cleanup must block on the active read.
	// Quiescence proves the ordering without polling a real-clock deadline.
	synctest.Wait()
	out.live.mu.Lock()
	closed := out.live.closed
	out.live.mu.Unlock()
	if !closed {
		t.Fatal("scope did not close before joining its active read")
	}
	select {
	case e := <-done:
		t.Fatal("refunded while read still active", e)
	default:
	}
	if f.authority.ForegroundUsage().Live != 1 || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] < 8<<20 {
		t.Fatal("active read lost LIVE/output custody")
	}
	close(observed.release)
	if e := <-readDone; !errors.Is(e, context.Canceled) {
		t.Fatal("active read was not cancelled/joined", e)
	}
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal("active read outcome omitted", e)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestLivePublicationDenialWinsOverCancellation(t *testing.T) {
	// This is an ordering control, not a wall-clock qualification: race
	// instrumentation must not expire the Reader before the revoke boundary.
	synctest.Test(t, testLivePublicationDenialWinsOverCancellation)
}

func testLivePublicationDenialWinsOverCancellation(t *testing.T) {
	f, _ := newLiveFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`); cancel() }}
	q, request, output := liveRequests("denial-cancel")
	var readErr error
	_, e := ConsumeForeground(ctx, f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, result, err := f.reader.Window(ctx, request, time.Second, out, f.input)
		readErr = err
		if result.PublicationStatements != 2 {
			t.Fatalf("fresh authority/principal fence did not run: %+v, err=%v", result, err)
		}
		if !reflect.DeepEqual(page, BrowsePage{}) {
			t.Fatal("denied output escaped")
		}
		return err
	})
	if !errors.Is(readErr, ErrDenied) || !errors.Is(e, ErrDenied) {
		t.Fatal("cancellation hid current denial", readErr, e)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestLiveAdmissionElapsedAdvancesTrustedClock(t *testing.T) {
	f, _ := newLiveFixture(t)
	f.exec(t, `UPDATE authorization_family_tokens SET expires_at=?`, f.input.Now.Add(50*time.Millisecond).Format(time.RFC3339Nano))
	q, request, output := liveRequests("clock")
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		// Deterministic elapsed-admission witness, with no sleeping/native timing
		// assumption. Authority deadlines still use their original real absolutes.
		out.live.started = out.live.started.Add(-100 * time.Millisecond)
		page, _, e := f.reader.Window(ctx, request, time.Second, out, f.input)
		if !reflect.DeepEqual(page, BrowsePage{}) {
			t.Fatal("expired token leaked after admission")
		}
		return e
	})
	if !errors.Is(e, ErrDenied) {
		t.Fatal("admission time omitted from trusted clock", e)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestLiveMissingProfileAndBadOutputStayUnallocated(t *testing.T) {
	f := newBrowseFixture(t)
	q, _, output := liveRequests("unqualified")
	before := f.authority.Usage()
	entered := false
	result, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(context.Context, BrowseOutput) error { entered = true; return nil })
	if !errors.Is(e, governor.ErrBusy) || result.Refusal == nil || result.Refusal.Code != governor.Unqualified || entered || f.authority.Usage() != before {
		t.Fatal("missing profile allocated output", result, e)
	}
}

func TestLiveCancelledOriginalContextAndMissingClock(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, request, output := liveRequests("original-context")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, e := ConsumeForeground(ctx, f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		in := f.input
		in.Now = time.Time{}
		page, result, e := f.reader.Window(ctx, request, time.Second, out, in)
		if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, BrowsePage{}) || result.LookupStatements != 0 {
			t.Fatal("missing request clock accepted", e)
		}
		cancel()
		page, result, e = f.reader.Window(context.Background(), request, time.Second, out, f.input)
		if !errors.Is(e, context.Canceled) || !reflect.DeepEqual(page, BrowsePage{}) || result.LookupStatements != 0 {
			t.Fatal("caller bypassed original cancellation", e)
		}
		return e
	})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestLiveCustodyCoversEveryWindowAndWholeRetry(t *testing.T) {
	f, r := newLiveFixture(t)
	observed := &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
		usage := f.authority.Usage()[governor.Foreground]
		if f.authority.ForegroundUsage().Live != 1 || usage[governor.MemoryBytes] != (20<<20)+4096 || usage[governor.Reader] != 1 || usage[governor.QueueBytes] != 4096 {
			t.Fatal("window lost or doubled live/read/output custody", phase, usage)
		}
		if phase == 3 {
			f.exec(t, `UPDATE catalog_entities SET revision=revision+1 WHERE id=1`)
		}
	}}
	r.snapshots = observed
	q, request, output := liveRequests("retry")
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, result, e := r.Detail(ctx, request, time.Second, out, TitleInput{View: f.input, Item: 1, Asset: 1})
		if e != nil {
			return e
		}
		if page.Item.Revision != 2 || result.Attempts != 2 || observed.phase != 8 || result.LookupStatements+result.PublicationStatements != 52 {
			t.Fatal("whole-operation retry lifetime/counts", observed.phase, result.Attempts)
		}
		assertLiveUsage(t, f, 1, 0)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestLiveGrantRevocationStopsNewWindows(t *testing.T) {
	for _, mode := range []string{"live", "output", "epoch"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newLiveFixture(t)
			q, request, output := liveRequests("revocation-" + mode)
			_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
				observed := &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() {
					switch mode {
					case "live":
						f.governor.Cancel(out.live.live)
					case "output":
						f.governor.Cancel(out.Grant)
					case "epoch":
						f.authority.EnterStatic()
					}
				}}
				f.reader.snapshots = observed
				page, result, e := f.reader.Window(ctx, request, time.Second, out, f.input)
				if !errors.Is(e, governor.ErrGrant) || !reflect.DeepEqual(page, BrowsePage{}) || observed.phase != 1 || !result.Publication.Started.IsZero() {
					t.Fatal("revocation started another native window", mode, observed.phase, e)
				}
				assertLiveUsage(t, f, 1, 0)
				return e
			})
			if !errors.Is(e, governor.ErrGrant) {
				t.Fatal(e)
			}
			assertLiveUsage(t, f, 0, 0)
		})
	}
}

func TestLiveOutputRefusalReturnsMetadataWithoutConsumer(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, _, output := liveRequests("output-refusal")
	blocker := output
	blocker.Key.ID = "retained-other-output"
	blocker.Need[governor.MemoryBytes] = 80 << 20
	held := f.governor.Try(blocker)
	if held.Refusal != nil {
		t.Fatal(held.Refusal)
	}
	defer func() {
		f.governor.Cancel(held.Grant)
		if e := f.authority.Retired(held.Grant); e != nil {
			t.Error(e)
		}
	}()
	before := f.authority.Usage()
	called := false
	result, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(context.Context, BrowseOutput) error { called = true; return nil })
	if !errors.Is(e, governor.ErrBusy) || result.Refusal == nil || result.Refusal.Code != governor.Capacity || called || f.authority.ForegroundUsage().Live != 0 || f.authority.Usage() != before {
		t.Fatal("output refusal leaked/ran a consumer", result, e)
	}
}

func TestLivePrimaryPanicSurvivesCleanupClockPanic(t *testing.T) {
	var fail atomic.Bool
	primary, secondary := errors.New("consumer panic"), errors.New("cleanup clock panic")
	f, _ := newLiveFixture(t, func() time.Time {
		if fail.Load() {
			panic(secondary)
		}
		return time.Now()
	})
	q, _, output := liveRequests("cleanup-panic")
	var saved BrowseOutput
	var got any
	func() {
		defer func() { got = recover() }()
		_, _ = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(_ context.Context, out BrowseOutput) error { saved = out; fail.Store(true); panic(primary) })
	}()
	fail.Store(false)
	if got != primary {
		t.Fatal("cleanup replaced primary panic", got)
	}
	// Both actual debits retire before the injected LIVE-promotion clock panic.
	// The consumer's primary panic must still be the observed outcome.
	_ = saved

	assertLiveUsage(t, f, 0, 0)
}

func TestLiveNativeRefusalIsReportedByConsumerScope(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, request, output := liveRequests("reader-refusal")
	blocker := request
	blocker.Key.ID = "other-native-reader"
	held := f.governor.Try(blocker)
	if held.Refusal != nil {
		t.Fatal(held.Refusal)
	}
	defer func() {
		f.governor.Cancel(held.Grant)
		if e := f.authority.Retired(held.Grant); e != nil {
			t.Error(e)
		}
	}()
	result, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, e := f.reader.Window(ctx, request, time.Second, out, f.input)
		if !reflect.DeepEqual(page, BrowsePage{}) {
			t.Fatal("refused reader published output")
		}
		return e
	})
	if !errors.Is(e, governor.ErrBusy) || result.Refusal == nil || result.Refusal.Code != governor.Capacity || f.authority.ForegroundUsage().Live != 0 {
		t.Fatal("consumer lost native refusal", result, e)
	}
}

func TestLiveCompositionRejectsForeignGovernorAndPool(t *testing.T) {
	f, _ := newLiveFixture(t)
	foreign, _ := newLiveFixture(t)
	q, request, output := liveRequests("foreign")
	before, otherBefore := f.authority.Usage(), foreign.authority.Usage()
	called := false
	_, e := ConsumeForeground(t.Context(), foreign.governor, f.authority, f.snapshots, q, output, func(context.Context, BrowseOutput) error { called = true; return nil })
	if !errors.Is(e, ErrInput) || called || f.authority.Usage() != before || foreign.authority.Usage() != otherBefore {
		t.Fatal("foreign governor admitted/allocated", e)
	}

	_, e = ConsumeForeground(t.Context(), f.governor, f.authority, foreign.snapshots, q, output, func(context.Context, BrowseOutput) error { called = true; return nil })
	if !errors.Is(e, ErrInput) || called || f.authority.Usage() != before || foreign.authority.Usage() != otherBefore {
		t.Fatal("foreign pool admitted/allocated", e)
	}
	_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, result, e := foreign.reader.Window(ctx, request, time.Second, out, foreign.input)
		if !errors.Is(e, governor.ErrGrant) || !reflect.DeepEqual(page, BrowsePage{}) || result.LookupStatements != 0 {
			t.Fatal("foreign snapshot pool used LIVE", e)
		}
		return e
	})
	if !errors.Is(e, governor.ErrGrant) {
		t.Fatal(e)
	}
	assertLiveUsage(t, f, 0, 0)
	assertLiveUsage(t, foreign, 0, 0)
}

func TestLiveUncertainOutputRetirementKeepsLIVECharged(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, _, output := liveRequests("uncertain-output")
	var saved BrowseOutput
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(_ context.Context, out BrowseOutput) error {
		saved = out
		// Deliberate consumer misuse: only the scope driver may attest retirement.
		return f.authority.Retired(out.Grant)
	})
	if !errors.Is(e, governor.ErrRetirement) || f.authority.ForegroundUsage().Live != 1 || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 4096 {
		t.Fatal("uncertain output retirement refunded LIVE", e)
	}
	if e := f.authority.CheckForeground(saved.live.live, q); !errors.Is(e, governor.ErrGrant) {
		t.Fatal("uncertain LIVE use not revoked", e)
	}
	if e := f.authority.Retired(saved.live.live); e != nil {
		t.Fatal(e)
	}
	assertLiveUsage(t, f, 0, 0)
}

// A per-read child cancellation must wake entry validation even while its
// consumer/LIVE context remains valid and another validator holds the ledger.
func TestLiveReadOwnCancellationInterruptsEntryValidation(t *testing.T) {
	type contention struct {
		calls         atomic.Int32
		held, release chan struct{}
	}
	var active atomic.Pointer[contention]
	f, _ := newLiveFixture(t, func() time.Time {
		if c := active.Load(); c != nil && c.calls.Add(1) == 1 {
			close(c.held) // Check calls Now while holding the ledger.
			<-c.release
		}
		return time.Now()
	})
	synctest.Test(t, func(t *testing.T) {
		q, request, output := liveRequests("read-child-cancel")
		_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			c := &contention{held: make(chan struct{}), release: make(chan struct{})}
			active.Store(c)
			holder := make(chan error, 1)
			go func() { holder <- f.authority.Check(out.Grant, out.Request) }()
			<-c.held
			defer func() {
				close(c.release)
				if e := <-holder; e != nil {
					t.Error(e)
				}
				active.Store(nil)
			}()
			readCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, e := liveRead(readCtx, request, time.Second, out, f.snapshots, time.Now(), func(context.Context, governor.Request, time.Duration, time.Time) (int, BrowseReadResult, error) {
					t.Error("cancelled entry reached read work")
					return 1, BrowseReadResult{}, nil
				})
				done <- e
			}()
			synctest.Wait()
			select {
			case e := <-done:
				t.Fatal("entry did not wait for ledger", e)
			default:
			}
			cancel()
			synctest.Wait()
			select {
			case e := <-done:
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			default:
				t.Fatal("read cancellation waited for ledger handoff")
			}
			if ctx.Err() != nil || out.live.used {
				t.Fatal("child cancellation consumed the operation or cancelled its owner")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertLiveUsage(t, f, 0, 0)
	})
}
