package social

import (
	"context"
	"database/sql"
)

const GroupWaitSeconds = 5
const MemberLeaveSeconds = 600

func (s *Store) beginWait(g *record) {
	resume := "paused"
	switch g.state {
	case "playing", "host-reconnecting-playing":
		resume = "playing"
	case "lobby", "preparing", "ready":
		resume = "ready"
	case "waiting":
		resume = g.waitResumeState
	}
	g.state = "waiting"
	g.waitResumeState = resume
	g.waitUntilMS = s.ms() + GroupWaitSeconds*1000
}

func (s *Store) publishTimeline(ctx context.Context, tx *sql.Tx, g record, command string, notes *OverrideNotes) error {
	return s.publish(ctx, tx, g.id, EventTransport, map[string]any{
		"command": command, "groupState": g.state, "state": timelineState(g.state), "itemId": g.itemID, "currentEntryId": g.currentEntryID,
		"anchorPositionUs": counter(g.positionUS), "anchorAt": stamp(g.positionUpdatedMS), "rate": Rate{counter(g.rateNum), counter(g.rateDen)},
		"queuePosition": g.queuePosition, "revision": counter(g.revision + 1), "playbackRevision": counter(g.playbackRevision), "waitUntil": optionalStamp(g.waitUntilMS), "override": notes,
	})
}

func (s *Store) releaseWait(ctx context.Context, tx *sql.Tx, g *record) (bool, error) {
	if g.state != "waiting" {
		return false, nil
	}
	var unready int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM social_group_members WHERE group_id=? AND state='joined' AND max(seen_at_ms,joined_at_ms)>? AND (readiness<>'ready' OR readiness_revision<>? OR abs(readiness_position_us-?)>750000)`, g.id, s.ms()-MemberStaleSeconds*1000, g.playbackRevision, g.positionUS).Scan(&unready); e != nil {
		return false, e
	}
	if unready > 0 && s.ms() < g.waitUntilMS {
		return false, nil
	}
	at := s.ms()
	if at > g.waitUntilMS {
		at = g.waitUntilMS
	}
	g.state, g.resumeState = g.waitResumeState, g.waitResumeState
	g.positionUpdatedMS = at
	g.waitUntilMS = 0
	return true, s.publishTimeline(ctx, tx, *g, "ready", nil)
}

func (s *Store) sweepMembers(ctx context.Context, tx *sql.Tx, g *record) (bool, error) {
	result, e := tx.ExecContext(ctx, `UPDATE social_group_members SET state='left',left_at_ms=? WHERE group_id=? AND state='joined' AND role='member' AND max(seen_at_ms,joined_at_ms)<?`, s.ms(), g.id, s.ms()-MemberLeaveSeconds*1000)
	if e != nil {
		return false, e
	}
	n, e := result.RowsAffected()
	if e != nil || n == 0 {
		return false, e
	}
	return true, s.publish(ctx, tx, g.id, EventMembers, map[string]any{"revision": counter(g.revision + 1)})
}
