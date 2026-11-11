package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/together"
)

// Watch Together. A group keeps a shared clock, a queue and its members; each member plays
// through their own ordinary playback session, and the group gives nobody access to a title.
// A title a member may not see has no id, name or picture in that member's room. Changes come
// on the change feed as topic "group"; the app reads the room again.

// GroupCreate is the body of POST /v1/groups.
type GroupCreate struct {
	Name string `json:"name"`
	// DisplayName is the name the others see, the profile's name unless given.
	DisplayName string `json:"displayName,omitempty"`
	// Control is host (only the host plays, pauses and chooses) or everyone.
	Control string `json:"control"`
}

// GroupJoin is the body of POST /v1/groups/join.
type GroupJoin struct {
	Code        string `json:"code"`
	DisplayName string `json:"displayName,omitempty"`
}

// GroupEdit is the body of PATCH /v1/groups/{id}. Only what is sent changes.
type GroupEdit struct {
	Control string `json:"control,omitempty"`
	// Host is a member id to make host.
	Host       string `json:"host,omitempty"`
	BecomeHost bool   `json:"becomeHost,omitempty"`
	// NewCode replaces the invitation code; Joins is how many it lets in (8 unless said).
	NewCode *GroupCodeAsk `json:"newCode,omitempty"`
}

type GroupCodeAsk struct {
	Joins int `json:"joins,omitempty"`
}

// GroupAction is the body of POST /v1/groups/{id}/playback: play, pause, seek (positionMs),
// watch (entryId, from positionMs) or goAhead (start now without whoever is not ready or may
// not see the title).
type GroupAction struct {
	Action     string `json:"action"`
	PositionMs int64  `json:"positionMs,omitempty"`
	EntryID    string `json:"entryId,omitempty"`
}

// GroupQueue is the body of PUT /v1/groups/{id}/queue: the whole queue as it should be. An
// entry already in the queue is named by its entryId; a new one by its itemId.
type GroupQueue struct {
	Entries []GroupQueueEntry `json:"entries"`
}

type GroupQueueEntry struct {
	EntryID string `json:"entryId,omitempty"`
	ItemID  string `json:"itemId,omitempty"`
}

// GroupMe is the body of PUT /v1/groups/{id}/me, sent every reportEveryMs: where this member
// is. State is ready (loaded and waiting at the group's place), playing, paused or buffering.
// Here moves the member to this device from another.
type GroupMe struct {
	State      string `json:"state"`
	EntryID    string `json:"entryId,omitempty"`
	PositionMs int64  `json:"positionMs"`
	Here       bool   `json:"here,omitempty"`
}

// Room is a group as one member sees it.
type Room struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Control string  `json:"control"`
	You     RoomYou `json:"you"`
	// Invitation is shown to the host only.
	Invitation *RoomCode    `json:"invitation,omitempty"`
	Clock      RoomClock    `json:"clock"`
	Host       RoomHost     `json:"host"`
	Members    []RoomMember `json:"members"`
	Queue      []RoomEntry  `json:"queue"`
	// CantSee, for whoever may control, counts the members who may not see what is on: "Someone
	// in the group can't see this title on their profile", with Go ahead without them.
	CantSee int `json:"cantSee,omitempty"`
	// How a player keeps in step: left alone within inStepMs, a small change of speed up to
	// jumpMs, a jump beyond it. Report where it is every reportEveryMs.
	InStepMs      int64      `json:"inStepMs"`
	JumpMs        int64      `json:"jumpMs"`
	ReportEveryMs int64      `json:"reportEveryMs"`
	Ended         *RoomEnded `json:"ended,omitempty"`

	status int
}

func (r Room) reply(http.Header) int { return r.status }

type RoomYou struct {
	MemberID      string `json:"memberId"`
	Host          bool   `json:"host"`
	MayControl    bool   `json:"mayControl"`
	MayBecomeHost bool   `json:"mayBecomeHost"`
}

type RoomCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
	JoinsLeft int       `json:"joinsLeft"`
}

// RoomClock is the shared place: positionMs at the server time `at`, moving on while playing.
// While waiting, playback is about to start or go on and waits for everyone, until waitUntil.
type RoomClock struct {
	EntryID    string     `json:"entryId,omitempty"`
	PositionMs int64      `json:"positionMs"`
	At         time.Time  `json:"at"`
	Playing    bool       `json:"playing"`
	WaitUntil  *time.Time `json:"waitUntil,omitempty"`
	Ready      int        `json:"ready"`
	WaitingFor int        `json:"waitingFor"`
}

// RoomHost says whether the host is here; when not, from when, when playback pauses for
// everyone and when the group ends.
type RoomHost struct {
	Present   bool       `json:"present"`
	GoneSince *time.Time `json:"goneSince,omitempty"`
	PausesAt  *time.Time `json:"pausesAt,omitempty"`
	EndsAt    *time.Time `json:"endsAt,omitempty"`
}

// RoomMember is one member. A member whose Privacy setting hides them is Private, without names.
type RoomMember struct {
	ID       string `json:"id"`
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
	Private  bool   `json:"private,omitempty"`
	Host     bool   `json:"host"`
	You      bool   `json:"you"`
	// Status is in_step, buffering, catching_up or away.
	Status string `json:"status"`
}

// RoomEntry is one title of the queue. A title this member may not see has only its entryId.
type RoomEntry struct {
	EntryID   string `json:"entryId"`
	ItemID    string `json:"itemId,omitempty"`
	Title     string `json:"title,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Available bool   `json:"available"`
}

// RoomEnded says why a group ended: ended_by_host, no_host or everyone_left. After a restart
// a group is simply not found.
type RoomEnded struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// GroupSummary is one group in the list of those a profile is in.
type GroupSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members int    `json:"members"`
	Host    bool   `json:"host"`
}

func groupError(err error) error {
	switch {
	case errors.Is(err, together.ErrNotFound):
		return ErrNotFound()
	case errors.Is(err, together.ErrBadCode):
		return refusal(http.StatusNotFound, "code_invalid", "That code didn't work.", false)
	case errors.Is(err, together.ErrFull):
		return ErrConflict("group_full", "This group is full.")
	case errors.Is(err, together.ErrTooMany):
		return ErrConflict("too_many_groups", "You're in as many groups as you can be. Leave one first.")
	case errors.Is(err, together.ErrNotAllowed):
		return refusal(http.StatusForbidden, "host_controls", "The host controls playback.", false)
	case errors.Is(err, together.ErrNotHost):
		return refusal(http.StatusForbidden, "not_host", "Only the host can do that.", false)
	case errors.Is(err, together.ErrElsewhere):
		return ErrConflict("in_group_elsewhere", "You're in this group on another device.")
	case errors.Is(err, together.ErrNothingOn):
		return ErrConflict("nothing_chosen", "Choose something to watch first.")
	case errors.Is(err, together.ErrNotInStep):
		return ErrConflict("not_in_step", "You can become host once you're in step and the host has been gone two minutes.")
	case errors.Is(err, together.ErrKeyReused):
		return errKeyReused()
	case errors.Is(err, together.ErrEnded):
		return refusal(http.StatusGone, "group_ended", "This group has ended.", false)
	case errors.Is(err, together.ErrQueueTooBig):
		return ErrField("entries", "A group's queue holds at most 256 titles.")
	}
	return err
}

func (s *Server) groupRoutes() {
	for _, route := range []*Route{
		Handle(s, "GET /v1/groups", WithProfile, s.listGroups),
		Handle(s, "POST /v1/groups", WithProfile, s.createGroup),
		Handle(s, "POST /v1/groups/join", WithProfile, s.joinGroup),
		Handle(s, "GET /v1/groups/{id}", WithProfile, func(r *http.Request, _ None) (Room, error) {
			if err := s.mayWatchTogether(r); err != nil {
				return Room{}, err
			}
			g, err := s.groups.Get(r.PathValue("id"), CallerFrom(r.Context()).ProfileID)
			return s.room(r, g, err)
		}),
		Handle(s, "PATCH /v1/groups/{id}", WithProfile, func(r *http.Request, req GroupEdit) (Room, error) {
			if err := s.mayWatchTogether(r); err != nil {
				return Room{}, err
			}
			if req.Control != "" && req.Control != together.ControlHost && req.Control != together.ControlEveryone {
				return Room{}, ErrField("control", "Choose host or everyone.")
			}
			edit := together.Edit{Control: req.Control, Host: req.Host, BecomeHost: req.BecomeHost}
			if req.NewCode != nil {
				edit.NewCode = &req.NewCode.Joins
			}
			g, err := s.groups.Change(r.PathValue("id"), CallerFrom(r.Context()).ProfileID, edit)
			return s.room(r, g, err)
		}),
		Handle(s, "DELETE /v1/groups/{id}", WithProfile, func(r *http.Request, _ None) (None, error) {
			return None{}, groupError(s.groups.End(r.PathValue("id"), CallerFrom(r.Context()).ProfileID))
		}),
		Handle(s, "POST /v1/groups/{id}/playback", WithProfile, s.groupPlayback),
		Handle(s, "PUT /v1/groups/{id}/queue", WithProfile, s.groupQueue),
		Handle(s, "PUT /v1/groups/{id}/me", WithProfile, func(r *http.Request, req GroupMe) (Room, error) {
			if err := s.mayWatchTogether(r); err != nil {
				return Room{}, err
			}
			switch req.State {
			case "ready", "playing", "paused", "buffering":
			default:
				return Room{}, ErrField("state", "The state is ready, playing, paused or buffering.")
			}
			c := CallerFrom(r.Context())
			g, err := s.groups.Me(r.PathValue("id"), c.ProfileID, together.Report{SignInID: c.SessionID, State: req.State,
				EntryID: req.EntryID, PositionMs: req.PositionMs, Here: req.Here})
			return s.room(r, g, err)
		}),
		// Leaving is always possible, even once the profile's Watch Together is turned off.
		Handle(s, "DELETE /v1/groups/{id}/me", WithProfile, func(r *http.Request, _ None) (None, error) {
			return None{}, groupError(s.groups.Leave(r.PathValue("id"), CallerFrom(r.Context()).ProfileID))
		}),
	} {
		// A group is people on devices, so these take a signed-in person and not a key.
		route.Scope = ScopeSession
	}
}

// mayWatchTogether refuses a profile whose own Watch Together switch, or its account's, is off.
func (s *Server) mayWatchTogether(r *http.Request) error {
	m, err := s.db.GroupMemberOf(r.Context(), CallerFrom(r.Context()).ProfileID)
	switch {
	case err != nil:
		return err
	case !m.Allowed:
		return refusal(http.StatusForbidden, "watch_together_off", "Watch Together is turned off for this profile.", false)
	}
	return nil
}

// newMember is the caller as a group member: named by username, then the name in the group,
// and private when the profile's Privacy setting says so.
func (s *Server) newMember(r *http.Request, displayName string) (together.Member, error) {
	ctx := r.Context()
	c := CallerFrom(ctx)
	m, err := s.db.GroupMemberOf(ctx, c.ProfileID)
	if err != nil {
		return together.Member{}, err
	}
	if !m.Allowed {
		return together.Member{}, refusal(http.StatusForbidden, "watch_together_off", "Watch Together is turned off for this profile.", false)
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = m.ProfileName
	}
	if utf8.RuneCountInString(name) > 40 {
		return together.Member{}, ErrField("displayName", "Use a name of at most 40 characters.")
	}
	private := false
	stored, err := s.db.StoredPreferences(ctx, c.ProfileID, "", 0)
	for _, p := range stored {
		if p.Key == "privacy.includeInWatchTogether" && p.DeviceClass == "" && !p.Device {
			private = p.Value == "false"
		}
	}
	return together.Member{ProfileID: c.ProfileID, UserID: c.UserID, SignInID: c.SessionID, Username: m.Username, Name: name,
		Private: private}, err
}

func (s *Server) listGroups(r *http.Request, _ None) (Page[GroupSummary], error) {
	if err := s.mayWatchTogether(r); err != nil {
		return Page[GroupSummary]{}, err
	}
	c := CallerFrom(r.Context())
	out := Page[GroupSummary]{Items: []GroupSummary{}}
	for _, g := range s.groups.List(c.ProfileID) {
		out.Items = append(out.Items, GroupSummary{ID: g.ID, Name: g.Name, Members: len(g.Members), Host: g.Host == c.ProfileID})
	}
	return out, nil
}

func (s *Server) createGroup(r *http.Request, req GroupCreate) (Room, error) {
	key, err := idempotencyKey(r)
	if err != nil {
		return Room{}, err
	}
	name := strings.TrimSpace(req.Name)
	switch {
	case name == "" || utf8.RuneCountInString(name) > 60:
		return Room{}, ErrField("name", "Give the group a name of at most 60 characters.")
	case req.Control != together.ControlHost && req.Control != together.ControlEveryone:
		return Room{}, ErrField("control", "Choose who controls playback: host or everyone.")
	}
	m, err := s.newMember(r, req.DisplayName)
	if err != nil {
		return Room{}, err
	}
	g, created, err := s.groups.Create(m, name, req.Control, key, requestHash(req))
	out, err := s.room(r, g, err)
	if created {
		out.status = http.StatusCreated
	}
	return out, err
}

func (s *Server) joinGroup(r *http.Request, req GroupJoin) (Room, error) {
	m, err := s.newMember(r, req.DisplayName)
	if err != nil {
		return Room{}, err
	}
	g, err := s.groups.Join(req.Code, m)
	if err == nil {
		s.checkCantSee(r.Context(), g)
	}
	return s.room(r, g, err)
}

func (s *Server) groupPlayback(r *http.Request, req GroupAction) (Room, error) {
	if err := s.mayWatchTogether(r); err != nil {
		return Room{}, err
	}
	switch req.Action {
	case "play", "pause", "seek", "watch", "goAhead":
	default:
		return Room{}, ErrField("action", "The action is play, pause, seek, watch or goAhead.")
	}
	g, err := s.groups.Play(r.PathValue("id"), CallerFrom(r.Context()).ProfileID,
		together.Action{Kind: req.Action, PositionMs: req.PositionMs, EntryID: req.EntryID})
	if err == nil && req.Action == "watch" {
		g = s.checkCantSee(r.Context(), g)
	}
	return s.room(r, g, err)
}

// groupQueue replaces the queue. A new title must be one the person sending it may see, and a
// kind a group plays; anything else is not found, so nothing is learnt of titles hidden from them.
func (s *Server) groupQueue(r *http.Request, req GroupQueue) (Room, error) {
	if err := s.mayWatchTogether(r); err != nil {
		return Room{}, err
	}
	if len(req.Entries) > together.MaxQueue {
		return Room{}, ErrField("entries", "A group's queue holds at most 256 titles.")
	}
	var wanted []string
	for _, e := range req.Entries {
		if e.EntryID == "" {
			wanted = append(wanted, e.ItemID)
		}
	}
	titles, err := s.db.GroupTitles(r.Context(), viewer(r), wanted)
	if err != nil {
		return Room{}, err
	}
	found := map[string]db.GroupTitle{}
	for _, t := range titles {
		found[t.PublicID] = t
	}
	entries := make([]together.Entry, 0, len(req.Entries))
	for n, e := range req.Entries {
		if e.EntryID != "" {
			entries = append(entries, together.Entry{ID: e.EntryID})
			continue
		}
		t, ok := found[e.ItemID]
		if !ok {
			return Room{}, ErrField("entries["+strconv.Itoa(n)+"].itemId", "This title can't be played in a group.")
		}
		entries = append(entries, together.Entry{ItemID: t.ID, PublicID: t.PublicID, Kind: t.Kind})
	}
	g, err := s.groups.SetQueue(r.PathValue("id"), CallerFrom(r.Context()).ProfileID, entries)
	return s.room(r, g, err)
}

// checkCantSee reads which members may not see what is on, and tells the group.
func (s *Server) checkCantSee(ctx context.Context, g together.Group) together.Group {
	var item int64
	for _, e := range g.Queue {
		if e.ID == g.Clock.EntryID {
			item = e.ItemID
		}
	}
	if item == 0 {
		return g
	}
	var cant []int64
	for _, m := range g.Members {
		if ok, err := s.db.Visible(ctx, db.Viewer{UserID: m.UserID, ProfileID: m.ProfileID}, item); err != nil {
			return g // read again at the next change
		} else if !ok {
			cant = append(cant, m.ProfileID)
		}
	}
	s.groups.SetCantSee(g.ID, g.Clock.EntryID, cant)
	g.CantSee = cant
	return g
}

// room shapes a group for the member asking: titles they may not see have no id or name, a
// private member has no names, and the invitation is the host's.
func (s *Server) room(r *http.Request, g together.Group, err error) (Room, error) {
	if err != nil {
		return Room{}, groupError(err)
	}
	c := CallerFrom(r.Context())
	now := s.now()
	ids := make([]int64, len(g.Queue))
	for n, e := range g.Queue {
		ids[n] = e.ItemID
	}
	titles, err := s.db.VisibleTitles(r.Context(), viewer(r), ids)
	if err != nil {
		return Room{}, err
	}
	me := g.Member(c.ProfileID)
	out := Room{ID: g.ID, Name: g.Name, Control: g.Control, Members: make([]RoomMember, 0, len(g.Members)),
		Queue: make([]RoomEntry, 0, len(g.Queue)), InStepMs: together.InStep.Milliseconds(), JumpMs: together.Jump.Milliseconds(),
		ReportEveryMs: together.ReportEvery.Milliseconds()}
	out.You = RoomYou{MemberID: me.ID, Host: g.Host == c.ProfileID, MayControl: g.MayControl(c.ProfileID),
		MayBecomeHost: g.MayBecomeHost(c.ProfileID, now)}
	if out.You.Host && g.Ended == nil && now.Before(g.CodeExpires) && g.JoinsLeft > 0 {
		out.Invitation = &RoomCode{Code: g.Code, ExpiresAt: g.CodeExpires.UTC(), JoinsLeft: g.JoinsLeft}
	}
	ready, waitingFor := g.Ready(now)
	out.Clock = RoomClock{EntryID: g.Clock.EntryID, PositionMs: g.Position(now), At: now.UTC(), Playing: g.Clock.Playing,
		Ready: ready, WaitingFor: waitingFor}
	if !g.Clock.WaitUntil.IsZero() {
		until := g.Clock.WaitUntil.UTC()
		out.Clock.WaitUntil = &until
	}
	out.Host.Present = g.HostGoneSince.IsZero() && g.Host != 0
	if !g.HostGoneSince.IsZero() {
		since, pauses, ends := g.HostGoneSince.UTC(), g.HostGoneSince.Add(together.HostPause).UTC(), g.HostGoneSince.Add(together.HostEnd).UTC()
		out.Host.GoneSince, out.Host.PausesAt, out.Host.EndsAt = &since, &pauses, &ends
	}
	for _, m := range g.Members {
		member := RoomMember{ID: m.ID, Host: m.ProfileID == g.Host, You: m.ProfileID == c.ProfileID, Status: g.Status(m, now)}
		if m.Private && !member.You {
			member.Private = true
		} else {
			member.Username, member.Name = m.Username, m.Name
		}
		out.Members = append(out.Members, member)
	}
	for _, e := range g.Queue {
		entry := RoomEntry{EntryID: e.ID}
		if title, ok := titles[e.ItemID]; ok {
			entry.ItemID, entry.Title, entry.Kind, entry.Available = e.PublicID, title, e.Kind.String(), true
		}
		out.Queue = append(out.Queue, entry)
	}
	if out.You.MayControl {
		out.CantSee = len(g.CantSee)
	}
	if g.Ended != nil {
		out.Ended = &RoomEnded{Reason: g.Ended.Reason, At: g.Ended.At.UTC()}
	}
	return out, nil
}
