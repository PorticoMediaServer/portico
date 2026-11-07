package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	wcAPIEventInsert stmt.ID = "wc.api_event.insert"
	wcAPIEventOldest stmt.ID = "wc.api_event.oldest_eligible"
	wcAPIEventDelete stmt.ID = "wc.api_event.delete_exact"
)

// One already-sealed hint selected by the parent's compiled, currently scoped
// recipe. Data is already encoded by that owner, never an arbitrary callback or
// command receipt. W retains the finite audience/type/payload policy and ALL
// byte/memory/index/WAL/terminal forecasts, including the old row's envelope.
// Possessing these native values grants no authority or publishing capability.
type wcAPIEvent struct {
	audience, eventType, resourceKind, resourceID, revision, data string
	atMS                                                          int64
}

// Private accounting only: never disclose the victim or wake the feed before
// W's physical parent commit. These values are not an Applied result.
type wcAPIEventEffect struct {
	insertedID, insertedVariableBytes int64
	removedID, removedVariableBytes   int64
	removed                           bool
}

// TEXT payload bytes only, not native record/index/pager/WAL cost or a quota.
// Native UTF8 text bindings preserve these byte widths, including multibyte data.
func wcAPIEventVariableBytes(q wcAPIEvent) (int64, error) {
	var n int64
	for _, s := range [6]string{q.audience, q.eventType, q.resourceKind, q.resourceID, q.revision, q.data} {
		if !utf8.ValidString(s) || int64(len(s)) > math.MaxInt64-n {
			return 0, ErrResolveRequired
		}
		n += int64(len(s))
	}
	return n, nil
}

// The ONE append-owner protocol. No range DELETE, trim, scan, loop, census or
// janitor. Every appender must be exclusive/qualified and bootstrap with <=N
// rows; then each INSERT paired with at most ONE oldest eligible full-PK DELETE
// preserves committed row count <=N even with ID gaps. W owns that precondition,
// original replay classification and the SAME parent Work/commit/rollback.
func appendWCAPIEvent(ctx context.Context, turn *commandTurn, q wcAPIEvent) (out wcAPIEventEffect, err error) {
	if turn == nil {
		return out, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	if q.audience == "" || q.eventType == "" || q.atMS < 0 {
		return out, ErrInvalid
	}
	out.insertedVariableBytes, err = wcAPIEventVariableBytes(q)
	if err != nil {
		return out, err
	}
	out.insertedID, err = wc008DraftInteger(ctx, turn, wcAPIEventInsert, q.audience, q.eventType, q.resourceKind, q.resourceID, q.revision, q.data, q.atMS)
	if err != nil {
		return out, err
	}
	if out.insertedID <= 0 {
		return out, ErrResolveRequired
	}
	// Positive native id and positive fixed N make subtraction safe at both ends.
	cutoff := out.insertedID - apievents.RingSize
	row, err := wc008DraftQuery(ctx, turn, wcAPIEventOldest, cutoff)
	if err != nil {
		return out, err
	}
	var old wcAPIEvent
	err = row.Scan(&out.removedID, &old.audience, &old.eventType, &old.resourceKind, &old.resourceID, &old.revision, &old.data, &old.atMS)
	if errors.Is(err, sql.ErrNoRows) {
		out.removedID = 0
		return out, turn.checkLifetimes(ctx)
	}
	if err != nil {
		return out, err
	}
	if out.removedID <= 0 || out.removedID > cutoff || out.removedID >= out.insertedID || old.audience == "" || old.eventType == "" || old.atMS < 0 {
		return out, ErrResolveRequired
	}
	out.removedVariableBytes, err = wcAPIEventVariableBytes(old)
	if err != nil {
		return out, err
	}
	deleted, err := wc008DraftInteger(ctx, turn, wcAPIEventDelete, out.removedID)
	if err != nil {
		return out, err
	}
	if deleted != out.removedID {
		return out, ErrConflict
	}
	out.removed = true
	return out, turn.checkLifetimes(ctx)
}

func wcAPIEventDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{ID: wcAPIEventInsert, Owner: "store/writer WC API event append owner", CallSites: []string{"store/writer/wc_api_events.go"}, Kind: stmt.Write,
			SQL:     `INSERT INTO api_events(audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES(?,?,?,?,?,?,?) RETURNING id`,
			Params:  []stmt.Param{{Name: "audience", Type: stmt.Text}, {Name: "type", Type: stmt.Text}, {Name: "resource_kind", Type: stmt.Text}, {Name: "resource_id", Type: stmt.Text}, {Name: "revision", Type: stmt.Text}, {Name: "data", Type: stmt.Text}, {Name: "at", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "one_parent_hint", Bindings: []any{"profile:local:account:profile", "progress.changed", "library", "library-one", "3", `{"observation":"42"}`, int64(2000)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: wcAPIEventOldest, Owner: "store/writer WC API event append owner", CallSites: []string{"store/writer/wc_api_events.go"}, Kind: stmt.Read,
			SQL:    `SELECT id,audience,type,resource_kind,resource_id,revision,data,at_ms FROM api_events WHERE id<=? ORDER BY id LIMIT 1`,
			Params: []stmt.Param{{Name: "cutoff", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "inclusive_oldest", Bindings: []any{int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"api_events"}}},
		{ID: wcAPIEventDelete, Owner: "store/writer WC API event append owner", CallSites: []string{"store/writer/wc_api_events.go"}, Kind: stmt.Write,
			SQL: `DELETE FROM api_events WHERE id=? RETURNING id`, Params: []stmt.Param{{Name: "oldest_id", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "exact_oldest", Bindings: []any{int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"api_events"}}},
	}
}
