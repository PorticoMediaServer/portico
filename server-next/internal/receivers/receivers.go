// Package receivers keeps the account's devices that are open to playback from its other
// devices, and what is being asked of them: to play something (a move), or to let another
// device control what it plays. All of it lives in memory; a restart forgets it, and the
// devices open themselves again and ask again. What a TV remembers about which devices it
// allowed is kept on the TV, which answers a repeat request by itself.
package receivers

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/livefeed"
)

const (
	// Present is how long a device stays open to playback after it last said so. It says so
	// every RenewEvery.
	Present    = 2 * time.Minute
	RenewEvery = time.Minute
	// Timeout is how long a device has to start what it was asked to play. After it the
	// request has failed and nothing else changes.
	Timeout = 2 * time.Minute
	// kept is how long an answered request can still be read.
	kept = 2 * time.Minute
	// perReceiver bounds the requests one device holds, open and answered, and the devices
	// it has allowed in this run of the server.
	perReceiver = 16
	maxAllowed  = 64
)

// Request kinds.
const (
	KindPlay    = "play"
	KindControl = "control"
)

// Request states. Asking waits for the device's person; allowed means the device agreed
// (for play, it is starting); playing, declined, failed and cancelled are final.
const (
	StateAsking    = "asking"
	StateAllowed   = "allowed"
	StatePlaying   = "playing"
	StateDeclined  = "declined"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// Why a request failed.
const (
	FailedTimedOut     = "timed_out"
	FailedCouldntStart = "couldnt_start"
	FailedClosed       = "not_accepting" // the device stopped accepting playback
)

var (
	// ErrNotAccepting: the device is not open to playback from other devices.
	ErrNotAccepting = errors.New("the device does not accept playback from other devices")
	// ErrAnswered: the request was already answered, cancelled or timed out.
	ErrAnswered = errors.New("the request is over")
	// ErrBusy: the device already holds as many requests as it may.
	ErrBusy = errors.New("the device has too many requests")
	// ErrKeyReused: the Idempotency-Key was already used for a different request.
	ErrKeyReused = errors.New("idempotency key reused with a different request")
)

// Device is one signed-in device of an account.
type Device struct {
	SignInID  int64
	UserID    int64
	ProfileID int64
	DeviceID  int64
	// PublicID is the device's id, and the receiver's.
	PublicID string
	Name     string
	Platform string
	// SignInPublicID names the sign-in, so a device can remember which ones it allowed.
	SignInPublicID string
}

// Receiver is a device open to playback, as listed.
type Receiver struct {
	Device
	// Allowed lists the sign-ins the device let control it in this run of the server.
	Allowed []int64
	SeenAt  time.Time
}

// Play is what a device is asked to play, and from where.
type Play struct {
	ItemID     string  `json:"itemId"`
	PositionMs int64   `json:"positionMs"`
	AudioID    *string `json:"audioTrackId,omitempty"`
	// SubtitleID is the subtitle to show; "" is off and absent is the device's own choice.
	SubtitleID *string `json:"subtitleTrackId,omitempty"`
	// QueueID is the asking device's queue, for the device to copy with POST /v1/queues.
	QueueID string `json:"queueId,omitempty"`
}

// Request is one thing asked of a device.
type Request struct {
	ID    string
	Kind  string
	From  Device
	To    Device
	State string
	// Failed says why a failed request failed.
	Failed string
	Play   *Play
	// Moving is the asking device's own session that stops once the device plays.
	Moving string
	// Playing is the device's session once it reports playing.
	Playing   string
	CreatedAt time.Time
	UpdatedAt time.Time

	key, hash string
}

// Open reports whether the request is still waiting for its device: to be answered, or for a
// move, to start playing.
func (r Request) Open() bool {
	return r.State == StateAsking || r.State == StateAllowed && r.Kind == KindPlay
}

type receiver struct {
	Receiver
}

// Service holds every open device and every request. It is safe for concurrent use. Its
// size is bounded: a device is forgotten two minutes after it last said it was open, and each
// device holds at most 16 requests, each kept two minutes after it is over.
type Service struct {
	live *livefeed.Publisher
	now  func() time.Time

	mu        sync.Mutex
	receivers map[int64]*receiver // by sign-in
	requests  map[string]*Request
}

// New returns the service. now is the server's clock.
func New(live *livefeed.Publisher, now func() time.Time) *Service {
	return &Service{live: live, now: now, receivers: map[int64]*receiver{}, requests: map[string]*Request{}}
}

func (s *Service) tell(userID int64, op, id string) {
	s.live.ToAccount(userID, livefeed.TopicReceiver, op, id)
}

// Open says a device accepts playback from its account's other devices, and keeps it listed
// for Present. Another sign-in of the same device and account gives way to this one.
func (s *Service) Open(d Device) Receiver {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	r := s.receivers[d.SignInID]
	if r == nil {
		for id, other := range s.receivers {
			if other.DeviceID == d.DeviceID && other.UserID == d.UserID {
				s.closeLocked(id)
			}
		}
		r = &receiver{Receiver{Device: d}}
		s.receivers[d.SignInID] = r
		s.tell(d.UserID, livefeed.OpUpdated, "")
	}
	r.Device, r.SeenAt = d, s.now()
	return r.copy()
}

// Close says a device no longer accepts playback from other devices. What it allowed is
// forgotten and what was being asked of it fails.
func (s *Service) Close(signInID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked(signInID)
}

func (s *Service) closeLocked(signInID int64) {
	r := s.receivers[signInID]
	if r == nil {
		return
	}
	delete(s.receivers, signInID)
	for _, req := range s.requests {
		if req.To.SignInID == signInID && req.Open() {
			s.finishLocked(req, StateFailed, FailedClosed)
		}
	}
	s.tell(r.UserID, livefeed.OpUpdated, "")
}

func (r *receiver) copy() Receiver {
	out := r.Receiver
	out.Allowed = slices.Clone(r.Allowed)
	return out
}

// List returns the account's devices that accept playback, in the order they opened.
func (s *Service) List(userID int64) []Receiver {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	var out []Receiver
	for _, r := range s.receivers {
		if r.UserID == userID {
			out = append(out, r.copy())
		}
	}
	slices.SortFunc(out, func(a, b Receiver) int { return a.SeenAt.Compare(b.SeenAt) })
	return out
}

// find returns the account's open device with this public id.
func (s *Service) find(userID int64, publicID string) *receiver {
	for _, r := range s.receivers {
		if r.UserID == userID && r.PublicID == publicID {
			return r
		}
	}
	return nil
}

// Allows reports whether the open device a session plays on has let a sign-in control it.
// A device that is not open to playback lets nobody.
func (s *Service) Allows(receiverSignIn, controllerSignIn int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	r := s.receivers[receiverSignIn]
	return r != nil && slices.Contains(r.Allowed, controllerSignIn)
}

// Ask asks one of the account's open devices to play something or to be controlled. A device
// that already allowed the asking sign-in is not asked again. The same key and body sent again
// return the same request; created says this call made it.
func (s *Service) Ask(from Device, to string, kind string, play *Play, moving, key, hash string) (req Request, created bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	if key != "" {
		for _, other := range s.requests {
			if other.From.SignInID == from.SignInID && other.key == key {
				if other.hash != hash {
					return Request{}, false, ErrKeyReused
				}
				return *other, false, nil
			}
		}
	}
	r := s.find(from.UserID, to)
	if r == nil || r.DeviceID == from.DeviceID {
		return Request{}, false, ErrNotAccepting
	}
	held := 0
	for _, other := range s.requests {
		if other.To.DeviceID == r.DeviceID {
			held++
		}
	}
	if held >= perReceiver {
		return Request{}, false, ErrBusy
	}
	now := s.now()
	out := &Request{ID: db.NewID("rcq"), Kind: kind, From: from, To: r.Device, State: StateAsking, Play: play, Moving: moving,
		CreatedAt: now, UpdatedAt: now, key: key, hash: hash}
	if slices.Contains(r.Allowed, from.SignInID) {
		// Asked and allowed before in this run: the device's person is not asked again.
		out.State = StateAllowed
	}
	s.requests[out.ID] = out
	s.tell(from.UserID, livefeed.OpUpdated, out.ID)
	return *out, true, nil
}

// Get returns one of the account's requests.
func (s *Service) Get(userID int64, id string) (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	req := s.requests[id]
	if req == nil || req.From.UserID != userID {
		return Request{}, db.ErrNotFound
	}
	return *req, nil
}

// Pending lists what is being asked of a device now, oldest first.
func (s *Service) Pending(signInID int64) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	var out []Request
	for _, req := range s.requests {
		if req.To.SignInID == signInID && req.Open() {
			out = append(out, *req)
		}
	}
	slices.SortFunc(out, func(a, b Request) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// Cancel withdraws a request from the device that made it. Cancelling one that is over
// changes nothing.
func (s *Service) Cancel(signInID int64, id string) (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	req := s.requests[id]
	if req == nil || req.From.SignInID != signInID {
		return Request{}, db.ErrNotFound
	}
	if req.Open() {
		s.finishLocked(req, StateCancelled, "")
	}
	return *req, nil
}

// Answer is the asked device's answer: allow, decline, playing (with its session, which the
// caller has checked plays what was asked) or failed. Allowing lets the asking sign-in control
// this device until either signs out or the server restarts. Saying playing to an asking
// request allows it too.
func (s *Service) Answer(signInID int64, id, answer, session string) (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	req := s.requests[id]
	r := s.receivers[signInID]
	switch {
	case req == nil || req.To.SignInID != signInID:
		return Request{}, db.ErrNotFound
	case r == nil:
		return Request{}, ErrNotAccepting
	case !req.Open():
		if (answer == StatePlaying && req.State == StatePlaying && req.Playing == session) || answer == req.State {
			return *req, nil // the same answer sent again
		}
		return *req, ErrAnswered
	}
	allow := func() {
		if !slices.Contains(r.Allowed, req.From.SignInID) {
			if len(r.Allowed) == maxAllowed {
				r.Allowed = r.Allowed[1:]
			}
			r.Allowed = append(r.Allowed, req.From.SignInID)
		}
	}
	switch answer {
	case StateAllowed:
		allow()
		// Allowing control is the whole answer; allowing a move waits for playing.
		s.finishLocked(req, StateAllowed, "")
	case StateDeclined:
		s.finishLocked(req, StateDeclined, "")
	case StatePlaying:
		allow()
		req.Playing = session
		s.finishLocked(req, StatePlaying, "")
	case StateFailed:
		s.finishLocked(req, StateFailed, FailedCouldntStart)
	}
	return *req, nil
}

func (s *Service) finishLocked(req *Request, state, failed string) {
	req.State, req.Failed, req.UpdatedAt = state, failed, s.now()
	s.tell(req.From.UserID, livefeed.OpUpdated, req.ID)
}

// sweepLocked forgets devices that stopped saying they are open, fails requests their
// device did not start in time, and forgets requests answered a while ago.
func (s *Service) sweepLocked() {
	now := s.now()
	for id, r := range s.receivers {
		if now.Sub(r.SeenAt) > Present {
			s.closeLocked(id)
		}
	}
	for id, req := range s.requests {
		switch {
		case req.Open() && now.Sub(req.CreatedAt) > Timeout:
			s.finishLocked(req, StateFailed, FailedTimedOut)
		case !req.Open() && now.Sub(req.UpdatedAt) > kept:
			delete(s.requests, id)
		}
	}
}

// Sweep does the same as every call does, so a request that timed out is told to its
// device even when nobody asks. The server calls it every few seconds.
func (s *Service) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
}
