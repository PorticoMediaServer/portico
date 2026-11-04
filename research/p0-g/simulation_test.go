package governor

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"
)

var _ Governor = (*Fake)(nil)
var _ Retirer = (*Fake)(nil)

func require(t *testing.T, ok bool, message string) {
	t.Helper()
	if !ok {
		t.Fatal(message)
	}
}
func request(c *Clock, cl Class, a Amounts) Request {
	return Request{Class: cl, Need: a, Deadline: c.Now().Add(time.Second)}
}
func granted(t *testing.T, g *Fake, r Request) *Grant {
	t.Helper()
	d := g.Try(r)
	require(t, d.Grant != nil, "unexpected refusal")
	return d.Grant
}

// Random grants, cancellation, denied successors, delayed exits, bad accounting,
// stale capabilities, duplicate retirements, and a stream alive during HTTP silence.
func exerciseAccounting(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	c := &Clock{}
	g := NewFake(c)
	stream := granted(t, g, request(c, Foreground, Amounts{2, 1, 32, 1000, 32, 32, 0, 0}))
	live := []*Grant{stream}
	for n := 0; n < 160; n++ {
		c.MS++
		if len(live) > 1 && rng.Intn(3) == 0 {
			i := 1 + rng.Intn(len(live)-1)
			p := live[i]
			before := g.Used
			g.Cancel(p)
			require(t, before == g.Used, "cancel returned physical capacity")
			require(t, !g.Valid(p), "cancelled capability still authorizes")
			require(t, g.Retired(p) == nil, "exit failed")
			require(t, g.Retired(p) != nil, "double credit")
			live = append(live[:i], live[i+1:]...)
		} else {
			cl := Class(rng.Intn(4))
			a := Amounts{}
			for k := range a {
				a[k] = int64(rng.Intn(3))
			}
			if d := g.Try(request(c, cl, a)); d.Grant != nil {
				live = append(live, d.Grant)
			}
		}
		require(t, g.Check() == nil, "resource invariant")
	}
	// Corrupt a cached count; independent physical inventory preserves debt and FG.
	g.Used[Background][Helper]--
	require(t, g.Watchdog(), "drift not detected")
	require(t, g.Check() == nil, "reconciliation failed")
	require(t, !g.Valid(stream), "old epoch authority accepted")
	require(t, g.Try(request(c, Background, Amounts{})).Refusal.Code == FailSafe, "fail-safe admitted spare work")
	// Foreground service from unused static floor survives accounting failure.
	fg := g.Try(request(c, Foreground, Amounts{}))
	require(t, fg.Grant != nil, "safe FG refused")
	require(t, g.Retired(fg.Grant) == nil, "FG retirement")
	for _, p := range live {
		require(t, g.Retired(p) == nil, "retire retained debt")
	}
	require(t, g.Check() == nil && len(g.Physical) == 0, "leaked physical holdings")
	// Unexited BG occupies its cap forever, but cannot take any foreground floor.
	bg := granted(t, g, request(c, ScanMinimum, g.Floors[ScanMinimum]))
	g.Cancel(bg)
	require(t, g.Try(request(c, ScanMinimum, Amounts{1})).Grant == nil, "replacement overcommitted debt")
	fg2 := granted(t, g, request(c, Foreground, g.Floors[Foreground]))
	_ = g.Retired(fg2)
	_ = g.Retired(bg)
	// Refusal is pure: no pending waiter, no hidden resource, no goroutine.
	before := g.Used
	require(t, g.Try(request(c, Foreground, Amounts{-1})).Refusal.Code == Invalid, "negative amount")
	require(t, before == g.Used, "refusal mutated ledger")
}

type receipt struct {
	phase                int
	attempts, generation int
	applied              int
	next                 int64
}
type work struct {
	arrival  int64
	duration int64
	index    int
}
type event struct {
	at         int64
	kind       int
	job        int
	generation int
}
type result struct {
	ServiceMissTicks                                               int
	TailLatency                                                    []int64
	TailUnits                                                      int
	Latency                                                        []int64
	Applied, Retried, Refused, LostWake, Crash, Stale, RetireDelay int
	MaxScanGap                                                     int64
	ScanMS                                                         int64
}

// Fake single-writer, fake IO, fake process exit. Every loop advances exactly 1ms.
// Scan pipeline: claim tx -> read IO -> helper -> canonical publication tx.
// A cancelled helper cannot publish; next attempt starts only after physical exit.
func simulate(t *testing.T, seed int64, weak, bg, busy, longTail bool) result {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	c := &Clock{}
	g := NewFake(c)
	var out result
	tailWindows := [][2]int64{}
	const count = 64
	jobs := make([]receipt, count)
	for i := range jobs {
		jobs[i].next = 0
	}
	events := []event{}
	fg := []work{}
	writerUntil := int64(0)
	var finish func()
	activeJob := -1
	cursor := 0
	nextScan := int64(0)
	helperUntil := int64(0)
	var helperGrant *Grant
	readySince := int64(-1)
	lastVisible := int64(0)
	visibleDuring := 0
	serviceTicks := make([]int, 3000)
	windowService := 0
	off := int64(rng.Intn(10))
	service := int64(2)
	if busy {
		service = 9
	}
	quantum := int64(5)
	if weak {
		quantum = 5 // 8ms model failed p95; shrink weak-host units before admission
	}
	// Active stream exists from start through drain; no queued HTTP needed to hold it.
	stream := granted(t, g, request(c, Foreground, Amounts{2, 1, 32, 1000, 32, 32}))
	for c.MS = 0; c.MS < 30000; c.MS++ {
		now := c.MS
		if now == writerUntil && finish != nil {
			finish()
			finish = nil
		}
		if now < 3000 && now >= off && (now-off)%10 == 0 {
			fg = append(fg, work{arrival: now, duration: service, index: len(out.Latency) + len(fg)})
		}
		// Event order is insertion order, deterministic even when clocks tie.
		remain := events[:0]
		for _, e := range events {
			if e.at > now {
				remain = append(remain, e)
				continue
			}
			j := &jobs[e.job]
			if e.kind == 1 {
				j.phase = 2
			} // source IO physically done
			if e.kind == 2 {
				require(t, g.Retired(helperGrant) == nil, "helper physical exit")
				if e.generation != j.generation {
					out.Stale++
					j.phase = 0
					j.next = now + 10
					activeJob = -1
				} else {
					j.phase = 4
				}
				helperGrant = nil
			}
		}
		events = remain
		if !bg && now >= 3000 && len(fg) == 0 && finish == nil {
			break
		}
		// Each runnable scan stage receives service within 100ms under busy foreground.
		// Here read/helper starts use reserved resources and don't need writer turns.
		if bg && activeJob >= 0 {
			j := &jobs[activeJob]
			if j.phase == 1 {
				j.phase = 11
				events = append(events, event{now + int64(1+rng.Intn(7)), 1, activeJob, j.generation})
			}
			if j.phase == 2 && helperGrant == nil {
				helperGrant = granted(t, g, request(c, ScanMinimum, Amounts{0, 1, 8, 100, 4}))
				j.phase = 3
				helperUntil = now + int64(1+rng.Intn(15))
				if weak {
					helperUntil += 15
				}
				gen := j.generation
				if !busy && rng.Intn(5) == 0 {
					g.Cancel(helperGrant)
					j.generation++
					helperUntil += int64(10 + rng.Intn(80))
					out.Retried++
					out.RetireDelay++
				}
				events = append(events, event{helperUntil, 2, activeJob, gen})
			}
		}

		// Admitted foreground envelope = <=90% writer service, <= protected vector.
		// Minimum scan reservation is not a second writer or a foreground promotion.
		scanRunnable := false
		if bg {
			if activeJob >= 0 {
				scanRunnable = jobs[activeJob].phase == 0 || jobs[activeJob].phase == 4
			} else {
				for k := 0; k < count; k++ {
					i := (cursor + k) % count
					if jobs[i].phase == 0 && jobs[i].next <= now {
						activeJob = i
						cursor = (i + 1) % count
						scanRunnable = true
						break
					}
				}
			}
		}
		// Ready-stage age is measured EVERY tick, including first/trailing gaps,
		// not just at a subsequent grant. Exclude a currently executing scan tx.
		scanExecuting := false
		for _, h := range g.Physical {
			if h.request.Writer && h.request.Class == ScanMinimum {
				scanExecuting = true
			}
		}
		if bg && scanRunnable && !scanExecuting {
			if readySince < 0 {
				readySince = now
			}
			age := now - readySince
			if age > out.MaxScanGap {
				out.MaxScanGap = age
			}
			require(t, longTail || age <= 125, fmt.Sprintf("seed %d first/trailing runnable stage starved at %d age %d", seed, now, age))
		} else {
			readySince = -1
		}
		if bg && busy && now < 3000 {
			if scanExecuting {
				serviceTicks[now] = 1
			}
			windowService += serviceTicks[now]
			if now >= 1000 {
				windowService -= serviceTicks[now-1000]
			}
			if now >= 999 {
				if windowService < 50 {
					out.ServiceMissTicks++
				}
				require(t, longTail || windowService >= 50, fmt.Sprintf("seed %d reserved scan service missing at %d: %d/1000ms", seed, now, windowService))
			}
			require(t, longTail || now-lastVisible <= 500, fmt.Sprintf("seed %d no visible publication during foreground at %d", seed, now))
		}
		if finish != nil {
			require(t, g.Check() == nil, "writer accounting")
			continue
		}
		chooseScan := scanRunnable && (len(fg) == 0 || now >= nextScan)
		if chooseScan {
			nextScan = now + 90 // <=9ms foreground residual + guard keeps >=5% in 1s windows
			readySince = -1
			i := activeJob
			j := &jobs[i]
			publishing := j.phase == 4
			wr := request(c, ScanMinimum, Amounts{0, 0, 1, 0, 0, 0, 1})
			wr.Writer = true
			wr.MaxHold = time.Duration(quantum) * time.Millisecond
			p := granted(t, g, wr)
			actual := quantum
			if longTail && rng.Intn(100) == 0 {
				actual = int64(40 + rng.Intn(6))
				out.TailUnits++
				tailWindows = append(tailWindows, [2]int64{now, now + actual})
			}
			writerUntil = now + actual
			out.ScanMS += actual
			finish = func() {
				require(t, g.Retired(p) == nil, "writer retirement")
				if publishing {
					// Canonical effect, receipt and cursor change atomically in fake DB.
					require(t, j.applied == 0, "duplicate canonical application")
					j.applied++
					j.phase = 5
					out.Applied++
					if c.MS < 3000 {
						lastVisible = c.MS
						visibleDuring++
					}
					activeJob = -1
					if rng.Intn(3) == 0 {
						out.Crash++
					} // crash AFTER commit: no replay of applied receipt
					if rng.Intn(2) == 0 {
						out.LostWake++
					} // reconciliation consults persisted phase
				} else {
					j.attempts++
					j.generation++
					j.phase = 1
				}
			}
		} else if len(fg) > 0 {
			w := fg[0]
			fg = fg[1:]
			p := granted(t, g, request(c, Foreground, Amounts{1, 0, 1}))
			writerUntil = now + w.duration
			finish = func() {
				out.Latency = append(out.Latency, c.MS-w.arrival)
				for _, span := range tailWindows {
					if w.arrival >= span[0] && w.arrival < span[1] {
						out.TailLatency = append(out.TailLatency, c.MS-w.arrival)
						break
					}
				}
				require(t, g.Retired(p) == nil, "FG retirement")
			}
		} else if bg && now < 3000 && rng.Intn(4) == 0 {
			// Optional background writer work fits one residual, never queues ahead FG.
			wr := request(c, Background, Amounts{})
			wr.Writer = true
			wr.MaxHold = time.Duration(quantum) * time.Millisecond
			p := granted(t, g, wr)
			actual := quantum
			if longTail && rng.Intn(100) == 0 {
				actual = int64(40 + rng.Intn(6))
				out.TailUnits++
				tailWindows = append(tailWindows, [2]int64{now, now + actual})
			}
			writerUntil = now + actual
			finish = func() { require(t, g.Retired(p) == nil, "optional retirement") }
		}
		// Fill a bounded command queue with oversized candidates: refuse before effects.
		if bg && now%100 == 0 {
			before := g.Used
			d := g.Try(request(c, Background, Amounts{0, 0, 1 << 30}))
			require(t, d.Refusal != nil && before == g.Used, "refusal changed state")
			out.Refused++
		}
		require(t, g.Check() == nil, "floor/physical invariant")
		if bg && now >= 3000 && out.Applied == count && len(fg) == 0 && finish == nil {
			break
		}
	}
	require(t, !bg || out.Applied == count, fmt.Sprintf("seed=%d weak=%v bg=%v busy=%v applied=%d active=%d jobs=%v events=%v", seed, weak, bg, busy, out.Applied, activeJob, jobs, events))
	if bg && busy {
		require(t, visibleDuring >= 5, "no minimum visible scan publication during active foreground")
	}
	for _, j := range jobs {
		require(t, !bg || (j.applied == 1 && j.phase == 5), "receipt/cursor not atomic")
	}
	_ = g.Retired(stream)
	require(t, len(g.Physical) == 0, "physical holdings leaked after drain")
	return out
}
func percentile(a []int64, p int) int64 {
	b := append([]int64(nil), a...)
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	return b[(len(b)*p+99)/100-1]
}

func TestSeeds(t *testing.T) {
	n := 10000
	if s := os.Getenv("SEEDS"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 1 {
			t.Fatal("invalid SEEDS")
		}
		n = v
	}
	type summary struct {
		Profile                                                       string
		Seeds                                                         int
		ForegroundSamples                                             int
		WorstP95DeltaMS, WorstP99DeltaMS, MaxRunnableScanGapMS        int64
		Applied, Retry, Refused, LostWake, Crash, Stale, DelayedExits int
	}
	sums := []summary{}
	for _, weak := range []bool{false, true} {
		s := summary{Profile: map[bool]string{false: "reference-model", true: "weak-model"}[weak], Seeds: n}
		for seed := int64(0); seed < int64(n); seed++ {
			if !weak {
				exerciseAccounting(t, seed)
			}
			// Same foreground arrival/service process for on/off; foreground-only and
			// sustained 90%-writer cases alternate; RNG background never changes arrivals.
			busy := seed%2 == 0
			a := simulate(t, seed, weak, false, busy, false)
			b := simulate(t, seed, weak, true, busy, false)
			require(t, len(a.Latency) == len(b.Latency), fmt.Sprintf("seed %d refused foreground", seed))
			p95 := percentile(b.Latency, 95) - percentile(a.Latency, 95)
			p99 := percentile(b.Latency, 99) - percentile(a.Latency, 99)
			require(t, p95 <= 10 && p99 <= 25, fmt.Sprintf("seed %d weak=%v FG delta %d/%d", seed, weak, p95, p99))
			// Max interval includes IO/helper states; delayed exits have known <=101ms bound.
			require(t, b.MaxScanGap <= 200, fmt.Sprintf("seed %d scan starved: %d", seed, b.MaxScanGap))
			if p95 > s.WorstP95DeltaMS {
				s.WorstP95DeltaMS = p95
			}
			if p99 > s.WorstP99DeltaMS {
				s.WorstP99DeltaMS = p99
			}
			if b.MaxScanGap > s.MaxRunnableScanGapMS {
				s.MaxRunnableScanGapMS = b.MaxScanGap
			}
			s.ForegroundSamples += len(b.Latency)
			s.Applied += b.Applied
			s.Retry += b.Retried
			s.Refused += b.Refused
			s.LostWake += b.LostWake
			s.Crash += b.Crash
			s.Stale += b.Stale
			s.DelayedExits += b.RetireDelay
		}
		sums = append(sums, s)
	}
	data, e := json.MarshalIndent(sums, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	fmt.Println(string(data))
	if path := os.Getenv("RESULT_JSON"); path != "" {
		if e := os.WriteFile(path, append(data, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
}

// The fake publication arbiter models the conflicting scope only. Nonconflicting
// scopes remain runnable. Security wins regardless of prep lease or scan debt.
type preparation struct {
	end           int64
	revision      int
	ready, denied bool
}
type arbiter struct {
	now      int64
	revision int
	prep     *preparation
	scanOwed bool
}

func (a *arbiter) prepare() bool {
	if a.prep != nil || a.scanOwed {
		return false
	}
	a.prep = &preparation{end: a.now + 12000, revision: a.revision}
	return true
}
func (a *arbiter) publish() bool {
	if a.prep != nil && a.now < a.prep.end {
		return false
	}
	a.prep = nil
	a.revision++
	a.scanOwed = false
	return true
}
func (a *arbiter) handoff() bool {
	p := a.prep
	if p == nil {
		return false
	}
	ok := a.now < p.end && p.ready && !p.denied && p.revision == a.revision
	a.prep = nil
	a.scanOwed = true
	return ok
}
func (a *arbiter) security() {
	a.revision++
	if a.prep != nil {
		a.prep.denied = true
	}
}
func TestPreparationHandoff(t *testing.T) {
	for seed := int64(0); seed < 10000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a := &arbiter{}
		for owner := 0; owner < 4; owner++ {
			require(t, a.prepare(), "initial lease refused")
			require(t, !a.publish(), "scan published under lease")
			a.now += int64(1 + rng.Intn(10000))
			a.prep.ready = true
			require(t, !a.publish(), "cache insertion released lease")
			late := rng.Intn(8) == 0
			revoked := rng.Intn(8) == 0
			if late {
				a.now = a.prep.end + 1
			}
			if revoked {
				a.security()
			}
			require(t, a.handoff() == (!late && !revoked), "wrong ready handoff / unauthorized frame")
			require(t, !a.prepare(), "next prep bypassed owed publication")
			require(t, a.publish(), "scan publication starved by owners")
		}
	}
}

func TestLimitsAndRetirement(t *testing.T) {
	c := &Clock{}
	g := NewFake(c)
	r := request(c, ScanMinimum, Amounts{0, 1, 8})
	p := granted(t, g, r)
	c.MS = 2000
	require(t, !g.Valid(p), "expired grant authorized new work")
	before := g.Used
	g.Cancel(p)
	require(t, g.Used == before, "deadline freed helper")
	require(t, g.Retired(p) == nil, "physical exit")
	r.Writer = true
	r.MaxHold = 26 * time.Millisecond
	require(t, g.Try(r).Refusal != nil, "unbounded writer admitted")
}

// A qualification experiment, not a passing latency assertion. Q's measured
// 40.9/42.7ms accepted-body holds motivate a fixed 1% 40..45ms tail.
func TestLongUnitTail(t *testing.T) {
	type report struct {
		ServiceMissTicks                                                                                                                       int
		Profile                                                                                                                                string
		Seeds, ForegroundSamples, TailUnits, TailArrivalSamples                                                                                int
		ForegroundP95MS, ForegroundP99MS, P95DeltaMS, P99DeltaMS, TailArrivalP95MS, TailArrivalP99MS, WorstSeedP95DeltaMS, WorstSeedP99DeltaMS int64
	}
	reports := []report{}
	for _, weak := range []bool{false, true} {
		r := report{Profile: map[bool]string{false: "reference-model", true: "weak-model"}[weak], Seeds: 1000}
		base, loaded, inflight := []int64{}, []int64{}, []int64{}
		for seed := int64(0); seed < 1000; seed++ {
			a := simulate(t, seed, weak, false, true, false)
			b := simulate(t, seed, weak, true, true, true)
			base = append(base, a.Latency...)
			loaded = append(loaded, b.Latency...)
			inflight = append(inflight, b.TailLatency...)
			r.TailUnits += b.TailUnits
			r.ServiceMissTicks += b.ServiceMissTicks
			d95 := percentile(b.Latency, 95) - percentile(a.Latency, 95)
			d99 := percentile(b.Latency, 99) - percentile(a.Latency, 99)
			if d95 > r.WorstSeedP95DeltaMS {
				r.WorstSeedP95DeltaMS = d95
			}
			if d99 > r.WorstSeedP99DeltaMS {
				r.WorstSeedP99DeltaMS = d99
			}
		}
		r.ForegroundSamples = len(loaded)
		r.TailArrivalSamples = len(inflight)
		r.ForegroundP95MS = percentile(loaded, 95)
		r.ForegroundP99MS = percentile(loaded, 99)
		r.P95DeltaMS = r.ForegroundP95MS - percentile(base, 95)
		r.P99DeltaMS = r.ForegroundP99MS - percentile(base, 99)
		r.TailArrivalP95MS = percentile(inflight, 95)
		r.TailArrivalP99MS = percentile(inflight, 99)
		reports = append(reports, r)
	}
	data, e := json.MarshalIndent(reports, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	fmt.Println(string(data))
	if path := os.Getenv("TAIL_RESULT_JSON"); path != "" {
		if e := os.WriteFile(path, append(data, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
}
