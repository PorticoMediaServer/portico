package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// TransportRequest is one authoritative group command.
//
// `expectedRevision` fences the command against the playback revision the caller
// last read; `idempotencyKey` makes the command replay-safe. Both are required:
// a retried Play that arrives after a Pause must not undo the Pause, and a
// retried Play that arrives twice must not be applied twice.
type TransportRequest struct {
	ProtocolVersion  string `json:"protocolVersion"`
	IdempotencyKey   string `json:"idempotencyKey"`
	ExpectedRevision string `json:"expectedRevision"`
	Command          string `json:"command"`
	PositionUS       string `json:"positionUs,omitempty"`
	QueuePosition    *int   `json:"queuePosition,omitempty"`
	EntryID          string `json:"entryId,omitempty"`
	Rate             *Rate  `json:"rate,omitempty"`
	// AllowUnavailable is the documented host override. The group refuses to
	// start an entry some member cannot see; a host may override, and the group
	// records that it did so. A member can never set it.
	AllowUnavailable bool `json:"allowUnavailable,omitempty"`
}

// TransportReceipt is the durable answer to one command. A replay of the same
// idempotency key returns the stored receipt with disposition `duplicate`; the
// same key with a different body is a conflict, never a silent overwrite.
type TransportReceipt struct {
	ProtocolVersion string `json:"protocolVersion"`
	// ServerTime is the answer's time, like every other social response
	// (CD-01): clients read it to place the timeline. It is set when the
	// answer is sent, so a replayed receipt carries the replay's time.
	ServerTime       string         `json:"serverTime"`
	GroupID          string         `json:"groupId"`
	IdempotencyKey   string         `json:"idempotencyKey"`
	Disposition      string         `json:"disposition"`
	Command          string         `json:"command"`
	GroupState       string         `json:"groupState"`
	Revision         string         `json:"revision"`
	PlaybackRevision string         `json:"playbackRevision"`
	QueueRevision    string         `json:"queueRevision"`
	RecordedAt       string         `json:"recordedAt"`
	Timeline         Timeline       `json:"timeline"`
	Settings         Settings       `json:"settings"`
	Override         *OverrideNotes `json:"override"`
}

// OverrideNotes records that a host started an entry some members cannot see.
// It is published to the whole group, so the override is never invisible.
type OverrideNotes struct {
	Reason         string   `json:"reason"`
	BlockedMembers []string `json:"blockedMemberIds"`
}

type SettingsRequest struct {
	ProtocolVersion  string  `json:"protocolVersion"`
	IdempotencyKey   string  `json:"idempotencyKey"`
	ExpectedRevision string  `json:"expectedRevision"`
	Shuffle          *bool   `json:"shuffleEnabled,omitempty"`
	Repeat           *string `json:"repeatMode,omitempty"`
}

// ReadinessRequest is a participant's own report. Readiness is evidence about
// one member's transport. Matching readiness releases a bounded seek/load wait;
// a fenced host end report anchors the group at the end of the title.
type ReadinessRequest struct {
	ProtocolVersion  string `json:"protocolVersion"`
	Readiness        string `json:"readiness"`
	PlaybackRevision string `json:"playbackRevision"`
	Ended            bool   `json:"ended,omitempty"`
	PositionUS       string `json:"positionUs,omitempty"`
}

// mayControl applies the host authority policy. `host-only` is the contract
// default: the host alone issues authoritative commands and a participant's
// local Pause is a request to the host. `anyone` is this server's documented
// opt-in extension for small trusted groups; it widens transport and queue
// commands to every joined member and nothing else — ending the group,
// transferring host authority and issuing invitations stay with the host.
func (s *Store) mayControl(g record, m membership) bool {
	return m.role == "host" || g.hostAuthority == "anyone"
}

// command wraps one idempotent group mutation: receipt lookup, revision fence,
// apply, trim, commit. The applied function returns the response body that is
// stored in the receipt and replayed verbatim on retry.
func (s *Store) command(ctx context.Context, p identity.Principal, id, key string, body any, apply func(*sql.Tx, *record, membership) (any, error)) (any, error) {
	gated, e := s.begin(ctx)
	if e != nil {
		return nil, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	g, e := loadGroup(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	m, e := loadMember(ctx, tx, id, p.Viewer)
	if e != nil {
		return nil, e
	}
	if m.state != "joined" {
		return nil, errNotMember
	}
	want := digestJSON(body)
	var stored, seen string
	e = tx.QueryRowContext(ctx, `SELECT digest,response FROM social_group_receipts WHERE group_id=? AND member_id=? AND idempotency_key=?`, id, m.id, key).Scan(&seen, &stored)
	if e == nil {
		if !sameSecret(seen, want) {
			return nil, &Fault{Code: "idempotency_key_reused", Status: 409, Message: "That idempotency key was used for a different request."}
		}
		return json.RawMessage(stored), nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	swept, e := s.sweepGroup(ctx, tx, &g)
	if e != nil {
		return nil, e
	}
	if g.state == "ended" || g.state == "failed" {
		if swept {
			if e = s.save(ctx, tx, g, true); e != nil {
				return nil, e
			}
			if e = gated.Commit(); e != nil {
				return nil, e
			}
			s.Wake(id)
		}
		return nil, errGroupEnded
	}
	if e = s.limit(ctx, tx, "group-command:"+id+":"+m.id, 120, time.Minute); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE social_group_members SET seen_at_ms=? WHERE id=?`, s.ms(), m.id); e != nil {
		return nil, e
	}
	g.activitySeenMS = s.ms()
	m, e = loadJoinedMember(ctx, tx, id, p.Viewer)
	if e != nil {
		return nil, e
	}
	out, e := apply(tx, &g, m)
	if e != nil {
		return nil, e
	}
	if e = s.save(ctx, tx, g, true); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(out)
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO social_group_receipts(group_id,member_id,idempotency_key,digest,response,created_at_ms) VALUES(?,?,?,?,?,?)`, id, m.id, key, want, string(raw), s.ms()); e != nil {
		return nil, e
	}
	// The receipt window is bounded per group: the oldest keys fall out first.
	if _, e = tx.ExecContext(ctx, `DELETE FROM social_group_receipts WHERE group_id=? AND rowid NOT IN (SELECT rowid FROM social_group_receipts WHERE group_id=? ORDER BY created_at_ms DESC,rowid DESC LIMIT ?)`, id, id, MaxReceipts); e != nil {
		return nil, e
	}
	if e = gated.Commit(); e != nil {
		return nil, e
	}
	s.Wake(id)
	return out, nil
}

// Transport applies one authoritative command.
func (s *Store) Transport(ctx context.Context, p identity.Principal, id string, req TransportRequest) (TransportReceipt, error) {
	var out TransportReceipt
	if req.ProtocolVersion != Protocol || !text(req.IdempotencyKey, 120) {
		return out, errInvalid
	}
	expected, ok := parseCounter(req.ExpectedRevision)
	if !ok {
		return out, errInvalid
	}
	if req.PositionUS != "" {
		value, valid := parseCounter(req.PositionUS)
		if !valid || value > MaxPositionUS {
			return out, errInvalid
		}
	}
	switch req.Command {
	case "play", "pause", "stop", "next", "previous":
	case "seek", "load":
		if value, ok := parseCounter(req.PositionUS); (!ok || value > MaxPositionUS) && req.PositionUS != "" {
			return out, errInvalid
		}
		if req.Command == "seek" && req.PositionUS == "" {
			return out, errInvalid
		}
		if req.Command == "load" && !ValidID(req.EntryID) {
			return out, errInvalid
		}
	case "set-queue-position":
		if req.QueuePosition == nil || *req.QueuePosition < 0 || *req.QueuePosition >= MaxQueueEntries {
			return out, errInvalid
		}
	default:
		return out, errInvalid
	}
	if req.Rate != nil {
		num, okNum := parseCounter(req.Rate.Numerator)
		den, okDen := parseCounter(req.Rate.Denominator)
		if !okNum || !okDen || num == 0 || den == 0 || num > 4000000 || den > 1000000 || num > 4*den || 2*num < den {
			return out, errInvalid
		}
	}
	response, e := s.command(ctx, p, id, req.IdempotencyKey, req, func(tx *sql.Tx, g *record, m membership) (any, error) {
		if !s.mayControl(*g, m) {
			return nil, errHost
		}
		if expected != g.playbackRevision {
			return nil, &Fault{Code: "revision_conflict", Status: 409, Message: "The group changed.", Detail: map[string]any{"currentRevision": counter(g.playbackRevision)}}
		}
		if req.AllowUnavailable && m.role != "host" {
			return nil, errHost
		}
		if m.role == "host" {
			g.hostSeenMS = s.ms()
			if e := s.hostReturned(ctx, tx, g); e != nil {
				return nil, e
			}
		}
		notes, e := s.applyTransport(ctx, tx, g, m, req)
		if e != nil {
			return nil, e
		}
		g.playbackRevision++
		g.lastCommand, g.lastCommandID = req.Command, req.IdempotencyKey
		if e = s.publishTimeline(ctx, tx, *g, req.Command, notes); e != nil {
			return nil, e
		}
		return TransportReceipt{
			ProtocolVersion: Protocol, GroupID: g.id, IdempotencyKey: req.IdempotencyKey, Disposition: "accepted",
			Command: req.Command, GroupState: g.state, Revision: counter(g.revision + 1), PlaybackRevision: counter(g.playbackRevision), QueueRevision: counter(g.queueRevision),
			RecordedAt: stamp(s.ms()),
			Timeline: Timeline{ItemID: g.itemID, CurrentEntryID: g.currentEntryID, State: timelineState(g.state),
				AnchorPositionUS: counter(g.positionUS), AnchorAt: stamp(g.positionUpdatedMS),
				Rate: Rate{Numerator: counter(g.rateNum), Denominator: counter(g.rateDen)}, QueuePosition: g.queuePosition, WaitUntil: optionalStamp(g.waitUntilMS)},
			Settings: Settings{Shuffle: g.shuffle, Repeat: g.repeat},
			Override: notes,
		}, nil
	})
	if e != nil {
		return out, e
	}
	out, e = decodeReceipt(response)
	if e == nil {
		out.Timeline.ItemID, e = s.itemFor(ctx, p, id, out.Timeline.ItemID)
	}
	out.ServerTime = stamp(s.ms())
	return out, e
}

// decodeReceipt unwraps a command result. A replay arrives as the stored JSON
// body; it is returned byte for byte apart from `disposition`, which becomes
// `duplicate` so a client can tell a fresh acceptance from a retry.
func decodeReceipt(response any) (TransportReceipt, error) {
	var out TransportReceipt
	if raw, ok := response.(json.RawMessage); ok {
		if e := json.Unmarshal(raw, &out); e != nil {
			return out, e
		}
		out.Disposition = "duplicate"
		return out, nil
	}
	out, _ = response.(TransportReceipt)
	return out, nil
}

// applyTransport moves the group's authoritative timeline. Every branch either
// re-anchors the clock or refuses; none of them touch a member's own transport.
func (s *Store) applyTransport(ctx context.Context, tx *sql.Tx, g *record, m membership, req TransportRequest) (*OverrideNotes, error) {
	now := s.ms()
	position := s.extrapolate(*g)
	if req.Rate != nil {
		g.rateNum, _ = parseCounter(req.Rate.Numerator)
		g.rateDen, _ = parseCounter(req.Rate.Denominator)
	}
	switch req.Command {
	case "pause":
		g.waitUntilMS = 0
		g.positionUS, g.positionUpdatedMS = position, now
		g.state, g.resumeState = "paused", "paused"
		if g.itemID == "" {
			g.state = "lobby"
		}
		return nil, nil
	case "stop":
		g.waitUntilMS = 0
		g.positionUS, g.positionUpdatedMS = 0, now
		g.state, g.resumeState = "ready", "paused"
		if g.itemID == "" {
			// Nothing is loaded, so there is nothing to be ready for.
			g.state = "lobby"
		}
		return nil, nil
	case "seek":
		target, _ := parseCounter(req.PositionUS)
		g.positionUS, g.positionUpdatedMS = target, now
		if g.itemID == "" {
			return nil, fault("target_unavailable", 409, "The group has nothing loaded.")
		}
		s.beginWait(g)
		return nil, nil
	case "play":
		if g.itemID == "" {
			return nil, &Fault{Code: "target_unavailable", Status: 409, Message: "The group has nothing loaded."}
		}
		notes, e := s.gateEntry(ctx, tx, g, g.currentEntryID, req.AllowUnavailable)
		if e != nil {
			return nil, e
		}
		if req.PositionUS != "" {
			if target, ok := parseCounter(req.PositionUS); ok {
				position = target
			}
		}
		g.positionUS, g.positionUpdatedMS = position, now
		g.waitUntilMS = 0
		g.state, g.resumeState = "playing", "playing"
		return notes, nil
	}
	// The remaining commands all select a different queue entry.
	rows, e := readQueue(ctx, tx, g.id)
	if e != nil {
		return nil, e
	}
	if len(rows) == 0 {
		return nil, &Fault{Code: "target_unavailable", Status: 409, Message: "The group queue is empty."}
	}
	target := g.queuePosition
	switch req.Command {
	case "next":
		target = g.queuePosition + 1
		if target >= len(rows) {
			if g.repeat != "all" {
				return nil, &Fault{Code: "target_unavailable", Status: 409, Message: "The group is at the end of its queue."}
			}
			target = 0
		}
	case "previous":
		target = g.queuePosition - 1
		if target < 0 {
			if g.repeat != "all" {
				return nil, &Fault{Code: "target_unavailable", Status: 409, Message: "The group is at the start of its queue."}
			}
			target = len(rows) - 1
		}
	case "set-queue-position":
		target = *req.QueuePosition
	case "load":
		found := false
		for _, r := range rows {
			if r.entryID == req.EntryID {
				target, found = r.position, true
			}
		}
		// An entry that is not in the queue is refused, like a position past
		// its end. Falling through would reload whatever is current from the
		// start.
		if !found {
			return nil, &Fault{Code: "target_unavailable", Status: 409, Message: "No such queue entry."}
		}
	}
	entry, ok, e := entryAt(ctx, tx, g.id, target)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, &Fault{Code: "target_unavailable", Status: 409, Message: "No queue entry at that position."}
	}
	notes, e := s.gateEntry(ctx, tx, g, entry.entryID, req.AllowUnavailable)
	if e != nil {
		return nil, e
	}
	g.queuePosition, g.currentEntryID, g.itemID = target, entry.entryID, entry.itemID
	g.positionUS, g.positionUpdatedMS = 0, now
	if req.Command == "load" && req.PositionUS != "" {
		if start, ok := parseCounter(req.PositionUS); ok {
			g.positionUS = start
		}
	}
	if g.state == "lobby" || g.state == "ready" || g.state == "preparing" {
		g.state, g.resumeState = "preparing", "paused"
	}
	s.beginWait(g)
	if e = s.publish(ctx, tx, g.id, EventQueue, map[string]any{"queueRevision": counter(g.queueRevision), "currentEntryId": g.currentEntryID}); e != nil {
		return nil, e
	}
	return notes, nil
}

// gateEntry enforces the all-participant visibility check. An entry that some
// joined member cannot see is refused with `media_no_longer_accessible` and the
// blocked member ids, so the host can decide. The host may override; the
// override is recorded on the receipt and broadcast to the group.
func (s *Store) gateEntry(ctx context.Context, tx *sql.Tx, g *record, entryID string, override bool) (*OverrideNotes, error) {
	if entryID == "" {
		return nil, nil
	}
	var itemID string
	e := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=social_group_queue.item_id),'') FROM social_group_queue WHERE group_id=? AND entry_id=?`, g.id, entryID).Scan(&itemID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	people, e := joinedParticipants(ctx, tx, g.id, s.ms()-MemberStaleSeconds*1000)
	if e != nil {
		return nil, e
	}
	blockedBy, e := s.blocked(ctx, tx, []string{itemID}, people)
	if e != nil {
		return nil, e
	}
	list := blockedBy[itemID]
	if len(list) == 0 {
		return nil, nil
	}
	if !override {
		return nil, &Fault{Code: "media_no_longer_accessible", Status: 409, Message: "Some members cannot see that item.", Detail: map[string]any{"blockedMemberIds": list, "entryId": entryID}}
	}
	return &OverrideNotes{Reason: "host-override", BlockedMembers: list}, nil
}

// UpdateSettings changes group shuffle and repeat.
func (s *Store) UpdateSettings(ctx context.Context, p identity.Principal, id string, req SettingsRequest) (GroupResponse, error) {
	var out GroupResponse
	if req.ProtocolVersion != Protocol || !text(req.IdempotencyKey, 120) {
		return out, errInvalid
	}
	expected, ok := parseCounter(req.ExpectedRevision)
	if !ok {
		return out, errInvalid
	}
	if req.Repeat != nil && *req.Repeat != "none" && *req.Repeat != "one" && *req.Repeat != "all" {
		return out, errInvalid
	}
	if req.Shuffle == nil && req.Repeat == nil {
		return out, errInvalid
	}
	response, e := s.command(ctx, p, id, req.IdempotencyKey, req, func(tx *sql.Tx, g *record, m membership) (any, error) {
		if !s.mayControl(*g, m) {
			return nil, errHost
		}
		if expected != g.settingsRevision {
			return nil, &Fault{Code: "revision_conflict", Status: 409, Message: "The group changed.", Detail: map[string]any{"currentRevision": counter(g.settingsRevision)}}
		}
		g.settingsRevision++
		if req.Shuffle != nil {
			g.shuffle = *req.Shuffle
		}
		if req.Repeat != nil {
			g.repeat = *req.Repeat
		}
		if e := s.publish(ctx, tx, g.id, EventSettings, Settings{Shuffle: g.shuffle, Repeat: g.repeat}); e != nil {
			return nil, e
		}
		g.revision++
		snapshot, e := s.project(ctx, tx, p, *g, m)
		g.revision--
		if e != nil {
			return nil, e
		}
		return snapshot, nil
	})
	if e != nil {
		return out, e
	}
	if raw, ok := response.(json.RawMessage); ok {
		return out, json.Unmarshal(raw, &out)
	}
	out, _ = response.(GroupResponse)
	return out, nil
}

// ReportReadiness records one member's own readiness and republishes the
// aggregate. It takes no idempotency key: a readiness report is a fact about
// now, and the newest one always wins.
func (s *Store) ReportReadiness(ctx context.Context, p identity.Principal, id string, req ReadinessRequest) (ReadinessReceipt, error) {
	var receipt ReadinessReceipt
	if req.ProtocolVersion != Protocol {
		return receipt, errInvalid
	}
	switch req.Readiness {
	case "buffering", "ready", "lagging":
	default:
		return receipt, errInvalid
	}
	position, ok := parseCounter(req.PositionUS)
	if (!ok || position > MaxPositionUS) && req.PositionUS != "" {
		return receipt, errInvalid
	}
	_, e := s.mutateGroupAs(ctx, p, id, false, &receipt, func(tx *sql.Tx, g *record, m membership) error {
		epoch, valid := parseCounter(req.PlaybackRevision)
		if !valid {
			return errInvalid
		}
		if epoch != g.playbackRevision {
			return nil
		}
		var was string
		var wasRevision, wasAt int64
		if e := tx.QueryRowContext(ctx, `SELECT readiness,readiness_revision,readiness_at_ms FROM social_group_members WHERE id=?`, m.id).Scan(&was, &wasRevision, &wasAt); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE social_group_members SET readiness=?,readiness_position_us=?,readiness_at_ms=?,readiness_revision=? WHERE id=?`, req.Readiness, position, s.ms(), g.playbackRevision, m.id); e != nil {
			return e
		}
		if m.role == "host" {
			if e := s.hostReturned(ctx, tx, g); e != nil {
				return e
			}
		}
		if req.Ended && m.role == "host" && timelineState(g.state) == "playing" {
			g.positionUS = position
			g.positionUpdatedMS = s.ms()
			g.state, g.resumeState = "paused", "paused"
			g.playbackRevision++
			return s.publishTimeline(ctx, tx, *g, "ended", nil)
		}
		if _, e := s.releaseWait(ctx, tx, g); e != nil {
			return e
		}
		summary, e := s.readinessSummary(ctx, tx, *g)
		if e != nil {
			return e
		}
		// The lobby promotes itself to `ready` only when every joined member has
		// reported ready. One member buffering never pauses the others; it just
		// holds the aggregate, and the host may still start anyway.
		if g.state == "preparing" && summary.Aggregate == "ready" {
			g.state = "ready"
		}
		// Every member hears every other member's reports, so a report that
		// changes nothing anyone is shown (the same readiness, the same room
		// summary, a fresh report) is not re-broadcast: in steady playback the
		// room's traffic no longer grows with the square of its size. The
		// member's position is still kept, for snapshots and the wait.
		raw, _ := json.Marshal(summary)
		fresh := wasAt > 0 && s.ms()-wasAt <= ReadinessStaleSeconds*1000
		if was == req.Readiness && wasRevision == g.playbackRevision && fresh && string(raw) == g.readinessSummary {
			return nil
		}
		if _, e = tx.ExecContext(ctx, `UPDATE social_groups SET readiness_summary=? WHERE id=?`, string(raw), g.id); e != nil {
			return e
		}
		g.readinessSummary = string(raw)
		return s.publish(ctx, tx, g.id, EventReadiness, map[string]any{"memberId": m.id, "readiness": req.Readiness, "positionUs": counter(position), "reportedAt": stamp(s.ms()), "aggregate": summary})
	})
	return receipt, e
}
