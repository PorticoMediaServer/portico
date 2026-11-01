package playbackv1

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"log"
	"math"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Playback observations (P2-19, Justin 28 Oct): the playing device says where
// it is about once a second and the latest sample wins. Each live session keeps
// that latest sample, and the watched time it proves, in memory only. A single
// worker writes them in batches: every 15 seconds, sooner after a pause, seek or
// end. A user command (Stop, a change, any end) first writes the latest sample
// in its own transaction, so a final position is never lost to the batch
// schedule. A tick never takes the writer and is never refused for capacity: a
// busy writer only means more ticks replace each other before the next batch.
//
// Nothing here is durable. After a restart the in-memory owner is gone and the
// device claims a new epoch once (409 observation_epoch_changed, then claim);
// what a crash can lose is the unwritten window, at most about 15 seconds.

const (
	// ObservationEvery is how often the playing device samples.
	ObservationEvery = time.Second
	// observationFlush is how long a changed sample may wait in memory.
	observationFlush = 15 * time.Second
	// observationEdge is how soon a pause, seek, end, error or skip is written.
	observationEdge = 250 * time.Millisecond
	// observationBatch is the most sessions one writer transaction carries.
	observationBatch = 16
	// observationPull packs a batch with sessions due within this much longer.
	observationPull = 5 * time.Second
	// observationCarry is how far the played clock may run ahead of the
	// server's own elapsed time since the claim: the claim reply's network
	// delay, never more.
	observationCarry = 2 * time.Second
	// observationSilent is how long an owner may go unheard before another
	// process on the same device may claim the session without its epoch.
	observationSilent = 5 * time.Second
	// observationRenew renews a still-live but unchanged session's lease once
	// less than this remains of it (about every 30 seconds at a 120 s lease).
	observationRenew = 90 * time.Second
	// observationIdle drops an owner nobody has heard from in this long and
	// that holds nothing unwritten (its lease ended it long before).
	observationIdle = 150 * time.Second
	// observationEntries bounds memory. Only a claim is refused at it.
	observationEntries = 8192
	// observationSkips bounds the marker skips one session holds unwritten.
	observationSkips = 8
)

var (
	// ErrObservationEpoch says the sample's epoch is not this session's current
	// owner (another process took over, or the server restarted): claim again.
	ErrObservationEpoch = errors.New("observation epoch changed")
	// ErrObservationOwned refuses a claim while another live owner holds it.
	ErrObservationOwned = errors.New("observation owned")
	// ErrObservationsFull refuses a claim at the in-memory bound.
	ErrObservationsFull = errors.New("observations full")
)

// Observation is one sample from the playing device.
type Observation struct {
	ObservedPlayback *playback.ObservedPlayback `json:"observedPlayback,omitempty"`
	SubtitleTrackID  *TrackRef                  `json:"subtitleTrackId,omitempty"`
	PartIndex        *int                       `json:"partIndex,omitempty"`
	Skipped          *SkipReport                `json:"skipped,omitempty"`
	Error            *ReportedError             `json:"error,omitempty"`

	Epoch string `json:"epoch"`
	State string `json:"state"`
	// ViewTag is the channel view ETag the device holds (channels only).
	ViewTag      string `json:"viewTag,omitempty"`
	AudioTrackID string `json:"audioTrackId,omitempty"`

	Seq        int64 `json:"seq"`
	Generation int   `json:"generation"`
	PositionMs int64 `json:"positionMs"`
	// MonotonicMs is the device's own clock since it received the claim.
	MonotonicMs int64 `json:"monotonicMs"`
	// PlayClockMs is wall time actually spent playing since the claim (not
	// buffering, not paused, never multiplied by the rate). It is the
	// evidence watched time is credited from.
	PlayClockMs int64 `json:"playClockMs"`
	// SeekSerial moves on every seek or other jump in position.
	SeekSerial    int64   `json:"seekSerial"`
	Rate          float64 `json:"rate"`
	BufferedMs    int64   `json:"bufferedMs,omitempty"`
	BandwidthKbps int64   `json:"bandwidthKbps,omitempty"`
	DroppedFrames int64   `json:"droppedFrames,omitempty"`
}

// ObservationAck answers an accepted (or harmlessly stale) sample.
type ObservationAck struct {
	// Session is a channel's current view, sent only when ViewTag changed.
	Session     *SessionView `json:"session,omitempty"`
	LeaseUntil  time.Time    `json:"leaseUntil"`
	ViewTag     string       `json:"viewTag,omitempty"`
	AcceptedSeq int64        `json:"acceptedSeq"`
	// Channel says the caller should add the channel view (not on the wire).
	Channel bool `json:"-"`
}

// ObserverClaim asks to own a session's observations.
type ObserverClaim struct {
	// Replaces is the epoch handed over (JS to native, or the owner itself
	// after a restart). Empty claims a session nobody is reporting for.
	Replaces   string `json:"replaces,omitempty"`
	Generation int    `json:"generation"`
}

// ObserverGrant is the owner's epoch and cadence.
type ObserverGrant struct {
	Epoch      string    `json:"epoch"`
	LeaseUntil time.Time `json:"leaseUntil"`
	EveryMs    int64     `json:"everyMs"`
}

// ObservationStats are exact counts since the process started.
type ObservationStats struct {
	Accepted, Coalesced, Duplicate, StaleGeneration, WrongEpoch uint64
	Claims, ClaimsRefused, Rebased                              uint64
	Applied, Renewed, Drained, BatchesFailed, EvidenceRefused   uint64
	SkipsDropped                                                uint64
}

type observationCounters struct {
	accepted, coalesced, duplicate, staleGeneration, wrongEpoch atomic.Uint64
	claims, claimsRefused, rebased                              atomic.Uint64
	applied, renewed, drained, batchesFailed, evidenceRefused   atomic.Uint64
	skipsDropped                                                atomic.Uint64
}

func (c *observationCounters) snapshot() ObservationStats {
	return ObservationStats{
		Accepted: c.accepted.Load(), Coalesced: c.coalesced.Load(), Duplicate: c.duplicate.Load(), StaleGeneration: c.staleGeneration.Load(), WrongEpoch: c.wrongEpoch.Load(),
		Claims: c.claims.Load(), ClaimsRefused: c.claimsRefused.Load(), Rebased: c.rebased.Load(),
		Applied: c.applied.Load(), Renewed: c.renewed.Load(), Drained: c.drained.Load(), BatchesFailed: c.batchesFailed.Load(), EvidenceRefused: c.evidenceRefused.Load(),
		SkipsDropped: c.skipsDropped.Load(),
	}
}

// observer is one session's owner: who may report, the latest sample and the
// watched time it proves. Everything is guarded by observations.mu.
type observer struct {
	principal                identity.Principal
	session, device, epoch   string
	account, profile, author string
	channel                  bool

	claimed  time.Time
	lastSeen time.Time
	// sampled says this epoch has accepted a sample (a claim whose reply was
	// lost may be claimed again until it has).
	sampled bool

	// The evidence basis. Credit is cumulative: earned within this basis plus
	// offset from earlier ones; applied is how much of it is written.
	baseAt             time.Time
	basePlay, baseMono int64
	// chainAt is when this owner record was first claimed. All credit, across
	// clock restarts and handovers, stays within the server's time since.
	chainAt            time.Time
	earned, offset     int64
	applied            int64
	lastPlay, lastMono int64

	seq, generation int64
	latest          Observation
	latestAt        time.Time
	have, dirty     bool
	alive           bool
	version, touch  uint64
	edgeVersion     uint64
	edge            bool

	// What the last written sample said, for edges and seek evidence.
	appliedAt    time.Time
	appliedSeek  int64
	appliedState string
	appliedKnown bool
	// settled is the last reported state other than buffering.
	settled    string
	lastApply  time.Time
	leaseUntil time.Time

	skips []SkipReport
	seen  []string

	busy     bool
	retryAt  time.Time
	failures int
}

func (e *observer) credited() int64 { return e.offset + e.earned }

// due is when the worker should next write this owner (zero: nothing to do).
func (e *observer) due() time.Time {
	var at time.Time
	switch {
	case e.dirty:
		from := e.lastApply
		if from.IsZero() {
			from = e.claimed
		}
		at = from.Add(observationFlush)
		if e.edge {
			// Soon, but at most one early write a second per session.
			early := e.latestAt.Add(observationEdge)
			if second := e.lastApply.Add(time.Second); second.After(early) {
				early = second
			}
			at = minTime(at, early)
		}
	case e.alive:
		at = e.leaseUntil.Add(-observationRenew)
	default:
		return time.Time{}
	}
	if e.retryAt.After(at) {
		return e.retryAt
	}
	return at
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

type observations struct {
	mu           sync.Mutex
	entries      map[string]*observer
	running      bool
	kick         chan struct{}
	counts       observationCounters
	logged       ObservationStats
	loggedAt     time.Time
	closed       bool
	workerCancel context.CancelFunc
	workerDone   chan struct{}
	workOnce     sync.Once
	work         chan struct{}
}

// observations returns the store, made on first use.
func (s *Service) observations() *observations {
	s.obsOnce.Do(func() {
		s.obs = &observations{entries: map[string]*observer{}, kick: make(chan struct{}, 1), loggedAt: s.now()}
	})
	return s.obs
}

// ObservationStats reports the exact counters.
func (s *Service) ObservationStats() ObservationStats {
	return s.observations().counts.snapshot()
}

func newEpoch() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// requireObservationProtocol refuses to create a session for a client that
// cannot report it: such a client would play with nobody keeping its lease or
// its place.
func (s *Service) requireObservationProtocol(c Caller) error {
	if c.PlaybackProtocol < PlaybackProtocol {
		return ErrClientUpdateRequired
	}
	return nil
}

// ClaimObserver makes the caller's device the owner of a session's
// observations and returns a fresh epoch. It writes nothing.
func (s *Service) ClaimObserver(ctx context.Context, c Caller, id string, claim ObserverClaim) (ObserverGrant, error) {
	o := s.observations()
	if claim.Generation < 0 || len(claim.Replaces) > 64 {
		return ObserverGrant{}, &FieldError{Path: "generation"}
	}
	r, err := s.session(ctx, c, id)
	if err != nil {
		return ObserverGrant{}, err
	}
	if r.ended > 0 {
		return ObserverGrant{}, ErrEnded
	}
	if r.device != c.DeviceID {
		return ObserverGrant{}, ErrNotFound
	}
	if !isChannel(r) && claim.Generation != r.generation {
		return ObserverGrant{}, ErrRevision
	}
	now := s.now()
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ObserverGrant{}, context.Canceled
	}
	e := o.entries[id]
	switch {
	case e == nil:
		if len(o.entries) >= observationEntries {
			o.mu.Unlock()
			o.counts.claimsRefused.Add(1)
			return ObserverGrant{}, ErrObservationsFull
		}
		e = &observer{session: id, device: r.device, account: r.account, profile: r.profile, author: r.authority, channel: isChannel(r),
			generation: int64(claim.Generation), leaseUntil: time.UnixMilli(r.lease), chainAt: now}
		o.entries[id] = e
	case claim.Replaces != "" && claim.Replaces == e.epoch, !e.sampled, now.Sub(e.lastSeen) > observationSilent:
		// Handover keeps the same owner record, so unwritten credit and the
		// latest sample carry over and a batch in flight still marks it.
		e.offset += e.earned
	default:
		o.mu.Unlock()
		o.counts.claimsRefused.Add(1)
		return ObserverGrant{}, ErrObservationOwned
	}
	e.epoch, e.principal, e.claimed, e.lastSeen, e.sampled = newEpoch(), c.Principal, now, now, false
	e.baseAt, e.basePlay, e.baseMono, e.earned, e.lastPlay, e.lastMono, e.seq = now, 0, 0, 0, 0, 0, 0
	if e.appliedAt.IsZero() {
		e.appliedAt = now
	}
	grant := ObserverGrant{Epoch: e.epoch, EveryMs: ObservationEvery.Milliseconds(), LeaseUntil: e.leaseUntil}
	o.startLocked(s)
	o.mu.Unlock()
	o.counts.claims.Add(1)
	return grant, nil
}

func validObservation(ob Observation) error {
	switch ob.State {
	case "playing", "paused", "buffering", "ended", "error":
	default:
		return &FieldError{Path: "state"}
	}
	switch {
	case len(ob.Epoch) != 22:
		return &FieldError{Path: "epoch"}
	case ob.Seq < 1 || ob.Seq > 1<<53-1:
		return &FieldError{Path: "seq"}
	case ob.Generation < 0:
		return &FieldError{Path: "generation"}
	case ob.PositionMs < 0 || ob.PositionMs > maxStartPositionMs:
		return &FieldError{Path: "positionMs"}
	case ob.MonotonicMs < 0 || ob.MonotonicMs > 1<<53-1:
		return &FieldError{Path: "monotonicMs"}
	case ob.PlayClockMs < 0 || ob.PlayClockMs > ob.MonotonicMs:
		return &FieldError{Path: "playClockMs"}
	case ob.SeekSerial < 0 || ob.SeekSerial > 1<<53-1:
		return &FieldError{Path: "seekSerial"}
	case math.IsNaN(ob.Rate) || math.IsInf(ob.Rate, 0) || ob.Rate < 0 || ob.Rate > 4:
		return &FieldError{Path: "rate"}
	case ob.PartIndex != nil && *ob.PartIndex < 0:
		return &FieldError{Path: "partIndex"}
	case ob.BufferedMs < 0 || ob.BandwidthKbps < 0 || ob.DroppedFrames < 0:
		return &FieldError{Path: "bandwidthKbps"}
	case len(ob.ViewTag) > 128:
		return &FieldError{Path: "viewTag"}
	case !ob.ObservedPlayback.Valid():
		return &FieldError{Path: "observedPlayback"}
	}
	if k := ob.Skipped; k != nil && (!markerIDPattern.MatchString(k.MarkerID) || k.Mode != "automatic" && k.Mode != "manual" || k.PositionMs < 0 || k.PositionMs > maxStartPositionMs) {
		return &FieldError{Path: "skipped"}
	}
	if e := ob.Error; e != nil && (len(e.Code) > 64 || len(e.Detail) > 512) {
		return &FieldError{Path: "error"}
	}
	return nil
}

// Observe takes one sample. On the hit path it reads and writes nothing but
// this session's owner record: authority is checked at claim and again in the
// transaction that writes the sample.
func (s *Service) Observe(ctx context.Context, c Caller, id string, ob Observation) (ObservationAck, error) {
	if err := validObservation(ob); err != nil {
		return ObservationAck{}, err
	}
	o := s.observations()
	now := s.now()
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ObservationAck{}, context.Canceled
	}
	e := o.entries[id]
	if e == nil {
		o.mu.Unlock()
		return ObservationAck{}, s.observationMiss(ctx, c, id)
	}
	if e.account != c.Principal.AccountID || e.profile != c.Principal.ProfileID || e.author != c.Principal.Authority || e.device != c.DeviceID {
		o.mu.Unlock()
		return ObservationAck{}, ErrNotFound
	}
	if ob.Epoch != e.epoch {
		o.mu.Unlock()
		o.counts.wrongEpoch.Add(1)
		return ObservationAck{}, ErrObservationEpoch
	}
	e.principal, e.lastSeen, e.alive, e.sampled = c.Principal, now, true, true
	e.touch++
	switch {
	case ob.Seq <= e.seq:
		o.counts.duplicate.Add(1)
	case int64(ob.Generation) < e.generation:
		e.seq = ob.Seq
		o.counts.staleGeneration.Add(1)
	default:
		o.acceptLocked(e, ob, now)
	}
	ack := ObservationAck{AcceptedSeq: e.seq, LeaseUntil: e.leaseUntil, Channel: e.channel}
	o.wakeLocked()
	o.mu.Unlock()
	return ack, nil
}

// acceptLocked replaces the latest sample and raises the watched-time ceiling.
//
// Credit is a cumulative monotone ceiling (LOCAL P3): the least of the play
// clock, the device's own clock and the server's elapsed time since the basis
// began (plus the claim reply's carry). Time a late packet could not prove is
// proved by the next one; nothing beyond real elapsed time is ever credited.
func (o *observations) acceptLocked(e *observer, ob Observation, now time.Time) {
	if ob.MonotonicMs < e.lastMono || ob.PlayClockMs < e.lastPlay || ob.PlayClockMs-e.lastPlay > ob.MonotonicMs-e.lastMono {
		// The device's clocks went backwards or claim more play than time:
		// keep what was earned and start a new basis at this sample.
		e.offset += e.earned
		e.earned, e.baseAt, e.basePlay, e.baseMono = 0, now, ob.PlayClockMs, ob.MonotonicMs
		o.counts.rebased.Add(1)
	} else {
		ceiling := min(ob.PlayClockMs-e.basePlay, ob.MonotonicMs-e.baseMono, now.Sub(e.baseAt).Milliseconds()+observationCarry.Milliseconds())
		e.earned = max(e.earned, ceiling)
	}
	// One carry for the whole record: a basis restarted by a clock rollback or
	// a handover cannot earn its allowance again.
	if !e.chainAt.IsZero() {
		if limit := now.Sub(e.chainAt).Milliseconds() + observationCarry.Milliseconds() - e.offset; e.earned > limit {
			e.earned = max(limit, 0)
		}
	}
	e.lastPlay, e.lastMono = ob.PlayClockMs, ob.MonotonicMs
	// An edge is what the written session would show differently: a pause or
	// resume, a seek, an end or an error. Buffering is not one (it changes no
	// written state), so a flapping network never forces early writes.
	previous, had := e.latest, e.have
	settled := ob.State
	if settled == "buffering" {
		settled = e.settled
	}
	edge := !had || settled != e.settled || ob.SeekSerial != previous.SeekSerial || ob.State == "ended" || ob.State == "error"
	e.settled = settled
	if k := ob.Skipped; k != nil && !o.skipSeenLocked(e, *k) {
		if len(e.skips) < observationSkips {
			e.skips = append(e.skips, *k)
			edge = true
		} else {
			o.counts.skipsDropped.Add(1)
		}
	}
	if e.dirty {
		o.counts.coalesced.Add(1)
	}
	ob.ViewTag, ob.Skipped = "", nil
	e.latest, e.latestAt, e.have, e.dirty = ob, now, true, true
	e.seq, e.generation = ob.Seq, int64(ob.Generation)
	e.version++
	if edge {
		e.edge, e.edgeVersion = true, e.version
	}
	o.counts.accepted.Add(1)
}

func (o *observations) skipSeenLocked(e *observer, k SkipReport) bool {
	for _, pending := range e.skips {
		if pending.MarkerID == k.MarkerID && pending.Mode == k.Mode {
			return true
		}
	}
	for _, seen := range e.seen {
		if seen == skipKey(k) {
			return true
		}
	}
	return false
}

// skipKey names a skip by marker and mode: an automatic skip the server
// declined to record must not hide the viewer's later manual skip of the same
// marker (the skip table itself keeps one row per session and marker).
func skipKey(k SkipReport) string { return k.Mode + "/" + k.MarkerID }

// observationMiss answers a sample with no owner record: the session ended,
// is not the caller's, or the server restarted (claim again).
func (s *Service) observationMiss(ctx context.Context, c Caller, id string) error {
	r, err := s.session(ctx, c, id)
	switch {
	case err != nil:
		return err
	case r.ended > 0:
		return ErrEnded
	case r.device != c.DeviceID:
		return ErrNotFound
	}
	s.observations().counts.wrongEpoch.Add(1)
	return ErrObservationEpoch
}

// LatestPosition is the newest position the playing device reported for the
// session's current generation, if one is waiting unwritten. A change that
// names no position re-plans from here rather than from the last batch.
func (s *Service) latestObservedPosition(id string, generation int) (int64, bool) {
	o := s.observations()
	o.mu.Lock()
	defer o.mu.Unlock()
	e := o.entries[id]
	if e == nil || !e.dirty || e.latest.Generation != generation {
		return 0, false
	}
	return e.latest.PositionMs, true
}

// observedBy reports whether the caller's device owns the session's observer.
func (s *Service) observedBy(id string, c Caller) bool {
	o := s.observations()
	o.mu.Lock()
	defer o.mu.Unlock()
	e := o.entries[id]
	return e != nil && e.device == c.DeviceID && e.account == c.Principal.AccountID && e.profile == c.Principal.ProfileID && e.author == c.Principal.Authority
}

// latestObservedState is the state the playing device last reported, if any.
func (s *Service) latestObservedState(id string) (string, time.Time, bool) {
	o := s.observations()
	o.mu.Lock()
	defer o.mu.Unlock()
	e := o.entries[id]
	if e == nil || !e.have {
		return "", time.Time{}, false
	}
	return e.latest.State, e.latestAt, true
}

// retire forgets a session's owner once its end has committed.
func (o *observations) retire(id string) {
	o.mu.Lock()
	delete(o.entries, id)
	o.mu.Unlock()
}

func (o *observations) wakeLocked() {
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

func (o *observations) startLocked(s *Service) {
	o.wakeLocked()
	if o.running || o.closed {
		return
	}
	o.running = true
	ctx, cancel := context.WithCancel(context.Background())
	o.workerCancel, o.workerDone = cancel, make(chan struct{})
	done := o.workerDone
	supervise.Go("playbackv1.observations", func() { o.run(ctx, s, done) })
}

// observationCapture is what one apply writes, taken under the store lock
// after the writer was admitted, so no other apply can take the same credit.
type observationCapture struct {
	e         *observer
	principal identity.Principal
	sample    Observation
	received  time.Time
	from      time.Time
	credited  int64
	applied   int64
	version   uint64
	touch     uint64
	lastSeen  time.Time
	have      bool
	alive     bool
	seeked    bool
	errorEdge bool
	skips     []SkipReport
}

func (o *observations) captureLocked(e *observer) observationCapture {
	c := observationCapture{e: e, principal: e.principal, received: e.latestAt, from: e.appliedAt, credited: e.credited(), applied: e.applied,
		version: e.version, touch: e.touch, lastSeen: e.lastSeen, have: e.dirty, alive: e.alive}
	if c.have {
		c.sample = e.latest
		c.seeked = e.appliedKnown && e.latest.SeekSerial != e.appliedSeek
		c.errorEdge = e.latest.State == "error" && e.appliedState != "error"
		c.skips = append([]SkipReport(nil), e.skips...)
	}
	return c
}

// observationResult is what a committed apply tells the owner record.
type observationResult struct {
	consumed bool  // the credit up to capture is written
	lease    int64 // the durable lease written (0: unchanged)
	retire   bool  // the session ended
}

// mark records a committed apply. It runs in AfterCommit, before the
// writer is released, so the next apply never sees the same credit unwritten.
func (o *observations) mark(c observationCapture, res observationResult, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e := c.e
	if res.consumed {
		e.applied = max(e.applied, c.credited)
		if c.have {
			e.appliedAt = c.received
		}
	}
	if c.have {
		e.appliedSeek, e.appliedState, e.appliedKnown = c.sample.SeekSerial, c.sample.State, true
		for _, k := range c.skips {
			for i, pending := range e.skips {
				if pending.MarkerID == k.MarkerID && pending.Mode == k.Mode {
					e.skips = append(e.skips[:i], e.skips[i+1:]...)
					break
				}
			}
			e.seen = append(e.seen, skipKey(k))
			if len(e.seen) > 4*observationSkips {
				e.seen = e.seen[1:]
			}
		}
	}
	if res.lease > 0 && time.UnixMilli(res.lease).After(e.leaseUntil) {
		e.leaseUntil = time.UnixMilli(res.lease)
	}
	if e.version == c.version {
		e.dirty = false
	}
	if e.edgeVersion <= c.version {
		e.edge = false
	}
	if e.touch == c.touch {
		e.alive = false
	}
	e.lastApply, e.failures, e.retryAt = now, 0, time.Time{}
	if res.retire {
		delete(o.entries, e.session)
	}
}

// run is the single batch worker. It holds at most one writer ticket and
// exits when no session is observed.
func (o *observations) run(ctx context.Context, s *Service, done chan struct{}) {
	defer func() {
		o.mu.Lock()
		if o.workerDone == done {
			o.running = false
		}
		close(done)
		o.mu.Unlock()
	}()
	for {
		if err := o.acquireWork(ctx); err != nil {
			return
		}
		now := s.now()
		o.mu.Lock()
		if len(o.entries) == 0 {
			o.running = false
			o.mu.Unlock()
			o.releaseWork()
			return
		}
		next, batch := o.takeLocked(now)
		o.mu.Unlock()
		o.logIfChanged(now)
		if len(batch) == 0 {
			o.releaseWork()
			wait := time.Minute
			if !next.IsZero() {
				wait = max(next.Sub(now), time.Millisecond)
			}
			timer := time.NewTimer(min(wait, time.Minute))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			case <-o.kick:
				timer.Stop()
			}
			continue
		}
		_ = s.applyObservationBatch(ctx, o, batch)
		o.releaseWork()
	}
}

// takeLocked picks up to observationBatch owners once the earliest is due,
// packing in those due within observationPull, oldest first. It also drops
// owners idle long past their lease with nothing unwritten.
func (o *observations) takeLocked(now time.Time) (time.Time, []*observer) {
	type candidate struct {
		e  *observer
		at time.Time
	}
	var due []candidate
	var next time.Time
	for id, e := range o.entries {
		if e.busy {
			continue
		}
		at := e.due()
		if at.IsZero() {
			if !e.dirty && now.Sub(e.lastSeen) > observationIdle {
				delete(o.entries, id)
			}
			continue
		}
		if next.IsZero() || at.Before(next) {
			next = at
		}
		if !at.After(now.Add(observationPull)) {
			due = append(due, candidate{e, at})
		}
	}
	if next.IsZero() || next.After(now) {
		return next, nil
	}
	sort.Slice(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	batch := make([]*observer, 0, min(len(due), observationBatch))
	for _, c := range due[:min(len(due), observationBatch)] {
		c.e.busy = true
		batch = append(batch, c.e)
	}
	return next, batch
}

// observationBatchPlan budgets routine checkpoints. A newly recorded play also
// has atomic personal/history rows and their indexes; observationActivationPlan
// declares that separate event after witnessing actual history creation.
func observationBatchPlan(sessions int) dbwork.WritePlan {
	unit := dbwork.DefaultPlan(dbwork.ClassEstablishedPlayback)
	n := int64(max(sessions, 1))
	return dbwork.WritePlan{Name: "playback-observation-batch", Rows: unit.Rows * n / 4, Pages: unit.Pages * n / 4, VMOps: unit.VMOps * n / 4,
		Why: "up to 16 playing sessions' latest samples, one savepoint each; batching them is what keeps 1 Hz playback off the writer"}
}

// observationActivationPlan adds only the inherent index pages of a new history
// unit. Hosted 10k/30k/100k activation probes measured 32 rows, ~20.5k VM and
// 52–82 pages; WAL attribution found atomic rows/indexes alone exceed 32 pages.
// 96 pages allows headroom for B-tree splits. Row/VM budgets and ordinary units
// stay unchanged. One occurrence may create server and personal history together;
// restoring paused/cleared personal history is another creation, not a checkpoint.
func observationActivationPlan(sessions, activations int) dbwork.WritePlan {
	plan := observationBatchPlan(sessions)
	if activations == 0 {
		return plan
	}
	plan.Name = "playback-history-activation-batch"
	plan.Pages += 64 * int64(activations)
	plan.Why = "96 pages per actual new playback-history unit for atomic history, personal state, snapshots and their indexes; other sessions retain the 32-page checkpoint unit"
	return plan
}

const observationHistoryWitnessSQL = `SELECT
 EXISTS(SELECT 1 FROM play_history WHERE playback_id=s.media_session_id),
 EXISTS(SELECT 1 FROM personal_history WHERE profile_id=? AND id=s.media_session_id)
 FROM playback_v1_sessions s WHERE s.id=?`

// observationHistoryMask uses unique/primary-key witnesses. It never walks the
// catalogue and never chooses a plan from measured cost. Read it before and
// after the apply, inside its savepoint, so failed partial work is accounted for
// before rollback too. Channels and liveness-only captures create no history.
func observationHistoryMask(ctx context.Context, tx *sql.Tx, c observationCapture) (int, error) {
	if !c.have || c.e.channel {
		return 3, nil
	}
	var server, personal bool
	err := tx.QueryRowContext(ctx, observationHistoryWitnessSQL, identity.PersonalKey(c.principal.Viewer), c.e.session).Scan(&server, &personal)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	mask := 0
	if server {
		mask |= 1
	}
	if personal {
		mask |= 2
	}
	return mask, nil
}

// applyObservationBatch writes up to observationBatch owners in one writer
// transaction, one savepoint each: one session's failure leaves the others.
func (s *Service) applyObservationBatch(ctx context.Context, o *observations, batch []*observer) error {
	release := func(failed []*observer) {
		now := s.now()
		o.mu.Lock()
		for _, e := range batch {
			e.busy = false
		}
		for _, e := range failed {
			e.failures++
			e.retryAt = now.Add([]time.Duration{time.Second, 2 * time.Second, 5 * time.Second}[min(e.failures, 3)-1])
		}
		o.mu.Unlock()
	}
	w, err := dbwork.Begin(dbwork.WithPlan(ctx, observationBatchPlan(len(batch))), s.DB, dbwork.ClassEstablishedPlayback)
	if err != nil {
		o.counts.batchesFailed.Add(1)
		release(batch)
		return err
	}
	defer w.Rollback()
	tx, txCtx := w.Tx(), w.Context()
	var after []func()
	var failed []*observer
	var failures error
	activations := 0
	for _, e := range batch {
		o.mu.Lock()
		c := o.captureLocked(e)
		o.mu.Unlock()
		if _, err = tx.ExecContext(txCtx, `SAVEPOINT observation`); err != nil {
			break
		}
		before, witnessErr := observationHistoryMask(txCtx, tx, c)
		if witnessErr != nil {
			err = witnessErr
			break
		}
		res, post, applyErr := s.applyObservationTx(txCtx, tx, o, c, false)
		if before != 3 {
			after, witnessErr := observationHistoryMask(txCtx, tx, c)
			if witnessErr != nil {
				err = witnessErr
				break
			}
			if after&^before != 0 {
				activations++
				if err = dbwork.UsePlan(tx, observationActivationPlan(len(batch), activations)); err != nil {
					break
				}
			}
		}
		if applyErr != nil {
			failures = errors.Join(failures, applyErr)
			if _, err = tx.ExecContext(txCtx, `ROLLBACK TO observation`); err != nil {
				break
			}
			failed = append(failed, e)
		}
		if _, err = tx.ExecContext(txCtx, `RELEASE observation`); err != nil {
			break
		}
		if applyErr == nil {
			now := s.now()
			if err = dbwork.AfterCommit(tx, func() { o.mark(c, res, now) }); err != nil {
				break
			}
			after = append(after, post...)
		}
	}
	if err == nil {
		err = w.Commit()
	}
	if err != nil {
		o.counts.batchesFailed.Add(1)
		release(batch)
		return err
	}
	release(failed)
	s.hub.wake()
	for _, f := range after {
		f()
	}
	return failures
}

// drainObservationTx writes a session's unwritten sample and credit inside a
// command's own writer transaction, before the command's effect (Stop, a
// change, an end). It never changes the session's state or revision: the
// command decides those.
func (s *Service) drainObservationTx(ctx context.Context, tx *sql.Tx, id string) error {
	o := s.observations()
	o.mu.Lock()
	e := o.entries[id]
	if e == nil || (!e.dirty && !e.alive && e.credited() == e.applied) {
		o.mu.Unlock()
		return nil
	}
	c := o.captureLocked(e)
	o.mu.Unlock()
	res, _, err := s.applyObservationTx(ctx, tx, o, c, true)
	if err != nil {
		return err
	}
	now := s.now()
	o.counts.drained.Add(1)
	return dbwork.AfterCommit(tx, func() { o.mark(c, res, now) })
}

// applyObservationTx writes one capture. A batch apply also moves the
// session's state (with its event) and ends it on a reported end; a drain
// leaves state to its command. post runs after commit, outside the writer.
func (s *Service) applyObservationTx(ctx context.Context, tx *sql.Tx, o *observations, c observationCapture, drain bool) (observationResult, []func(), error) {
	// A captured sample may outlive its HTTP request and the profile retirement
	// commit. Deny before even a liveness-only lease update or channel write.
	retired, fenceErr := identity.ProfileRetiredTx(ctx, tx, c.principal.Viewer)
	if fenceErr != nil {
		return observationResult{}, nil, fenceErr
	}
	if retired {
		return observationResult{consumed: true, retire: true}, nil, nil
	}
	r, err := loadRow(ctx, tx, c.e.session)
	if errors.Is(err, ErrNotFound) {
		return observationResult{retire: true}, nil, nil
	}
	if err != nil {
		return observationResult{}, nil, err
	}
	if r.ended > 0 {
		return observationResult{retire: !drain}, nil, nil
	}
	now := s.now()
	// A lease runs from when the device was last heard, not from when the
	// batch happened to run: a stalled writer never lends a lost device time.
	lease := c.lastSeen.Add(s.lease()).UnixMilli()
	if !c.have {
		// Only liveness to write (a repeated or older-generation sample): the
		// lease, which an expiry check drained into must see.
		if !c.alive {
			return observationResult{}, nil, nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=? AND ended_ms=0 AND lease_expires_ms<?`, lease, r.id, lease); err != nil {
			return observationResult{}, nil, err
		}
		o.counts.renewed.Add(1)
		return observationResult{lease: lease}, nil, nil
	}
	if isChannel(r) {
		return s.applyChannelObservationTx(ctx, tx, o, r, c, drain, now, lease)
	}
	ob := c.sample
	proved := &catalog.PlayedTime{FromMs: c.from.UnixMilli(), ToMs: c.received.UnixMilli(), WatchedMs: c.credited - c.applied}
	fresh := ob.Generation == r.generation && r.state != "preparing"
	if !fresh {
		// The device has not caught up with a change yet: it is alive, and
		// its credit waits for a sample of the current generation.
		if _, err = tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET lease_expires_ms=max(lease_expires_ms,?) WHERE id=? AND ended_ms=0`, lease, r.id); err != nil {
			return observationResult{}, nil, err
		}
		return observationResult{lease: lease}, nil, nil
	}
	seq := r.lastSeq + 1
	var post []func()
	legacy := "playing"
	switch ob.State {
	case "paused", "buffering":
		legacy = "paused"
	case "ended":
		legacy = "ended"
		if !s.endedNearEndIn(ctx, tx, r.media, ob.PositionMs) {
			legacy = "paused"
		}
	case "error":
		legacy = "paused"
		if e := ob.Error; e != nil && c.errorEdge && r.media != "" && engineFailureCodes[e.Code] && !drain {
			principal, media, code, detail := c.principal, r.media, e.Code, e.Detail
			post = append(post, func() { _, _ = s.Playback.RecordRouteFailure(context.Background(), principal, media, code, detail) })
		}
	}
	consumed := r.media == ""
	if r.media != "" {
		seeked := c.seeked
		err = s.Playback.ObservationEvidenceTx(ctx, tx, c.principal, r.media, r.mediaGeneration, int64(seq), float64(ob.PositionMs)/1000, legacy, ob.Rate, &seeked, ob.State == "playing", c.received, proved)
		switch {
		case err == nil:
			consumed = true
		case refusedEvidence(err):
			// The device's authority or the title's policy changed since the
			// claim: its evidence is dropped, never written (and never blocks
			// the end or command it was drained into), and the media routes
			// fence the bytes.
			o.counts.evidenceRefused.Add(1)
			consumed = true
			if !drain {
				// Nothing of the sample is written and the lease is not
				// renewed: the owner is retired, so the device's next sample
				// is answered by the session's own authority check (not
				// found), and the session ends when its lease lapses.
				return observationResult{consumed: true, retire: true}, nil, nil
			}
		default:
			return observationResult{}, nil, err
		}
	}
	state := r.state
	if !drain && (ob.State == "playing" || ob.State == "paused") {
		state = ob.State
	}
	audio, subtitle, part := r.audioTrack, r.subtitleTrack, r.partIndex
	if ob.AudioTrackID != "" {
		audio = ob.AudioTrackID
	}
	if ob.SubtitleTrackID != nil {
		subtitle = string(*ob.SubtitleTrackID)
	}
	if ob.PartIndex != nil {
		part = *ob.PartIndex
	}
	changed := state != r.state
	revision := r.revision
	if changed {
		revision++
	}
	res, err := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET state=?,position_ms=?,last_seq=?,part_index=?,audio_track_id=?,subtitle_track_id=?,bandwidth_kbps=?,lease_expires_ms=max(lease_expires_ms,?),revision=?,updated_ms=?,`+pausedSinceUpdate+` WHERE id=? AND ended_ms=0 AND generation=?`,
		state, ob.PositionMs, seq, part, audio, subtitle, ob.BandwidthKbps, lease, revision, now.UnixMilli(), state, now.UnixMilli(), r.id, r.generation)
	if err != nil {
		return observationResult{}, nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return observationResult{}, nil, errors.New("playbackv1: observed session changed under its writer")
	}
	if err = playback.StoreObservedPlaybackTx(ctx, tx, r.id, r.device, r.generation, ob.Generation, int64(seq), now.UnixMilli(), ob.ObservedPlayback); err != nil {
		return observationResult{}, nil, err
	}
	r.state, r.position, r.lastSeq, r.partIndex, r.audioTrack, r.subtitleTrack, r.revision, r.lease = state, ob.PositionMs, seq, part, audio, subtitle, revision, lease
	for _, k := range c.skips {
		diagnostic, err := s.recordSkipTx(ctx, tx, r, k)
		if err != nil {
			return observationResult{}, nil, err
		}
		if diagnostic != "" && s.SkipEvidence != nil {
			position := k.PositionMs
			post = append(post, func() {
				s.SkipEvidence(context.Background(), diagnostic, map[string]int64{"count": 1, "durationMs": position})
			})
		}
	}
	result := observationResult{consumed: consumed, lease: lease}
	if !drain {
		if changed {
			if err = s.sessionEventsTx(ctx, tx, r, now, false); err != nil {
				return observationResult{}, nil, err
			}
		}
		if ob.State == "ended" {
			if err = s.endTxRaw(ctx, tx, r, now, "ended", ""); err != nil {
				return observationResult{}, nil, err
			}
			result.retire = true
			if media := r.media; media != "" {
				post = append(post, func() { _ = s.Playback.StopV1(context.Background(), media) })
			}
		}
	}
	o.counts.applied.Add(1)
	return result, post, nil
}

// applyChannelObservationTx is the channel form: the generation is the
// buffer's, watched time is live wall time, and a position at a pending seek
// target acknowledges it.
func (s *Service) applyChannelObservationTx(ctx context.Context, tx *sql.Tx, o *observations, r row, c observationCapture, drain bool, now time.Time, lease int64) (observationResult, []func(), error) {
	ob := c.sample
	st, err := s.Channels.ReadTx(ctx, tx, rowPrincipal(r), r.id)
	if err != nil {
		if isControlFault(err) {
			// The channel refuses this viewer now: nothing is written and the
			// session's own read ends it with the reason.
			o.counts.evidenceRefused.Add(1)
			return observationResult{consumed: true}, nil, nil
		}
		return observationResult{}, nil, err
	}
	stale := func() (observationResult, []func(), error) {
		_, err := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET lease_expires_ms=max(lease_expires_ms,?) WHERE id=? AND ended_ms=0`, lease, r.id)
		return observationResult{lease: lease}, nil, err
	}
	if strconv.Itoa(ob.Generation) != st.Media.BufferGeneration {
		return stale()
	}
	seq := r.lastSeq + 1
	state := r.state
	if !drain && (ob.State == "playing" || ob.State == "paused") {
		state = ob.State
	}
	changed := state != r.state
	revision := r.revision
	if changed {
		revision++
	}
	result, err := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET state=?,position_ms=?,last_seq=?,lease_expires_ms=max(lease_expires_ms,?),revision=?,updated_ms=?,`+pausedSinceUpdate+` WHERE id=? AND ended_ms=0 AND generation=? AND EXISTS(SELECT 1 FROM playback_channel_sessions c WHERE c.session_id=playback_v1_sessions.id AND c.buffer_ordinal=? AND c.ended_ms=0)`,
		state, ob.PositionMs, seq, lease, revision, now.UnixMilli(), state, now.UnixMilli(), r.id, r.generation, ob.Generation)
	if err != nil {
		return observationResult{}, nil, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return stale()
	}
	proved := &catalog.PlayedTime{FromMs: c.from.UnixMilli(), ToMs: c.received.UnixMilli(), WatchedMs: c.credited - c.applied}
	if err = catalog.RecordLiveObservationTx(tx, r.id, c.received.UnixMilli(), ob.State, proved); err != nil {
		return observationResult{}, nil, err
	}
	ack := ""
	if q := st.Desired.Seek; q != nil && q.Kind == "position" && q.PositionUS != nil && q.Generation == st.Media.BufferGeneration && st.AcknowledgedSeekID != q.ID {
		target, _ := strconv.ParseInt(*q.PositionUS, 10, 64)
		if d := ob.PositionMs*1000 - target; d >= -1_000_000 && d <= 1_000_000 {
			ack = q.ID
		}
	}
	// A position outside the window or a gap is recorded as the seek's error;
	// neither fails the sample.
	if observedErr := s.Channels.ObserveTx(ctx, tx, r.id, st.Media.BufferGeneration, int64(seq), ob.PositionMs*1000, ack); observedErr != nil {
		if !isControlFault(observedErr) {
			return observationResult{}, nil, observedErr
		}
	} else if err = playback.StoreObservedPlaybackTx(ctx, tx, r.id, r.device, r.generation, ob.Generation, int64(seq), now.UnixMilli(), ob.ObservedPlayback); err != nil {
		return observationResult{}, nil, err
	}
	res := observationResult{consumed: true, lease: lease}
	if !drain {
		if changed {
			r.state, r.revision = state, revision
			if err = s.sessionEventsTx(ctx, tx, r, now, false); err != nil {
				return observationResult{}, nil, err
			}
		}
		if ob.State == "ended" {
			if err = s.endTxRaw(ctx, tx, r, now, "ended", ""); err != nil {
				return observationResult{}, nil, err
			}
			res.retire = true
		}
	}
	o.counts.applied.Add(1)
	return res, nil, nil
}

// refusedEvidence is an authority or policy answer, not a failure: retrying
// the same evidence would be refused again.
func refusedEvidence(err error) bool {
	return errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrNotVisible)
}

// logIfChanged writes one summary line a minute while anything changed. The
// counters are exact; nothing is sampled and no session is named.
func (o *observations) logIfChanged(now time.Time) {
	if now.Sub(o.loggedAt) < time.Minute {
		return
	}
	current := o.counts.snapshot()
	if current == o.logged {
		o.loggedAt = now
		return
	}
	d := func(a, b uint64) uint64 { return a - b }
	l := o.logged
	log.Printf("Playback observations in the last minute: %d accepted (%d replaced unwritten, %d repeated, %d for an old generation, %d for another epoch), %d claims (%d refused), %d written, %d renewed, %d drained by commands, %d batches failed, %d refused by authority, %d clock restarts, %d skips dropped",
		d(current.Accepted, l.Accepted), d(current.Coalesced, l.Coalesced), d(current.Duplicate, l.Duplicate), d(current.StaleGeneration, l.StaleGeneration), d(current.WrongEpoch, l.WrongEpoch),
		d(current.Claims, l.Claims), d(current.ClaimsRefused, l.ClaimsRefused), d(current.Applied, l.Applied), d(current.Renewed, l.Renewed), d(current.Drained, l.Drained),
		d(current.BatchesFailed, l.BatchesFailed), d(current.EvidenceRefused, l.EvidenceRefused), d(current.Rebased, l.Rebased), d(current.SkipsDropped, l.SkipsDropped))
	o.logged, o.loggedAt = current, now
}
