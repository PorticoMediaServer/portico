package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// HostGraceSeconds is how long a silent host is still reported as connected.
// The contract's two-minute pause and ten-minute end boundaries are measured
// from the host's last proof of life, not from the end of this grace window.
const HostGraceSeconds = 30

// --- wire shapes -------------------------------------------------------------

type Rate struct {
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}

// Timeline is the group's authoritative playback clock. A member computes its
// own target as anchorPositionUs + (serverTime - anchorAt) * rate while the
// state is `playing`, and applies the correction bands in Sync.
type Timeline struct {
	ItemID           string  `json:"itemId"`
	CurrentEntryID   string  `json:"currentEntryId"`
	State            string  `json:"state"`
	AnchorPositionUS string  `json:"anchorPositionUs"`
	AnchorAt         string  `json:"anchorAt"`
	Rate             Rate    `json:"rate"`
	QueuePosition    int     `json:"queuePosition"`
	WaitUntil        *string `json:"waitUntil"`
}

type Settings struct {
	Shuffle bool   `json:"shuffleEnabled"`
	Repeat  string `json:"repeatMode"`
}

type Permissions struct {
	IsHost         bool `json:"isHost"`
	CanControl     bool `json:"canControl"`
	CanManageQueue bool `json:"canManageQueue"`
	// CanClaimHost is the caller's current permission to become host of a group
	// whose host has passed the pause boundary. Absent means false.
	CanClaimHost bool `json:"canClaimHost,omitempty"`
}

// Authority is the group's playback authority: its host's device, and the v1
// session that device is playing (B8a). The group owns no playback; members each
// play their own session and follow the group's timeline. State is "bound" while
// the host device plays a session (PlaybackID), "fresh" when it plays nothing or
// no device is bound yet (after a transfer), and "retired" when the device is
// signed out (the host must return on another device or transfer).
type Authority struct {
	DeviceID   string  `json:"deviceId"`
	PlaybackID *string `json:"playbackId"`
	State      string  `json:"state"`
}
type HostPresence struct {
	Presence   string  `json:"presence"`
	LastSeenAt string  `json:"lastSeenAt"`
	PauseAt    *string `json:"pauseAt"`
	EndAt      *string `json:"endAt"`
}

type Member struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"displayName"`
	Username    string  `json:"username,omitempty"`
	Private     bool    `json:"private"`
	Role        string  `json:"role"`
	State       string  `json:"state"`
	Readiness   string  `json:"readiness"`
	PositionUS  string  `json:"positionUs"`
	ReportedAt  *string `json:"reportedAt"`
	Presence    string  `json:"presence"`
	JoinedAt    string  `json:"joinedAt"`
}

// ReadinessSummary aggregates per-member readiness. `aggregate` is the worst
// state present: lagging beats buffering beats ready. A member whose last report
// is older than ReadinessStaleSeconds counts as stale and, like lagging, holds
// the aggregate away from ready.
type ReadinessSummary struct {
	Aggregate   string `json:"aggregate"`
	Ready       int    `json:"ready"`
	Buffering   int    `json:"buffering"`
	Lagging     int    `json:"lagging"`
	Stale       int    `json:"stale"`
	MemberCount int    `json:"memberCount"`
}

// Sync publishes the §13 correction bands so a third-party client does not have
// to hard-code them. Values are milliseconds of drift from the target position.
type Sync struct {
	NoCorrectionUnderMS  int    `json:"noCorrectionUnderMs"`
	RateCorrectionMinRPM string `json:"rateCorrectionMinimum"`
	RateCorrectionMaxRPM string `json:"rateCorrectionMaximum"`
	RateCorrectionMaxMS  int    `json:"rateCorrectionMaxMs"`
	SeekAtOrOverMS       int    `json:"seekAtOrOverMs"`
}

var defaultSync = Sync{NoCorrectionUnderMS: 750, RateCorrectionMinRPM: "0.90", RateCorrectionMaxRPM: "1.10", RateCorrectionMaxMS: 4000, SeekAtOrOverMS: 3000}

type Group struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	State               string `json:"state"`
	HostAuthority       string `json:"hostAuthority"`
	HostMemberID        string `json:"hostMemberId"`
	Revision            string `json:"revision"`
	PlaybackRevision    string `json:"playbackRevision"`
	SettingsRevision    string `json:"settingsRevision"`
	GovernanceRevision  string `json:"governanceRevision"`
	QueueRevision       string `json:"queueRevision"`
	ReconnectGeneration string `json:"reconnectGeneration"`
	LastCommand         string `json:"lastCommand"`
	LastCommandID       string `json:"lastCommandId"`
	EndedReason         string `json:"endedReason"`
	// EventOrdinal is the newest ordinal on the group's event ledger at the time
	// of this projection. A client opening the event stream resumes from it.
	EventOrdinal string           `json:"eventOrdinal"`
	CreatedAt    string           `json:"createdAt"`
	UpdatedAt    string           `json:"updatedAt"`
	Permissions  Permissions      `json:"permissions"`
	Authority    Authority        `json:"authority"`
	Host         HostPresence     `json:"host"`
	Timeline     Timeline         `json:"timeline"`
	Settings     Settings         `json:"settings"`
	Sync         Sync             `json:"sync"`
	Readiness    ReadinessSummary `json:"readiness"`
	Members      []Member         `json:"members"`
	Queue        *Queue           `json:"queue"`
	MemberID     string           `json:"viewerMemberId"`
}

// EventOrdinalHint parses the ledger ordinal this projection was taken at.
func (g Group) EventOrdinalHint() int64 {
	n, _ := parseCounter(g.EventOrdinal)
	return n
}

type GroupResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerTime      string `json:"serverTime"`
	Group           Group  `json:"group"`
}

// ReadinessReceipt answers a member's readiness report: the room's state and
// counts, not its member list. Members learn of each other's changes from the
// group's events, so a report costs the same in a room of 2 or 32.
type ReadinessReceipt struct {
	ProtocolVersion  string           `json:"protocolVersion"`
	ServerTime       string           `json:"serverTime"`
	GroupID          string           `json:"groupId"`
	State            string           `json:"state"`
	Revision         string           `json:"revision"`
	PlaybackRevision string           `json:"playbackRevision"`
	Readiness        ReadinessSummary `json:"readiness"`
}

type GroupListResponse struct {
	ProtocolVersion string  `json:"protocolVersion"`
	ServerTime      string  `json:"serverTime"`
	Groups          []Group `json:"groups"`
}

// CreateGroupRequest opens a group bound to the creating device (B8a).
type CreateGroupRequest struct {
	ProtocolVersion string `json:"protocolVersion"`
	Name            string `json:"name"`
	DisplayName     string `json:"displayName,omitempty"`
	HostAuthority   string `json:"hostAuthority,omitempty"`
}

type JoinRequest struct {
	ProtocolVersion string `json:"protocolVersion"`
	Code            string `json:"code"`
	DisplayName     string `json:"displayName,omitempty"`
}

type TransferRequest struct {
	ProtocolVersion  string `json:"protocolVersion"`
	MemberID         string `json:"memberId"`
	ExpectedRevision string `json:"expectedRevision"`
}

type EndRequest struct {
	ProtocolVersion  string `json:"protocolVersion"`
	ExpectedRevision string `json:"expectedRevision"`
}

// --- record -----------------------------------------------------------------

type record struct {
	id, name, authority, accountID, profileID                         string
	hostMemberID, hostAuthority, state                                string
	revision, playbackRevision, queueRevision                         int64
	governanceRevision, activitySeenMS, settingsRevision, waitUntilMS int64
	waitResumeState                                                   string
	reconnect, eventOrdinal                                           int64
	hostDevice                                                        string
	itemID, currentEntryID                                            string
	positionUS, positionUpdatedMS                                     int64
	rateNum, rateDen                                                  int64
	queuePosition                                                     int
	shuffle                                                           bool
	repeat                                                            string
	lastCommand, lastCommandID, resumeState                           string
	hostSeenMS                                                        int64
	pauseAfter, endAfter                                              int64
	endedReason                                                       string
	createdMS, updatedMS                                              int64
	readinessSummary                                                  string
}

const recordColumns = `id,name,authority,account_id,profile_id,host_member_id,host_authority,state,revision,playback_revision,queue_revision,reconnect_generation,event_ordinal,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=social_groups.item_id),''),current_entry_id,position_us,position_updated_ms,rate_numerator,rate_denominator,queue_position,shuffle,repeat_mode,last_command,last_command_id,resume_state,host_seen_ms,pause_after_seconds,end_after_seconds,ended_reason,created_at_ms,updated_at_ms,host_device_id,settings_revision,wait_until_ms,wait_resume_state,activity_seen_ms,governance_revision,readiness_summary`

// resolveSocialItem maps a public item id to the integer the social tables
// store (0 when there is none). A name that resolves to nothing stores 0; a
// deleted entity reads back as "" and the visibility seam treats it as gone,
// so nothing observable survives the dangling reference either way.
func resolveSocialItem(ctx context.Context, tx *sql.Tx, public string) (int64, error) {
	if public == "" {
		return 0, nil
	}
	entity, err := entityid.Resolve(ctx, tx, public)
	if errors.Is(err, entityid.ErrNotFound) {
		return 0, nil
	}
	return entity, err
}

func scanRecord(row interface{ Scan(...any) error }) (record, error) {
	var g record
	e := row.Scan(&g.id, &g.name, &g.authority, &g.accountID, &g.profileID, &g.hostMemberID, &g.hostAuthority, &g.state, &g.revision, &g.playbackRevision, &g.queueRevision, &g.reconnect, &g.eventOrdinal, &g.itemID, &g.currentEntryID, &g.positionUS, &g.positionUpdatedMS, &g.rateNum, &g.rateDen, &g.queuePosition, &g.shuffle, &g.repeat, &g.lastCommand, &g.lastCommandID, &g.resumeState, &g.hostSeenMS, &g.pauseAfter, &g.endAfter, &g.endedReason, &g.createdMS, &g.updatedMS, &g.hostDevice, &g.settingsRevision, &g.waitUntilMS, &g.waitResumeState, &g.activitySeenMS, &g.governanceRevision, &g.readinessSummary)
	return g, e
}

func loadGroup(ctx context.Context, tx *sql.Tx, id string) (record, error) {
	g, e := scanRecord(tx.QueryRowContext(ctx, `SELECT `+recordColumns+` FROM social_groups WHERE id=?`, id))
	if errors.Is(e, sql.ErrNoRows) {
		return g, errNotFound
	}
	return g, e
}

type membership struct {
	id, displayName, role, state, readiness string
	positionUS, readinessMS, joinedMS       int64
	seenMS, readinessRevision               int64
}

func loadMember(ctx context.Context, tx *sql.Tx, group string, v identity.Viewer) (membership, error) {
	var m membership
	e := tx.QueryRowContext(ctx, `SELECT id,display_name,role,state,readiness,readiness_position_us,readiness_at_ms,joined_at_ms,seen_at_ms,readiness_revision FROM social_group_members WHERE group_id=? AND authority=? AND account_id=? AND profile_id=?`, group, v.Authority, v.AccountID, v.ProfileID).Scan(&m.id, &m.displayName, &m.role, &m.state, &m.readiness, &m.positionUS, &m.readinessMS, &m.joinedMS, &m.seenMS, &m.readinessRevision)
	if errors.Is(e, sql.ErrNoRows) {
		return m, errNotMember
	}
	return m, e
}

// loadJoinedMember is loadMember for a read: a member who has left keeps a row
// (so a rejoin keeps its member id) but is no longer in the room, and reads the
// group no more than somebody who never joined.
func loadJoinedMember(ctx context.Context, tx *sql.Tx, group string, v identity.Viewer) (membership, error) {
	m, e := loadMember(ctx, tx, group, v)
	if e == nil && m.state != "joined" {
		return m, errNotMember
	}
	return m, e
}

// --- lifecycle ---------------------------------------------------------------

// CreateGroup opens a group with the caller as host, bound to the caller's
// authenticated device: the group's authority is a fact of the request, never a
// claim in its body.
func (s *Store) CreateGroup(ctx context.Context, p identity.Principal, device string, req CreateGroupRequest) (GroupResponse, error) {
	var out GroupResponse
	if req.ProtocolVersion != Protocol || !text(req.Name, 120) {
		return out, errInvalid
	}
	if req.HostAuthority == "" {
		req.HostAuthority = "host-only"
	}
	if req.HostAuthority != "host-only" && req.HostAuthority != "anyone" {
		return out, errInvalid
	}
	if req.DisplayName == "" {
		req.DisplayName = "Viewer"
	}
	if !text(req.DisplayName, 64) {
		return out, errInvalid
	}
	if device == "" {
		return out, &Fault{Code: "device_required", Status: 409, Message: "Hosting a group needs a signed-in device."}
	}
	gated, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = s.limit(ctx, tx, "group-create:"+viewerKey(p.Viewer), 8, time.Hour); e != nil {
		return out, e
	}
	var live int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM social_groups WHERE authority=? AND account_id=? AND profile_id=? AND state NOT IN ('ended','failed')`, p.Authority, p.AccountID, p.ProfileID).Scan(&live); e != nil {
		return out, e
	}
	if live >= 4 {
		return out, fault("capacity_reached", 429, "You already have several groups open.")
	}
	now := s.ms()
	id, member := token("grp_"), token("mem_")
	if _, e = tx.ExecContext(ctx, `INSERT INTO social_groups(id,name,authority,account_id,profile_id,host_member_id,host_authority,state,host_device_id,pause_after_seconds,end_after_seconds,host_seen_ms,position_updated_ms,created_at_ms,updated_at_ms) VALUES(?,?,?,?,?,?,?,'lobby',?,?,?,?,?,?,?)`,
		id, req.Name, p.Authority, p.AccountID, p.ProfileID, member, req.HostAuthority, device, HostPauseSeconds, HostEndSeconds, now, now, now, now); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO social_group_members(id,group_id,authority,account_id,profile_id,display_name,role,state,readiness,readiness_at_ms,joined_at_ms,seen_at_ms) VALUES(?,?,?,?,?,?,'host','joined','ready',?,?,?)`, member, id, p.Authority, p.AccountID, p.ProfileID, req.DisplayName, now, now, now); e != nil {
		return out, e
	}
	if e = s.publish(ctx, tx, id, EventMembers, map[string]any{"revision": counter(1)}); e != nil {
		return out, e
	}
	g, e := loadGroup(ctx, tx, id)
	if e != nil {
		return out, e
	}
	out, e = s.project(ctx, tx, p, g, membership{id: member, role: "host", state: "joined", readiness: "ready", readinessMS: now, joinedMS: now})
	if e != nil {
		return out, e
	}
	if e = gated.Commit(); e != nil {
		return out, e
	}
	// A new group is live, so the timeline must advance with nobody watching.
	// After the commit, never inside the transaction.
	s.ensureLive()
	return out, nil
}

// Heartbeat records host or member presence. For the host it is the proof of
// life the reconnect timeline is measured from; a host that returns inside the
// pause boundary restores the group's prior playing/paused state.
func (s *Store) Heartbeat(ctx context.Context, p identity.Principal, device, id string) (GroupResponse, error) {
	return s.mutateTelemetry(ctx, p, id, func(tx *sql.Tx, g *record, m membership) error {
		if m.role != "host" {
			return nil
		}
		// The host's heartbeat binds its device: a new host after a transfer, or
		// the same host on another device.
		if device != "" {
			g.hostDevice = device
		}
		return s.hostReturned(ctx, tx, g)
	})
}

// hostReturned is the reconnect half of the timeline. Restoring the pre-loss
// state is only correct before the pause boundary: once the group has paused,
// resume_state is already `paused` and the host must send an explicit Play.
func (s *Store) hostReturned(ctx context.Context, tx *sql.Tx, g *record) error {
	g.hostSeenMS = s.ms()
	if !isReconnecting(g.state) {
		return nil
	}
	g.state = resumed(*g)
	g.reconnect++
	if g.state == "playing" {
		// The anchor kept extrapolating through the outage, so it is still true.
		return s.publish(ctx, tx, g.id, EventHost, map[string]any{"presence": "connected", "reconnectGeneration": counter(g.reconnect)})
	}
	g.positionUS = s.extrapolate(*g)
	g.positionUpdatedMS = s.ms()
	return s.publish(ctx, tx, g.id, EventHost, map[string]any{"presence": "connected", "reconnectGeneration": counter(g.reconnect)})
}

func isReconnecting(state string) bool {
	return state == "host-reconnecting-playing" || state == "host-reconnecting-paused"
}

// resumed is the state a reconnecting group returns to. resume_state records
// the pre-loss phase as well as playing or paused. An empty group always
// returns to the lobby rather than to a pause of nothing.
func resumed(g record) string {
	if g.resumeState != "playing" && g.itemID == "" {
		return "lobby"
	}
	return g.resumeState
}

// extrapolate returns the position the group's clock has reached now.
func (s *Store) extrapolate(g record) int64 {
	if g.itemID == "" {
		return 0
	}
	playing := g.state == "playing" || g.state == "host-reconnecting-playing"
	if !playing || g.positionUpdatedMS <= 0 || g.rateDen == 0 {
		return g.positionUS
	}
	elapsed := s.ms() - g.positionUpdatedMS
	if elapsed <= 0 {
		return g.positionUS
	}
	return min(MaxPositionUS, g.positionUS+elapsed*1000*g.rateNum/g.rateDen)
}

// EndGroup closes a group. Only the host may end it; the contract makes an
// explicit host Leave without a transfer end the group as well.
func (s *Store) EndGroup(ctx context.Context, p identity.Principal, id string, req EndRequest) (GroupResponse, error) {
	if req.ProtocolVersion != Protocol {
		return GroupResponse{}, errInvalid
	}
	return s.mutate(ctx, p, id, func(tx *sql.Tx, g *record, m membership) error {
		if m.role != "host" {
			return errHost
		}
		expected, ok := parseCounter(req.ExpectedRevision)
		if !ok {
			return errInvalid
		}
		if expected != g.governanceRevision {
			return &Fault{Code: "revision_conflict", Status: 409, Message: "The group changed.", Detail: map[string]any{"currentRevision": counter(g.governanceRevision)}}
		}
		return s.endGroup(ctx, tx, g, "host-ended")
	})
}

func (s *Store) endGroup(ctx context.Context, tx *sql.Tx, g *record, reason string) error {
	g.state, g.endedReason = "ended", reason
	g.waitUntilMS = 0
	g.governanceRevision++
	g.playbackRevision++
	now := s.ms()
	if _, e := tx.ExecContext(ctx, `UPDATE social_groups SET ended_at_ms=? WHERE id=?`, now, g.id); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE social_group_invites SET revoked=1 WHERE group_id=?`, g.id); e != nil {
		return e
	}
	return s.publish(ctx, tx, g.id, EventEnded, map[string]any{"reason": reason, "endedAt": stamp(now)})
}

// Leave removes the caller. A member leaving is routine; the host leaving ends
// the group unless authority was transferred first.
func (s *Store) Leave(ctx context.Context, p identity.Principal, id string) (GroupResponse, error) {
	return s.mutate(ctx, p, id, func(tx *sql.Tx, g *record, m membership) error {
		if _, e := tx.ExecContext(ctx, `UPDATE social_group_members SET state='left',left_at_ms=? WHERE id=?`, s.ms(), m.id); e != nil {
			return e
		}
		if e := s.publish(ctx, tx, g.id, EventMembers, map[string]any{"revision": counter(g.revision + 1)}); e != nil {
			return e
		}
		if m.role == "host" {
			return s.endGroup(ctx, tx, g, "host-left")
		}
		var remaining int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM social_group_members WHERE group_id=? AND state='joined'`, g.id).Scan(&remaining); e != nil {
			return e
		}
		if remaining == 0 {
			return s.endGroup(ctx, tx, g, "empty")
		}
		return nil
	})
}

// TransferHost moves host authority to another joined member. The group binds to
// the new host's device: at once when the new host is the caller, else on the new
// host's first heartbeat (until then the group has no authoritative session).
// hostClaimFacts are the member facts TransferHost checks for a self-claim.
// They are also what a read uses to publish canClaimHost, so the permission and
// the mutation cannot drift.
type hostClaimFacts struct {
	role, state, readiness    string
	seenMS, joinedMS, readyMS int64
	readyRevision             int64
}

// hostClaimGovernanceAllowed checks only whether a non-host self-claim is in
// its governance window. Refuse closed windows before reading target readiness.
func hostClaimGovernanceAllowed(g record, f hostClaimFacts, now int64) bool {
	return f.role != "host" && f.state == "joined" && g.state != "ended" &&
		(isReconnecting(g.state) || g.hostAuthority == "anyone") &&
		now-g.hostSeenMS >= g.pauseAfter*1000
}

// hostClaimAllowed adds current, connected readiness to the governance window.
// Reads and mutations use this same full rule for accepting a self-claim.
func hostClaimAllowed(g record, f hostClaimFacts, now int64) bool {
	if !hostClaimGovernanceAllowed(g, f, now) {
		return false
	}
	seen := f.seenMS
	if f.joinedMS > seen {
		seen = f.joinedMS
	}
	return f.readiness == "ready" && now-seen <= HostGraceSeconds*1000 &&
		now-f.readyMS <= ReadinessStaleSeconds*1000 && (g.itemID == "" || f.readyRevision == g.playbackRevision)
}

func (s *Store) TransferHost(ctx context.Context, p identity.Principal, device, id string, req TransferRequest) (GroupResponse, error) {
	if req.ProtocolVersion != Protocol || !ValidID(req.MemberID) {
		return GroupResponse{}, errInvalid
	}
	return s.mutate(ctx, p, id, func(tx *sql.Tx, g *record, m membership) error {
		expected, ok := parseCounter(req.ExpectedRevision)
		if !ok {
			return errInvalid
		}
		if expected != g.governanceRevision {
			return &Fault{Code: "revision_conflict", Status: 409, Message: "The group changed.", Detail: map[string]any{"currentRevision": counter(g.governanceRevision)}}
		}
		// Either the sitting host hands authority over, or the named member is
		// the caller claiming a host that has passed the pause boundary. A
		// non-host naming someone else is never that caller.
		if m.role != "host" && req.MemberID != m.id {
			return errHost
		}
		if m.role != "host" && !hostClaimGovernanceAllowed(*g, hostClaimFacts{role: m.role, state: m.state}, s.ms()) {
			return errHost
		}
		var state, role string
		var targetAuthority, targetAccount, targetProfile string
		e := tx.QueryRowContext(ctx, `SELECT state,role,authority,account_id,profile_id FROM social_group_members WHERE id=? AND group_id=?`, req.MemberID, g.id).Scan(&state, &role, &targetAuthority, &targetAccount, &targetProfile)
		if errors.Is(e, sql.ErrNoRows) {
			return &Fault{Code: "member_not_found", Status: 404, Message: "No such member."}
		}
		if e != nil {
			return e
		}
		var ready string
		var seenAt, readyAt, readyRevision int64
		if e = tx.QueryRowContext(ctx, `SELECT readiness,max(seen_at_ms,joined_at_ms),readiness_at_ms,readiness_revision FROM social_group_members WHERE id=?`, req.MemberID).Scan(&ready, &seenAt, &readyAt, &readyRevision); e != nil {
			return e
		}
		if ready != "ready" || s.ms()-seenAt > HostGraceSeconds*1000 || s.ms()-readyAt > ReadinessStaleSeconds*1000 || g.itemID != "" && readyRevision != g.playbackRevision {
			return fault("participant_incompatible", 409, "That member is not ready to host.")
		}
		if state != "joined" {
			return &Fault{Code: "participant_incompatible", Status: 409, Message: "That member is not in the group."}
		}
		selfClaim := req.MemberID == m.id && hostClaimAllowed(*g, hostClaimFacts{role: role, state: state, readiness: ready, seenMS: seenAt, readyMS: readyAt, readyRevision: readyRevision}, s.ms())
		if m.role != "host" && !selfClaim {
			return errHost
		}
		if targetAuthority == p.Authority && targetAccount == p.AccountID && targetProfile == p.ProfileID && device != "" {
			g.hostDevice = device
		} else {
			g.hostDevice = ""
		}
		if _, e = tx.ExecContext(ctx, `UPDATE social_group_members SET role='member' WHERE group_id=? AND role='host'`, g.id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE social_group_members SET role='host' WHERE id=?`, req.MemberID); e != nil {
			return e
		}
		g.playbackRevision++
		g.governanceRevision++
		g.hostMemberID = req.MemberID
		g.hostSeenMS = s.ms()
		g.reconnect++
		if isReconnecting(g.state) {
			g.state = resumed(*g)
			if g.state != "playing" {
				g.positionUS = s.extrapolate(*g)
				g.positionUpdatedMS = s.ms()
			}
		}
		return s.publish(ctx, tx, g.id, EventHost, map[string]any{"presence": "connected", "hostMemberId": req.MemberID, "reconnectGeneration": counter(g.reconnect)})
	})
}

// ListGroups returns the live groups the caller is a joined member of.
func (s *Store) ListGroups(ctx context.Context, p identity.Principal) (GroupListResponse, error) {
	out := GroupListResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Groups: []Group{}}
	// A read, on a snapshot, with no write gate. See settled.
	tx, done, e := dbwork.BeginRead(ctx, s.DB)
	if e != nil {
		return out, e
	}
	defer done()
	rows, e := tx.QueryContext(ctx, `SELECT group_id FROM social_group_members WHERE authority=? AND account_id=? AND profile_id=? AND state='joined'`, p.Authority, p.AccountID, p.ProfileID)
	if e != nil {
		return out, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	for _, id := range ids {
		g, e := loadGroup(ctx, tx, id)
		if e != nil {
			return out, e
		}
		g = s.settled(g)
		if g.state == "ended" || g.state == "failed" {
			continue
		}
		m, e := loadMember(ctx, tx, id, p.Viewer)
		if e != nil {
			return out, e
		}
		one, e := s.project(ctx, tx, p, g, m)
		if e != nil {
			return out, e
		}
		one.Group.Queue = nil
		out.Groups = append(out.Groups, one.Group)
	}
	return out, nil
}

// ReadGroup projects one group for one member. Everything the member may not see
// is already removed here; there is no second filter downstream.
func (s *Store) ReadGroup(ctx context.Context, p identity.Principal, id string) (GroupResponse, error) {
	var out GroupResponse
	// A read, on a snapshot, with no write gate. Expiry is computed rather than
	// written: a member polling their group must see a host who has gone away as
	// gone, but must not make every one of those polls a transaction. The UPDATE
	// and the events belong to the next writer, or to the sweep.
	tx, done, e := dbwork.BeginRead(ctx, s.DB)
	if e != nil {
		return out, e
	}
	defer done()
	g, e := loadGroup(ctx, tx, id)
	if e != nil {
		return out, e
	}
	m, e := loadJoinedMember(ctx, tx, id, p.Viewer)
	if e != nil {
		return out, e
	}
	g = s.settled(g)
	return s.project(ctx, tx, p, g, m)
}

// StreamMember is the narrow membership recheck for an already-open event
// stream. The caller separately reauthorizes the live session and profile.
func (s *Store) StreamMember(ctx context.Context, p identity.Principal, id string) (bool, error) {
	var joined bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM social_group_members WHERE group_id=? AND authority=? AND account_id=? AND profile_id=? AND state='joined')`, id, p.Authority, p.AccountID, p.ProfileID).Scan(&joined)
	return joined, err
}

// mutate is the one write path every group command shares: load, sweep the host
// timeline, run the command, bump the revision, publish and commit.
func (s *Store) mutate(ctx context.Context, p identity.Principal, id string, apply func(*sql.Tx, *record, membership) error) (GroupResponse, error) {
	return s.mutateGroup(ctx, p, id, true, apply)
}
func (s *Store) mutateTelemetry(ctx context.Context, p identity.Principal, id string, apply func(*sql.Tx, *record, membership) error) (GroupResponse, error) {
	return s.mutateGroup(ctx, p, id, false, apply)
}
func (s *Store) mutateGroup(ctx context.Context, p identity.Principal, id string, semantic bool, apply func(*sql.Tx, *record, membership) error) (GroupResponse, error) {
	return s.mutateGroupAs(ctx, p, id, semantic, nil, apply)
}

// mutateGroupAs is mutateGroup; with a receipt it fills that from the saved
// room in place of the member's full projection (and returns no projection).
func (s *Store) mutateGroupAs(ctx context.Context, p identity.Principal, id string, semantic bool, receipt *ReadinessReceipt, apply func(*sql.Tx, *record, membership) error) (GroupResponse, error) {
	var out GroupResponse
	gated4, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	g, e := loadGroup(ctx, tx, id)
	if e != nil {
		return out, e
	}
	m, e := loadMember(ctx, tx, id, p.Viewer)
	if e != nil {
		return out, e
	}
	before := g
	swept, e := s.sweepGroup(ctx, tx, &g)
	if e != nil {
		return out, e
	}
	if g.state == "ended" || g.state == "failed" {
		// A group the timeline just ended must stay ended: commit the sweep
		// before refusing the command, or the next caller would see it alive.
		if swept {
			if e = s.save(ctx, tx, g, true); e != nil {
				return out, e
			}
			if e = gated4.Commit(); e != nil {
				return out, e
			}
			s.Wake(id)
		}
		return out, errGroupEnded
	}
	m, e = loadJoinedMember(ctx, tx, id, p.Viewer)
	if e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE social_group_members SET seen_at_ms=? WHERE id=?`, s.ms(), m.id); e != nil {
		return out, e
	}
	g.activitySeenMS = s.ms()
	if e = s.limit(ctx, tx, "group-telemetry:"+id+":"+m.id, 120, time.Minute); e != nil {
		return out, e
	}
	if e = apply(tx, &g, m); e != nil {
		return out, e
	}
	bump := semantic || before.state != g.state || before.reconnect != g.reconnect
	if e = s.save(ctx, tx, g, bump); e != nil {
		return out, e
	}
	g, e = loadGroup(ctx, tx, id)
	if e != nil {
		return out, e
	}
	if receipt != nil {
		summary := ReadinessSummary{}
		if g.readinessSummary == "" || json.Unmarshal([]byte(g.readinessSummary), &summary) != nil {
			if summary, e = s.readinessSummary(ctx, tx, g); e != nil {
				return out, e
			}
		}
		*receipt = ReadinessReceipt{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), GroupID: g.id, State: g.state, Revision: counter(g.revision), PlaybackRevision: counter(g.playbackRevision), Readiness: summary}
	} else {
		if m, e = loadMember(ctx, tx, id, p.Viewer); e != nil {
			return out, e
		}
		if out, e = s.project(ctx, tx, p, g, m); e != nil {
			return out, e
		}
	}
	if e = gated4.Commit(); e != nil {
		return out, e
	}
	s.Wake(id)
	return out, nil
}

func (s *Store) save(ctx context.Context, tx *sql.Tx, g record, bump bool) error {
	revision := g.revision
	if bump {
		revision++
	}
	entity, e := resolveSocialItem(ctx, tx, g.itemID)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE social_groups SET name=?,host_member_id=?,host_authority=?,state=?,revision=?,playback_revision=?,queue_revision=?,reconnect_generation=?,item_id=?,current_entry_id=?,position_us=?,position_updated_ms=?,rate_numerator=?,rate_denominator=?,queue_position=?,shuffle=?,repeat_mode=?,host_device_id=?,last_command=?,last_command_id=?,resume_state=?,host_seen_ms=?,ended_reason=?,updated_at_ms=?,settings_revision=?,wait_until_ms=?,wait_resume_state=?,activity_seen_ms=?,governance_revision=? WHERE id=?`,
		g.name, g.hostMemberID, g.hostAuthority, g.state, revision, g.playbackRevision, g.queueRevision, g.reconnect, entity, g.currentEntryID, g.positionUS, g.positionUpdatedMS, g.rateNum, g.rateDen, g.queuePosition, g.shuffle, g.repeat, g.hostDevice, g.lastCommand, g.lastCommandID, g.resumeState, g.hostSeenMS, g.endedReason, s.ms(), g.settingsRevision, g.waitUntilMS, g.waitResumeState, g.activitySeenMS, g.governanceRevision, g.id)
	return e
}

// --- host reconnect timeline -------------------------------------------------

// sweepGroup applies the host-disconnect timeline to one loaded group. It is
// called on every read and every write, and by Sweep for groups nobody is
// touching, so the boundaries fire on wall-clock time and not on traffic.
//
// Timeline (contract §11): the group enters host-reconnecting once the host has
// been silent past the grace window, and playback continues. At
// pause_after_seconds (two minutes) the group pauses atomically. At
// end_after_seconds (ten minutes) it ends. A reconnect before the pause boundary
// restores the prior playing/paused state; after it, the group stays paused and
// the host must send an explicit Play.
func (s *Store) sweepGroup(ctx context.Context, tx *sql.Tx, g *record) (bool, error) {
	if g.state == "ended" || g.state == "failed" {
		return false, nil
	}
	changed, e := s.sweepMembers(ctx, tx, g)
	if e != nil {
		return false, e
	}
	if released, e := s.releaseWait(ctx, tx, g); e != nil {
		return false, e
	} else if released {
		changed = true
	}
	elapsed := (s.ms() - g.hostSeenMS) / 1000
	if g.hostAuthority == "anyone" {
		elapsed = (s.ms() - max(g.hostSeenMS, g.activitySeenMS)) / 1000
	}
	switch {
	case elapsed >= g.endAfter:
		if e := s.endGroup(ctx, tx, g, "host-unavailable"); e != nil {
			return false, e
		}
		return true, nil
	case elapsed >= g.pauseAfter:
		if g.state == "host-reconnecting-paused" {
			return changed, nil
		}
		g.positionUS = s.extrapolate(*g)
		g.positionUpdatedMS = s.ms()
		g.state, g.resumeState = "host-reconnecting-paused", "paused"
		g.playbackRevision++
		if e := s.publish(ctx, tx, g.id, EventHost, map[string]any{"presence": "reconnecting", "pausedAt": stamp(s.ms())}); e != nil {
			return false, e
		}
		if e := s.publish(ctx, tx, g.id, EventTransport, map[string]any{"state": "paused", "anchorPositionUs": counter(g.positionUS), "anchorAt": stamp(g.positionUpdatedMS), "reason": "host-unavailable"}); e != nil {
			return false, e
		}
		return true, nil
	case elapsed >= HostGraceSeconds:
		if isReconnecting(g.state) {
			return changed, nil
		}
		if g.state == "playing" {
			g.resumeState, g.state = "playing", "host-reconnecting-playing"
		} else {
			g.resumeState, g.state = g.state, "host-reconnecting-paused"
		}
		if e := s.publish(ctx, tx, g.id, EventHost, map[string]any{"presence": "reconnecting"}); e != nil {
			return false, e
		}
		return true, nil
	}
	return changed, nil
}

// settled is sweepGroup's arithmetic without its effects: the state a group has
// already reached because time passed, computed in memory.
//
// Watch Together is chatty by design — a heartbeat every ten seconds per member,
// a read after most events, a queue read on queue events — and every one of
// those reads used to open a gated write transaction, because reading a group
// meant sweeping it and sweeping it meant possibly writing. A six-person group
// idling in front of a paused film was a steady stream of transactions through
// the server's single writer.
//
// The expiry itself is not optional: a host who has gone away must show as gone
// the moment anybody looks. So the reads compute it here, on a snapshot, and
// leave the UPDATE and the events to whoever writes next — a command, or the
// sweep. Two readers computing the same thing get the same answer, because it is
// a function of the row and the clock and nothing else.
func (s *Store) settled(g record) record {
	if g.state == "ended" || g.state == "failed" {
		return g
	}
	if g.state == "waiting" && s.ms() >= g.waitUntilMS {
		g.state = g.waitResumeState
		g.positionUpdatedMS = g.waitUntilMS
		g.waitUntilMS = 0
	}
	elapsed := (s.ms() - g.hostSeenMS) / 1000
	if g.hostAuthority == "anyone" {
		elapsed = (s.ms() - max(g.hostSeenMS, g.activitySeenMS)) / 1000
	}
	switch {
	case elapsed >= g.endAfter:
		g.state, g.endedReason = "ended", "host-unavailable"
	case elapsed >= g.pauseAfter:
		if g.state == "host-reconnecting-paused" {
			return g
		}
		g.positionUS = s.extrapolate(g)
		g.positionUpdatedMS = s.ms()
		g.state, g.resumeState = "host-reconnecting-paused", "paused"
	case elapsed >= HostGraceSeconds:
		if isReconnecting(g.state) {
			return g
		}
		if g.state == "playing" {
			g.resumeState, g.state = "playing", "host-reconnecting-playing"
		} else {
			g.resumeState, g.state = g.state, "host-reconnecting-paused"
		}
	}
	return g
}

// due reports whether settled would change this group, so Sweep can open a write
// transaction only for the groups that need one. On a server where nothing has
// timed out — which is nearly always — a sweep is one read and no writes at all.
func (s *Store) due(g record) bool {
	if g.state == "ended" || g.state == "failed" {
		return false
	}
	settled := s.settled(g)
	return settled.state != g.state || settled.resumeState != g.resumeState
}

// Sweep advances every live group's host timeline. The process-wide sweeper
// calls it on a tick whether or not any stream is open; a test calls it
// directly after moving the clock.
func (s *Store) Sweep(ctx context.Context) error {
	// One read decides which groups have anything to do. Opening a write
	// transaction per live group per tick — and rolling it back when nothing had
	// changed, which was almost always — put a group's idle cost on the single
	// writer that playback controls and sign-ins also queue behind.
	rows, e := dbwork.Query(ctx, s.DB, `SELECT `+recordColumns+` FROM social_groups WHERE state NOT IN ('ended','failed')`)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		g, scanErr := scanRecord(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		if s.due(g) {
			ids = append(ids, g.id)
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	staleRows, e := dbwork.Query(ctx, s.DB, `SELECT DISTINCT group_id FROM social_group_members WHERE role='member' AND state='joined' AND max(seen_at_ms,joined_at_ms)<?`, s.ms()-MemberLeaveSeconds*1000)
	if e != nil {
		return e
	}
	for staleRows.Next() {
		var id string
		if e = staleRows.Scan(&id); e != nil {
			staleRows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = staleRows.Err()
	staleRows.Close()
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		gated5, e := s.begin(ctx)
		if e != nil {
			return e
		}
		tx := gated5.Tx()
		g, e := loadGroup(ctx, tx, id)
		if e != nil {
			gated5.Rollback()
			return e
		}
		changed, e := s.sweepGroup(ctx, tx, &g)
		if e != nil {
			gated5.Rollback()
			return e
		}
		if !changed {
			gated5.Rollback()
			continue
		}
		if e = s.save(ctx, tx, g, true); e != nil {
			gated5.Rollback()
			return e
		}
		if e = gated5.Commit(); e != nil {
			return e
		}
		s.Wake(id)
	}
	// The sweeper stops itself when no live group remains, so a server with no
	// live group runs no loop and no query. Stream holds alone never keep it
	// alive: holders are counted, but only live groups hold the loop.
	return s.stopIfIdle(ctx)
}

// --- projection --------------------------------------------------------------

func (s *Store) project(ctx context.Context, tx *sql.Tx, p identity.Principal, g record, m membership) (GroupResponse, error) {
	now := s.ms()
	out := GroupResponse{ProtocolVersion: Protocol, ServerTime: stamp(now)}
	members, summary, e := s.members(ctx, tx, g)
	if e != nil {
		return out, e
	}
	queue, e := s.projectQueue(ctx, tx, p, g, m)
	if e != nil {
		return out, e
	}
	host := HostPresence{Presence: "connected", LastSeenAt: stamp(g.hostSeenMS)}
	if g.hostAuthority == "anyone" && s.ms()-g.hostSeenMS >= HostGraceSeconds*1000 {
		host.Presence = "reconnecting"
	}
	if isReconnecting(g.state) {
		host.Presence = "reconnecting"
		host.PauseAt = optionalStamp(g.hostSeenMS + g.pauseAfter*1000)
		host.EndAt = optionalStamp(g.hostSeenMS + g.endAfter*1000)
	}
	if g.state == "ended" && g.endedReason == "host-unavailable" {
		host.Presence = "absent"
	}

	canControl := m.role == "host" || g.hostAuthority == "anyone"
	canClaimHost := hostClaimAllowed(g, hostClaimFacts{role: m.role, state: m.state, readiness: m.readiness, seenMS: m.seenMS, joinedMS: m.joinedMS, readyMS: m.readinessMS, readyRevision: m.readinessRevision}, s.ms())
	// The bound device and the session it plays are the host's own equipment.
	// Everybody else is told only what state the authority is in.
	authority := Authority{State: "fresh"}
	if m.role == "host" {
		authority.DeviceID = g.hostDevice
	}
	out.Group = Group{
		ID: g.id, Name: g.name, State: g.state, HostAuthority: g.hostAuthority, HostMemberID: g.hostMemberID,
		Revision: counter(g.revision), PlaybackRevision: counter(g.playbackRevision), SettingsRevision: counter(g.settingsRevision), GovernanceRevision: counter(g.governanceRevision), QueueRevision: counter(g.queueRevision),
		ReconnectGeneration: counter(g.reconnect), LastCommand: g.lastCommand, LastCommandID: g.lastCommandID,
		EndedReason: g.endedReason, EventOrdinal: counter(g.eventOrdinal), CreatedAt: stamp(g.createdMS), UpdatedAt: stamp(g.updatedMS),
		Permissions: Permissions{IsHost: m.role == "host", CanControl: canControl, CanManageQueue: canControl, CanClaimHost: canClaimHost},
		Authority:   authority,
		Host:        host,
		Timeline: Timeline{
			ItemID: g.itemID, CurrentEntryID: g.currentEntryID, State: timelineState(g.state),
			AnchorPositionUS: counter(g.positionUS), AnchorAt: stamp(g.positionUpdatedMS),
			Rate: Rate{Numerator: counter(g.rateNum), Denominator: counter(g.rateDen)}, QueuePosition: g.queuePosition, WaitUntil: optionalStamp(g.waitUntilMS),
		},
		Settings:  Settings{Shuffle: g.shuffle, Repeat: g.repeat},
		Sync:      defaultSync,
		Readiness: summary,
		Members:   members,
		Queue:     queue,
		MemberID:  m.id,
	}
	visible, e := s.visible(ctx, tx, p.Viewer, g.itemID)
	if e != nil {
		return out, e
	}
	if !visible {
		out.Group.Timeline.ItemID = ""
	}
	if s.Playback != nil && g.hostDevice != "" {
		session, present, e := s.Playback.DeviceSessionTx(ctx, tx, g.hostDevice)
		if e != nil {
			return out, e
		}
		switch {
		case !present:
			out.Group.Authority.State = "retired"
		case session.Live:
			out.Group.Authority.State = "bound"
			if m.role == "host" {
				id := session.ID
				out.Group.Authority.PlaybackID = &id
			}
		}
	}
	return out, nil
}

func timelineState(state string) string {
	switch state {
	case "playing", "host-reconnecting-playing":
		return "playing"
	case "lobby", "creating":
		return "idle"
	case "ended", "failed":
		return "stopped"
	}
	return "paused"
}

// readinessSummary is the room's readiness counts alone, as members() counts
// them, in one aggregate over the room's rows: a readiness report needs no
// member list.
func (s *Store) readinessSummary(ctx context.Context, tx *sql.Tx, g record) (ReadinessSummary, error) {
	summary := ReadinessSummary{Aggregate: "ready"}
	now := s.ms()
	if e := tx.QueryRowContext(ctx, `SELECT count(*),
 COALESCE(sum(readiness_at_ms<=0 OR ?2-readiness_at_ms>?3),0),
 COALESCE(sum(NOT(readiness_at_ms<=0 OR ?2-readiness_at_ms>?3) AND readiness='ready'),0),
 COALESCE(sum(NOT(readiness_at_ms<=0 OR ?2-readiness_at_ms>?3) AND readiness='lagging'),0)
 FROM social_group_members WHERE group_id=?1 AND state='joined' AND ?2-max(seen_at_ms,joined_at_ms)<=?4`, g.id, now, ReadinessStaleSeconds*1000, MemberStaleSeconds*1000).Scan(&summary.MemberCount, &summary.Stale, &summary.Ready, &summary.Lagging); e != nil {
		return summary, e
	}
	summary.Buffering = summary.MemberCount - summary.Stale - summary.Ready - summary.Lagging
	switch {
	case summary.Lagging > 0 || summary.Stale > 0:
		summary.Aggregate = "lagging"
	case summary.Buffering > 0:
		summary.Aggregate = "buffering"
	case summary.MemberCount == 0:
		summary.Aggregate = "buffering"
	}
	return summary, nil
}

func (s *Store) members(ctx context.Context, tx *sql.Tx, g record) ([]Member, ReadinessSummary, error) {
	summary := ReadinessSummary{Aggregate: "ready"}
	rows, e := tx.QueryContext(ctx, `SELECT id,display_name,COALESCE((SELECT username FROM accounts WHERE id=m.account_id),''),role,state,readiness,readiness_position_us,readiness_at_ms,joined_at_ms,max(seen_at_ms,joined_at_ms),`+watchTogetherVisible("m")+` FROM social_group_members m WHERE group_id=? AND state IN ('invited','joined') ORDER BY joined_at_ms,id`, g.id)
	if e != nil {
		return nil, summary, e
	}
	defer rows.Close()
	out := []Member{}
	now := s.ms()
	for rows.Next() {
		var m Member
		var positionUS, reportedMS, joinedMS, seenMS int64
		var public bool
		if e = rows.Scan(&m.ID, &m.DisplayName, &m.Username, &m.Role, &m.State, &m.Readiness, &positionUS, &reportedMS, &joinedMS, &seenMS, &public); e != nil {
			return nil, summary, e
		}
		m.PositionUS, m.ReportedAt, m.JoinedAt = counter(positionUS), optionalStamp(reportedMS), stamp(joinedMS)
		stale := reportedMS <= 0 || now-reportedMS > ReadinessStaleSeconds*1000
		switch {
		case now-seenMS > MemberStaleSeconds*1000:
			m.Presence = "disconnected"
		case now-seenMS > HostGraceSeconds*1000:
			m.Presence = "away"
		default:
			m.Presence = "connected"
		}
		if m.State == "joined" && m.Presence != "disconnected" {
			summary.MemberCount++
			switch {
			case stale:
				summary.Stale++
			case m.Readiness == "ready":
				summary.Ready++
			case m.Readiness == "lagging":
				summary.Lagging++
			default:
				summary.Buffering++
			}
		}
		if !public {
			m.Private = true
			m.DisplayName = "Viewer"
			m.Username = ""
			m.PositionUS = "0"
			m.ReportedAt = nil
		}
		out = append(out, m)
	}
	if e = rows.Err(); e != nil {
		return nil, summary, e
	}
	switch {
	case summary.Lagging > 0 || summary.Stale > 0:
		summary.Aggregate = "lagging"
	case summary.Buffering > 0:
		summary.Aggregate = "buffering"
	case summary.MemberCount == 0:
		summary.Aggregate = "buffering"
	}
	return out, summary, nil
}
