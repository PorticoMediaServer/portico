package governor

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	PreparationLifetime            = 12 * time.Second
	PreparationMaxActive           = 4
	PreparationMaxBytes      int64 = 64 << 20
	PreparationMaxJobBytes   int64 = 16 << 20
	PreparationMetadataBytes int64 = 4096
)

var (
	ErrPreparation          = errors.New("invalid preparation lease")
	ErrPreparationExpired   = errors.New("preparation absolute deadline expired")
	ErrPreparationStale     = errors.New("preparation dependencies changed")
	ErrPublicationFootprint = errors.New("publication escaped its admitted dependency footprint")
)

// PublicationFootprint names FACT dependencies, not command IDs. Zero fields
// are wildcards; an absent footprint conservatively means every dependency.
// Personal inputs may conservatively name their whole domain (all profiles).
// Only a closed trusted purpose may choose these values, never a worker/client.
type PublicationFootprint []Scope

// PreparationProfile explicitly opts a SEPARATE phase2 composition in. Nil in
// Config leaves all phase1a admission and commit semantics unchanged. Limits may
// be lowered for smaller hosts, never raised above DESIGN F-04. ReservedBytes
// includes metadata, cache, intermediate values AND copies. ProtectedBytes is a
// separately funded nonloanable foreground-memory floor, not extra capacity.
// This is admission arithmetic, not a physical-memory/latency qualification.
type PreparationProfile struct {
	MaxActive                             int
	MaxBytes, MaxJobBytes, ProtectedBytes int64
}

type PreparationRequest struct {
	Key           JobKey // full canonical query/fence identity, owned by trusted composition
	Owner         OwnerID
	Deadline      time.Time // original preparation deadline; no Renew API exists
	ReservedBytes int64
	Dependencies  PublicationFootprint
}

type PreparationDecision struct {
	Lease   *PreparationLease
	Refusal *Refusal
}

// PreparationLease has no SQL or authorization capability. Protection survives
// native window closure and Ready until the FIRST authorized value handoff.
// Physical memory is a separate holding and survives logical cancellation,
// expiry, invalidation and handoff until RetirePreparation attests actual exit.
type PreparationLease struct {
	issuer  *ledger
	grant   *Grant
	request PreparationRequest
	ctx     context.Context
	done    chan struct{}
	state   preparationState // protected by issuer.mu
	err     error
}

type preparationState uint8

const (
	preparationComputing preparationState = iota
	preparationReady
	preparationReleased
)

type preparationLedger struct {
	profile PreparationProfile
	leases  [PreparationMaxActive]*PreparationLease // includes unretired physical debt
	bytes   int64
}

func configurePreparation(c Config) (preparationLedger, error) {
	if c.Preparation == nil {
		return preparationLedger{}, nil
	}
	p := *c.Preparation
	if p.MaxActive < 1 || p.MaxActive > PreparationMaxActive || p.MaxBytes < PreparationMetadataBytes || p.MaxBytes > PreparationMaxBytes ||
		p.MaxJobBytes < PreparationMetadataBytes || p.MaxJobBytes > PreparationMaxJobBytes || p.MaxJobBytes > p.MaxBytes ||
		p.ProtectedBytes <= 0 || p.ProtectedBytes > c.Partitions[Foreground][MemoryBytes] ||
		p.MaxBytes > c.Partitions[Foreground][MemoryBytes]-p.ProtectedBytes || c.MaxGrantsPerClass <= p.MaxActive {
		return preparationLedger{}, errors.New("invalid or unfunded preparation profile")
	}
	return preparationLedger{profile: p}, nil
}

func validFootprint(f []Scope) bool {
	if len(f) > maxScopes {
		return false
	}
	for _, s := range f {
		if s.Library < 0 || s.Source < 0 {
			return false
		}
	}
	return true
}

func scopeOverlaps(a, b Scope) bool {
	return (a.Library == 0 || b.Library == 0 || a.Library == b.Library) &&
		(a.Domain == 0 || b.Domain == 0 || a.Domain == b.Domain) &&
		(a.Source == 0 || b.Source == 0 || a.Source == b.Source)
}
func footprintsOverlap(a, b []Scope) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if scopeOverlaps(x, y) {
				return true
			}
		}
	}
	return false
}

// PublicationCovered requires every actual scope to fit wholly inside ONE
// declared scope. Overlap alone is insufficient: wildcard resolution is wider.
func PublicationCovered(declared, actual PublicationFootprint) bool {
	if !validFootprint(declared) || !validFootprint(actual) {
		return false
	}
	if len(declared) == 0 {
		return true
	}
	if len(actual) == 0 {
		return false
	} // unknown actual footprint is global
	for _, x := range actual {
		covered := false
		for _, d := range declared {
			if (d.Library == 0 || d.Library == x.Library) && (d.Domain == 0 || d.Domain == x.Domain) && (d.Source == 0 || d.Source == x.Source) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// BeginPreparation is nonblocking admission: it drains a currently conflicting
// discretionary writer by refusing until that writer PHYSICALLY retires. It
// never cancels a writer or keeps a WAL snapshot between preparation quanta.
// ctx is the server-owned job lifetime, not an individual HTTP poll lifetime.
// The caller deduplicates identical jobs and attaches polls to this same lease;
// calling Begin again cannot extend or replace an owner's active protection.
func (a *Authority) BeginPreparation(ctx context.Context, q PreparationRequest) PreparationDecision {
	if a == nil || a.ledger == nil {
		return PreparationDecision{Refusal: &Refusal{Code: Invalid}}
	}
	g := a.ledger
	now := g.config.Now()
	refuse := func(code RefusalCode) PreparationDecision {
		return PreparationDecision{Refusal: &Refusal{Code: code, RetryAt: now.Add(100 * time.Millisecond)}}
	}
	if g.preparation.profile.MaxActive == 0 {
		return refuse(Unqualified)
	}
	if ctx == nil || q.Owner == 0 || q.Key.ID == "" || len(q.Key.ID) > maxKeyBytes || !validFootprint(q.Dependencies) || q.ReservedBytes < PreparationMetadataBytes || q.ReservedBytes > g.preparation.profile.MaxJobBytes {
		return refuse(Invalid)
	}
	q.Deadline = minTime(q.Deadline, now.Add(PreparationLifetime))
	if d, ok := ctx.Deadline(); ok {
		q.Deadline = minTime(q.Deadline, d)
	}
	if ctx.Err() != nil || !q.Deadline.After(now) {
		return refuse(Deadline)
	}
	if !g.mu.TryLock() {
		return refuse(Capacity)
	}
	defer g.mu.Unlock()
	g.refreshPreparations(now)
	if g.state.Load()&1 != 0 {
		return refuse(FailSafe)
	}
	free := -1
	for i, p := range g.preparation.leases {
		if p == nil {
			if free < 0 {
				free = i
			}
			continue
		}
		if p.state != preparationReleased && (p.request.Owner == q.Owner || footprintsOverlap(p.request.Dependencies, q.Dependencies)) {
			return refuse(Conflict)
		}
	}
	for _, h := range g.live {
		// All active writer grants block capture, including foreground writers.
		// Expired/cancelled writer grants still own physical publication custody.
		if h.request.Writer && footprintsOverlap(h.request.Footprint, q.Dependencies) {
			return refuse(Conflict)
		}
	}
	if free < 0 || g.preparationCount() >= g.preparation.profile.MaxActive || q.ReservedBytes > g.preparation.profile.MaxBytes-g.preparation.bytes ||
		g.counts[Foreground] >= g.config.MaxGrantsPerClass || g.sequence == math.MaxUint64 ||
		q.ReservedBytes > g.config.Partitions[Foreground][MemoryBytes]-g.used[Foreground][MemoryBytes] {
		return refuse(Capacity)
	}
	q.Key.ID = strings.Clone(q.Key.ID)
	q.Dependencies = slices.Clone(q.Dependencies)
	g.sequence++
	grant := &Grant{id: g.sequence, epoch: g.state.Load(), issuer: g}
	p := &PreparationLease{issuer: g, grant: grant, request: q, ctx: ctx, done: make(chan struct{})}
	r := Request{Key: q.Key, Owner: q.Owner, Class: Foreground, Deadline: q.Deadline, Need: Amounts{MemoryBytes: q.ReservedBytes}}
	g.live[grant.id] = &holding{grant: grant, request: r}
	g.counts[Foreground]++
	g.used[Foreground][MemoryBytes] += q.ReservedBytes
	g.preparation.bytes += q.ReservedBytes
	g.preparation.leases[free] = p
	return PreparationDecision{Lease: p}
}

func (g *ledger) preparationCount() int {
	n := 0
	for _, p := range g.preparation.leases {
		if p != nil {
			n++
		}
	}
	return n
}
func (p *PreparationLease) release(err error) {
	if p.state != preparationReleased {
		p.state = preparationReleased
		p.err = err
		close(p.done)
	}
}
func (g *ledger) refreshPreparations(now time.Time) {
	for _, p := range g.preparation.leases {
		if p == nil || p.state == preparationReleased {
			continue
		}
		switch {
		case p.ctx.Err() != nil:
			p.release(p.ctx.Err())
		case !now.Before(p.request.Deadline):
			p.release(ErrPreparationExpired)
		case p.grant.epoch != g.state.Load() || p.grant.cancelled.Load():
			p.release(ErrPreparationStale)
		}
	}
}
func (a *Authority) preparationLocked(p *PreparationLease) error {
	if p == nil || p.issuer != a.ledger || a.ledger.find(p.grant) == nil {
		return ErrPreparation
	}
	a.ledger.refreshPreparations(a.ledger.config.Now())
	if p.state == preparationReleased {
		if p.err != nil {
			return p.err
		}
		return ErrPreparation
	}
	return nil
}

// CheckPreparation is required before each quantum, cache reuse and delivery.
// It neither renews the absolute deadline nor authorizes a viewer. Dependencies
// and every security/source revision still need fresh same-snapshot validation.
func (a *Authority) CheckPreparation(p *PreparationLease) error {
	if a == nil || a.ledger == nil {
		return ErrPreparation
	}
	a.ledger.mu.Lock()
	defer a.ledger.mu.Unlock()
	return a.preparationLocked(p)
}
func (a *Authority) ReadyPreparation(p *PreparationLease) error {
	if a == nil || a.ledger == nil {
		return ErrPreparation
	}
	a.ledger.mu.Lock()
	defer a.ledger.mu.Unlock()
	if err := a.preparationLocked(p); err != nil {
		return err
	}
	p.state = preparationReady // protection deliberately remains held
	return nil
}

// HandoffPreparation linearizes the first ready response. The trusted read
// driver calls this AFTER a fresh authorized response snapshot has verified
// dependencies and copied COMPLETE values plus matching revisions/cursor into
// separately admitted output. No DB/native/consumer callback runs under mu.
// New foreground publication wins until this exact handoff; background remains
// excluded while a ready cache waits for a poll. Serialize copied values after
// release with the ordinary current-security delivery checks and output grant.
func (a *Authority) HandoffPreparation(p *PreparationLease) error {
	if a == nil || a.ledger == nil {
		return ErrPreparation
	}
	a.ledger.mu.Lock()
	defer a.ledger.mu.Unlock()
	if err := a.preparationLocked(p); err != nil {
		return err
	}
	if p.state != preparationReady {
		return ErrPreparation
	}
	p.release(nil)
	return nil
}
func (a *Authority) CancelPreparation(p *PreparationLease) error {
	if a == nil || a.ledger == nil {
		return ErrPreparation
	}
	a.ledger.mu.Lock()
	defer a.ledger.mu.Unlock()
	if p == nil || p.issuer != a.ledger || a.ledger.find(p.grant) == nil {
		return ErrPreparation
	}
	p.release(context.Canceled)
	return nil
}

// RetirePreparation is a trusted physical-exit attestation: every intermediate,
// cache and owned consumer reference must have gone. Logical release alone does
// not refund memory, owner metadata or the four-job physical-debt bound.
func (a *Authority) RetirePreparation(p *PreparationLease) error {
	if a == nil || a.ledger == nil {
		return ErrPreparation
	}
	g := a.ledger
	g.mu.Lock()
	defer g.mu.Unlock()
	if p == nil || p.issuer != g || g.find(p.grant) == nil {
		return ErrPreparation
	}
	p.release(context.Canceled)
	for i, held := range g.preparation.leases {
		if held == p {
			g.preparation.leases[i] = nil
			break
		}
	}
	g.preparation.bytes -= p.request.ReservedBytes
	g.used[Foreground][MemoryBytes] -= p.request.ReservedBytes
	g.counts[Foreground]--
	delete(g.live, p.grant.id)
	return nil
}

// Deadline/Done expose immutable job-lifetime information, never resource or
// authorization capability. Done signals observed release (or expiry on next
// authority entry); drivers also bound each quantum by Deadline itself.
func (p *PreparationLease) Deadline() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.request.Deadline
}
func (p *PreparationLease) Done() <-chan struct{} {
	if p == nil {
		return nil
	}
	return p.done
}
func (p *PreparationLease) PayloadBytes() int64 {
	if p == nil {
		return 0
	}
	return p.request.ReservedBytes - PreparationMetadataBytes
}

// Called under the SAME governor mutex as writer admission. ScanMinimum is
// discretionary publication too. Only foreground/recovery publication may
// invalidate a lease; readers/nonwriters never wait behind its conflict scope.
func (g *ledger) preparationPublicationCode(r Request, now time.Time) RefusalCode {
	if g.preparation.profile.MaxActive == 0 || !r.Writer {
		return 0
	}
	g.refreshPreparations(now)
	if !validFootprint(r.Footprint) {
		return Invalid
	}
	if r.Class == Foreground || r.Class == Recovery {
		return 0
	}
	for _, p := range g.preparation.leases {
		if p != nil && p.state != preparationReleased && footprintsOverlap(r.Footprint, p.request.Dependencies) {
			return Conflict
		}
	}
	return 0
}
func (g *ledger) preparationPublicationAdmitted(r Request) {
	if g.preparation.profile.MaxActive == 0 || !r.Writer || (r.Class != Foreground && r.Class != Recovery) {
		return
	}
	for _, p := range g.preparation.leases {
		if p != nil && footprintsOverlap(r.Footprint, p.request.Dependencies) {
			p.release(ErrPreparationStale)
		}
	}
}

// InterruptPreparations lets trusted security/media/time-critical-capture
// composition invalidate conflicting work without borrowing a writer slot.
// It never refunds resources or waits for computation/consumer cancellation.
func (a *Authority) InterruptPreparations(f PublicationFootprint) error {
	if a == nil || a.ledger == nil || !validFootprint(f) {
		return ErrPublicationFootprint
	}
	g := a.ledger
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.preparation.profile.MaxActive == 0 {
		return nil
	}
	for _, p := range g.preparation.leases {
		if p != nil && footprintsOverlap(f, p.request.Dependencies) {
			p.release(ErrPreparationStale)
		}
	}
	return nil
}

// CheckPublication is the accepting-transaction seam, immediately before
// COMMIT, AFTER current authority and resolved effects have been read. A wider
// target must roll back, expand its footprint and re-admit from zero custody.
// When phase2 is disabled this is a no-op, preserving phase1a qualification.
func (a *Authority) CheckPublication(p *Grant, expected Request, actual PublicationFootprint) error {
	if a == nil || a.ledger == nil {
		return ErrGrant
	}
	g := a.ledger
	if g.preparation.profile.MaxActive == 0 {
		return nil
	}
	if !g.mu.TryLock() {
		return ErrBusy
	}
	defer g.mu.Unlock()
	h := g.find(p)
	now := g.config.Now()
	if h == nil || !expected.Writer || p.cancelled.Load() || p.epoch != g.state.Load() || !h.request.Deadline.After(now) || !sameRequest(h.request, expected) {
		return ErrGrant
	}
	if !PublicationCovered(PublicationFootprint(expected.Footprint), actual) {
		return ErrPublicationFootprint
	}
	if code := g.preparationPublicationCode(expected, now); code != 0 {
		return ErrBusy
	}
	g.preparationPublicationAdmitted(expected)
	return nil
}
