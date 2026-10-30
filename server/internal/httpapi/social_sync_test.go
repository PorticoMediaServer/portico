package httpapi

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/social"
)

func TestSocialPlaybackWaitAndIndependentRevisions(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	joined := f.join(f.member, f.invite(id))
	now := time.Now()
	store, p := f.socialStore(&now)
	guest, e := f.d.Identity.Authenticate(f.member)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	read := func() social.GroupResponse {
		t.Helper()
		out, e := store.ReadGroup(ctx, p, id)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	command := func(command, position string) social.TransportReceipt {
		t.Helper()
		g := read().Group
		out, e := store.Transport(ctx, p, id, social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: command + "-" + g.PlaybackRevision, ExpectedRevision: g.PlaybackRevision, Command: command, PositionUS: position, QueuePosition: intp(0)})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	report := func(host bool, epoch, position string) {
		t.Helper()
		who := guest
		if host {
			who = p
		}
		_, e := store.ReportReadiness(ctx, who, id, social.ReadinessRequest{ProtocolVersion: social.Protocol, PlaybackRevision: epoch, Readiness: "ready", PositionUS: position})
		if e != nil {
			t.Fatal(e)
		}
	}
	before := read().Group
	if _, e = store.Heartbeat(ctx, guest, "", id); e != nil {
		t.Fatal(e)
	}
	report(false, joined.Group.PlaybackRevision, "0")
	after := read().Group
	if after.Revision != before.Revision || after.PlaybackRevision != before.PlaybackRevision {
		t.Fatal("presence invalidated commands", before.Revision, after.Revision)
	}
	q, e := store.MutateQueue(ctx, p, id, social.QueueRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "append", ExpectedRevision: after.QueueRevision, Operation: "append", ItemIDs: []string{f.item}})
	if e != nil {
		t.Fatal(e)
	}
	// Queue edits do not invalidate playback or settings fences.
	if read().Group.PlaybackRevision != before.PlaybackRevision {
		t.Fatal("queue invalidated playback")
	}
	loaded, e := store.Transport(ctx, p, id, social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "load", ExpectedRevision: before.PlaybackRevision, Command: "load", EntryID: q.Queue.Entries[0].EntryID})
	if e != nil {
		t.Fatal(e)
	}
	if read().Group.State != "waiting" || loaded.Timeline.WaitUntil == nil {
		t.Fatal("load did not hold the clock")
	}
	report(true, loaded.PlaybackRevision, "0")
	report(false, loaded.PlaybackRevision, "0")
	if read().Group.State != "ready" {
		t.Fatal("everyone ready did not release load")
	}
	playing := command("play", "")
	now = now.Add(100 * time.Second)
	// Use the changed rate only after anchoring the position played at the old rate.
	rate, e := store.Transport(ctx, p, id, social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "rate", ExpectedRevision: playing.PlaybackRevision, Command: "pause", Rate: &social.Rate{Numerator: "2", Denominator: "1"}})
	if e != nil {
		t.Fatal(e)
	}
	if rate.Timeline.AnchorPositionUS != "100000000" {
		t.Fatal("new rate changed elapsed position", rate.Timeline)
	}
	_ = command("play", "")
	seek := command("seek", "120000000")
	if read().Group.State != "waiting" {
		t.Fatal("seek did not wait")
	}
	// A report in flight before the seek must not release it.
	report(true, loaded.PlaybackRevision, "120000000")
	report(false, loaded.PlaybackRevision, "120000000")
	if read().Group.State != "waiting" {
		t.Fatal("stale readiness released the seek")
	}
	report(true, seek.PlaybackRevision, "120000000")
	now = now.Add(4 * time.Second)
	if read().Group.Timeline.AnchorPositionUS != "120000000" || read().Group.Timeline.State != "paused" {
		t.Fatal("clock ran while waiting")
	}
	now = now.Add(time.Second)
	g := read().Group
	if g.Timeline.State != "playing" {
		t.Fatal("absent member held the group past five seconds", g)
	}
	if e = store.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	// A late member catches up without re-entering a global wait.
	report(false, seek.PlaybackRevision, "120000000")
	if read().Group.State != "playing" {
		t.Fatal("late member paused everyone")
	}
	settingsBefore := read().Group.SettingsRevision
	_ = command("pause", "")
	enabled := true
	if _, e = store.UpdateSettings(ctx, p, id, social.SettingsRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "shuffle", ExpectedRevision: settingsBefore, Shuffle: &enabled}); e != nil {
		t.Fatal("transport invalidated settings", e)
	}
	// Only a real competing transport command conflicts.
	_, e = store.Transport(ctx, p, id, social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "old-pause", ExpectedRevision: seek.PlaybackRevision, Command: "pause"})
	if fault, ok := e.(*social.Fault); !ok || fault.Code != "revision_conflict" {
		t.Fatal("stale command accepted", e)
	}
	// A fenced end report stops extrapolation without automatically advancing the queue.
	play := command("play", "")
	_, e = store.ReportReadiness(ctx, p, id, social.ReadinessRequest{ProtocolVersion: social.Protocol, PlaybackRevision: play.PlaybackRevision, Readiness: "ready", PositionUS: "180000000", Ended: true})
	if e != nil {
		t.Fatal(e)
	}
	if read().Group.Timeline.State != "paused" {
		t.Fatal("clock kept running after media finished")
	}
	// Non-host memberships expire after ten minutes, releasing room capacity.
	now = now.Add(601 * time.Second)
	if _, e = store.Heartbeat(ctx, p, "", id); e == nil {
		t.Fatal("host-only group should end after its own ten minute absence")
	}
}

func TestSocialHandoffRequiresTheReceivingDeviceAndRemainsControllable(t *testing.T) {
	f := socialHTTP(t)
	tv := f.device("living-room-tv")
	var registered social.ReceiverResponse
	f.decode(f.as(tv.AccessToken, "POST", "/v1/receivers", social.ReceiverRequest{ProtocolVersion: social.Protocol, DisplayName: "TV", KeyFingerprint: strings.Repeat("A", 43)}), 201, &registered)
	if registered.Receiver.DeviceID != tv.DeviceID {
		t.Fatal("registration trusted a client device claim")
	}
	id := registered.Receiver.ID
	var grant social.GrantResponse
	f.decode(f.as(f.token, "POST", "/v1/receivers/"+id+"/grants", social.GrantRequest{ProtocolVersion: social.Protocol, ControllerDisplayName: "Phone"}), 201, &grant)
	self := f.as(f.token, "POST", "/v1/receivers/"+id+"/grants/"+grant.Grant.ID+"/decision", social.GrantDecision{ProtocolVersion: social.Protocol, Decision: "accept"})
	if self.Code != 403 {
		t.Fatal("phone approved itself", self.Code, self.Body.String())
	}
	f.decode(f.as(tv.AccessToken, "POST", "/v1/receivers/"+id+"/grants/"+grant.Grant.ID+"/decision", social.GrantDecision{ProtocolVersion: social.Protocol, Decision: "accept"}), 200, &grant)
	source := f.play(f.token)
	var prepared social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs", social.HandoffRequest{ProtocolVersion: social.Protocol, RequestID: "device-proof", ReceiverID: id, GrantID: grant.Grant.ID, SourcePlaybackID: source}), 201, &prepared)
	request := social.HandoffReadiness{ProtocolVersion: social.Protocol, Readiness: "playing", ReceiverPlaybackID: source, PositionUS: "0", ExpectedRevision: prepared.Handoff.Revision}
	wrong := f.as(tv.AccessToken, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/readiness", request)
	if wrong.Code != 409 || errorCode(t, wrong) != "handoff_not_ready" {
		t.Fatal("another device's playback proved readiness", wrong.Code, wrong.Body.String())
	}
	destination := f.play(tv.AccessToken)
	request.ReceiverPlaybackID = destination
	wrong = f.as(f.token, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/readiness", request)
	if wrong.Code != 403 {
		t.Fatal("phone reported TV readiness", wrong.Code, wrong.Body.String())
	}
	var ready social.HandoffResponse
	f.decode(f.as(tv.AccessToken, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/readiness", request), 200, &ready)
	var committed social.HandoffResponse
	f.decode(f.as(f.token, "POST", "/v1/handoffs/"+prepared.Handoff.ID+"/commit", social.HandoffCommit{ProtocolVersion: social.Protocol, ExpectedRevision: ready.Handoff.Revision}), 200, &committed)
	if committed.Handoff.ReceiverPlaybackID != destination || sessionState(t, f, source) != "transferred" {
		t.Fatal("handoff did not commit")
	}
	var current playbackv1.SessionView
	f.callAs(f.token, "GET", "/v1/playback/sessions/"+destination, nil, nil, 200, &current)
	f.callAs(f.token, "PATCH", "/v1/playback/sessions/"+destination, map[string]string{"If-Match": current.Revision, "Content-Type": "application/merge-patch+json"}, map[string]any{"state": "paused"}, 200, &current)
	if current.State != "paused" {
		t.Fatal("phone could not pause destination")
	}
	f.callAs(f.token, "PATCH", "/v1/playback/sessions/"+destination, map[string]string{"If-Match": current.Revision, "Content-Type": "application/merge-patch+json"}, map[string]any{"seek": map[string]any{"positionMs": 5000, "id": "phone-seek"}}, 200, &current)
	var position int64
	if e := f.d.DB.QueryRow(`SELECT position_ms FROM playback_v1_sessions WHERE id=?`, destination).Scan(&position); e != nil || position != 5000 {
		t.Fatal("phone seek did not reach destination", position, e)
	}
	f.callAs(f.token, "PATCH", "/v1/playback/sessions/"+destination, map[string]string{"If-Match": current.Revision, "Content-Type": "application/merge-patch+json"}, map[string]any{"state": "playing"}, 200, &current)
	f.callAs(f.token, "DELETE", "/v1/playback/sessions/"+destination, nil, nil, 204, nil)
	if sessionState(t, f, destination) == "active" {
		t.Fatal("phone stop did not reach destination")
	}
}

func TestSocialHiddenTitlesAreRedactedInSnapshotsEventsAndReceipts(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("anyone")
	id := created.Group.ID
	f.join(f.outside, f.invite(id))
	var q social.QueueResponse
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/queue", social.QueueRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "hidden-queue", ExpectedRevision: created.Group.QueueRevision, Operation: "append", ItemIDs: []string{f.item}}), 200, &q)
	var selected social.TransportReceipt
	f.decode(f.as(f.token, "POST", "/v1/groups/"+id+"/transport", social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "hidden-load", ExpectedRevision: created.Group.PlaybackRevision, Command: "load", EntryID: q.Queue.Entries[0].EntryID, AllowUnavailable: true}), 200, &selected)
	var blocked social.GroupResponse
	f.decode(f.as(f.outside, "GET", "/v1/groups/"+id, nil), 200, &blocked)
	if blocked.Group.Timeline.ItemID != "" || blocked.Group.Timeline.CurrentEntryID == "" {
		t.Fatal("hidden current title was disclosed or lost its placeholder", blocked.Group.Timeline)
	}
	request := social.TransportRequest{ProtocolVersion: social.Protocol, IdempotencyKey: "hidden-pause", ExpectedRevision: selected.PlaybackRevision, Command: "pause"}
	var receipt social.TransportReceipt
	f.decode(f.as(f.outside, "POST", "/v1/groups/"+id+"/transport", request), 200, &receipt)
	if receipt.Timeline.ItemID != "" {
		t.Fatal("receipt disclosed the hidden title")
	}
	f.decode(f.as(f.outside, "POST", "/v1/groups/"+id+"/transport", request), 200, &receipt)
	if receipt.Disposition != "duplicate" || receipt.Timeline.ItemID != "" {
		t.Fatal("duplicate receipt bypassed current visibility")
	}
	store := f.d.Social()
	principal, e := f.d.Identity.Authenticate(f.outside)
	if e != nil {
		t.Fatal(e)
	}
	events, _, e := store.EventsSince(context.Background(), principal, id, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), f.item) {
			t.Fatal("event disclosed the hidden title", string(event.Payload))
		}
	}
	f.decode(f.as(f.outside, "POST", "/v1/groups/"+id+"/leave", nil), 200, nil)
	if _, _, e = store.EventsSince(context.Background(), principal, id, 0); e == nil {
		t.Fatal("left member read the event ledger")
	}
}

func TestSocialPresenceAndHostRecoveryRespectMemberLeases(t *testing.T) {
	f := socialHTTP(t)
	created := f.group("host-only")
	id := created.Group.ID
	joined := f.join(f.member, f.invite(id))
	now := time.Now()
	store, p := f.socialStore(&now)
	ctx := context.Background()
	guest, e := f.d.Identity.Authenticate(f.member)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(31 * time.Second)
	if _, e = store.Heartbeat(ctx, guest, "", id); e != nil {
		t.Fatal(e)
	}
	g, e := store.ReadGroup(ctx, p, id)
	if e != nil {
		t.Fatal(e)
	}
	var mine social.Member
	for _, m := range g.Group.Members {
		if m.ID == joined.Group.MemberID {
			mine = m
		}
	}
	if mine.Presence != "connected" {
		t.Fatal("lobby heartbeat did not prove presence", mine)
	}
	_, e = store.TransferHost(ctx, guest, "", id, social.TransferRequest{ProtocolVersion: social.Protocol, MemberID: joined.Group.MemberID, ExpectedRevision: g.Group.GovernanceRevision})
	if fault, ok := e.(*social.Fault); !ok || fault.Code != "host_required" {
		t.Fatal("member claimed host inside two minute grace", e)
	}
	now = now.Add(90 * time.Second)
	if _, e = store.ReportReadiness(ctx, guest, id, social.ReadinessRequest{ProtocolVersion: social.Protocol, PlaybackRevision: g.Group.PlaybackRevision, Readiness: "ready", PositionUS: "0"}); e != nil {
		t.Fatal(e)
	}
	g, e = store.ReadGroup(ctx, p, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.TransferHost(ctx, guest, "", id, social.TransferRequest{ProtocolVersion: social.Protocol, MemberID: joined.Group.MemberID, ExpectedRevision: g.Group.GovernanceRevision}); e != nil {
		t.Fatal("eligible member could not recover host after pause boundary", e)
	}
	// The former host is an ordinary silent member and is eventually removed.
	now = now.Add(601 * time.Second)
	if _, e = store.Heartbeat(ctx, guest, "", id); e == nil {
		t.Fatal("the absent new host did not reach its own end boundary")
	}
	// In anyone-control groups a live member keeps the group reachable without the host.
	other := f.group("anyone")
	otherID := other.Group.ID
	otherJoined := f.join(f.member, f.invite(otherID))
	gone := f.join(f.outside, f.invite(otherID))
	now = time.Now().Add(601 * time.Second)
	// Simulate the active participant's real heartbeats before the stale boundary.
	if _, e = f.d.DB.Exec(`UPDATE social_group_members SET seen_at_ms=? WHERE id=?`, now.UnixMilli(), otherJoined.Group.MemberID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.d.DB.Exec(`UPDATE social_groups SET activity_seen_ms=? WHERE id=?`, now.UnixMilli(), otherID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Heartbeat(ctx, guest, "", otherID); e != nil {
		t.Fatal("live anyone-group member was blocked by absent host", e)
	}
	g, e = store.ReadGroup(ctx, guest, otherID)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range g.Group.Members {
		if m.ID == gone.Group.MemberID {
			t.Fatal("silent member stayed joined", m)
		}
	}
}

func TestSocialBoundsGroupCreationAndFiltersNames(t *testing.T) {
	f := socialHTTP(t)
	bad := f.as(f.token, "POST", "/v1/groups", social.CreateGroupRequest{ProtocolVersion: social.Protocol, Name: "Film\u202e", DisplayName: "Host"})
	if bad.Code != 400 {
		t.Fatal("text-direction controls were accepted", bad.Code, bad.Body.String())
	}
	for i := 0; i < 4; i++ {
		f.group("host-only")
	}
	capped := f.as(f.token, "POST", "/v1/groups", social.CreateGroupRequest{ProtocolVersion: social.Protocol, Name: "Film night", DisplayName: "Host"})
	if capped.Code != 429 || errorCode(t, capped) != "capacity_reached" {
		t.Fatal("live group cap did not apply", capped.Code, capped.Body.String())
	}
}

// A closed governance window is forbidden regardless of target readiness.
func TestSocialHostClaimGovernancePrecedesReadiness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"host-only", "anyone"} {
		for _, elapsed := range []time.Duration{0, 31 * time.Second} {
			for _, readiness := range []string{"ready", "buffering"} {
				t.Run(mode+"/"+elapsed.String()+"/"+readiness, func(t *testing.T) {
					f := socialHTTP(t)
					created := f.group(mode)
					id := created.Group.ID
					joined := f.join(f.member, f.invite(id))
					now := time.Now().Add(elapsed)
					store, host := f.socialStore(&now)
					guest, err := f.d.Identity.Authenticate(f.member)
					if err != nil {
						t.Fatal(err)
					}
					ctx := context.Background()
					if _, err = store.ReportReadiness(ctx, guest, id, social.ReadinessRequest{ProtocolVersion: social.Protocol, PlaybackRevision: created.Group.PlaybackRevision, Readiness: readiness, PositionUS: "0"}); err != nil {
						t.Fatal(err)
					}
					g, err := store.ReadGroup(ctx, host, id)
					if err != nil {
						t.Fatal(err)
					}
					for _, target := range []string{joined.Group.MemberID, created.Group.MemberID} {
						_, err = store.TransferHost(ctx, guest, "", id, social.TransferRequest{ProtocolVersion: social.Protocol, MemberID: target, ExpectedRevision: g.Group.GovernanceRevision})
						if failure, ok := err.(*social.Fault); !ok || failure.Status != 403 || failure.Code != "host_required" {
							t.Fatalf("target %s: expected403 host_required, got %v", target, err)
						}
					}
				})
			}
		}
	}
}
