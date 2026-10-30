package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/social"
)

// socialFixture is a v1 server with the extra viewers social playback needs: a
// second member who can see the library and a third who cannot, so visibility
// placeholders are exercised against the real library policy rather than a
// stub. Every bearer is bound to a device, as groups bind to devices (B8a).
type socialFixture struct {
	*tl6V1Fixture
	d       Dependencies
	h       http.Handler
	token   string // the owner's device
	member  string // bearer for account2/profile2, can see the library
	outside string // bearer for account3/profile3, can see nothing
	item    string
	profile string
}

func socialHTTP(t *testing.T) *socialFixture {
	t.Helper()
	base := newTL6V1Fixture(t, 1)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := base.db.Exec(query, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account2','member',X'00','profile2',1)`)
	exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account3','outside',X'00','profile3',1)`)
	exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='account2'`, `["`+base.library+`"]`)
	member := base.deviceFor("account2", "profile2", "member", "member-phone")
	outside := base.deviceFor("account3", "profile3", "member", "outside-phone")
	return &socialFixture{tl6V1Fixture: base, d: base.deps, h: base.handler, token: base.owner.AccessToken, member: member.AccessToken, outside: outside.AccessToken, item: base.items[0], profile: base.owner.Viewer.ProfileID}
}

func (f *socialFixture) as(token, method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var raw []byte
	if body != nil {
		var e error
		raw, e = json.Marshal(body)
		if e != nil {
			f.t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

// play starts a v1 session on the fixture's film from the given bearer's device
// and returns its id.
func (f *socialFixture) play(token string) string {
	f.t.Helper()
	f.callAs(token, "PUT", "/v1/me/devices/current/capabilities", nil, tl6WebCapabilities(), 204, nil)
	var s playbackv1.SessionView
	f.callAs(token, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "social-start-" + strings.Repeat("0", 8) + token[len(token)-8:]}, tl6StartBody(f.item, map[string]any{"startFrom": "beginning"}), 201, &s)
	return s.ID
}

// source starts a v1 session on a fresh device of the owner's: a handoff source.
func (f *socialFixture) source(installation string) string {
	f.t.Helper()
	return f.play(f.device(installation).AccessToken)
}

// group opens a Watch Together group bound to the owner's device.
func (f *socialFixture) group(authority string) social.GroupResponse {
	f.t.Helper()
	w := f.as(f.token, "POST", "/v1/groups", social.CreateGroupRequest{ProtocolVersion: social.Protocol, Name: "Movie night", DisplayName: "Host", HostAuthority: authority})
	var out social.GroupResponse
	f.decode(w, 201, &out)
	return out
}

func (f *socialFixture) invite(id string) string {
	f.t.Helper()
	w := f.as(f.token, "POST", "/v1/groups/"+id+"/invites", social.InviteRequest{ProtocolVersion: social.Protocol})
	var out social.InviteResponse
	f.decode(w, 201, &out)
	return out.Invite.Code
}

func (f *socialFixture) join(token, code string) social.GroupResponse {
	f.t.Helper()
	w := f.as(token, "POST", "/v1/groups/join", social.JoinRequest{ProtocolVersion: social.Protocol, Code: code, DisplayName: "Guest"})
	var out social.GroupResponse
	f.decode(w, 200, &out)
	return out
}

func (f *socialFixture) decode(w *httptest.ResponseRecorder, want int, out any) {
	f.t.Helper()
	if w.Code != want {
		f.t.Fatalf("status %d (want %d): %s", w.Code, want, w.Body.String())
	}
	if out != nil {
		if e := json.Unmarshal(w.Body.Bytes(), out); e != nil {
			f.t.Fatalf("decode %v: %s", e, w.Body.String())
		}
	}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatalf("decode error %v: %s", e, w.Body.String())
	}
	return body.Error.Code
}

func TestSocialGroupLifecycleInvitesAndMembership(t *testing.T) {
	f := socialHTTP(t)
	playing := f.play(f.token)
	created := f.group("host-only")
	if created.Group.State != "lobby" || created.Group.Revision != "1" || !created.Group.Permissions.IsHost {
		t.Fatalf("unexpected new group %+v", created.Group)
	}
	// The group binds the host's device and follows the v1 session it plays; no
	// controller is involved.
	a := created.Group.Authority
	if a.DeviceID == "" || a.PlaybackID == nil || *a.PlaybackID != playing || a.State != "bound" {
		t.Fatalf("group did not bind the host device's session: %+v", a)
	}
	id := created.Group.ID
	// The group routes carry no playback authority: a v2-shaped body is refused.
	if w := f.as(f.token, "POST", "/v1/groups", map[string]any{"protocolVersion": social.Protocol, "name": "Old", "controllerId": "c"}); w.Code != 400 {
		t.Fatalf("a controller field was accepted %d %s", w.Code, w.Body.String())
	}
	// A heartbeat keeps the binding; stopping the session leaves the device
	// bound with nothing playing.
	var beat social.GroupResponse
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/heartbeat", nil), 200, &beat)
	if beat.Group.Authority.DeviceID != a.DeviceID || beat.Group.Authority.State != "bound" {
		t.Fatalf("heartbeat moved the binding: %+v", beat.Group.Authority)
	}
	f.callAs(f.token, "DELETE", "/v1/playback/sessions/"+playing, nil, nil, 204, nil)
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &beat)
	if beat.Group.Authority.State != "fresh" || beat.Group.Authority.PlaybackID != nil {
		t.Fatalf("an ended session is still the group's: %+v", beat.Group.Authority)
	}

	// A code is required: joining without one is not a route at all, and a code
	// that was never issued is indistinguishable from a wrong guess.
	w := f.as(f.member, "POST", "/v1/groups/join", social.JoinRequest{ProtocolVersion: social.Protocol, Code: "ZZZZZZZZ"})
	if w.Code != 404 || errorCode(t, w) != "invite_not_found" {
		t.Fatalf("unknown code %d %s", w.Code, w.Body.String())
	}
	code := f.invite(id)
	joined := f.join(f.member, code)
	if len(joined.Group.Members) != 2 || joined.Group.Permissions.IsHost || joined.Group.Permissions.CanControl {
		t.Fatalf("host-only policy gave a member control: %+v", joined.Group.Permissions)
	}
	// A member may not issue invitations or end the group.
	w = f.as(f.member, "POST", "/v1/groups/"+id+"/invites", social.InviteRequest{ProtocolVersion: social.Protocol})
	if w.Code != 403 || errorCode(t, w) != "host_required" {
		t.Fatalf("member issued an invitation %d %s", w.Code, w.Body.String())
	}
	w = f.as(f.member, "POST", "/v1/groups/"+id+"/end", social.EndRequest{ProtocolVersion: social.Protocol})
	if w.Code != 403 {
		t.Fatalf("member ended the group %d %s", w.Code, w.Body.String())
	}
	// A viewer who is not a member cannot read the group at all.
	w = f.as(f.outside, "GET", "/v1/groups/"+id, nil)
	if w.Code != 403 || errorCode(t, w) != "membership_revoked" {
		t.Fatalf("non-member read the group %d %s", w.Code, w.Body.String())
	}
	w = f.as(f.member, "POST", "/v1/groups/"+id+"/leave", nil)
	f.decode(w, 200, nil)
	var snapshot social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	if len(snapshot.Group.Members) != 1 {
		t.Fatalf("member did not leave: %+v", snapshot.Group.Members)
	}
	w = f.as(f.token, "POST", "/v1/groups/"+id+"/end", social.EndRequest{ProtocolVersion: social.Protocol, ExpectedRevision: snapshot.Group.GovernanceRevision})
	var ended social.GroupResponse
	f.decode(w, 200, &ended)
	if ended.Group.State != "ended" || ended.Group.EndedReason != "host-ended" {
		t.Fatalf("group did not end: %+v", ended.Group)
	}
	// An ended group refuses further commands.
	w = f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "after-end", ExpectedRevision: ended.Group.PlaybackRevision, Command: "pause"})
	if w.Code != 409 || errorCode(t, w) != "group_ended" {
		t.Fatalf("ended group accepted a command %d %s", w.Code, w.Body.String())
	}
}

func TestSocialTransportRevisionFenceAndIdempotency(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	load := func(revision string) social.TransportReceipt {
		t.Helper()
		w := f.as(f.token, "POST", "/v1/groups/"+id+"/queue", social.QueueRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "queue-" + revision, ExpectedRevision: "1", Operation: "append", ItemIDs: []string{f.item}})
		var queue social.QueueResponse
		f.decode(w, 200, &queue)
		var snapshot social.GroupResponse
		f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
		w = f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "load-" + revision, ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "set-queue-position", QueuePosition: intp(0)})
		var receipt social.TransportReceipt
		f.decode(w, 200, &receipt)
		return receipt
	}
	loaded := load("a")
	if loaded.Disposition != "accepted" || loaded.Timeline.ItemID != f.item {
		t.Fatalf("load did not select the entry: %+v", loaded)
	}
	// A stale expectedRevision is refused and reports the current one.
	w := f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "play-stale", ExpectedRevision: "1", Command: "play"})
	if w.Code != 409 || errorCode(t, w) != "revision_conflict" {
		t.Fatalf("stale revision accepted %d %s", w.Code, w.Body.String())
	}
	var conflict struct {
		Error struct {
			CurrentRevision string `json:"currentRevision"`
		} `json:"error"`
	}
	f.decode(w, 409, &conflict)
	if conflict.Error.CurrentRevision != loaded.PlaybackRevision {
		t.Fatalf("conflict did not name the current revision: %+v", conflict)
	}
	play := social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "play-1", ExpectedRevision: loaded.PlaybackRevision, Command: "play"}
	var first social.TransportReceipt
	firstAnswer := f.as(f.token, "POST", "/v1/groups/"+id+"/transport", play)
	// CD-01: the receipt is what the spec (and client-core) require, serverTime
	// included; before it, every command was applied but reported as failed.
	tl6AssertSpecResponse(t, "POST", "/v1/groups/{id}/transport", firstAnswer)
	f.decode(firstAnswer, 200, &first)
	if first.Disposition != "accepted" || first.Timeline.State != "playing" {
		t.Fatalf("play not applied: %+v", first)
	}
	// The same key with the same body replays the stored receipt and does not
	// advance the group a second time.
	var replay social.TransportReceipt
	replayAnswer := f.as(f.token, "POST", "/v1/groups/"+id+"/transport", play)
	tl6AssertSpecResponse(t, "POST", "/v1/groups/{id}/transport", replayAnswer)
	f.decode(replayAnswer, 200, &replay)
	if replay.Disposition != "duplicate" || replay.Revision != first.Revision {
		t.Fatalf("replay was applied again: %+v", replay)
	}
	// The same key with a different body is a conflict, never a silent overwrite.
	mutated := play
	mutated.Command = "pause"
	w = f.as(f.token, "POST", "/v1/groups/"+id+"/transport", mutated)
	if w.Code != 409 || errorCode(t, w) != "idempotency_key_reused" {
		t.Fatalf("reused key with a new body %d %s", w.Code, w.Body.String())
	}
	var snapshot social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	if snapshot.Group.Revision != first.Revision || snapshot.Group.Timeline.State != "playing" {
		t.Fatalf("group moved on a duplicate: %+v", snapshot.Group)
	}
}

func intp(v int) *int { return &v }

func TestSocialReadinessAggregation(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	if created.Group.Readiness.Aggregate != "ready" || created.Group.Readiness.MemberCount != 1 {
		t.Fatalf("host alone is not ready: %+v", created.Group.Readiness)
	}
	joined := f.join(f.member, f.invite(id))
	if joined.Group.Readiness.Aggregate != "buffering" || joined.Group.Readiness.Buffering != 1 || joined.Group.Readiness.Ready != 1 {
		t.Fatalf("a joining member did not hold the aggregate: %+v", joined.Group.Readiness)
	}
	var lagging social.ReadinessReceipt
	f.decode(f.as(f.member, "POST", "/v1/groups/"+id+"/readiness", social.ReadinessRequest{ProtocolVersion: social.Protocol, PlaybackRevision: joined.Group.PlaybackRevision, Readiness: "lagging", PositionUS: "1000000"}), 200, &lagging)
	if lagging.Readiness.Aggregate != "lagging" || lagging.Readiness.Lagging != 1 {
		t.Fatalf("lagging did not dominate: %+v", lagging.Readiness)
	}
	// A report answers with the room's counts, never its member list.
	w := f.as(f.member, "POST", "/v1/groups/"+id+"/readiness", social.ReadinessRequest{ProtocolVersion: social.Protocol, PlaybackRevision: joined.Group.PlaybackRevision, Readiness: "ready", PositionUS: "2000000"})
	if strings.Contains(w.Body.String(), `"members"`) {
		t.Fatalf("readiness report returned the member list: %s", w.Body)
	}
	var ready social.ReadinessReceipt
	f.decode(w, 200, &ready)
	if ready.Readiness.Aggregate != "ready" || ready.Readiness.Ready != 2 || ready.GroupID != id || ready.PlaybackRevision != joined.Group.PlaybackRevision {
		t.Fatalf("all-ready did not aggregate: %+v", ready)
	}
	var room social.GroupResponse
	f.decode(f.as(f.member, "GET", "/v1/groups/"+id, nil), 200, &room)
	for _, m := range room.Group.Members {
		if m.Role == "member" && m.PositionUS != "2000000" {
			t.Fatalf("member position not recorded: %+v", m)
		}
	}
	// A stale report is not a ready report: the aggregate falls back on its own.
	store := f.d.Social()
	store.Now = func() time.Time { return time.Now().Add(time.Minute) }
	principal, e := f.d.Identity.Authenticate(f.token)
	if e != nil {
		t.Fatal(e)
	}
	stale, e := store.ReadGroup(context.Background(), principal, id)
	if e != nil {
		t.Fatal(e)
	}
	if stale.Group.Readiness.Aggregate != "lagging" || stale.Group.Readiness.Stale != 2 {
		t.Fatalf("stale readiness still counted as ready: %+v", stale.Group.Readiness)
	}
}

func TestSocialQueueVisibilityPlaceholdersAndHostOverride(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	blocked := f.join(f.outside, f.invite(id))
	if len(blocked.Group.Members) != 2 {
		t.Fatalf("restricted viewer did not join: %+v", blocked.Group.Members)
	}
	var queue social.QueueResponse
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/queue", social.QueueRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "q1", ExpectedRevision: "1", Operation: "append", ItemIDs: []string{f.item}}), 200, &queue)
	if len(queue.Queue.Entries) != 1 || queue.Queue.Entries[0].ItemID == nil {
		t.Fatalf("host cannot see the entry it added: %+v", queue.Queue)
	}
	if queue.Queue.Eligibility == nil || queue.Queue.Eligibility.BlockedEntries != 1 || len(queue.Queue.Eligibility.Members) != 1 {
		t.Fatalf("host did not get the eligibility summary: %+v", queue.Queue.Eligibility)
	}
	// The member who cannot see the item gets an opaque placeholder: the entry
	// id and its position survive, the item id does not.
	var theirs social.QueueResponse
	f.decode(f.as(f.outside, "GET", "/v1/groups/"+id+"/queue", nil), 200, &theirs)
	if len(theirs.Queue.Entries) != 1 {
		t.Fatalf("placeholder did not preserve queue position: %+v", theirs.Queue)
	}
	entry := theirs.Queue.Entries[0]
	if !entry.Unavailable || entry.ItemID != nil || entry.EntryID != queue.Queue.Entries[0].EntryID || entry.Position != 0 {
		t.Fatalf("placeholder leaked or lost position: %+v", entry)
	}
	if theirs.Queue.Eligibility != nil {
		t.Fatal("a member was given the host eligibility summary")
	}
	// The group refuses to start an entry a member cannot see.
	var snapshot social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	start := social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "start-blocked", ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "set-queue-position", QueuePosition: intp(0)}
	w := f.as(f.token, "POST", "/v1/groups/"+id+"/transport", start)
	if w.Code != 409 || errorCode(t, w) != "media_no_longer_accessible" {
		t.Fatalf("group started media a member cannot see %d %s", w.Code, w.Body.String())
	}
	// A member may not override; only the host may, and the override is recorded.
	memberOverride := social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "member-override", ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "set-queue-position", QueuePosition: intp(0), AllowUnavailable: true}
	w = f.as(f.outside, "POST", "/v1/groups/"+id+"/transport", memberOverride)
	if w.Code != 403 {
		t.Fatalf("member overrode visibility %d %s", w.Code, w.Body.String())
	}
	override := start
	override.IdempotencyKey, override.AllowUnavailable = "start-override", true
	var receipt social.TransportReceipt
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/transport", override), 200, &receipt)
	if receipt.Override == nil || receipt.Override.Reason != "host-override" || len(receipt.Override.BlockedMembers) != 1 {
		t.Fatalf("override was not recorded: %+v", receipt.Override)
	}
	if receipt.Timeline.ItemID != f.item {
		t.Fatalf("override did not start the entry: %+v", receipt.Timeline)
	}
}

// A member who has left is no longer in the room: the group, its queue and its
// events stop being readable to them the moment they leave, exactly as they are
// for somebody who never joined.
func TestSocialDepartedMemberCannotReadTheGroup(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	f.join(f.member, f.invite(id))
	f.decode(f.as(f.member, "GET", "/v1/groups/"+id, nil), 200, nil)
	f.decode(f.as(f.member, "GET", "/v1/groups/"+id+"/queue", nil), 200, nil)
	f.decode(f.as(f.member, "POST", "/v1/groups/"+id+"/leave", nil), 200, nil)
	for _, path := range []string{"/v1/groups/" + id, "/v1/groups/" + id + "/queue", "/v1/groups/" + id + "/events"} {
		w := f.as(f.member, "GET", path, nil)
		if w.Code != 403 || errorCode(t, w) != "membership_revoked" {
			t.Fatalf("a departed member read %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Joining again with a valid code is what readmits them.
	f.join(f.member, f.invite(id))
	f.decode(f.as(f.member, "GET", "/v1/groups/"+id, nil), 200, nil)
}

// The device a group is bound to, and the session that device plays, identify
// the host's own equipment. Other members are told whether the group has a live
// authority, not which device or session it is.
func TestSocialAuthorityIdentifiersAreTheHostsOnly(t *testing.T) {
	f := socialHTTP(t)
	playing := f.play(f.token)
	created := f.group("host-only")
	id := created.Group.ID
	joined := f.join(f.member, f.invite(id))
	if a := joined.Group.Authority; a.DeviceID != "" || a.PlaybackID != nil || a.State != "bound" {
		t.Fatalf("a member was given the host's device or session: %+v", a)
	}
	var theirs social.GroupResponse
	w := f.as(f.member, "GET", "/v1/groups/"+id, nil)
	f.decode(w, 200, &theirs)
	if a := theirs.Group.Authority; a.DeviceID != "" || a.PlaybackID != nil || a.State != "bound" || strings.Contains(w.Body.String(), playing) || strings.Contains(w.Body.String(), created.Group.Authority.DeviceID) {
		t.Fatalf("a member read the host's device or session: %s", w.Body.String())
	}
	var mine social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &mine)
	if a := mine.Group.Authority; a.DeviceID == "" || a.PlaybackID == nil || *a.PlaybackID != playing || a.State != "bound" {
		t.Fatalf("the host lost its own authority: %+v", a)
	}
}

func TestSocialAnyoneAuthorityPolicy(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("anyone")
	id := created.Group.ID
	joined := f.join(f.member, f.invite(id))
	if !joined.Group.Permissions.CanControl || joined.Group.Permissions.IsHost {
		t.Fatalf("anyone policy did not widen control: %+v", joined.Group.Permissions)
	}
	w := f.as(f.member, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "member-pause", ExpectedRevision: joined.Group.PlaybackRevision, Command: "pause"})
	f.decode(w, 200, nil)
	// Widening transport never widens governance.
	w = f.as(f.member, "POST", "/v1/groups/"+id+"/invites", social.InviteRequest{ProtocolVersion: social.Protocol})
	if w.Code != 403 {
		t.Fatalf("anyone policy widened invitations %d %s", w.Code, w.Body.String())
	}
}

// socialStore returns a store over the fixture's database with an injectable
// clock, so timeline boundaries are tested without sleeping.
func (f *socialFixture) socialStore(now *time.Time) (*social.Store, identity.Principal) {
	f.t.Helper()
	store := f.d.Social()
	store.Now = func() time.Time { return *now }
	p, e := f.d.Identity.Authenticate(f.token)
	if e != nil {
		f.t.Fatal(e)
	}
	return store, p
}

func TestSocialHostReconnectTimeline(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/queue", social.QueueRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "q", ExpectedRevision: "1", Operation: "append", ItemIDs: []string{f.item}}), 200, nil)
	var snapshot social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "load", ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "set-queue-position", QueuePosition: intp(0)}), 200, &snapshot)
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	var playing social.TransportReceipt
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "play", ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "play"}), 200, &playing)
	if playing.Timeline.State != "playing" {
		t.Fatalf("group is not playing: %+v", playing.Timeline)
	}

	now := time.Now()
	store, principal := f.socialStore(&now)
	ctx := context.Background()

	// Inside the grace window the host is still connected.
	now = now.Add(20 * time.Second)
	state, e := store.ReadGroup(ctx, principal, id)
	if e != nil {
		t.Fatal(e)
	}
	if state.Group.State != "playing" || state.Group.Host.Presence != "connected" {
		t.Fatalf("grace window ended early: %+v", state.Group)
	}
	// Past the grace window the group is reconnecting but still playing: the
	// contract lets playback continue until the pause boundary.
	now = now.Add(40 * time.Second)
	if e = store.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if state, e = store.ReadGroup(ctx, principal, id); e != nil {
		t.Fatal(e)
	}
	if state.Group.State != "host-reconnecting-playing" || state.Group.Host.Presence != "reconnecting" || state.Group.Timeline.State != "playing" {
		t.Fatalf("group did not enter host-reconnecting: %+v", state.Group)
	}
	if state.Group.Host.PauseAt == nil || state.Group.Host.EndAt == nil {
		t.Fatal("reconnecting group did not publish its pause and end deadlines")
	}
	// A reconnect before the pause boundary restores the prior playing state.
	if _, e = store.Heartbeat(ctx, principal, "", id); e != nil {
		t.Fatal(e)
	}
	if state, e = store.ReadGroup(ctx, principal, id); e != nil {
		t.Fatal(e)
	}
	if state.Group.State != "playing" || state.Group.ReconnectGeneration != "2" {
		t.Fatalf("reconnect did not restore playing: %+v", state.Group)
	}
	// Two minutes of silence pauses the group atomically and anchors the clock.
	now = now.Add(social.HostPauseSeconds*time.Second + time.Second)
	if e = store.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if state, e = store.ReadGroup(ctx, principal, id); e != nil {
		t.Fatal(e)
	}
	if state.Group.State != "host-reconnecting-paused" || state.Group.Timeline.State != "paused" {
		t.Fatalf("pause boundary did not fire: %+v", state.Group)
	}
	if state.Group.Timeline.AnchorPositionUS == "0" {
		t.Fatal("pause boundary discarded the extrapolated position")
	}
	// A reconnect after the pause boundary returns to paused: the host must send
	// an explicit Play, which is exactly what `resumeState` records.
	if _, e = store.Heartbeat(ctx, principal, "", id); e != nil {
		t.Fatal(e)
	}
	if state, e = store.ReadGroup(ctx, principal, id); e != nil {
		t.Fatal(e)
	}
	if state.Group.State != "paused" {
		t.Fatalf("late reconnect resumed playback by itself: %+v", state.Group)
	}
	// Ten minutes of silence ends the group.
	now = now.Add(social.HostEndSeconds*time.Second + time.Second)
	if e = store.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if state, e = store.ReadGroup(ctx, principal, id); e != nil {
		t.Fatal(e)
	}
	if state.Group.State != "ended" || state.Group.EndedReason != "host-unavailable" {
		t.Fatalf("end boundary did not fire: %+v", state.Group)
	}
}

// A group with nothing loaded is a lobby, and stays one: neither a host who
// drops out and comes back nor a Stop turns it into a pause or a ready room of
// nothing, so the first title it loads still goes through preparing.
func TestSocialEmptyLobbyStaysALobby(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	var receipt social.TransportReceipt
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "stop-nothing", ExpectedRevision: created.Group.PlaybackRevision, Command: "stop"}), 200, &receipt)
	var snapshot social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	if snapshot.Group.State != "lobby" || snapshot.Group.Timeline.State != "idle" {
		t.Fatalf("stopping an empty lobby left it %q (timeline %q)", snapshot.Group.State, snapshot.Group.Timeline.State)
	}

	now := time.Now()
	store, principal := f.socialStore(&now)
	ctx := context.Background()
	for _, silence := range []time.Duration{(social.HostGraceSeconds + 10) * time.Second, (social.HostPauseSeconds + 10) * time.Second} {
		now = now.Add(silence)
		if e := store.Sweep(ctx); e != nil {
			t.Fatal(e)
		}
		state, e := store.ReadGroup(ctx, principal, id)
		if e != nil {
			t.Fatal(e)
		}
		if state.Group.State != "host-reconnecting-paused" {
			t.Fatalf("a silent host did not put the lobby in reconnecting: %q", state.Group.State)
		}
		if _, e = store.Heartbeat(ctx, principal, "", id); e != nil {
			t.Fatal(e)
		}
		if state, e = store.ReadGroup(ctx, principal, id); e != nil {
			t.Fatal(e)
		}
		if state.Group.State != "lobby" || state.Group.Timeline.State != "idle" {
			t.Fatalf("a host returning after %s left the empty lobby %q (timeline %q)", silence, state.Group.State, state.Group.Timeline.State)
		}
	}
	// The first title therefore still prepares.
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	var queue social.QueueResponse
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/queue", social.QueueRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "q", ExpectedRevision: snapshot.Group.QueueRevision, Operation: "append", ItemIDs: []string{f.item}}), 200, &queue)
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "first-load", ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "load", EntryID: queue.Queue.Entries[0].EntryID}), 200, &receipt)
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	if snapshot.Group.State != "waiting" {
		t.Fatalf("the first title did not prepare: %q", snapshot.Group.State)
	}
}

// Load names one queue entry. An id that is not in the queue is refused; it
// must never be answered by reloading whatever is current from the start.
func TestSocialLoadRefusesAnUnknownEntry(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	var queue social.QueueResponse
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/queue", social.QueueRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "q", ExpectedRevision: "1", Operation: "append", ItemIDs: []string{f.item}}), 200, &queue)
	var snapshot social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	var receipt social.TransportReceipt
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "load", ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "load", EntryID: queue.Queue.Entries[0].EntryID, PositionUS: "90000000"}), 200, &receipt)
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "seek", ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "seek", PositionUS: "120000000"}), 200, &receipt)
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	w := f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "load-missing", ExpectedRevision: snapshot.Group.PlaybackRevision, Command: "load", EntryID: "ent_not_in_this_queue"})
	if w.Code != 409 || errorCode(t, w) != "target_unavailable" {
		t.Fatalf("an unknown entry was loaded: %d %s", w.Code, w.Body.String())
	}
	var after social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &after)
	if after.Group.Timeline.AnchorPositionUS != "120000000" || after.Group.Revision != snapshot.Group.Revision {
		t.Fatalf("a refused load moved the group: %+v", after.Group.Timeline)
	}
}

func TestSocialHostTransfer(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	joined := f.join(f.member, f.invite(id))
	var memberID string
	for _, m := range joined.Group.Members {
		if m.Role == "member" {
			memberID = m.ID
		}
	}
	var snapshot social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &snapshot)
	// A member cannot seize authority from a connected host.
	w := f.as(f.member, "POST", "/v1/groups/"+id+"/host-transfer", social.TransferRequest{ProtocolVersion: social.Protocol, MemberID: memberID, ExpectedRevision: snapshot.Group.GovernanceRevision})
	if w.Code != 403 {
		t.Fatalf("member seized host authority %d %s", w.Code, w.Body.String())
	}
	f.decode(f.as(f.member, "POST", "/v1/groups/"+id+"/readiness", social.ReadinessRequest{ProtocolVersion: social.Protocol, PlaybackRevision: joined.Group.PlaybackRevision, Readiness: "ready", PositionUS: "0"}), 200, nil)
	// A stale revision refuses the transfer.
	w = f.as(f.token, "POST", "/v1/groups/"+id+"/host-transfer", social.TransferRequest{ProtocolVersion: social.Protocol, MemberID: memberID, ExpectedRevision: "0"})
	if w.Code != 409 || errorCode(t, w) != "revision_conflict" {
		t.Fatalf("stale transfer accepted %d %s", w.Code, w.Body.String())
	}
	var moved social.GroupResponse
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/host-transfer", social.TransferRequest{ProtocolVersion: social.Protocol, MemberID: memberID, ExpectedRevision: snapshot.Group.GovernanceRevision}), 200, &moved)
	if moved.Group.HostMemberID != memberID || moved.Group.Permissions.IsHost || moved.Group.GovernanceRevision == snapshot.Group.GovernanceRevision {
		t.Fatalf("host authority did not move: %+v", moved.Group)
	}
	var theirs social.GroupResponse
	f.decode(f.as(f.member, "GET", "/v1/groups/"+id, nil), 200, &theirs)
	if !theirs.Group.Permissions.IsHost || !theirs.Group.Permissions.CanControl {
		t.Fatalf("new host has no authority: %+v", theirs.Group.Permissions)
	}
	// The old host is now an ordinary member.
	w = f.as(f.token, "POST", "/v1/groups/"+id+"/invites", social.InviteRequest{ProtocolVersion: social.Protocol})
	if w.Code != 403 {
		t.Fatalf("old host kept governance %d %s", w.Code, w.Body.String())
	}
	// Until the incoming host checks in, the group follows no device: the old
	// host's device is never the new host's authority.
	if theirs.Group.Authority.DeviceID != "" || theirs.Group.Authority.PlaybackID != nil {
		t.Fatalf("transfer kept the old host's device: %+v", theirs.Group.Authority)
	}
	// The new host's heartbeat binds its own device and the session it plays.
	playing := f.play(f.member)
	var rebound social.GroupResponse
	f.decode(f.as(f.member, "POST", "/v1/groups/"+id+"/heartbeat", nil), 200, &rebound)
	if rebound.Group.Authority.DeviceID == "" || rebound.Group.Authority.DeviceID == created.Group.Authority.DeviceID || rebound.Group.Authority.PlaybackID == nil || *rebound.Group.Authority.PlaybackID != playing {
		t.Fatalf("new host did not bind its device: %+v", rebound.Group.Authority)
	}
	// The old host's heartbeat is a member's and moves nothing.
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/heartbeat", nil), 200, nil)
	var after social.GroupResponse
	f.decode(f.as(f.member, "GET", "/v1/groups/"+id, nil), 200, &after)
	if after.Group.Authority.DeviceID != rebound.Group.Authority.DeviceID {
		t.Fatalf("a member's heartbeat moved the binding: %+v", after.Group.Authority)
	}
	// When the host leaves, the group ends.
	f.decode(f.as(f.member, "POST", "/v1/groups/"+id+"/leave", nil), 200, nil)
	var ended social.GroupResponse
	f.decode(f.as(f.token, "GET", "/v1/groups/"+id, nil), 200, &ended)
	if ended.Group.State != "ended" || ended.Group.EndedReason != "host-left" {
		t.Fatalf("host leaving did not end the group: %+v", ended.Group)
	}
}

func TestSocialInviteExpiryAndRevocation(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	now := time.Now()
	store, principal := f.socialStore(&now)
	ctx := context.Background()
	issued, e := store.CreateInvite(ctx, principal, id, social.InviteRequest{ProtocolVersion: social.Protocol})
	if e != nil {
		t.Fatal(e)
	}
	guest, e := f.d.Identity.Authenticate(f.member)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(social.InviteTTLSeconds*time.Second + time.Second)
	_, e = store.Join(ctx, guest, social.JoinRequest{ProtocolVersion: social.Protocol, Code: issued.Invite.Code})
	var fault *social.Fault
	if !asFault(e, &fault) || fault.Code != "invite_expired" {
		t.Fatalf("expired invitation accepted: %v", e)
	}
	// A fresh invitation still works, and revoking it closes it immediately.
	fresh, e := store.CreateInvite(ctx, principal, id, social.InviteRequest{ProtocolVersion: social.Protocol})
	if e != nil {
		t.Fatal(e)
	}
	if e = store.RevokeInvite(ctx, principal, id, fresh.Invite.ID); e != nil {
		t.Fatal(e)
	}
	_, e = store.Join(ctx, guest, social.JoinRequest{ProtocolVersion: social.Protocol, Code: fresh.Invite.Code})
	if !asFault(e, &fault) || fault.Code != "invite_revoked" {
		t.Fatalf("revoked invitation accepted: %v", e)
	}
	// A single-use invitation is consumed exactly once.
	once, e := store.CreateInvite(ctx, principal, id, social.InviteRequest{ProtocolVersion: social.Protocol, MaxUses: 1})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.Join(ctx, guest, social.JoinRequest{ProtocolVersion: social.Protocol, Code: once.Invite.Code}); e != nil {
		t.Fatal(e)
	}
	outside, e := f.d.Identity.Authenticate(f.outside)
	if e != nil {
		t.Fatal(e)
	}
	_, e = store.Join(ctx, outside, social.JoinRequest{ProtocolVersion: social.Protocol, Code: once.Invite.Code})
	if !asFault(e, &fault) || fault.Code != "invite_consumed" {
		t.Fatalf("single-use invitation reused: %v", e)
	}
	// Issuance is rate limited per group.
	for i := 0; i < social.InvitesPerGroupHour+2; i++ {
		_, e = store.CreateInvite(ctx, principal, id, social.InviteRequest{ProtocolVersion: social.Protocol})
	}
	if !asFault(e, &fault) || fault.Code != "rate_limited" {
		t.Fatalf("invitation issuance is not rate limited: %v", e)
	}
}

func asFault(e error, out **social.Fault) bool {
	f, ok := e.(*social.Fault)
	if ok {
		*out = f
	}
	return ok
}

func TestSocialEventStreamResumeAndHeartbeat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	f.join(f.member, f.invite(id))
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/queue", social.QueueRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "q", ExpectedRevision: "1", Operation: "append", ItemIDs: []string{f.item}}), 200, nil)

	stream := func(token, lastEventID string, wait time.Duration) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()
		r := httptest.NewRequest("GET", "/v1/groups/"+id+"/events", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Accept", "text/event-stream")
		if lastEventID != "" {
			r.Header.Set("Last-Event-ID", lastEventID)
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
			t.Fatalf("stream content type %q: %s", got, w.Body.String())
		}
		return w.Body.String()
	}
	// A fresh stream opens with one live per-member snapshot.
	body := stream(f.token, "", 1200*time.Millisecond)
	if !strings.Contains(body, "event: "+social.EventSnapshot) || !strings.Contains(body, "\"hostAuthority\":\"host-only\"") {
		t.Fatalf("stream did not open with a snapshot: %q", body)
	}
	// Resuming replays only what came after the ordinal, with its `id:` intact.
	body = stream(f.token, "1", 1200*time.Millisecond)
	if strings.Contains(body, "event: "+social.EventSnapshot) {
		t.Fatalf("resume resent a snapshot: %q", body)
	}
	if !strings.Contains(body, "id: 2\n") || !strings.Contains(body, "event: "+social.EventQueue) {
		t.Fatalf("resume did not replay the ledger: %q", body)
	}
	// A resume point that has fallen out of retention gets one gap frame and a
	// fresh snapshot rather than a silent partial replay.
	if _, e := f.d.DB.Exec(`DELETE FROM social_group_events WHERE group_id=? AND ordinal<=2`, id); e != nil {
		t.Fatal(e)
	}
	body = stream(f.token, "1", 1200*time.Millisecond)
	if !strings.Contains(body, "event: "+social.EventResumeGap) || !strings.Contains(body, "event: "+social.EventSnapshot) {
		t.Fatalf("retention gap was not reported: %q", body)
	}
	// A non-member is refused before a single stream byte is written.
	r := httptest.NewRequest("GET", "/v1/groups/"+id+"/events", nil)
	r.Header.Set("Authorization", "Bearer "+f.outside)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 403 || strings.Contains(w.Body.String(), "event:") {
		t.Fatalf("non-member opened the stream %d %s", w.Code, w.Body.String())
	}
}

func TestSocialHandoffTwoPhaseCommitAndRollback(t *testing.T) {
	f := socialHTTP(t)
	principal, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	changes, unsubscribe := social.SubscribeReceiverEvents(f.d.DB, principal)
	defer unsubscribe()
	<-changes
	assertArrival := func(kind, receiverID string) {
		t.Helper()
		select {
		case event := <-changes:
			if event.Kind != kind || event.ReceiverID != receiverID {
				t.Fatal(event)
			}
		case <-time.After(time.Second):
			t.Fatal("missing receiver event", kind)
		}
	}
	receiver := social.ReceiverRequest{ProtocolVersion: social.Protocol, DeviceID: "tv-1", DisplayName: "Living room", Platform: "androidtv", KeyFingerprint: strings.Repeat("A", 43)}
	var registered social.ReceiverResponse
	f.decode(f.as(f.token, "POST", "/v1/receivers", receiver), 201, &registered)
	if registered.Receiver.Kind != "portico" || registered.Receiver.AuthorizationRevision != "1" {
		t.Fatalf("receiver registration: %+v", registered.Receiver)
	}
	var directory social.ReceiverListResponse
	f.decode(f.as(f.token, "GET", "/v1/receivers", nil), 200, &directory)
	if len(directory.Receivers) != 1 {
		t.Fatalf("receiver directory: %+v", directory.Receivers)
	}
	// Another viewer's directory never lists it.
	var theirs social.ReceiverListResponse
	f.decode(f.as(f.member, "GET", "/v1/receivers", nil), 200, &theirs)
	if len(theirs.Receivers) != 0 {
		t.Fatalf("receiver leaked across viewers: %+v", theirs.Receivers)
	}
	receiverID := registered.Receiver.ID

	var grant social.GrantResponse
	f.decode(f.as(f.token, "POST", "/v1/receivers/"+receiverID+"/grants", social.GrantRequest{ProtocolVersion: social.Protocol, ControllerDeviceID: "phone-1", ControllerDisplayName: "Phone"}), 201, &grant)
	assertArrival("grant", receiverID)
	if grant.Grant.State != "pending" {
		t.Fatalf("per-device policy auto-accepted: %+v", grant.Grant)
	}
	// The television learns of the request from its inbox, and nobody else's inbox shows it.
	var inbox social.ReceiverInbox
	f.decode(f.as(f.token, "GET", "/v1/receivers/"+receiverID+"/inbox", nil), 200, &inbox)
	if len(inbox.Grants) != 1 || inbox.Grants[0].ID != grant.Grant.ID || len(inbox.Handoffs) != 0 {
		t.Fatalf("pending grant missing from the inbox: %+v", inbox)
	}
	if w := f.as(f.member, "GET", "/v1/receivers/"+receiverID+"/inbox", nil); w.Code != 404 {
		t.Fatalf("another viewer read this receiver's inbox: %d", w.Code)
	}
	source := f.source("source")
	// A handoff cannot be prepared against a grant the receiver has not accepted.
	prepare := social.HandoffRequest{ProtocolVersion: social.Protocol, RequestID: "handoff-1", ReceiverID: receiverID, GrantID: grant.Grant.ID, SourcePlaybackID: source}
	w := f.as(f.token, "POST", "/v1/handoffs", prepare)
	if w.Code != 403 || errorCode(t, w) != "grant_not_accepted" {
		t.Fatalf("handoff prepared without authorization %d %s", w.Code, w.Body.String())
	}
	f.decode(f.as(f.token, "POST", "/v1/receivers/"+receiverID+"/grants/"+grant.Grant.ID+"/decision", social.GrantDecision{ProtocolVersion: social.Protocol, Decision: "accept"}), 200, &grant)
	if grant.Grant.State != "accepted" {
		t.Fatalf("grant not accepted: %+v", grant.Grant)
	}
	if _, e := f.d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES(?,13,0)`, f.profile); e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, f.d.DB)
	c.Attributes(c.ID(f.item), "contentRating", "R")
	w = f.as(f.token, "POST", "/v1/handoffs", prepare)
	if w.Code != 404 || errorCode(t, w) != "not_found" {
		t.Fatalf("restricted transfer %d %s", w.Code, w.Body.String())
	}
	if _, e := f.d.DB.Exec(`DELETE FROM profile_restrictions WHERE profile_id=?`, f.profile); e != nil {
		t.Fatal(e)
	}
	var prepared social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs", prepare), 201, &prepared)
	assertArrival("handoff", receiverID)
	if prepared.Handoff.State != "prepared" || prepared.Handoff.Outcome != "waiting" || prepared.Handoff.SourceRetired {
		t.Fatalf("prepare did not retain the source: %+v", prepared.Handoff)
	}
	// A decided grant leaves the inbox; the open handoff arrives in it with what to play.
	f.decode(f.as(f.token, "GET", "/v1/receivers/"+receiverID+"/inbox", nil), 200, &inbox)
	if len(inbox.Grants) != 0 || len(inbox.Handoffs) != 1 || inbox.Handoffs[0].ID != prepared.Handoff.ID || inbox.Handoffs[0].ItemID == "" {
		t.Fatalf("open handoff missing from the inbox: %+v", inbox)
	}
	// A retried prepare is the same handoff.
	var again social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs", prepare), 201, &again)
	if again.Handoff.ID != prepared.Handoff.ID {
		t.Fatal("retried prepare created a second handoff")
	}
	// Commit before readiness is refused; the source is still live.
	w = f.as(f.token, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/commit", social.HandoffCommit{ProtocolVersion: social.Protocol, ExpectedRevision: prepared.Handoff.Revision})
	if w.Code != 409 || errorCode(t, w) != "handoff_not_ready" {
		t.Fatalf("commit accepted before readiness %d %s", w.Code, w.Body.String())
	}
	if state := sessionState(t, f, source); state != "active" {
		t.Fatalf("refused commit disturbed the source: %s", state)
	}
	// Anything other than a proven `playing` is not readiness.
	w = f.as(f.token, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/readiness", social.HandoffReadiness{ProtocolVersion: social.Protocol, Readiness: "buffered", ReceiverPlaybackID: "receiver-1", PositionUS: "5000000"})
	if w.Code != 400 {
		t.Fatalf("weak readiness accepted %d %s", w.Code, w.Body.String())
	}
	receiverPlayback := f.play(f.token)
	var ready social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/readiness", social.HandoffReadiness{ProtocolVersion: social.Protocol, Readiness: "playing", ReceiverPlaybackID: receiverPlayback, PositionUS: "5000000", ExpectedRevision: prepared.Handoff.Revision}), 200, &ready)
	if ready.Handoff.Outcome != "pending" || ready.Handoff.State != "prepared" || ready.Handoff.SourceRetired {
		t.Fatalf("readiness was treated as acceptance: %+v", ready.Handoff)
	}
	if state := sessionState(t, f, source); state != "active" {
		t.Fatalf("readiness retired the source: %s", state)
	}
	// A stale handoff revision refuses the commit.
	w = f.as(f.token, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/commit", social.HandoffCommit{ProtocolVersion: social.Protocol, ExpectedRevision: prepared.Handoff.Revision})
	if w.Code != 409 || errorCode(t, w) != "revision_conflict" {
		t.Fatalf("stale commit accepted %d %s", w.Code, w.Body.String())
	}
	var committed social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/commit", social.HandoffCommit{ProtocolVersion: social.Protocol, ExpectedRevision: ready.Handoff.Revision}), 200, &committed)
	if committed.Handoff.State != "committed" || committed.Handoff.Outcome != "accepted" || !committed.Handoff.SourceRetired {
		t.Fatalf("commit did not complete: %+v", committed.Handoff)
	}
	if committed.Handoff.CommittedPositionUS != "5000000" {
		t.Fatalf("receiver did not begin at the ready position: %+v", committed.Handoff)
	}
	if state := sessionState(t, f, source); state != "transferred" {
		t.Fatalf("commit did not end the source: %s", state)
	}
	// A retried commit is the same commit.
	var recommit social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/commit", social.HandoffCommit{ProtocolVersion: social.Protocol, ExpectedRevision: committed.Handoff.Revision}), 200, &recommit)
	if recommit.Handoff.Revision != committed.Handoff.Revision {
		t.Fatalf("retried commit advanced the handoff: %+v", recommit.Handoff)
	}

	// Rollback leaves the source exactly where it was.
	second := f.source("source-2")
	rollbackPrepare := social.HandoffRequest{ProtocolVersion: social.Protocol, RequestID: "handoff-2", ReceiverID: receiverID, GrantID: grant.Grant.ID, SourcePlaybackID: second}
	var open social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs", rollbackPrepare), 201, &open)
	var rolled social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs/"+open.Handoff.ID+"/rollback", social.HandoffRollback{ProtocolVersion: social.Protocol, ExpectedRevision: open.Handoff.Revision, Reason: "receiver-load-failed"}), 200, &rolled)
	if rolled.Handoff.State != "rolled_back" || rolled.Handoff.Outcome != "rejected" || rolled.Handoff.Reason != "receiver-load-failed" {
		t.Fatalf("rollback did not settle: %+v", rolled.Handoff)
	}
	if state := sessionState(t, f, second); state != "active" {
		t.Fatalf("rollback disturbed the retained source: %s", state)
	}
	// Committing a rolled-back handoff is refused.
	w = f.as(f.token, "POST", "/v1/handoffs/"+open.Handoff.ID+"/commit", social.HandoffCommit{ProtocolVersion: social.Protocol, ExpectedRevision: rolled.Handoff.Revision})
	if w.Code != 409 || errorCode(t, w) != "handoff_state_conflict" {
		t.Fatalf("settled handoff committed %d %s", w.Code, w.Body.String())
	}

	// Rotating the receiver key revokes every grant issued against the old one.
	rotated := receiver
	rotated.KeyFingerprint = strings.Repeat("B", 43)
	var rerolled social.ReceiverResponse
	f.decode(f.as(f.token, "POST", "/v1/receivers", rotated), 201, &rerolled)
	if rerolled.Receiver.AuthorizationRevision != "2" {
		t.Fatalf("key rotation did not advance the authorization revision: %+v", rerolled.Receiver)
	}
	var grants social.GrantListResponse
	f.decode(f.as(f.token, "GET", "/v1/receivers/"+receiverID+"/grants", nil), 200, &grants)
	for _, g := range grants.Grants {
		if g.State == "accepted" {
			t.Fatalf("a grant survived key rotation: %+v", g)
		}
	}
}

// sessionState is "active" for a live v1 session, else why it ended.
func sessionState(t *testing.T, f *socialFixture, id string) string {
	t.Helper()
	var ended int64
	var reason string
	if e := f.d.DB.QueryRow(`SELECT ended_ms,end_reason FROM playback_v1_sessions WHERE id=?`, id).Scan(&ended, &reason); e != nil {
		t.Fatal(e)
	}
	if ended == 0 {
		return "active"
	}
	return reason
}

// A receiver that cannot keep the event stream waits on the inbox itself: it is
// answered at once when something is waiting, as soon as something arrives, and
// empty when the wait elapses.
func TestSocialReceiverInboxWait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := socialHTTP(t)
	receiver := social.ReceiverRequest{ProtocolVersion: social.Protocol, DeviceID: "tv-wait", DisplayName: "Den", Platform: "roku", KeyFingerprint: strings.Repeat("B", 43)}
	var registered social.ReceiverResponse
	f.decode(f.as(f.token, "POST", "/v1/receivers", receiver), 201, &registered)
	id := registered.Receiver.ID
	path := "/v1/receivers/" + id + "/inbox/wait"

	// Nothing waiting: the read is held for the wait it names, then answers empty.
	started := time.Now()
	var inbox social.ReceiverInbox
	f.decode(f.as(f.token, "GET", path+"?waitMs=1000", nil), 200, &inbox)
	if held := time.Since(started); held < 900*time.Millisecond || held > 5*time.Second {
		t.Fatalf("an empty inbox was held for %s, not about a second", held)
	}
	if len(inbox.Grants) != 0 || len(inbox.Handoffs) != 0 || inbox.ReceiverID != id {
		t.Fatalf("an elapsed wait did not answer the empty inbox: %+v", inbox)
	}
	// A wait of nothing is the plain read.
	started = time.Now()
	f.decode(f.as(f.token, "GET", path+"?waitMs=0", nil), 200, &inbox)
	if held := time.Since(started); held > 2*time.Second {
		t.Fatalf("waitMs=0 was held for %s", held)
	}
	for _, bad := range []string{"-1", "55001", "soon"} {
		if w := f.as(f.token, "GET", path+"?waitMs="+bad, nil); w.Code != 400 || errorCode(t, w) != "invalid_request" {
			t.Fatalf("waitMs=%s answered %d %s", bad, w.Code, w.Body.String())
		}
	}
	// Another viewer cannot wait on this receiver.
	if w := f.as(f.member, "GET", path+"?waitMs=1000", nil); w.Code != 404 {
		t.Fatalf("another viewer waited on this receiver's inbox: %d", w.Code)
	}

	// A request that arrives during the wait ends it.
	type answer struct {
		inbox social.ReceiverInbox
		code  int
		held  time.Duration
	}
	done := make(chan answer, 1)
	go func() {
		begun := time.Now()
		w := f.as(f.token, "GET", path+"?waitMs=20000", nil)
		var got social.ReceiverInbox
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		done <- answer{got, w.Code, time.Since(begun)}
	}()
	time.Sleep(300 * time.Millisecond)
	var grant social.GrantResponse
	f.decode(f.as(f.token, "POST", "/v1/receivers/"+id+"/grants", social.GrantRequest{ProtocolVersion: social.Protocol, ControllerDeviceID: "phone-wait", ControllerDisplayName: "Phone"}), 201, &grant)
	select {
	case got := <-done:
		if got.code != 200 || len(got.inbox.Grants) != 1 || got.inbox.Grants[0].ID != grant.Grant.ID {
			t.Fatalf("the waiting read did not answer the new request: %d %+v", got.code, got.inbox)
		}
		if got.held > 5*time.Second {
			t.Fatalf("the waiting read took %s to notice the request", got.held)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting read did not end when a request arrived")
	}

	// Something already waiting answers at once, however long the wait asked for.
	started = time.Now()
	f.decode(f.as(f.token, "GET", path+"?waitMs=20000", nil), 200, &inbox)
	if held := time.Since(started); held > 2*time.Second || len(inbox.Grants) != 1 {
		t.Fatalf("a waiting grant was held back for %s: %+v", held, inbox)
	}
}

func TestSocialHandoffExpiryRollsBack(t *testing.T) {
	f := socialHTTP(t)
	var registered social.ReceiverResponse
	f.decode(f.as(f.token, "POST", "/v1/receivers", social.ReceiverRequest{ProtocolVersion: social.Protocol, DeviceID: "tv-x", DisplayName: "Bedroom", KeyFingerprint: strings.Repeat("C", 43), GrantPolicy: "open"}), 201, &registered)
	var grant social.GrantResponse
	f.decode(f.as(f.token, "POST", "/v1/receivers/"+registered.Receiver.ID+"/grants", social.GrantRequest{ProtocolVersion: social.Protocol, ControllerDeviceID: "phone-x"}), 201, &grant)
	if grant.Grant.State != "accepted" {
		t.Fatalf("open policy did not accept immediately: %+v", grant.Grant)
	}
	source := f.source("expiring")
	now := time.Now()
	store, principal := f.socialStore(&now)
	ctx := context.Background()
	prepared, e := store.PrepareHandoff(ctx, principal, social.HandoffRequest{ProtocolVersion: social.Protocol, RequestID: "expire-1", ReceiverID: registered.Receiver.ID, GrantID: grant.Grant.ID, SourcePlaybackID: source})
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(social.HandoffTTLSeconds*time.Second + time.Second)
	read, e := store.ReadHandoff(ctx, principal, prepared.Handoff.ID)
	if e != nil {
		t.Fatal(e)
	}
	if read.Handoff.State != "expired" || read.Handoff.Reason != "handoff-timeout" {
		t.Fatalf("stale handoff was not expired: %+v", read.Handoff)
	}
	if state := sessionState(t, f, source); state != "active" {
		t.Fatalf("expiry disturbed the retained source: %s", state)
	}
	_, e = store.CommitHandoff(ctx, principal, prepared.Handoff.ID, social.HandoffCommit{ProtocolVersion: social.Protocol, ExpectedRevision: read.Handoff.Revision})
	var fault *social.Fault
	if !asFault(e, &fault) || fault.Code != "handoff_expired" || fault.Status != 410 {
		t.Fatalf("expired handoff committed: %v", e)
	}
}

func TestSocialCastBootstrapRedeemReconnectAndScoping(t *testing.T) {
	f := socialHTTP(t)
	f.decode(f.as(f.token, "PUT", "/v1/cast/configuration", map[string]any{"protocolVersion": social.Protocol, "applicationId": "ABCD1234"}), 200, nil)
	var configuration struct {
		ApplicationID string `json:"applicationId"`
		ReceiverURL   string `json:"receiverUrl"`
	}
	f.decode(f.as(f.token, "GET", "/v1/cast/configuration", nil), 200, &configuration)
	if configuration.ApplicationID != "ABCD1234" || configuration.ReceiverURL != "/receiver/cast/" {
		t.Fatalf("cast configuration: %+v", configuration)
	}
	var bootstrap social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, DisplayName: "Living room TV"}), 201, &bootstrap)
	if len(bootstrap.Bootstrap.Code) != 6 || bootstrap.Bootstrap.ApplicationID != "ABCD1234" {
		t.Fatalf("bootstrap: %+v", bootstrap.Bootstrap)
	}
	// Redemption is unauthenticated, so a wrong code must teach nothing.
	w := f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: "ZZZZZZ", ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: "cast-1"})
	if w.Code != 404 || errorCode(t, w) != "cast_code_not_found" {
		t.Fatalf("unknown cast code %d %s", w.Code, w.Body.String())
	}
	var redeemed social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: bootstrap.Bootstrap.Code, ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: "cast-1", DisplayName: "Living room TV"}), 200, &redeemed)
	if redeemed.GrantSemantics != "initial" || redeemed.Scope.AccountID != f.owner.Viewer.AccountID || redeemed.Scope.ProfileID != f.profile {
		t.Fatalf("redeem scope: %+v", redeemed)
	}
	if redeemed.DeviceToken == "" || redeemed.Session.AccessToken == "" || redeemed.DeviceToken == redeemed.Session.AccessToken {
		t.Fatal("redeem did not issue two distinct credentials")
	}
	// The issued session is a bearer for the playback API and nothing else.
	if w = f.as(redeemed.Session.AccessToken, "GET", "/v1/me", nil); w.Code != 200 {
		t.Fatalf("redeemed session is not usable %d %s", w.Code, w.Body.String())
	}
	if w = f.as(redeemed.Session.AccessToken, "GET", "/v1/receivers", nil); w.Code != 403 {
		t.Fatalf("a paired screen reached beyond playback %d %s", w.Code, w.Body.String())
	}
	// The device token is not a bearer.
	if w = f.as(redeemed.DeviceToken, "GET", "/v1/me", nil); w.Code != 401 {
		t.Fatalf("device token accepted as a bearer %d %s", w.Code, w.Body.String())
	}
	// The code is single use.
	w = f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: bootstrap.Bootstrap.Code, ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: "cast-1"})
	if w.Code != 410 || errorCode(t, w) != "cast_code_consumed" {
		t.Fatalf("cast code reused %d %s", w.Code, w.Body.String())
	}
	// The paired device shows up in the viewer's own receiver directory only.
	var directory social.ReceiverListResponse
	f.decode(f.as(f.token, "GET", "/v1/receivers", nil), 200, &directory)
	found := false
	for _, r := range directory.Receivers {
		if r.Kind == "cast" && r.DeviceID == "cast-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("paired cast device is not in the directory: %+v", directory.Receivers)
	}
	var others social.ReceiverListResponse
	f.decode(f.as(f.member, "GET", "/v1/receivers", nil), 200, &others)
	if len(others.Receivers) != 0 {
		t.Fatalf("cast pairing leaked across viewers: %+v", others.Receivers)
	}
	// Reconnect rotates the device token; the old one is dead afterwards.
	var renewed social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: redeemed.DeviceToken}), 200, &renewed)
	if renewed.GrantSemantics != "rotation" || renewed.DeviceToken == redeemed.DeviceToken || renewed.Device.Generation != "2" {
		t.Fatalf("reconnect did not rotate: %+v", renewed)
	}
	w = f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: redeemed.DeviceToken})
	if w.Code != 404 || errorCode(t, w) != "cast_device_not_found" {
		t.Fatalf("rotated-away device token still works %d %s", w.Code, w.Body.String())
	}
	// A second viewer's pairing is scoped to that viewer, never to the first.
	var theirs social.CastBootstrapResponse
	f.decode(f.as(f.member, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol}), 201, &theirs)
	var theirSession social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: theirs.Bootstrap.Code, ExpectedBootstrapID: theirs.Bootstrap.ID, DeviceID: "cast-2"}), 200, &theirSession)
	if theirSession.Scope.AccountID != "account2" || theirSession.Scope.ProfileID != "profile2" {
		t.Fatalf("second pairing was scoped to the wrong viewer: %+v", theirSession.Scope)
	}
	// Cleanup from an older pairing generation must not revoke its replacement.
	principal, e := f.d.Identity.Authenticate(f.token)
	if e != nil {
		t.Fatal(e)
	}
	e = f.d.Social().RevokeCastDevice(context.Background(), principal, renewed.Device.ID, "1")
	if fault, ok := e.(*social.Fault); !ok || fault.Code != "revision_conflict" {
		t.Fatal("stale cleanup revoked a newer pairing", e)
	}
	var pairingState string
	if e = f.d.DB.QueryRow(`SELECT state FROM social_cast_devices WHERE id=?`, renewed.Device.ID).Scan(&pairingState); e != nil || pairingState != "active" {
		t.Fatal("new pairing did not survive stale cleanup", pairingState, e)
	}
	// Revoking a pairing fails the next reconnect closed.
	if w = f.as(f.token, "DELETE", "/v1/cast/devices/"+renewed.Device.ID, nil); w.Code != 204 {
		t.Fatalf("revoke %d %s", w.Code, w.Body.String())
	}
	var directoryState string
	if e = f.d.DB.QueryRow(`SELECT state FROM social_receivers WHERE id=?`, renewed.Device.ReceiverID).Scan(&directoryState); e != nil || directoryState != "retired" {
		t.Fatal("unpaired receiver stayed active in the directory", directoryState, e)
	}
	w = f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: renewed.DeviceToken})
	if w.Code != 403 || errorCode(t, w) != "cast_device_revoked" {
		t.Fatalf("revoked pairing reconnected %d %s", w.Code, w.Body.String())
	}
}

func TestSocialCastReceiverBundleIsServed(t *testing.T) {
	f := socialHTTP(t)
	for _, path := range []string{"/receiver/cast/", "/receiver/cast/receiver.js"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("receiver bundle %s: %d", path, w.Code)
		}
	}
}

// Mutate authority exactly after the first frame, without timing-dependent goroutines.
type revokingStreamWriter struct {
	*httptest.ResponseRecorder
	once func()
}

func (w *revokingStreamWriter) Flush() {
	w.ResponseRecorder.Flush()
	if w.once != nil {
		f := w.once
		w.once = nil
		f()
	}
}
func TestGroupStreamRechecksEachDelivery(t *testing.T) {
	for _, revoke := range []string{"membership", "session"} {
		t.Run(revoke, func(t *testing.T) {
			f := socialHTTP(t)
			g := f.group("host-only")
			f.join(f.member, f.invite(g.Group.ID))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r := httptest.NewRequest("GET", "/v1/groups/"+g.Group.ID+"/events", nil).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+f.member)
			// A gap frame is followed immediately by a fresh snapshot. Revocation after
			// the gap must suppress the snapshot, even if it was composed beforehand.
			r.Header.Set("Last-Event-ID", "0")
			if _, err := f.d.DB.Exec(`DELETE FROM social_group_events WHERE group_id=?`, g.Group.ID); err != nil {
				t.Fatal(err)
			}
			w := &revokingStreamWriter{ResponseRecorder: httptest.NewRecorder()}
			w.once = func() {
				if revoke == "membership" {
					if _, err := f.d.DB.Exec(`UPDATE social_group_members SET state='left' WHERE group_id=? AND account_id='account2'`, g.Group.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					p, err := f.d.Identity.Authenticate(f.member)
					if err != nil {
						t.Fatal(err)
					}
					if err = f.d.Identity.Logout(p); err != nil {
						t.Fatal(err)
					}
				}
			}
			f.h.ServeHTTP(w, r)
			if !strings.Contains(w.Body.String(), "event: resume-gap") || strings.Contains(w.Body.String(), "event: snapshot") {
				t.Fatalf("post-revocation disclosure: %s", w.Body)
			}
		})
	}
}
