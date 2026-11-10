package playback

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// StartInput is a request to play a title.
type StartInput struct {
	Caller Caller
	// Key is the request's Idempotency-Key and Hash a digest of its body: the same key with
	// the same body is the same start, however often it is sent.
	Key, Hash string
	ItemID    string
	Queue     *QueueLink
	Ask       Ask
	// PositionMs, when not nil, is where to start. Otherwise the title resumes, or with
	// FromStart begins at the beginning.
	PositionMs *int64
	FromStart  bool
	Paused     bool
	// Prepared, when set, is a stream got ready ahead of time for this start: it becomes the
	// session's stream, so playback carries on without a gap.
	Prepared *Prepared
}

// Prepared is the next title of a queue planned and its stream started before the current
// one ends. It is not a session yet and does not count as a stream.
type Prepared struct {
	SessionID string
	ItemID    string
	Plan      Plan
	Stream    Stream
	r         resolved
	caller    Caller
	// mu guards Stream and sess: the engine may replace the stream by itself at any time,
	// before the prepared stream has become a session or after.
	mu   sync.Mutex
	sess *session
}

// Prepare plans a title for the device asking and starts its stream under the id its session
// will have. from, when it names the caller's running session, hands its quality on. The
// caller must give the result to Start, or to Drop.
func (s *Service) Prepare(ctx context.Context, c Caller, itemID, from string) (*Prepared, error) {
	device, err := s.db.SessionDevice(ctx, c.SignInID)
	if err != nil {
		return nil, err
	}
	var ask Ask
	if sess, err := s.owned(from, c.Viewer, c.SignInID); err == nil {
		running := s.snapshot(sess)
		quality := running.Choice.Quality
		ask.Quality, ask.Network = &quality, running.Network
		c.AtHome = running.Network == NetworkLocal
	}
	r, err := s.resolve(ctx, c, device, itemID, ask, nil)
	if err != nil {
		return nil, err
	}
	p := &Prepared{SessionID: db.NewID("pbs"), ItemID: itemID, Plan: r.plan, r: r, caller: c}
	request := StartRequest{SessionID: p.SessionID, Item: r.item, Version: r.version, Plan: r.plan, Replaced: func(stream Stream) {
		p.mu.Lock()
		sess := p.sess
		if sess == nil {
			p.Stream = stream
		}
		p.mu.Unlock()
		if sess != nil {
			s.replaced(sess)(stream)
		}
	}}
	stream, err := s.streams.Start(ctx, request)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.Stream = stream
	p.mu.Unlock()
	return p, nil
}

// Drop stops a prepared stream that will not be played.
func (s *Service) Drop(p *Prepared) {
	if p != nil {
		s.streams.Stop(p.SessionID)
	}
}

// Start begins playing a title on the device asking, and ends whatever that device was
// playing. ready is false when the stream needs a moment longer: the app sends the same
// request again after RetryAfter and gets the same session. Nothing else is ever "preparing".
func (s *Service) Start(ctx context.Context, in StartInput) (out Session, ready, created bool, err error) {
	out, ready, created, err = s.start(ctx, in)
	return out, ready, created && ready, err
}

func (s *Service) start(ctx context.Context, in StartInput) (out Session, ready, created bool, err error) {
	key := startKey{in.Caller.Viewer.UserID, in.Key}
	s.mu.Lock()
	rec := s.starts[key]
	if rec != nil && rec.hash != in.Hash {
		s.mu.Unlock()
		return out, false, false, ErrKeyReused
	}
	if rec == nil {
		created = true
		rec = &startRecord{hash: in.Hash, at: s.now(), done: make(chan struct{})}
		s.starts[key] = rec
		s.mu.Unlock()
		sess, err := s.create(ctx, in)
		s.mu.Lock()
		if err != nil {
			delete(s.starts, key) // a refused start can be sent again and judged afresh
		} else {
			rec.sessionID = sess.ID
		}
		close(rec.done)
		s.mu.Unlock()
		if err != nil {
			return out, false, false, err
		}
	} else {
		s.mu.Unlock()
		select {
		case <-rec.done:
		case <-ctx.Done():
			return out, false, false, ctx.Err()
		}
	}
	sess := s.get(rec.sessionID)
	if sess == nil {
		// The first request with this key was refused, or its session is long over.
		return out, false, false, db.ErrNotFound
	}
	// A start that was told "preparing" and is now ready is still that app's first sight of
	// the session.
	s.mu.Lock()
	created = created || !sess.seen
	s.mu.Unlock()
	out, ready, err = s.awaitReady(ctx, sess)
	if ready && err == nil {
		s.mu.Lock()
		sess.seen = true
		s.mu.Unlock()
	}
	return out, ready, created, err
}

// CheckCapabilities refuses a capability document this server cannot plan from.
func (s *Service) CheckCapabilities(document json.RawMessage) error {
	return s.planner.CheckCapabilities(document)
}

// AccountLimit is how many streams the account behind a sign-in may play at once, 0 for no limit.
func (s *Service) AccountLimit(ctx context.Context, signInID int64) (int, error) {
	device, err := s.db.SessionDevice(ctx, signInID)
	if err != nil {
		return 0, err
	}
	limits, err := s.Limits(ctx)
	return limits.forAccount(device.UserPublicID), err
}

// awaitReady waits a moment for a new session's stream. A stream that fails ends the session.
func (s *Service) awaitReady(ctx context.Context, sess *session) (Session, bool, error) {
	s.mu.Lock()
	ready, ended := sess.ready, sess.State == StateEnded
	s.mu.Unlock()
	if ready || ended {
		return s.snapshot(sess), true, nil
	}
	wait, cancel := context.WithTimeout(ctx, readyWait)
	defer cancel()
	err := s.streams.Ready(wait, sess.ID)
	switch {
	case err == nil:
		s.mu.Lock()
		sess.ready = true
		s.mu.Unlock()
		return s.snapshot(sess), true, nil
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		return s.snapshot(sess), false, nil
	case ctx.Err() != nil:
		return Session{}, false, ctx.Err()
	}
	s.end(ctx, sess.ID, EndFailed, "")
	return Session{}, false, err
}

func (s *Service) create(ctx context.Context, in StartInput) (*session, error) {
	c := in.Caller
	device, err := s.db.SessionDevice(ctx, c.SignInID)
	if err != nil {
		return nil, err
	}
	var r resolved
	if in.Prepared != nil {
		r = in.Prepared.r
	} else if r, err = s.resolve(ctx, c, device, in.ItemID, in.Ask, nil); err != nil {
		return nil, err
	}
	limits, err := s.Limits(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	position := resumeAt(r.item, r.prefs)
	switch {
	case in.PositionMs != nil:
		position = max(*in.PositionMs, 0)
	case in.FromStart:
		position = 0
	}
	sess := &session{version: r.version}
	sess.Session = Session{ID: db.NewID("pbs"), UserID: c.Viewer.UserID, ProfileID: c.Viewer.ProfileID, SignInID: c.SignInID,
		Device: device, Item: r.item, VersionID: r.version.PublicID, fileID: r.version.FileID, Queue: in.Queue,
		State: StatePlaying, Choice: r.choice, Network: r.network, Plan: r.plan, Generation: 1,
		StartPositionMs: position, PositionMs: position, StartedAt: now, LeaseUntil: now.Add(lease)}
	if in.Prepared != nil {
		// The stream is already running under this id, from the start of the title.
		in.Prepared.mu.Lock()
		sess.ID, sess.Stream, sess.ready = in.Prepared.SessionID, in.Prepared.Stream, true
		in.Prepared.sess = sess
		in.Prepared.mu.Unlock()
		sess.StartPositionMs, sess.PositionMs, position = 0, 0, 0
	}
	sess.Item.Versions = nil
	if in.Paused {
		sess.State, sess.pausedSince = StatePaused, now
	}

	// Admission, replacing what this device was playing, and joining the registry are one
	// step, so two starts at once cannot both take the last allowed stream.
	s.mu.Lock()
	var replaced []*session
	account, total := 0, 0
	for _, other := range s.sessions {
		switch {
		case other.State == StateEnded:
		case other.Device.ID == device.ID:
			replaced = append(replaced, other)
		default:
			total++
			if other.UserID == c.Viewer.UserID {
				account++
			}
		}
	}
	if limit := limits.forAccount(device.UserPublicID); limit > 0 && account >= limit {
		s.mu.Unlock()
		return nil, &LimitReached{Scope: "account", Limit: limit}
	}
	if limits.Total > 0 && total >= limits.Total {
		s.mu.Unlock()
		return nil, &LimitReached{Scope: "server", Limit: limits.Total}
	}
	for _, other := range replaced {
		s.endLocked(other, EndReplaced, "")
	}
	s.started++
	sess.order = s.started
	s.sessions[sess.ID] = sess
	sess.op.Lock()
	s.mu.Unlock()
	defer sess.op.Unlock()
	for _, other := range replaced {
		s.finish(ctx, other)
	}

	stream := sess.Stream
	if in.Prepared == nil {
		stream, err = s.streams.Start(ctx, StartRequest{SessionID: sess.ID, Item: r.item, Version: r.version, Plan: r.plan, PositionMs: position,
			Replaced: s.replaced(sess)})
	}
	s.mu.Lock()
	if err != nil {
		delete(s.sessions, sess.ID)
		s.mu.Unlock()
		return nil, err
	}
	sess.Stream = stream
	ended := sess.State == StateEnded
	s.mu.Unlock()
	if ended {
		// Stopped or signed out while the stream was starting.
		s.streams.Stop(sess.ID)
	}
	if r.audioPicked || r.subtitlePicked {
		s.remember(ctx, c.Viewer, r, r.audioPicked, r.subtitlePicked)
	}
	return sess, nil
}

// replaced is what the engine calls when a session's stream gave way to another by itself:
// the session plays the new address, and its app learns of it from its next progress report.
func (s *Service) replaced(sess *session) func(Stream) {
	return func(stream Stream) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if sess.State != StateEnded {
			sess.Stream, sess.StartPositionMs = stream, sess.PositionMs
			sess.Generation++
		}
	}
}

// Change is a request to change a running session. Only what is set changes.
type Change struct {
	// State is playing or paused.
	State string
	Ask   Ask
	// AudioSet and the like say the request named that choice (Ask's nil means "not named").
	Replan bool
	// Seek moves playback; SeekID names the seek, so the same one is never applied twice.
	SeekMs *int64
	SeekID string
	// PositionMs is where the player is now: a new stream starts there.
	PositionMs *int64
	// Failure says the player could not play the current stream.
	Failure *Failure
}

// Failure is a player's report that it could not play what it was given.
type Failure struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// How long a failed way of playing a file is avoided on a device: a week when the failure
// names a fault of the stream itself, six hours when it might have been a passing problem.
const (
	certainFailureKept   = 7 * 24 * time.Hour
	uncertainFailureKept = 6 * time.Hour
)

var certainFailures = map[string]bool{"decode_error": true, "source_not_supported": true, "audio_decode_error": true,
	"manifest_incompatible": true}

// Change applies a change to the caller's own session. A change of quality, audio, version
// or a burned-in subtitle plans again, stops the stream and starts a new one where the player
// was. A change that cannot be made leaves playback as it was.
func (s *Service) Change(ctx context.Context, id string, c Caller, ch Change) (Session, error) {
	sess, err := s.owned(id, c.Viewer, c.SignInID)
	if err != nil {
		return Session{}, err
	}
	sess.op.Lock()
	defer sess.op.Unlock()
	now := s.now()
	s.mu.Lock()
	if sess.State == StateEnded {
		s.mu.Unlock()
		return Session{}, ErrEnded
	}
	before := sess.Session
	version := sess.version
	if ch.PositionMs != nil {
		s.sampleLocked(sess, *ch.PositionMs, sess.playedMs, now)
	}
	if ch.SeekMs != nil && ch.SeekID != sess.SeekID {
		s.sampleLocked(sess, *ch.SeekMs, sess.playedMs, now)
		sess.SeekID = ch.SeekID
	} else {
		ch.SeekMs = nil
	}
	if ch.State == StatePlaying || ch.State == StatePaused {
		s.setStateLocked(sess, ch.State, now)
	}
	sess.LeaseUntil = now.Add(lease)
	position := sess.PositionMs
	s.mu.Unlock()
	// A pause and a seek are saved at once.
	if ch.State != "" || ch.SeekMs != nil {
		s.Save(ctx, db.User, id)
	}

	if ch.Failure != nil {
		kept := uncertainFailureKept
		if certainFailures[ch.Failure.Code] {
			kept = certainFailureKept
		}
		if err := s.db.RecordRouteFailure(ctx, before.Device.ID, before.fileID, db.RouteFailure{Player: before.Plan.Player,
			Route: before.Plan.Route, Code: ch.Failure.Code, Detail: ch.Failure.Detail, FailedAt: now, ExpiresAt: now.Add(kept)}); err != nil {
			return Session{}, err
		}
	}
	a := ch.Ask
	replan := ch.Replan || ch.Failure != nil || a.VersionID != "" || a.Audio != nil || a.Subtitle != nil || a.Quality != nil ||
		a.SubtitleDelivery != "" || a.SubtitleOffsetMs != nil
	if !replan {
		// A seek needs nothing from the stream: the player asks for a later part of it.
		return s.snapshot(sess), nil
	}

	if a.VersionID == "" {
		a.VersionID = before.VersionID
	}
	if a.Network == "" {
		a.Network = before.Network
	}
	c.AtHome = before.Network == NetworkLocal
	r, err := s.resolve(ctx, c, before.Device, before.Item.PublicID, a, &before.Choice)
	if err != nil {
		return Session{}, err
	}
	stream := before.Stream
	generation := before.Generation
	if r.plan.Key != before.Plan.Key || r.version.PublicID != version.PublicID {
		s.streams.Stop(id)
		request := StartRequest{SessionID: id, Item: r.item, Version: r.version, Plan: r.plan, PositionMs: position, Replaced: s.replaced(sess)}
		if stream, err = s.streams.Start(ctx, request); err != nil {
			// Playback is left as it was: the stream it had is started again.
			old := StartRequest{SessionID: id, Item: r.item, Version: version, Plan: before.Plan, PositionMs: position, Replaced: s.replaced(sess)}
			if _, again := s.streams.Start(ctx, old); again != nil {
				s.end(ctx, id, EndFailed, "")
			}
			return Session{}, err
		}
		generation++
	}
	s.mu.Lock()
	ended := sess.State == StateEnded
	if !ended {
		sess.version, sess.VersionID, sess.fileID = r.version, r.version.PublicID, r.version.FileID
		sess.Choice, sess.Plan, sess.Stream = r.choice, r.plan, stream
		if generation != sess.Generation {
			sess.Generation, sess.StartPositionMs = generation, position
		}
	}
	out := sess.Session
	s.mu.Unlock()
	if ended {
		s.streams.Stop(id)
		return Session{}, ErrEnded
	}
	if a.Audio != nil || a.Subtitle != nil {
		s.remember(ctx, c.Viewer, r, a.Audio != nil, a.Subtitle != nil)
	}
	if a.SubtitleOffsetMs != nil {
		// A timing nudge is kept for this title, and does not carry to the next.
		if err := s.db.SetSubtitleOffset(ctx, c.Viewer, r.item.PublicID, *a.SubtitleOffsetMs, s.now()); err != nil {
			s.log.Error("keeping a subtitle timing nudge failed", "error", err)
		}
	}
	return out, nil
}

// LimitReached refuses a start at a stream limit. Scope is "account" (this person is playing
// as many streams as they are allowed) or "server".
type LimitReached struct {
	Scope string
	Limit int
}

func (e *LimitReached) Error() string { return "stream limit reached" }

// limitsKey is the settings row the stream limits are kept in.
const limitsKey = "playback.streams"

// Limits are the owner's caps on streams. Zero means no limit, which is the default. A
// stream is one device playing; a paused one counts until it ends.
type Limits struct {
	// Total is how many streams the whole server plays at once.
	Total int `json:"total,omitempty"`
	// PerPerson is how many each account plays at once, unless People names its own number.
	PerPerson int `json:"perPerson,omitempty"`
	// People is one account's own number, by account id. It wins over PerPerson, and never
	// lifts Total.
	People map[string]int `json:"people,omitempty"`
	// EndPausedVideoAfterMs ends video left paused this long. Music is never ended this way.
	EndPausedVideoAfterMs int64 `json:"endPausedVideoAfterMs,omitempty"`
}

func (l Limits) forAccount(userID string) int {
	if n, ok := l.People[userID]; ok {
		return n
	}
	return l.PerPerson
}

// Limits returns the owner's stream limits.
func (s *Service) Limits(ctx context.Context) (Limits, error) {
	var out Limits
	value, ok, err := s.db.Setting(ctx, limitsKey)
	if err != nil || !ok {
		return out, err
	}
	// A value this build cannot read is no limit, not a server that plays nothing.
	json.Unmarshal([]byte(value), &out)
	return out, nil
}

// SetLimits replaces the owner's stream limits. Lowering one never cuts off anyone already
// playing: a limit is only looked at when something starts.
func (s *Service) SetLimits(ctx context.Context, l Limits) error {
	if l.Total < 0 || l.PerPerson < 0 || l.EndPausedVideoAfterMs < 0 {
		return &Invalid{Field: "limits", Message: "A limit is a whole number, or 0 for none."}
	}
	for _, n := range l.People {
		if n < 0 {
			return &Invalid{Field: "people", Message: "A limit is a whole number, or 0 for none."}
		}
	}
	value, _ := json.Marshal(l)
	return s.db.SaveSetting(ctx, limitsKey, string(value), s.now())
}
