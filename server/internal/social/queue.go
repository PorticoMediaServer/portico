package social

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// QueueEntry is one shared-queue row as one member may see it.
//
// Contract §14: an entry a member cannot see is reduced to an opaque placeholder
// that preserves only `entryId` and `position`. `itemId` is null and no title,
// artwork, source or owner is carried, because the placeholder must not leak the
// existence of specific media in a library the member was never granted.
type QueueEntry struct {
	EntryID     string  `json:"entryId"`
	Position    int     `json:"position"`
	ItemID      *string `json:"itemId"`
	Unavailable bool    `json:"unavailable"`
	AddedBy     string  `json:"addedBy"`
}

// MemberEligibility is part of the host-only summary. A host must be able to see
// who is blocked and decide — remove the member or replace the entry — before the
// entry becomes current. Portico never silently ejects a member to start private
// media.
type MemberEligibility struct {
	MemberID     string   `json:"memberId"`
	DisplayName  string   `json:"displayName"`
	BlockedEntry []string `json:"blockedEntryIds"`
}

type QueueEligibility struct {
	BlockedEntries      int                 `json:"blockedEntries"`
	CurrentEntryBlocked bool                `json:"currentEntryBlocked"`
	Members             []MemberEligibility `json:"members"`
}

type Queue struct {
	Revision    string            `json:"revision"`
	Position    int               `json:"position"`
	Entries     []QueueEntry      `json:"entries"`
	Eligibility *QueueEligibility `json:"eligibility"`
}

type QueueResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerTime      string `json:"serverTime"`
	GroupID         string `json:"groupId"`
	Queue           Queue  `json:"queue"`
}

// QueueRequest mutates the shared queue. `operation` is append, replace, remove
// or move; every mutation carries `expectedRevision` and `idempotencyKey`.
type QueueRequest struct {
	ProtocolVersion    string   `json:"protocolVersion"`
	IdempotencyKey     string   `json:"idempotencyKey"`
	ExpectedRevision   string   `json:"expectedRevision"`
	Operation          string   `json:"operation"`
	ItemIDs            []string `json:"itemIds"`
	EntryID            string   `json:"entryId"`
	DestinationEntryID string   `json:"destinationEntryId"`
	Placement          string   `json:"placement"`
}

type queueRow struct {
	entryID  string
	position int
	itemID   string
	addedBy  string
}

func readQueue(ctx context.Context, tx *sql.Tx, group string) ([]queueRow, error) {
	rows, e := tx.QueryContext(ctx, `SELECT entry_id,position,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=social_group_queue.item_id),''),added_by FROM social_group_queue WHERE group_id=? ORDER BY position,entry_id`, group)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []queueRow
	for rows.Next() {
		var r queueRow
		if e = rows.Scan(&r.entryID, &r.position, &r.itemID, &r.addedBy); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type participant struct {
	id, displayName string
	viewer          identity.Viewer
}

func joinedParticipants(ctx context.Context, tx *sql.Tx, group string, activeSince int64) ([]participant, error) {
	rows, e := tx.QueryContext(ctx, `SELECT id,display_name,authority,account_id,profile_id,`+watchTogetherVisible("m")+` FROM social_group_members m WHERE group_id=? AND state='joined' AND max(seen_at_ms,joined_at_ms)>? ORDER BY joined_at_ms,id`, group, activeSince)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []participant
	for rows.Next() {
		var p participant
		var public bool
		if e = rows.Scan(&p.id, &p.displayName, &p.viewer.Authority, &p.viewer.AccountID, &p.viewer.ProfileID, &public); e != nil {
			return nil, e
		}
		if !public {
			p.displayName = "Viewer"
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// blocked computes, for every distinct item in `items`, the member ids that
// cannot see it. One Visible call per (item, member) pair, memoised per item.
func (s *Store) blocked(ctx context.Context, tx *sql.Tx, items []string, people []participant) (map[string][]string, error) {
	out := map[string][]string{}
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, done := out[item]; done {
			continue
		}
		var list []string
		for _, person := range people {
			ok, e := s.visible(ctx, tx, person.viewer, item)
			if e != nil {
				return nil, e
			}
			if !ok {
				list = append(list, person.id)
			}
		}
		out[item] = list
	}
	return out, nil
}

// projectQueue renders the shared queue for one member, replacing every entry
// that member cannot see with a placeholder and attaching the eligibility
// summary when the member is the host.
func (s *Store) projectQueue(ctx context.Context, tx *sql.Tx, p identity.Principal, g record, m membership) (*Queue, error) {
	rows, e := readQueue(ctx, tx, g.id)
	if e != nil {
		return nil, e
	}
	people, e := joinedParticipants(ctx, tx, g.id, s.ms()-MemberStaleSeconds*1000)
	if e != nil {
		return nil, e
	}
	if m.role != "host" {
		people = []participant{{id: m.id, viewer: p.Viewer}}
	}
	items := make([]string, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.itemID)
	}
	blockedBy, e := s.blocked(ctx, tx, items, people)
	if e != nil {
		return nil, e
	}
	// Compare the identity triple, not the whole Viewer: joinedParticipants reads
	// only authority, account and profile, and a bearer also carries a server id
	// and a role that are not part of who the member is.
	self := map[string]bool{}
	for _, person := range people {
		if person.viewer.Authority == p.Authority && person.viewer.AccountID == p.AccountID && person.viewer.ProfileID == p.ProfileID {
			self[person.id] = true
		}
	}
	out := &Queue{Revision: counter(g.queueRevision), Position: g.queuePosition, Entries: []QueueEntry{}}
	byMember := map[string][]string{}
	blockedCount, currentBlocked := 0, false
	for _, r := range rows {
		entry := QueueEntry{EntryID: r.entryID, Position: r.position, AddedBy: r.addedBy}
		list := blockedBy[r.itemID]
		if len(list) > 0 {
			blockedCount++
			if r.entryID == g.currentEntryID {
				currentBlocked = true
			}
		}
		hidden := false
		for _, id := range list {
			byMember[id] = append(byMember[id], r.entryID)
			if self[id] {
				hidden = true
			}
		}
		if hidden {
			entry.Unavailable = true
			entry.AddedBy = ""
		} else {
			item := r.itemID
			entry.ItemID = &item
		}
		out.Entries = append(out.Entries, entry)
	}
	if m.role == "host" {
		summary := &QueueEligibility{BlockedEntries: blockedCount, CurrentEntryBlocked: currentBlocked, Members: []MemberEligibility{}}
		for _, person := range people {
			if list := byMember[person.id]; len(list) > 0 {
				summary.Members = append(summary.Members, MemberEligibility{MemberID: person.id, DisplayName: person.displayName, BlockedEntry: list})
			}
		}
		out.Eligibility = summary
	}
	return out, nil
}

// ReadQueue is the standalone queue read; the same projection is embedded in a
// group snapshot.
func (s *Store) ReadQueue(ctx context.Context, p identity.Principal, id string) (QueueResponse, error) {
	var out QueueResponse
	// A read, on a snapshot, with no write gate.
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
	// The queue's projection reads the group's state, so it gets the same
	// computed expiry the group read gives. See Store.settled.
	g = s.settled(g)
	queue, e := s.projectQueue(ctx, tx, p, g, m)
	if e != nil {
		return out, e
	}
	return QueueResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), GroupID: id, Queue: *queue}, nil
}

// MutateQueue applies one queue operation under the group's queue revision.
func (s *Store) MutateQueue(ctx context.Context, p identity.Principal, id string, req QueueRequest) (QueueResponse, error) {
	var out QueueResponse
	if req.ProtocolVersion != Protocol || !text(req.IdempotencyKey, 120) {
		return out, errInvalid
	}
	expected, ok := parseCounter(req.ExpectedRevision)
	if !ok {
		return out, errInvalid
	}
	switch req.Operation {
	case "append", "replace":
		if len(req.ItemIDs) == 0 || len(req.ItemIDs) > MaxQueueEntries {
			return out, errInvalid
		}
		for _, item := range req.ItemIDs {
			if !ValidID(item) {
				return out, errInvalid
			}
		}
	case "remove":
		if !ValidID(req.EntryID) {
			return out, errInvalid
		}
	case "move":
		if !ValidID(req.EntryID) || !ValidID(req.DestinationEntryID) || (req.Placement != "before" && req.Placement != "after") {
			return out, errInvalid
		}
	default:
		return out, errInvalid
	}
	response, e := s.command(ctx, p, id, req.IdempotencyKey, req, func(tx *sql.Tx, g *record, m membership) (any, error) {
		if !s.mayControl(*g, m) {
			return nil, errHost
		}
		if expected != g.queueRevision {
			return nil, &Fault{Code: "revision_conflict", Status: 409, Message: "The queue changed.", Detail: map[string]any{"currentRevision": counter(g.queueRevision)}}
		}
		rows, e := readQueue(ctx, tx, g.id)
		if e != nil {
			return nil, e
		}
		switch req.Operation {
		case "replace":
			if _, e = tx.ExecContext(ctx, `DELETE FROM social_group_queue WHERE group_id=?`, g.id); e != nil {
				return nil, e
			}
			rows = nil
			fallthrough
		case "append":
			if len(rows)+len(req.ItemIDs) > MaxQueueEntries {
				return nil, &Fault{Code: "queue_full", Status: 409, Message: "The group queue is full."}
			}
			next := len(rows)
			for _, item := range req.ItemIDs {
				// The member adding an entry must be able to see it themselves;
				// visibility for everyone else becomes a placeholder, not a refusal.
				visible, e := s.visible(ctx, tx, p.Viewer, item)
				if e != nil {
					return nil, e
				}
				if !visible {
					return nil, &Fault{Code: "media_no_longer_accessible", Status: 403, Message: "You cannot add media you cannot see."}
				}
				entity, e := resolveSocialItem(ctx, tx, item)
				if e != nil {
					return nil, e
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO social_group_queue(group_id,entry_id,position,item_id,added_by,added_at_ms) VALUES(?,?,?,?,?,?)`, g.id, token("ent_"), next, entity, m.id, s.ms()); e != nil {
					return nil, e
				}
				next++
			}
		case "remove":
			result, e := tx.ExecContext(ctx, `DELETE FROM social_group_queue WHERE group_id=? AND entry_id=?`, g.id, req.EntryID)
			if e != nil {
				return nil, e
			}
			if n, _ := result.RowsAffected(); n == 0 {
				return nil, &Fault{Code: "entry_not_found", Status: 404, Message: "No such queue entry."}
			}
		case "move":
			order, e := queueOrderAfterMove(rows, req)
			if e != nil {
				return nil, e
			}
			if e = renumber(ctx, tx, g.id, order); e != nil {
				return nil, e
			}
		}
		// Positions are always renumbered densely from zero so `queuePosition`
		// and `position` never disagree after an insertion or a removal. A move
		// has already written its own order above.
		if req.Operation != "move" {
			rows, e = readQueue(ctx, tx, g.id)
			if e != nil {
				return nil, e
			}
			order := make([]string, 0, len(rows))
			for _, r := range rows {
				order = append(order, r.entryID)
			}
			if e = renumber(ctx, tx, g.id, order); e != nil {
				return nil, e
			}
		}
		g.queueRevision++
		if g.currentEntryID != "" {
			var current sql.NullInt64
			if e = tx.QueryRowContext(ctx, `SELECT position FROM social_group_queue WHERE group_id=? AND entry_id=?`, g.id, g.currentEntryID).Scan(&current); e != nil && e != sql.ErrNoRows {
				return nil, e
			}
			if current.Valid {
				g.queuePosition = int(current.Int64)
			}
		}
		if e = s.publish(ctx, tx, g.id, EventQueue, map[string]any{"queueRevision": counter(g.queueRevision)}); e != nil {
			return nil, e
		}
		queue, e := s.projectQueue(ctx, tx, p, *g, m)
		if e != nil {
			return nil, e
		}
		return QueueResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), GroupID: g.id, Queue: *queue}, nil
	})
	if e != nil {
		return out, e
	}
	if raw, ok := response.(json.RawMessage); ok {
		return out, json.Unmarshal(raw, &out)
	}
	out, _ = response.(QueueResponse)
	return out, nil
}

// queueOrderAfterMove computes the intended order for a move. Both the moved
// entry and its destination must exist, and they must differ.
func queueOrderAfterMove(rows []queueRow, req QueueRequest) ([]string, error) {
	missing := &Fault{Code: "entry_not_found", Status: 404, Message: "No such queue entry."}
	if req.EntryID == req.DestinationEntryID {
		return nil, errInvalid
	}
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.entryID != req.EntryID {
			order = append(order, r.entryID)
		}
	}
	if len(order) == len(rows) {
		return nil, missing
	}
	placed, found := make([]string, 0, len(rows)), false
	for _, id := range order {
		if id == req.DestinationEntryID {
			found = true
			if req.Placement == "before" {
				placed = append(placed, req.EntryID, id)
				continue
			}
			placed = append(placed, id, req.EntryID)
			continue
		}
		placed = append(placed, id)
	}
	if !found {
		return nil, missing
	}
	return placed, nil
}

func renumber(ctx context.Context, tx *sql.Tx, group string, order []string) error {
	for position, id := range order {
		if _, e := tx.ExecContext(ctx, `UPDATE social_group_queue SET position=? WHERE group_id=? AND entry_id=?`, position, group, id); e != nil {
			return e
		}
	}
	return nil
}

// entryAt reads the entry at one queue position.
func entryAt(ctx context.Context, tx *sql.Tx, group string, position int) (queueRow, bool, error) {
	var r queueRow
	e := tx.QueryRowContext(ctx, `SELECT entry_id,position,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=social_group_queue.item_id),''),added_by FROM social_group_queue WHERE group_id=? AND position=?`, group, position).Scan(&r.entryID, &r.position, &r.itemID, &r.addedBy)
	if e == sql.ErrNoRows {
		return r, false, nil
	}
	return r, e == nil, e
}
