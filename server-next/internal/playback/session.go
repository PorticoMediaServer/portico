package playback

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

const (
	// lease is how long a session lives without a word from its device.
	lease = 2 * time.Minute
	// How often a device reports where it is. It also reports at once on pause, seek and stop.
	reportPlaying = 10 * time.Second
	reportPaused  = 30 * time.Second
	// readyWait is how long a start waits for a stream before it answers "preparing".
	readyWait = 1500 * time.Millisecond
	// RetryAfter is when an app that was told "preparing" sends the same start again.
	RetryAfter = 500 * time.Millisecond
	// startsKept is how long a start is remembered by its Idempotency-Key, and endedKept how
	// long an ended session can still be read, so its app can say why it ended.
	startsKept = 2 * time.Minute
	endedKept  = 2 * time.Minute
	// saveEvery is how often progress is written. A device reports every ten seconds, so a
	// crash loses at most about fifteen.
	saveEvery  = 5 * time.Second
	sweepEvery = 5 * time.Second
)

// Session states.
const (
	StatePlaying = "playing"
	StatePaused  = "paused"
	StateEnded   = "ended"
)

// Why a session ended, when its own device did not stop it.
const (
	EndStopped       = "stopped"
	EndReplaced      = "replaced"       // the same device started something else
	EndLeaseExpired  = "lease_expired"  // the device went quiet
	EndSignedOut     = "signed_out"     // the sign-in was ended or the account turned off
	EndAccess        = "access_removed" // the profile may no longer see the title
	EndTerminated    = "terminated"     // the owner or an administrator stopped it
	EndPausedTooLong = "paused_timeout"
	EndFailed        = "failed"
)

// ErrEnded: the session is known and over.
var ErrEnded = errors.New("this playback has ended")

// ErrKeyReused: the Idempotency-Key was already used for a different start.
var ErrKeyReused = errors.New("idempotency key reused with a different request")

// QueueLink ties a session to the queue entry it plays.
type QueueLink struct {
	QueueID string `json:"queueId"`
	EntryID string `json:"entryId"`
}

// End says why a session ended and what the viewer is told.
type End struct {
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// Session is one device playing one title. Values of this type handed out by the Service are
// copies.
type Session struct {
	ID        string
	UserID    int64
	ProfileID int64
	// SignInID is the sign-in the session was started from. Ending it ends the session.
	SignInID int64
	Device   db.SessionDevice

	Item      db.PlaybackItem // without its versions
	VersionID string
	fileID    int64
	Queue     *QueueLink

	State   string
	Choice  Choice
	Network string
	Plan    Plan
	Stream  Stream
	// Generation counts the streams this session has had. It is told to the app and never
	// asked of it: a replaced stream has a new address, and that is the whole guard.
	Generation      int
	StartPositionMs int64
	SeekID          string

	// PositionMs is where the device last said it was.
	PositionMs int64
	StartedAt  time.Time
	// order is the session's place among all that were ever started: lists are oldest first.
	order       int64
	LeaseUntil  time.Time
	pausedSince time.Time
	End         *End
	endedAt     time.Time
}

// ReportEvery is how often the session's device should say where it is.
func (s Session) ReportEvery() time.Duration {
	if s.State == StatePaused {
		return reportPaused
	}
	return reportPlaying
}

// Viewer is who the session plays for.
func (s Session) Viewer() db.Viewer { return db.Viewer{UserID: s.UserID, ProfileID: s.ProfileID} }

// Video reports whether the session plays video; the rest is music and audiobooks.
func (s Session) Video() bool {
	return s.Item.Kind == db.KindMovie || s.Item.Kind == db.KindEpisode
}

type session struct {
	Session
	// op serialises starting, changing and stopping this session's stream.
	op      sync.Mutex
	version db.PlaybackVersion
	ready   bool
	// seen says the app has been handed the session as ready.
	seen bool
	// The latest progress sample and whether it still has to be saved.
	sampleAt time.Time
	playedMs int64
	dirty    bool
}

type startKey struct {
	userID int64
	key    string
}

type startRecord struct {
	hash      string
	sessionID string
	at        time.Time
	// done is closed once the first request with this key has made its session, or failed.
	done chan struct{}
}

// Service holds every running session.
type Service struct {
	db      *db.DB
	planner Planner
	streams Streams
	log     *slog.Logger
	now     func() time.Time

	mu       sync.Mutex
	sessions map[string]*session
	starts   map[startKey]*startRecord
	started  int64
	// accessChecked is when running sessions were last checked against what may be seen.
	accessChecked time.Time
}

// New returns the session service. Run must be started for leases and progress to be kept.
// now is the server's clock.
func New(d *db.DB, planner Planner, streams Streams, log *slog.Logger, now func() time.Time) *Service {
	s := &Service{db: d, planner: planner, streams: streams, log: log, now: now,
		sessions: map[string]*session{}, starts: map[startKey]*startRecord{}}
	d.WatchSessions(s.signInsChanged)
	return s
}

// Run saves progress in batches and ends sessions whose device went quiet, until ctx ends.
// Then it saves what is left, so a clean shutdown loses nothing.
func (s *Service) Run(ctx context.Context) {
	sweep, save := time.NewTicker(sweepEvery), time.NewTicker(saveEvery)
	defer sweep.Stop()
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Save(context.Background(), db.User)
			return
		case <-save.C:
			s.Save(ctx, db.Background)
		case <-sweep.C:
			s.Sweep(ctx)
		}
	}
}

func (s *Service) get(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

// owned returns the caller's own session: started from this sign-in, for this profile.
// Anyone else's is not found.
func (s *Service) owned(id string, v db.Viewer, signInID int64) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess == nil || sess.UserID != v.UserID || sess.ProfileID != v.ProfileID || sess.SignInID != signInID {
		return nil, db.ErrNotFound
	}
	return sess, nil
}

func (s *Service) snapshot(sess *session) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sess.Session
}

// Get returns the caller's own session. An ended one can be read for a couple of minutes.
func (s *Service) Get(id string, v db.Viewer, signInID int64) (Session, error) {
	sess, err := s.owned(id, v, signInID)
	if err != nil {
		return Session{}, err
	}
	return s.snapshot(sess), nil
}

// AccountSessions lists what an account is playing now, on every device and profile,
// oldest first.
func (s *Service) AccountSessions(userID int64) []Session {
	return s.list(func(sess *session) bool { return sess.UserID == userID })
}

// All lists every running session, for the owner's Now playing.
func (s *Service) All() []Session { return s.list(func(*session) bool { return true }) }

func (s *Service) list(want func(*session) bool) []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Session
	for _, sess := range s.sessions {
		if sess.State != StateEnded && want(sess) {
			out = append(out, sess.Session)
		}
	}
	sortSessions(out)
	return out
}

func sortSessions(list []Session) {
	slices.SortFunc(list, func(a, b Session) int { return cmp.Compare(a.order, b.order) })
}

// Report is what a device says about its playback: at least every ReportEvery, and at once
// on pause and seek.
type Report struct {
	// State is playing, paused, buffering, ended or error.
	State      string
	PositionMs int64
	// PlayedMs is how long the title has actually played in this session: not paused, not
	// buffering, not scaled by speed.
	PlayedMs int64
}

// Report takes the latest word from a session's device, renews its lease and returns the
// session. The latest sample wins; it is saved with the next batch, or at once when the
// device paused, finished or jumped.
func (s *Service) Report(ctx context.Context, id string, v db.Viewer, signInID int64, r Report) (Session, error) {
	sess, err := s.owned(id, v, signInID)
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	s.mu.Lock()
	if sess.State == StateEnded {
		s.mu.Unlock()
		return Session{}, ErrEnded
	}
	// A jump is a position that is not where playing on from the last sample would be.
	expected := sess.PositionMs
	if sess.State == StatePlaying && !sess.sampleAt.IsZero() {
		expected += now.Sub(sess.sampleAt).Milliseconds()
	}
	jumped := !sess.sampleAt.IsZero() && abs(r.PositionMs-expected) > 2*reportPlaying.Milliseconds()
	state := sess.State
	switch r.State {
	case StatePaused:
		state = StatePaused
	case StatePlaying:
		state = StatePlaying
	}
	edge := jumped || state != sess.State || r.State == StateEnded
	s.setStateLocked(sess, state, now)
	s.sampleLocked(sess, r.PositionMs, r.PlayedMs, now)
	sess.LeaseUntil = now.Add(lease)
	out := sess.Session
	s.mu.Unlock()
	if edge {
		s.Save(ctx, db.User, id)
	}
	return out, nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func (s *Service) setStateLocked(sess *session, state string, now time.Time) {
	if state == StatePaused && sess.State != StatePaused {
		sess.pausedSince = now
	}
	sess.State = state
}

func (s *Service) sampleLocked(sess *session, positionMs, playedMs int64, now time.Time) {
	sess.PositionMs = max(positionMs, 0)
	// Played time only grows, and never faster than the clock.
	sess.playedMs = min(max(sess.playedMs, playedMs), now.Sub(sess.StartedAt).Milliseconds())
	sess.sampleAt, sess.dirty = now, true
}

// Save writes the progress that has not been saved yet: of the named sessions, or of all.
func (s *Service) Save(ctx context.Context, p db.Priority, only ...string) {
	s.mu.Lock()
	var batch []db.Progress
	var owners []*session
	add := func(sess *session) {
		if sess == nil || !sess.dirty {
			return
		}
		sess.dirty = false
		owners = append(owners, sess)
		batch = append(batch, db.Progress{Viewer: sess.Viewer(), ItemID: sess.Item.PublicID, DeviceID: sess.Device.ID,
			PlayID: sess.ID, PositionMS: sess.PositionMs, At: sess.sampleAt, WatchedMS: sess.playedMs})
	}
	if len(only) > 0 {
		for _, id := range only {
			add(s.sessions[id])
		}
	} else {
		for _, sess := range s.sessions {
			add(sess)
		}
	}
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if _, err := s.db.SaveProgress(context.WithoutCancel(ctx), p, batch); err != nil {
		s.log.Error("saving playback progress failed", "error", err)
		s.mu.Lock()
		for _, sess := range owners {
			sess.dirty = true
		}
		s.mu.Unlock()
	}
}

// Stop ends a session from its own device, or from another of the account's devices (the
// stream limit screen lists them to be stopped). positionMs, when not nil, is the device's
// final position. Stopping a session that is unknown or already over succeeds.
func (s *Service) Stop(ctx context.Context, id string, userID int64, positionMs *int64) {
	s.mu.Lock()
	sess := s.sessions[id]
	if sess == nil || sess.UserID != userID || sess.State == StateEnded {
		s.mu.Unlock()
		return
	}
	if positionMs != nil {
		s.sampleLocked(sess, *positionMs, sess.playedMs, s.now())
	}
	s.endLocked(sess, EndStopped, "")
	s.mu.Unlock()
	s.finish(ctx, sess)
}

// Terminate ends any session with a message for its viewer: the owner or an administrator
// stopped it.
func (s *Service) Terminate(ctx context.Context, id, message string) {
	s.end(ctx, id, EndTerminated, message)
}

func (s *Service) end(ctx context.Context, id, reason, message string) {
	s.mu.Lock()
	sess := s.sessions[id]
	if sess == nil || sess.State == StateEnded {
		s.mu.Unlock()
		return
	}
	s.endLocked(sess, reason, message)
	s.mu.Unlock()
	s.finish(ctx, sess)
}

// endLocked marks a session over. From here it no longer counts as a stream and its device's
// reports are refused. finish must follow, outside the lock.
func (s *Service) endLocked(sess *session, reason, message string) {
	sess.State, sess.End, sess.endedAt = StateEnded, &End{Reason: reason, Message: message}, s.now()
}

// finish saves an ended session's last position and stops its stream, whose addresses stop
// working. It waits for a change still in flight, so no stream is left behind.
func (s *Service) finish(ctx context.Context, sess *session) {
	s.Save(ctx, db.User, sess.ID)
	sess.op.Lock()
	s.streams.Stop(sess.ID)
	sess.op.Unlock()
}

// Sweep ends sessions whose device went quiet and paused video the owner does not keep, and
// forgets ended sessions and old start keys.
func (s *Service) Sweep(ctx context.Context) {
	now := s.now()
	pausedLimit := time.Duration(0)
	s.mu.Lock()
	var over []*session
	anyPausedVideo := false
	for id, sess := range s.sessions {
		switch {
		case sess.State == StateEnded:
			if now.Sub(sess.endedAt) > endedKept {
				delete(s.sessions, id)
			}
		case now.After(sess.LeaseUntil):
			s.endLocked(sess, EndLeaseExpired, "")
			over = append(over, sess)
		case sess.State == StatePaused && sess.Video():
			anyPausedVideo = true
		}
	}
	for key, rec := range s.starts {
		if now.Sub(rec.at) > startsKept {
			delete(s.starts, key)
		}
	}
	s.mu.Unlock()
	if anyPausedVideo {
		if limits, err := s.Limits(ctx); err == nil {
			pausedLimit = time.Duration(limits.EndPausedVideoAfterMs) * time.Millisecond
		}
	}
	if pausedLimit > 0 {
		s.mu.Lock()
		for _, sess := range s.sessions {
			if sess.State == StatePaused && sess.Video() && now.Sub(sess.pausedSince) > pausedLimit {
				s.endLocked(sess, EndPausedTooLong, "")
				over = append(over, sess)
			}
		}
		s.mu.Unlock()
	}
	for _, sess := range over {
		s.finish(ctx, sess)
	}
	s.recheckAccess(ctx, now)
}

// accessEvery is how often running sessions are checked against what their profile may see.
// A sign-in's access changing is acted on at once; this catches a title that became hidden
// some other way (the owner gave it a label the profile has blocked), and a check that failed.
const accessEvery = time.Minute

func (s *Service) recheckAccess(ctx context.Context, now time.Time) {
	s.mu.Lock()
	due := now.Sub(s.accessChecked) >= accessEvery
	var running []Session
	if due {
		s.accessChecked = now
		for _, sess := range s.sessions {
			if sess.State != StateEnded {
				running = append(running, sess.Session)
			}
		}
	}
	s.mu.Unlock()
	for _, sess := range running {
		if ok, err := s.db.Visible(ctx, sess.Viewer(), sess.Item.ID); err == nil && !ok {
			s.end(ctx, sess.ID, EndAccess, "")
		}
	}
}

// signInsChanged is called by the database straight after a commit that ended sign-ins or
// changed what some may see. Sessions of an ended sign-in are over before it returns; the
// streams are stopped, and the others checked, off the writer.
func (s *Service) signInsChanged(ended, changed []int64) {
	in := func(list []int64, id int64) bool {
		for _, v := range list {
			if v == id {
				return true
			}
		}
		return false
	}
	var stop, check []*session
	s.mu.Lock()
	for _, sess := range s.sessions {
		switch {
		case sess.State == StateEnded:
		case in(ended, sess.SignInID):
			s.endLocked(sess, EndSignedOut, "")
			stop = append(stop, sess)
		case in(changed, sess.SignInID):
			check = append(check, sess)
		}
	}
	s.mu.Unlock()
	if len(stop)+len(check) == 0 {
		return
	}
	go func() {
		ctx := context.Background()
		for _, sess := range stop {
			s.finish(ctx, sess)
		}
		for _, sess := range check {
			ok, err := s.db.Visible(ctx, sess.Viewer(), sess.Item.ID)
			switch {
			case err != nil:
				// The check could not be made: the next sweep makes it again.
				s.mu.Lock()
				s.accessChecked = time.Time{}
				s.mu.Unlock()
			case !ok:
				s.end(ctx, sess.ID, EndAccess, "")
			}
		}
	}()
}
