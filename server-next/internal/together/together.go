// Package together runs Watch Together groups. A group only keeps a shared clock, a queue and
// who is in it: each member plays the title through their own ordinary playback session, on
// their own device, within their own profile's limits, and the group only says where to be.
// A group gives nobody access to anything. All of it lives in memory; a restart ends every
// group.
package together

import (
	"crypto/rand"
	"errors"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/livefeed"
)

// The numbers of the product definition.
const (
	MaxMembers = 32
	MaxQueue   = 256
	// CodeLife and DefaultJoins are how long an invitation code works and how many people it
	// lets in, unless the host says otherwise (at most MaxMembers joins).
	CodeLife     = 15 * time.Minute
	DefaultJoins = 8
	// WaitForAll is how long a start or a seek waits for everyone to be ready.
	WaitForAll = 5 * time.Second
	// InStep: a member closer than this is left alone. Up to Jump the player catches up by a
	// small change of speed; beyond it the player jumps to the group's place.
	InStep = 750 * time.Millisecond
	Jump   = 3 * time.Second
	// Without a host for HostPause, playback pauses for everyone and any member in step may
	// become host; after HostEnd the group ends.
	HostPause = 2 * time.Minute
	HostEnd   = 10 * time.Minute
	// A member reports every ReportEvery and is Away after Away without a word.
	ReportEvery = 5 * time.Second
	Away        = 30 * time.Second
	// groupsPerProfile bounds the groups one profile is in at once, and so the groups a server
	// holds; an idle group ends ten minutes after its host goes.
	groupsPerProfile = 8
	// endedKept is how long an ended group can still be read, so its members are told why.
	endedKept = 2 * time.Minute
)

// Who may control playback and the queue.
const (
	ControlHost     = "host"
	ControlEveryone = "everyone"
)

// How each member stands, as the room shows it.
const (
	StatusInStep     = "in_step"
	StatusBuffering  = "buffering"
	StatusCatchingUp = "catching_up"
	StatusAway       = "away"
)

// Why a group ended.
const (
	EndedByHost       = "ended_by_host"
	EndedNoHost       = "no_host"
	EndedEveryoneLeft = "everyone_left"
)

var (
	ErrNotFound    = db.ErrNotFound
	ErrBadCode     = errors.New("that code didn't work")
	ErrFull        = errors.New("the group is full")
	ErrTooMany     = errors.New("this profile is in as many groups as it may be")
	ErrNotAllowed  = errors.New("only whoever may control can do that")
	ErrNotHost     = errors.New("only the host can do that")
	ErrElsewhere   = errors.New("this profile is in the group on another device")
	ErrNothingOn   = errors.New("nothing is chosen to play")
	ErrNotInStep   = errors.New("only a member in step can become host, once the host has been gone two minutes")
	ErrKeyReused   = errors.New("idempotency key reused with a different request")
	ErrEnded       = errors.New("the group has ended")
	ErrQueueTooBig = errors.New("the queue holds at most 256 titles")
)

// Member is one profile in a group, on one device.
type Member struct {
	ID        string
	ProfileID int64
	UserID    int64
	SignInID  int64
	Username  string
	Name      string
	// Private hides the member's names from the others; they are still counted and shown in step or not.
	Private bool
	// What the member's device last said: its state (ready, playing, paused or buffering),
	// the entry it is on and where.
	State      string
	EntryID    string
	PositionMs int64
	SeenAt     time.Time
}

// Entry is one title in the group's queue. It names the title by its database id; who may
// see it is decided for each member when the room is read.
type Entry struct {
	ID       string
	ItemID   int64
	PublicID string
	Kind     db.ItemKind
}

// Clock is the group's shared place. While WaitUntil is set, playback is about to start or
// go on, and waits for everyone to be ready until then.
type Clock struct {
	EntryID    string
	PositionMs int64
	At         time.Time
	Playing    bool
	WaitUntil  time.Time
	// GoAhead: whoever controls said to go on without those who cannot see this title.
	GoAhead bool
}

// End says why and when a group ended.
type End struct {
	Reason string
	At     time.Time
}

// Group is a copy of one group as it stands.
type Group struct {
	ID      string
	Name    string
	Control string
	// Host is the host's profile, 0 while there is none. HostGoneSince is when the host left
	// or stopped being heard, zero while the host is here.
	Host          int64
	HostGoneSince time.Time
	Members       []Member
	Queue         []Entry
	Clock         Clock
	Code          string
	CodeExpires   time.Time
	JoinsLeft     int
	// CantSee are the members' profiles that may not see the current entry. They are not
	// waited for once whoever controls says go ahead; until then the title does not start.
	CantSee []int64
	Ended   *End

	createdBy             int64
	createKey, createHash string
}

// Position is where the group is at a moment.
func (g *Group) Position(now time.Time) int64 {
	if g.Clock.Playing && g.Clock.WaitUntil.IsZero() {
		return g.Clock.PositionMs + now.Sub(g.Clock.At).Milliseconds()
	}
	return g.Clock.PositionMs
}

// Member returns a member by profile.
func (g *Group) Member(profileID int64) *Member {
	for n := range g.Members {
		if g.Members[n].ProfileID == profileID {
			return &g.Members[n]
		}
	}
	return nil
}

// MayControl says whether a member may play, pause, seek and change the queue.
func (g *Group) MayControl(profileID int64) bool {
	return g.Ended == nil && (g.Control == ControlEveryone || g.Host == profileID)
}

// Status is how a member stands now.
func (g *Group) Status(m Member, now time.Time) string {
	switch {
	case now.Sub(m.SeenAt) > Away:
		return StatusAway
	case m.State == "buffering":
		return StatusBuffering
	case m.EntryID != g.Clock.EntryID:
		return StatusCatchingUp
	}
	at := m.PositionMs
	if m.State == "playing" {
		at += now.Sub(m.SeenAt).Milliseconds()
	}
	gap := time.Duration(abs(at-g.Position(now))) * time.Millisecond
	if gap < InStep {
		return StatusInStep
	}
	return StatusCatchingUp
}

// Ready counts the members waited for and those of them ready to play.
func (g *Group) Ready(now time.Time) (ready, waitedFor int) {
	for _, m := range g.Members {
		if now.Sub(m.SeenAt) > Away || slices.Contains(g.CantSee, m.ProfileID) {
			continue
		}
		waitedFor++
		if m.EntryID == g.Clock.EntryID && (m.State == "ready" || m.State == "playing") {
			ready++
		}
	}
	return ready, waitedFor
}

// MayBecomeHost says whether a member may take the host's place now.
func (g *Group) MayBecomeHost(profileID int64, now time.Time) bool {
	m := g.Member(profileID)
	return g.Ended == nil && m != nil && g.Host != profileID && !g.HostGoneSince.IsZero() &&
		now.Sub(g.HostGoneSince) >= HostPause && g.Status(*m, now) == StatusInStep
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// Service holds every group. It is safe for concurrent use.
type Service struct {
	live *livefeed.Publisher
	now  func() time.Time

	mu     sync.Mutex
	groups map[string]*Group
	codes  map[string]string // invitation code to group id
}

// New returns the service. now is the server's clock.
func New(live *livefeed.Publisher, now func() time.Time) *Service {
	return &Service{live: live, now: now, groups: map[string]*Group{}, codes: map[string]string{}}
}

// tellLocked asks every member to read the group again, and anyone else named.
func (s *Service) tellLocked(g *Group, also ...int64) {
	for _, m := range g.Members {
		s.live.ToProfile(m.ProfileID, livefeed.TopicGroup, livefeed.OpUpdated, g.ID)
	}
	for _, profileID := range also {
		s.live.ToProfile(profileID, livefeed.TopicGroup, livefeed.OpUpdated, g.ID)
	}
}

func copyGroup(g *Group) Group {
	out := *g
	out.Members, out.Queue, out.CantSee = slices.Clone(g.Members), slices.Clone(g.Queue), slices.Clone(g.CantSee)
	if g.Ended != nil {
		end := *g.Ended
		out.Ended = &end
	}
	return out
}

// live returns a group the profile is in.
func (s *Service) member(id string, profileID int64) (*Group, *Member, error) {
	g := s.groups[id]
	if g == nil {
		return nil, nil, ErrNotFound
	}
	m := g.Member(profileID)
	if m == nil {
		return nil, nil, ErrNotFound
	}
	return g, m, nil
}

// running is like member, for a change: an ended group changes no more.
func (s *Service) running(id string, profileID int64) (*Group, *Member, error) {
	g, m, err := s.member(id, profileID)
	if err == nil && g.Ended != nil {
		err = ErrEnded
	}
	return g, m, err
}

func (s *Service) inGroupsLocked(profileID int64) int {
	n := 0
	for _, g := range s.groups {
		if g.Ended == nil && g.Member(profileID) != nil {
			n++
		}
	}
	return n
}

// Create starts a group with its maker as host and gives it an invitation code. The same key
// and body sent again return the same group.
func (s *Service) Create(m Member, name, control, key, hash string) (Group, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != "" {
		for _, g := range s.groups {
			if g.createdBy == m.ProfileID && g.createKey == key {
				if g.createHash != hash {
					return Group{}, false, ErrKeyReused
				}
				return copyGroup(g), false, nil
			}
		}
	}
	if s.inGroupsLocked(m.ProfileID) >= groupsPerProfile {
		return Group{}, false, ErrTooMany
	}
	m.ID, m.SeenAt = db.NewID("gm"), s.now()
	g := &Group{ID: db.NewID("grp"), Name: name, Control: control, Host: m.ProfileID, Members: []Member{m},
		createdBy: m.ProfileID, createKey: key, createHash: hash}
	s.groups[g.ID] = g
	s.newCodeLocked(g, DefaultJoins)
	return copyGroup(g), true, nil
}

const codeLetters = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// NormalCode is a code as typed, in capitals and without spaces or dashes.
func NormalCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToUpper(code))
}

func (s *Service) newCodeLocked(g *Group, joins int) {
	delete(s.codes, g.Code)
	for {
		var b strings.Builder
		for range 8 {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(codeLetters))))
			b.WriteByte(codeLetters[n.Int64()])
		}
		if _, taken := s.codes[b.String()]; !taken {
			g.Code = b.String()
			break
		}
	}
	s.codes[g.Code] = g.ID
	g.CodeExpires, g.JoinsLeft = s.now().Add(CodeLife), joins
}

// Join adds a profile with a code. A member already in the group joins again on this device,
// without using up a join.
func (s *Service) Join(code string, m Member) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[s.codes[NormalCode(code)]]
	if g == nil || g.Ended != nil || s.now().After(g.CodeExpires) {
		return Group{}, ErrBadCode
	}
	if had := g.Member(m.ProfileID); had != nil {
		had.SignInID, had.SeenAt = m.SignInID, s.now()
		s.tellLocked(g)
		return copyGroup(g), nil
	}
	switch {
	case g.JoinsLeft <= 0:
		return Group{}, ErrBadCode
	case len(g.Members) >= MaxMembers:
		return Group{}, ErrFull
	case s.inGroupsLocked(m.ProfileID) >= groupsPerProfile:
		return Group{}, ErrTooMany
	}
	g.JoinsLeft--
	m.ID, m.SeenAt = db.NewID("gm"), s.now()
	g.Members = append(g.Members, m)
	s.tellLocked(g)
	return copyGroup(g), nil
}

// Get returns a group the profile is in, or was in until it ended a moment ago.
func (s *Service) Get(id string, profileID int64) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.member(id, profileID)
	if err != nil {
		return Group{}, err
	}
	return copyGroup(g), nil
}

// List returns the groups a profile is in, oldest first.
func (s *Service) List(profileID int64) []Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Group
	for _, g := range s.groups {
		if g.Ended == nil && g.Member(profileID) != nil {
			out = append(out, copyGroup(g))
		}
	}
	slices.SortFunc(out, func(a, b Group) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Report is what a member's device says: where it is and whether it is ready.
type Report struct {
	SignInID   int64
	State      string
	EntryID    string
	PositionMs int64
	// Here moves the member to this device from another.
	Here bool
}

// Me takes a member's report, which also keeps the member present. A member on another device
// is refused unless the report says to move here.
func (s *Service) Me(id string, profileID int64, r Report) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, m, err := s.running(id, profileID)
	if err != nil {
		return Group{}, err
	}
	if m.SignInID != r.SignInID && !r.Here {
		return Group{}, ErrElsewhere
	}
	now := s.now()
	before := g.Status(*m, now)
	m.SignInID, m.State, m.EntryID, m.PositionMs, m.SeenAt = r.SignInID, r.State, r.EntryID, max(r.PositionMs, 0), now
	changed := before != g.Status(*m, now)
	if g.Host == profileID && !g.HostGoneSince.IsZero() {
		g.HostGoneSince, changed = time.Time{}, true
	}
	if s.startIfReadyLocked(g, now) {
		changed = true
	}
	if changed {
		s.tellLocked(g)
	}
	return copyGroup(g), nil
}

// startIfReadyLocked starts a waiting clock once everyone waited for is ready, or the wait is
// over. A title someone may not see waits for whoever controls to say go ahead.
func (s *Service) startIfReadyLocked(g *Group, now time.Time) bool {
	if g.Clock.WaitUntil.IsZero() || len(g.CantSee) > 0 && !g.Clock.GoAhead {
		return false
	}
	ready, waitedFor := g.Ready(now)
	if ready < waitedFor && now.Before(g.Clock.WaitUntil) {
		return false
	}
	g.Clock.WaitUntil, g.Clock.At = time.Time{}, now
	return true
}

// Leave takes a member out. A host who leaves leaves the group without a host; a group
// everyone left ends.
func (s *Service) Leave(id string, profileID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.member(id, profileID)
	if err != nil || g.Ended != nil {
		return nil // leaving is safe to repeat
	}
	g.Members = slices.DeleteFunc(g.Members, func(m Member) bool { return m.ProfileID == profileID })
	g.CantSee = slices.DeleteFunc(g.CantSee, func(p int64) bool { return p == profileID })
	if g.Host == profileID {
		g.Host, g.HostGoneSince = 0, s.now()
	}
	if len(g.Members) == 0 {
		s.endLocked(g, EndedEveryoneLeft)
	}
	s.tellLocked(g, profileID)
	return nil
}

// End ends a group for everyone; only its host may. Each member's playback carries on by itself.
func (s *Service) End(id string, profileID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.member(id, profileID)
	switch {
	case err != nil:
		return err
	case g.Ended != nil:
		return nil
	case g.Host != profileID:
		return ErrNotHost
	}
	s.endLocked(g, EndedByHost)
	s.tellLocked(g)
	return nil
}

func (s *Service) endLocked(g *Group, reason string) {
	g.Ended = &End{Reason: reason, At: s.now()}
	delete(s.codes, g.Code)
}

// Edit is a change to a group: who controls, a new host, becoming host, a new code.
type Edit struct {
	Control    string
	Host       string // a member id
	BecomeHost bool
	// NewCode replaces the invitation code, letting this many in (0 is the default).
	NewCode *int
}

// Change makes an edit. Becoming host is for any member in step once the host has been gone
// two minutes; the rest is the host's.
func (s *Service) Change(id string, profileID int64, e Edit) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.running(id, profileID)
	if err != nil {
		return Group{}, err
	}
	now := s.now()
	if e.BecomeHost {
		if g.Host != profileID {
			if !g.MayBecomeHost(profileID, now) {
				return Group{}, ErrNotInStep
			}
			g.Host, g.HostGoneSince = profileID, time.Time{}
		}
		s.tellLocked(g)
		return copyGroup(g), nil
	}
	if g.Host != profileID {
		return Group{}, ErrNotHost
	}
	if e.Control != "" {
		g.Control = e.Control
	}
	if e.Host != "" {
		var next *Member
		for n := range g.Members {
			if g.Members[n].ID == e.Host {
				next = &g.Members[n]
			}
		}
		if next == nil {
			return Group{}, ErrNotFound
		}
		if next.ProfileID != profileID && g.Status(*next, now) != StatusInStep {
			return Group{}, ErrNotInStep
		}
		g.Host = next.ProfileID
	}
	if e.NewCode != nil {
		joins := *e.NewCode
		if joins <= 0 {
			joins = DefaultJoins
		}
		s.newCodeLocked(g, min(joins, MaxMembers))
	}
	s.tellLocked(g)
	return copyGroup(g), nil
}

// Action is a playback action for everyone: play, pause, seek (to PositionMs), watch (the
// queue's EntryID, from PositionMs) or goAhead (start now, without those not ready or not
// able to see the title).
type Action struct {
	Kind       string
	PositionMs int64
	EntryID    string
}

// Play applies an action. The last action wins; a member behind is brought back in step by
// their own player.
func (s *Service) Play(id string, profileID int64, a Action) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.running(id, profileID)
	if err != nil {
		return Group{}, err
	}
	if !g.MayControl(profileID) {
		return Group{}, ErrNotAllowed
	}
	now := s.now()
	c := &g.Clock
	if c.EntryID == "" && a.Kind != "watch" {
		return Group{}, ErrNothingOn
	}
	switch a.Kind {
	case "play":
		if !c.Playing {
			c.Playing, c.WaitUntil = true, now.Add(WaitForAll)
		}
	case "pause":
		c.PositionMs, c.Playing, c.WaitUntil = g.Position(now), false, time.Time{}
	case "seek":
		c.PositionMs, c.At = max(a.PositionMs, 0), now
		if c.Playing {
			c.WaitUntil = now.Add(WaitForAll)
		}
	case "watch":
		if !slices.ContainsFunc(g.Queue, func(e Entry) bool { return e.ID == a.EntryID }) {
			return Group{}, ErrNotFound
		}
		*c = Clock{EntryID: a.EntryID, PositionMs: max(a.PositionMs, 0), At: now, Playing: true, WaitUntil: now.Add(WaitForAll)}
		g.CantSee = nil // the caller says who cannot see the new title, with SetCantSee
	case "goAhead":
		c.GoAhead = true
		if !c.WaitUntil.IsZero() {
			c.WaitUntil = now // go on now
		}
	}
	s.startIfReadyLocked(g, now)
	s.tellLocked(g)
	return copyGroup(g), nil
}

// SetCantSee records which members may not see the current entry. The caller reads it from
// the database after anything that changes the entry or the members.
func (s *Service) SetCantSee(id, entryID string, profiles []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[id]
	if g == nil || g.Ended != nil || g.Clock.EntryID != entryID || slices.Equal(g.CantSee, profiles) {
		return
	}
	g.CantSee = profiles
	s.startIfReadyLocked(g, s.now())
	s.tellLocked(g)
}

// SetQueue replaces the queue with what whoever controls sent: entries with an id it has keep
// their place in the new order, new ones are added. Removing the current entry stops the clock.
func (s *Service) SetQueue(id string, profileID int64, entries []Entry) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.running(id, profileID)
	switch {
	case err != nil:
		return Group{}, err
	case !g.MayControl(profileID):
		return Group{}, ErrNotAllowed
	case len(entries) > MaxQueue:
		return Group{}, ErrQueueTooBig
	}
	kept := map[string]Entry{}
	for _, e := range g.Queue {
		kept[e.ID] = e
	}
	queue := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.ID != "" {
			old, ok := kept[e.ID]
			if !ok {
				return Group{}, ErrNotFound
			}
			delete(kept, e.ID) // an entry is listed once
			queue = append(queue, old)
			continue
		}
		e.ID = db.NewID("ge")
		queue = append(queue, e)
	}
	g.Queue = queue
	if !slices.ContainsFunc(queue, func(e Entry) bool { return e.ID == g.Clock.EntryID }) {
		g.Clock, g.CantSee = Clock{}, nil
	}
	s.tellLocked(g)
	return copyGroup(g), nil
}

// Sweep starts clocks whose wait is over, pauses and ends groups without a host, marks
// members away, and forgets codes and groups that are over. The server calls it every second.
func (s *Service) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, g := range s.groups {
		if g.Ended != nil {
			if now.Sub(g.Ended.At) > endedKept {
				delete(s.groups, id)
			}
			continue
		}
		changed := s.startIfReadyLocked(g, now)
		if g.Host != 0 && g.HostGoneSince.IsZero() {
			if host := g.Member(g.Host); host != nil && now.Sub(host.SeenAt) > Away {
				// The host's connection dropped: the countdown runs from the last word.
				g.HostGoneSince, changed = host.SeenAt, true
			}
		}
		if !g.HostGoneSince.IsZero() {
			gone := now.Sub(g.HostGoneSince)
			switch {
			case gone >= HostEnd:
				s.endLocked(g, EndedNoHost)
				changed = true
			case gone >= HostPause && g.Clock.Playing:
				g.Clock.PositionMs, g.Clock.Playing, g.Clock.WaitUntil = g.Position(now), false, time.Time{}
				changed = true
			}
		}
		// A member who just went quiet is shown Away.
		for _, m := range g.Members {
			if quiet := now.Sub(m.SeenAt); quiet > Away && quiet <= Away+time.Second {
				changed = true
			}
		}
		if changed {
			s.tellLocked(g)
		}
	}
	for code, id := range s.codes {
		if g := s.groups[id]; g == nil || now.After(g.CodeExpires) {
			delete(s.codes, code)
		}
	}
}
