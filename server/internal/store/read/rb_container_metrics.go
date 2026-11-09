package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Private permitted-child aggregate recipe for containers.
//
// This producer is an ephemeral preparation, never a durable cache entry, a
// second scheduler or an authority. It reuses ONE governor preparation lease and
// the F-04 original deadline/restart budget. Metrics are reduced ONLY from the
// container's current permitted children, walked to genuine EOF through a
// private keyset cursor. A partial walk never publishes a metric.
//
// The recipe supports the five authored container sources (show/season/album/
// book/collection). Collections may hold only leaf media; a nested container is
// explicitly unqualified instead of being flattened into a false aggregate.
//
// Eligibility is the existing private domain seam: the closed recipe proves the
// exact container subject and refuses every restricted policy until US's accepted
// account-limit/blocked-label read ABI lands. No source dictionary, container age
// or child age is promoted into membership permission here.
type RBContainerMetric uint16

const (
	RBContainerDuration RBContainerMetric = 1 << iota
	RBContainerCommunityRating
	RBContainerCriticRating
	RBContainerFileSize
	RBContainerResolution
	RBContainerLatestAired
	RBContainerEpisodeCount
	RBContainerPersonalRating
	RBContainerLastPlayed
	RBContainerPlayCount

	rbContainerMetricsAll = RBContainerDuration | RBContainerCommunityRating | RBContainerCriticRating |
		RBContainerFileSize | RBContainerResolution | RBContainerLatestAired | RBContainerEpisodeCount |
		RBContainerPersonalRating | RBContainerLastPlayed | RBContainerPlayCount
)

const (
	// rbContainerMetricsQuantaPerAdvance bounds one poll to a few snapshots. The
	// original 12s preparation deadline still bounds the whole job.
	rbContainerMetricsQuantaPerAdvance = 8
	// rbContainerMetricsMaxQueries is the finite proof recipe for ONE snapshot:
	// the common fence plus the container point (6), or the byte/payload legs of
	// one ordered window (up to 6 for a three-key source).
	rbContainerMetricsMaxQueries = 8
	// rbContainerMetricsRestartLimit mirrors the F-04 content-restart budget.
	rbContainerMetricsRestartLimit = 2
)

// errRBContainerMetricsContent marks a window that observed a moved container
// source revision. It is not a security or authorization invalidation: those
// deny immediately and are never restartable.
var errRBContainerMetricsContent = errors.New("container metrics source content changed")

// errRBContainerMetricsCapacity refuses a retained metric value that does not
// fit the entry's already admitted payload bytes.
var errRBContainerMetricsCapacity = errors.New("container metrics exceed admitted retained bytes")

func rbContainerMetricApplies(kind int64, metric RBContainerMetric) bool {
	switch metric {
	case RBContainerDuration, RBContainerCommunityRating, RBContainerFileSize,
		RBContainerPersonalRating, RBContainerLastPlayed, RBContainerPlayCount:
		return kind == 2 || kind == 3 || kind == 6 || kind == 8 || kind == 10
	case RBContainerCriticRating, RBContainerResolution:
		return kind == 2 || kind == 3 || kind == 10
	case RBContainerLatestAired:
		// browse_contract.go lists latestAired for show only; do not widen it
		// to season/collection here.
		return kind == 2
	case RBContainerEpisodeCount:
		return kind == 2 || kind == 3
	}
	return false
}

type RBContainerMetricsInput struct {
	// View carries the current library/kind/viewer anchor. After must be nil and
	// the policy is installed by the reader, never by the caller.
	View    BrowseInput
	Subject int64
	Metrics RBContainerMetric
}

type RBContainerMetrics struct {
	Container, Kind, Members int64
	Duration                 float64
	CommunityRating          float64
	CriticRating             float64
	FileSize                 int64
	Resolution               int64
	LatestAired              string
	EpisodeCount             int64
	PersonalRating           float64
	LastPlayed               string
	PlayCount                int64
}

type RBContainerMetricsResult struct {
	Metrics RBContainerMetrics
	// Present is the requested metric mask. A metric bit that is not present
	// was never requested and its field is zero/empty rather than a measured
	// zero value.
	Present RBContainerMetric
	// Proof binds the exact ordered permitted-member set and its values. It is
	// evidence, never an authorization grant.
	Proof      [32]byte
	Complete   bool
	Statements int
}

type RBContainerMetricsAdvance struct {
	Ready      bool
	Restarts   int
	RawRows    int
	Statements int
	Snapshot   governor.SnapshotResult
}

// RBContainerMetricsReader owns the bounded preparation jobs for this harness
// composition. It creates no goroutine and no second queue.
type RBContainerMetricsReader struct {
	mu   sync.Mutex
	jobs [governor.PreparationMaxActive]*RBContainerMetricsPreparation
	pool *governor.Snapshots
	a    *governor.Authority
}

// RBContainerMetricsPreparation is one opaque preparation job. Polls retain and
// reuse it; they never re-admit, re-size or re-deadline it. No snapshot, Rows,
// statement or SQL handle survives a quantum.
type RBContainerMetricsPreparation struct {
	mu        sync.Mutex
	reader    *RBContainerMetricsReader
	entry     *rbContainerMetricsEntry
	view      BrowseInput
	spec      rbContainerMetricsChildSpec
	started   time.Time
	cursor    *rbContainerMetricsCursor
	complete  bool
	requested RBContainerMetric
	metrics   RBContainerMetrics
	proof     hash.Hash
	// copy accounting is per window (one bounded snapshot), never a renewed
	// lease. The lease separately funds retained metric values.
	copied, copyLimit int64
}

type rbContainerMetricsCursor struct {
	keys []any
}

// rbContainerMetricsEntry is the values-only governor holding. It is not a cache
// purpose shared with another task and it never escapes the reader.
type rbContainerMetricsEntry struct {
	ctx                context.Context
	owner              governor.OwnerID
	reader             *RBContainerMetricsReader
	key                [32]byte
	lease              *governor.PreparationLease
	fence              browseFence
	captured, complete bool
	delivered, closed  bool
	// restarts is the job-wide F-04 content-restart budget. It survives across
	// Advance polls; a fresh per-call counter would let one job restart on
	// every poll and exceed the limit.
	restarts                int
	metrics                 RBContainerMetrics
	proof                   [32]byte
	retained, payloadBudget int64
}

func NewRBContainerMetricsReader(pool *governor.Snapshots, a *governor.Authority) (*RBContainerMetricsReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	return &RBContainerMetricsReader{pool: pool, a: a}, nil
}

// BeginContainerMetrics is an EXPLICIT preparation start. Polls retain the
// returned job; they must not Begin again with a fresh deadline. in.View.Now is
// trusted server request-entry time and the immutable query anchor.
func (r *RBContainerMetricsReader) BeginContainerMetrics(ctx context.Context, owner governor.OwnerID, bytes int64, in RBContainerMetricsInput) (*RBContainerMetricsPreparation, error) {
	if r == nil || ctx == nil || owner == 0 || in.View.Now.IsZero() || in.View.Database == "" || len(in.View.Database) > 256 ||
		in.View.TokenHash == "" || len(in.View.TokenHash) > 256 || in.View.Library <= 0 || in.View.After != nil ||
		in.View.policy != (readPolicyBinding{}) || in.Subject <= 0 || in.Metrics == 0 || in.Metrics&^rbContainerMetricsAll != 0 {
		return nil, ErrInput
	}
	spec, ok := rbContainerMetricsChildSpecFor(in.View.Kind)
	if !ok {
		return nil, ErrUnqualified
	}
	for _, metric := range []RBContainerMetric{RBContainerDuration, RBContainerCommunityRating, RBContainerCriticRating,
		RBContainerFileSize, RBContainerResolution, RBContainerLatestAired, RBContainerEpisodeCount,
		RBContainerPersonalRating, RBContainerLastPlayed, RBContainerPlayCount} {
		if in.Metrics&metric != 0 && !rbContainerMetricApplies(in.View.Kind, metric) {
			return nil, ErrUnqualified
		}
	}
	in.View.policy = readPolicyBinding{recipe: rbContainerMetricsPolicy, subject: in.Subject}
	if _, err := resolveReadPolicy(in.View); err != nil {
		return nil, err
	}
	// This bounded typed encoding is not a retained body or arbitrary query.
	canonical, err := json.Marshal(struct {
		Database, Token  string
		Library, Subject int64
		Kind             int64
		Metrics          RBContainerMetric
		Anchor           time.Time
	}{in.View.Database, in.View.TokenHash, in.View.Library, in.Subject, in.View.Kind, in.Metrics, in.View.Now})
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(canonical)
	deadline := time.Now().Add(governor.PreparationLifetime)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if !r.mu.TryLock() {
		return nil, governor.ErrBusy
	}
	defer r.mu.Unlock()
	free := -1
	for i, job := range r.jobs {
		if job != nil && job.entry != nil && !job.entry.closed && job.entry.key == key {
			if job.entry.lease.Deadline().After(time.Now()) {
				return job, nil
			}
			free = i
			continue
		}
		if free < 0 && (job == nil || job.entry.closed) {
			free = i
		}
	}
	if free < 0 {
		return nil, governor.ErrBusy
	}
	d := r.a.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 398, ID: fmt.Sprintf("%x", key[:])}, Owner: owner, Deadline: deadline, ReservedBytes: bytes, Dependencies: governor.PublicationFootprint{{Library: in.View.Library}}})
	if d.Lease == nil {
		return nil, governor.ErrBusy
	}
	entry := &rbContainerMetricsEntry{ctx: ctx, owner: owner, reader: r, key: key, lease: d.Lease}
	job := &RBContainerMetricsPreparation{reader: r, entry: entry, view: in.View, spec: spec, started: time.Now(), requested: in.Metrics, proof: sha256.New()}
	r.jobs[free] = job
	return job, nil
}

// Advance performs a bounded number of admitted raw quanta. A content
// invalidation restarts the whole walk (at most twice) with the SAME owner,
// grant, retained byte ceiling and original absolute deadline. No prior attempt's
// values can be published after a restart.
func (p *RBContainerMetricsPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result RBContainerMetricsAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.entry.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < rbContainerMetricsRaw+5 || budget <= 0 {
		return result, ErrInput
	}
	if !p.mu.TryLock() {
		return result, governor.ErrBusy
	}
	defer p.mu.Unlock()
	if p.complete {
		err = p.check()
		result.Ready = err == nil
		return result, err
	}
	if err = p.check(); err != nil {
		return result, err
	}
	for {
		err = p.walk(ctx, request, budget, &result)
		if err == nil {
			result.Ready = p.complete
			return result, nil
		}
		if !errors.Is(err, errRBContainerMetricsContent) {
			return result, err
		}
		if p.entry.restarts >= rbContainerMetricsRestartLimit || p.entry.delivered {
			_ = p.entry.close()
			return result, ErrStale
		}
		if err = p.reset(); err != nil {
			return result, err
		}
		p.entry.restarts++
		result.Restarts++
	}
}

func (p *RBContainerMetricsPreparation) walk(ctx context.Context, request governor.Request, budget time.Duration, result *RBContainerMetricsAdvance) error {
	deadline := time.Now().Add(budget)
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if p.entry.lease.Deadline().Before(deadline) {
		deadline = p.entry.lease.Deadline()
	}
	for n := 0; n < rbContainerMetricsQuantaPerAdvance; n++ {
		if !time.Now().Before(deadline) {
			return nil
		}
		err := p.window(ctx, request, budget, result)
		if errors.Is(err, errRBContainerMetricsContent) {
			return err
		}
		if err != nil {
			return err
		}
		if p.complete || !time.Now().Before(deadline) {
			return nil
		}
	}
	return nil
}

// window performs ONE fence snapshot and ONE admitted child window. The full
// fence and its container-subject witness are established before any child value
// is copied. The byte preflight and the payload then share one immutable
// snapshot, so every measured octet belongs to the exact row being copied.
func (p *RBContainerMetricsPreparation) window(ctx context.Context, request governor.Request, budget time.Duration, result *RBContainerMetricsAdvance) (err error) {
	p.copied = 0
	p.copyLimit = request.Need[governor.MemoryBytes] - p.reader.pool.NativeRowReservation()
	if p.copyLimit <= 0 {
		return governor.ErrSnapshotBudget
	}
	if request.Deadline.After(p.entry.lease.Deadline()) {
		request.Deadline = p.entry.lease.Deadline()
	}
	parent, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.entry.ctx, cancel)
	defer func() { stop(); cancel() }()
	var fence browseFence
	result.Snapshot, err = p.reader.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbContainerMetricsMaxQueries}, func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.Statements++
			return scope.Query(readStatementID(name), args...)
		}
		in := p.view
		in.Now = in.Now.Add(time.Since(p.started))
		f, e := readBrowseFence(query, in)
		if e != nil {
			return e
		}
		fence = f
		return nil
	})
	if result.Snapshot.Refusal != nil {
		err = governor.ErrBusy
	}
	if err != nil {
		return p.fail(err)
	}
	if !p.entry.captured {
		if err = p.entry.capture(fence); err != nil {
			return p.fail(err)
		}
	} else if p.entry.fence != fence {
		if !rbContainerMetricsPinnedFenceEqual(p.entry.fence, fence) {
			return p.fail(ErrDenied)
		}
		// Only reversible revision/age fields moved: re-capture the exact fence
		// so the restarted walk and its delivery use this generation.
		if e := p.entry.capture(fence); e != nil {
			return e
		}
		return errRBContainerMetricsContent
	}
	if p.requested&RBContainerFileSize != 0 && fence.role != "owner" {
		return p.fail(ErrDenied)
	}
	legs := p.windowLegs()
	args := make([][]any, len(legs))
	for i, leg := range legs {
		// The first source bind is the requested container, never the cohort's
		// compilation example.
		one := append([]any{fence.profile, p.view.policy.subject}, p.spec.binds[1:]...)
		one = append(one, leg.keys...)
		args[i] = append(one, int64(rbContainerMetricsRaw+1))
	}
	metrics := p.metrics
	metrics.Container, metrics.Kind = p.view.policy.subject, p.view.Kind
	var pending []byte
	var next *rbContainerMetricsCursor
	exhausted := false
	var buf [8]byte
	result.Snapshot, err = p.reader.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbContainerMetricsMaxQueries}, func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.Statements++
			return scope.Query(readStatementID(name), args...)
		}
		sizes := make([][]rbContainerMetricsSize, len(legs))
		var charge int64
		for i, leg := range legs {
			rows, e := query(leg.bytes, args[i]...)
			if e != nil {
				return e
			}
			measured, c, e := rbContainerMetricsOctets(rows, rbContainerMetricsRaw+1, len(p.spec.keys))
			if e != nil {
				return e
			}
			charge += c
			if charge > p.copyLimit-p.copied {
				return governor.ErrSnapshotBudget
			}
			sizes[i] = measured
		}
		p.copied += charge
		collected := 0
		full := false
		for i, leg := range legs {
			if err := scope.Context().Err(); err != nil {
				return err
			}
			if collected >= rbContainerMetricsRaw {
				full = true
				break
			}
			take := min(len(sizes[i]), rbContainerMetricsRaw-collected)
			if take == 0 {
				continue
			}
			callArgs := append(append([]any(nil), args[i][:len(args[i])-1]...), int64(take))
			rows, e := query(leg.payload, callArgs...)
			if e != nil {
				return e
			}
			if e = rbContainerMetricsMatches(sizes[i][:take], rows); e != nil {
				return e
			}
			for _, row := range rows {
				permitted, e := rbContainerMetricsReduce(p.spec, p.view, &metrics, row)
				if e != nil {
					return e
				}
				next = &rbContainerMetricsCursor{keys: append([]any(nil), row[16:]...)}
				if !permitted {
					continue
				}
				binary.LittleEndian.PutUint64(buf[:], uint64(row[0].(int64)))
				pending = append(pending, buf[:]...)
				binary.LittleEndian.PutUint64(buf[:], uint64(row[4].(int64)))
				pending = append(pending, buf[:]...)
			}
			result.RawRows += len(rows)
			collected += len(rows)
			if len(sizes[i]) > take {
				full = true
				break
			}
		}
		if !full {
			exhausted = true
		}
		return nil
	})
	if result.Snapshot.Refusal != nil {
		err = governor.ErrBusy
	}
	if err != nil {
		return p.fail(err)
	}
	p.metrics = metrics
	if len(pending) > 0 {
		_, _ = p.proof.Write(pending)
	}
	if next != nil {
		p.cursor = next
	}
	if !exhausted {
		return nil
	}
	if err = p.entry.retain(rbContainerMetricsMask(metrics, p.requested), rbContainerMetricsProof(p.proof)); err != nil {
		return p.fail(err)
	}
	if err = p.entry.finish(); err != nil {
		return p.fail(err)
	}
	p.complete = true
	return nil
}

func rbContainerMetricsProof(h hash.Hash) [32]byte {
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// rbContainerMetricsMask removes every metric the caller did not request. A
// derived value the request never asked for (notably owner-only file size) must
// not escape merely because the reducer computes it.
func rbContainerMetricsMask(m RBContainerMetrics, requested RBContainerMetric) RBContainerMetrics {
	if requested&RBContainerDuration == 0 {
		m.Duration = 0
	}
	if requested&RBContainerCommunityRating == 0 {
		m.CommunityRating = 0
	}
	if requested&RBContainerCriticRating == 0 {
		m.CriticRating = 0
	}
	if requested&RBContainerFileSize == 0 {
		m.FileSize = 0
	}
	if requested&RBContainerResolution == 0 {
		m.Resolution = 0
	}
	if requested&RBContainerLatestAired == 0 {
		m.LatestAired = ""
	}
	if requested&RBContainerEpisodeCount == 0 {
		m.EpisodeCount = 0
	}
	if requested&RBContainerPersonalRating == 0 {
		m.PersonalRating = 0
	}
	if requested&RBContainerLastPlayed == 0 {
		m.LastPlayed = ""
	}
	if requested&RBContainerPlayCount == 0 {
		m.PlayCount = 0
	}
	return m
}

// windowLegs returns the compiled ordered continuation legs for this window.
// They run from the deepest equality prefix to the shallowest, which is the
// index's ascending order (the same proof rb_group uses for its three seeks).
func (p *RBContainerMetricsPreparation) windowLegs() []rbContainerMetricsLeg {
	if p.cursor == nil {
		if p.spec.textFirst {
			return []rbContainerMetricsLeg{{bytes: "rb.container_metrics.children." + p.spec.name + ".first.bytes", payload: "rb.container_metrics.children." + p.spec.name + ".first"}}
		}
		return []rbContainerMetricsLeg{{bytes: "rb.container_metrics.children." + p.spec.name + ".tail0.bytes", payload: "rb.container_metrics.children." + p.spec.name + ".tail0", keys: []any{int64(0)}}}
	}
	legs := make([]rbContainerMetricsLeg, 0, len(p.spec.keys))
	for level := len(p.spec.keys) - 1; level >= 0; level-- {
		legs = append(legs, rbContainerMetricsLeg{
			bytes:   "rb.container_metrics.children." + p.spec.name + ".tail" + fmt.Sprint(level) + ".bytes",
			payload: "rb.container_metrics.children." + p.spec.name + ".tail" + fmt.Sprint(level),
			keys:    append([]any(nil), p.cursor.keys[:level+1]...),
		})
	}
	return legs
}

type rbContainerMetricsLeg struct {
	bytes, payload string
	keys           []any
}

// rbContainerMetricsReduce folds one raw child row into the typed metrics. It
// never invents eligibility: a retired or unsupported row advances the private
// cursor but contributes nothing, and a foreign/unsupported member refuses the
// whole operation rather than producing a partial aggregate.
func rbContainerMetricsReduce(spec rbContainerMetricsChildSpec, view BrowseInput, metrics *RBContainerMetrics, row []any) (bool, error) {
	if len(row) != 16+len(spec.keys) {
		return false, ErrUnqualified
	}
	id, ok := row[0].(int64)
	if !ok || id <= 0 {
		return false, ErrUnqualified
	}
	if _, ok := row[4].(int64); !ok {
		return false, ErrUnqualified
	}
	kind, ok := row[2].(int64)
	if !ok {
		return false, ErrUnqualified
	}
	retired, ok := row[3].(int64)
	if !ok {
		return false, ErrUnqualified
	}
	library, ok := row[1].(int64)
	if !ok {
		return false, ErrUnqualified
	}
	if library != view.Library {
		return false, ErrUnqualified
	}
	if !rbContainerMetricsChildKind(spec, kind) {
		if retired != 0 {
			return false, nil
		}
		if spec.name != "collection" {
			// A show/season/album/book hierarchy walk also passes intermediate
			// containers; they are never folded into the aggregate.
			return false, nil
		}
		// An authored collection may only hold leaf media. A nested container has
		// no complete child proof here; refusing is safer than flattening it into
		// a plausible but false aggregate.
		return false, ErrUnqualified
	}
	if retired != 0 {
		return false, nil
	}
	metrics.Members++
	if spec.kind == 2 || spec.kind == 3 {
		metrics.EpisodeCount = metrics.Members
	}
	rbContainerMetricsMaxFloat(&metrics.Duration, row[6])
	rbContainerMetricsMaxFloat(&metrics.CommunityRating, row[7])
	rbContainerMetricsMaxFloat(&metrics.CriticRating, row[8])
	rbContainerMetricsMaxInt(&metrics.FileSize, row[9])
	rbContainerMetricsMaxInt(&metrics.Resolution, row[10])
	if latest, ok := row[11].(string); ok && latest > metrics.LatestAired {
		metrics.LatestAired = strings.Clone(latest)
	}
	rbContainerMetricsMaxFloat(&metrics.PersonalRating, row[13])
	switch plays := row[14].(type) {
	case nil:
	case int64:
		if plays < 0 || metrics.PlayCount > int64(^uint64(0)>>1)-plays {
			return false, ErrUnqualified
		}
		metrics.PlayCount += plays
	default:
		return false, ErrUnqualified
	}
	switch played := row[15].(type) {
	case nil:
	case string:
		if played > metrics.LastPlayed {
			metrics.LastPlayed = strings.Clone(played)
		}
	default:
		return false, ErrUnqualified
	}
	return true, nil
}

func rbContainerMetricsMaxFloat(dst *float64, v any) {
	switch value := v.(type) {
	case nil:
	case int64:
		if float64(value) > *dst {
			*dst = float64(value)
		}
	case float64:
		if value > *dst {
			*dst = value
		}
	}
}

func rbContainerMetricsMaxInt(dst *int64, v any) {
	switch value := v.(type) {
	case nil:
	case int64:
		if value > *dst {
			*dst = value
		}
	}
}

// Metrics copies the completed aggregate inside one fresh authorized response
// snapshot. The exact whole fence (including the container-subject witness) must
// match before any value is handed off, and the first delivery linearizes with
// the governor exactly like every other prepared purpose.
func (p *RBContainerMetricsPreparation) Metrics(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput) (RBContainerMetricsResult, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil {
		return RBContainerMetricsResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBContainerMetricsResult, BrowseReadResult, error) {
		current.Now = at
		return p.metricsPage(ctx, request, budget, output, current)
	})
}

func (p *RBContainerMetricsPreparation) metricsPage(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput) (page RBContainerMetricsResult, result BrowseReadResult, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if ctx == nil || output.Authority == nil || output.Grant == nil || output.Request.Writer || output.Request.Owner != request.Owner || output.Request.Class != request.Class ||
		len(current.Database) == 0 || len(current.Database) > 256 || len(current.TokenHash) == 0 || len(current.TokenHash) > 256 || current.Library != p.view.Library || current.Kind != p.view.Kind ||
		budget <= 0 || request.Owner != p.entry.owner || request.Class != governor.Foreground || request.Writer || current.After != nil || current.Now.IsZero() {
		return page, result, ErrInput
	}
	if !p.mu.TryLock() {
		return page, result, governor.ErrBusy
	}
	defer p.mu.Unlock()
	if !p.complete {
		return page, result, governor.ErrBusy
	}
	deadline := rbContainerMetricsEarlier(request.Deadline, p.entry.lease.Deadline())
	deadline = rbContainerMetricsEarlier(deadline, time.Now().Add(min(budget, time.Second)))
	request.Deadline = deadline
	var fresh browseFence
	validation, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	result.Publication, err = p.reader.pool.ReadForecasted(validation, request, time.Until(deadline), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbContainerMetricsMaxQueries}, func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.PublicationStatements++
			if output.live != nil {
				if e := output.live.checkContext(validation); e != nil {
					return nil, e
				}
			}
			return scope.Query(readStatementID(name), args...)
		}
		view := current
		view.Now = current.Now.Add(time.Since(started))
		view.policy = p.view.policy
		var e error
		fresh, e = readBrowseFence(query, view)
		if e != nil {
			return e
		}
		if fresh != p.entry.fence {
			return ErrStale
		}
		if e = scope.Context().Err(); e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		size := 2*rbPreparedFenceBytes(fresh) + 4096 + 64 + 2*int64(len(p.entry.metrics.LatestAired)) + 2*int64(len(p.entry.metrics.LastPlayed))
		if size > output.Request.Need[governor.MemoryBytes]/2 {
			return governor.ErrSnapshotBudget
		}
		if e = output.Authority.CheckContext(validation, output.Grant, output.Request); e != nil {
			return e
		}
		if !p.entry.delivered {
			if e = p.reader.a.HandoffPreparation(p.entry.lease); e != nil {
				return e
			}
			p.entry.delivered = true
		}
		return nil
	})
	result.Attempts = 1
	if !result.Publication.Started.IsZero() {
		result.PublicationWindows = append(result.PublicationWindows, result.Publication)
	}
	if result.Publication.Refusal != nil {
		result.Refusal = result.Publication.Refusal
		err = governor.ErrBusy
	}
	if err != nil {
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) || errors.Is(err, governor.ErrSnapshotBudget) {
			_ = p.entry.close()
		}
		return RBContainerMetricsResult{}, result, err
	}
	if err = ctx.Err(); err != nil {
		return RBContainerMetricsResult{}, result, err
	}
	// Confirm the freshly observed token/horizon at actual publication, after
	// native cleanup; expiry must not leak a formerly-authorized metric.
	at := current.Now.Add(time.Since(started))
	expires, e1 := time.Parse(time.RFC3339Nano, fresh.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fresh.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		_ = p.entry.close()
		return RBContainerMetricsResult{}, result, ErrDenied
	}
	if !time.Now().Before(deadline) {
		return RBContainerMetricsResult{}, result, context.DeadlineExceeded
	}
	if err = output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return RBContainerMetricsResult{}, result, err
	}
	page = RBContainerMetricsResult{Metrics: p.entry.metrics, Present: p.requested, Proof: p.entry.proof, Complete: true, Statements: result.PublicationStatements}
	return page, result, nil
}

func rbContainerMetricsEarlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (p *RBContainerMetricsPreparation) reset() error {
	p.proof.Reset()
	p.cursor = nil
	p.metrics = RBContainerMetrics{}
	return p.check()
}

func (p *RBContainerMetricsPreparation) check() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	if p.entry.closed {
		return ErrInput
	}
	if err := p.entry.ctx.Err(); err != nil {
		_ = p.entry.close()
		return err
	}
	if !time.Now().Before(p.entry.lease.Deadline()) {
		_ = p.entry.close()
		return governor.ErrPreparationExpired
	}
	if !p.entry.delivered {
		if err := p.reader.a.CheckPreparation(p.entry.lease); err != nil {
			_ = p.entry.close()
			return err
		}
	}
	return nil
}

func (p *RBContainerMetricsPreparation) fail(err error) error {
	if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) || errors.Is(err, governor.ErrSnapshotBudget) || errors.Is(err, governor.ErrPreparationExpired) || errors.Is(err, errRBContainerMetricsCapacity) {
		_ = p.entry.close()
	}
	return err
}

func (p *RBContainerMetricsPreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entry.close()
}

// rbContainerMetricsPinnedFenceEqual proves a content restart observed the same
// authorization context. Library/container content revisions and the container
// subject witness may move; every identity, security, policy, lifetime and
// scope field must match exactly.
func rbContainerMetricsPinnedFenceEqual(pinned, fresh browseFence) bool {
	pinned.dataRevision, fresh.dataRevision = 0, 0
	pinned.orderRevision, fresh.orderRevision = 0, 0
	pinned.metadataRevision, fresh.metadataRevision = 0, 0
	pinned.ageRevision, fresh.ageRevision = 0, 0
	pinned.policyWitness, fresh.policyWitness = "", ""
	return pinned == fresh
}

func (e *rbContainerMetricsEntry) capture(f browseFence) error {
	if e == nil {
		return ErrInput
	}
	stringsBytes := rbPreparedFenceBytes(f)
	budget := e.lease.PayloadBytes() - 2*stringsBytes - 8192
	if budget <= 0 || budget < e.retained {
		return errRBContainerMetricsCapacity
	}
	for _, field := range []*string{&f.database, &f.token, &f.account, &f.profile, &f.membership, &f.lifetime, &f.library, &f.role, &f.memberLibraries, &f.profileLibraries, &f.profileLabels, &f.expiresAt, &f.horizon, &f.root, &f.bridgeRoot, &f.policyWitness} {
		*field = strings.Clone(*field)
	}
	e.fence = f
	e.payloadBudget = budget
	e.captured = true
	return nil
}

func (e *rbContainerMetricsEntry) retain(metrics RBContainerMetrics, proof [32]byte) error {
	if e == nil || !e.captured || e.complete || e.closed {
		return ErrInput
	}
	metrics.LatestAired = strings.Clone(metrics.LatestAired)
	metrics.LastPlayed = strings.Clone(metrics.LastPlayed)
	size := int64(64 + 2*len(metrics.LatestAired) + 2*len(metrics.LastPlayed))
	if size > e.payloadBudget-e.retained {
		return errRBContainerMetricsCapacity
	}
	e.retained += size
	e.metrics = metrics
	e.proof = proof
	return nil
}

func (e *rbContainerMetricsEntry) finish() error {
	if e == nil || e.closed || e.complete || !e.captured {
		return ErrInput
	}
	if err := e.reader.a.ReadyPreparation(e.lease); err != nil {
		_ = e.close()
		return err
	}
	e.complete = true
	return nil
}

func (e *rbContainerMetricsEntry) close() error {
	if e == nil {
		return ErrInput
	}
	if e.closed {
		return nil
	}
	e.metrics = RBContainerMetrics{}
	e.fence = browseFence{}
	e.closed = true
	if err := e.reader.a.CancelPreparation(e.lease); err != nil {
		return err
	}
	if err := e.reader.a.RetirePreparation(e.lease); err != nil {
		return err
	}
	e.reader.mu.Lock()
	defer e.reader.mu.Unlock()
	for i, job := range e.reader.jobs {
		if job != nil && job.entry == e {
			e.reader.jobs[i] = nil
			break
		}
	}
	return nil
}
