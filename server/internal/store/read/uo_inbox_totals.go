package read

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The published inbox scale cohort is 10000 retained raw records per view.
// Traverse only fixed scalar 100-row windows, each binding one extra row so the
// window proves whether more work exists, and one empty/over-limit probe. No
// payload column is copied and no partial total is a successful result: over
// the cohort bound or any fence change is a refusal/resync, never a guess.
const (
	uoInboxCountRawLimit = 10000
	uoInboxCountWindow   = 100
)

type uoInboxTotals struct {
	counts         uoInboxCounts
	total, rawRows int
	fence          uoInboxFence
}

func readUOInboxTotals(ctx context.Context, query uoInboxQuery, q uoInboxInput) (out uoInboxTotals, err error) {
	if ctx == nil || query == nil || !q.valid() || q.before != 0 {
		return out, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	fence, err := loadUOInboxFence(query, q)
	if err != nil {
		return out, err
	}
	before := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return uoInboxTotals{}, err
		}
		id := uoInboxCountFirst
		args := []any{q.principal, q.scope, q.audience, int64(uoInboxCountWindow + 1)}
		if before > 0 {
			id = uoInboxCountTail
			args = []any{q.principal, q.scope, q.audience, before, int64(uoInboxCountWindow + 1)}
		}
		rows, more, err := uoInboxRawWindow(query, id, args, uoInboxCountWindow)
		if err != nil {
			return uoInboxTotals{}, err
		}
		if len(rows) > 0 && uoInboxCountRawLimit-out.rawRows < len(rows) {
			// The bounded cohort is exhausted: an exact total over retained raw
			// records cannot be published, so the caller must resync/refuse.
			return uoInboxTotals{}, ErrUnqualified
		}
		for _, row := range rows {
			p, err := uoInboxPositionValue(row)
			if err != nil {
				return uoInboxTotals{}, err
			}
			if before > 0 && p.sequence >= before {
				return uoInboxTotals{}, ErrStale
			}
			before = p.sequence
			out.rawRows++
			if p.withdrawn.Valid || p.expires <= q.nowMS {
				continue
			}
			state, err := uoInboxState(p, fence.bulk)
			if err != nil {
				return uoInboxTotals{}, err
			}
			out.total++
			switch {
			case state.archivedMS.Valid:
				out.counts.archived++
			case state.readMS.Valid:
				out.counts.read++
			default:
				out.counts.unread++
			}
		}
		if !more {
			current, err := loadUOInboxFence(query, q)
			if err != nil {
				return uoInboxTotals{}, err
			}
			if current != fence {
				return uoInboxTotals{}, ErrStale
			}
			if err := ctx.Err(); err != nil {
				return uoInboxTotals{}, err
			}
			out.fence = fence
			return out, nil
		}
	}
}

const (
	uoInboxCountFirst stmt.ID = "read.uo.inbox.count_first"
	uoInboxCountTail  stmt.ID = "read.uo.inbox.count_tail"
)

func uoInboxCountDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	base := `SELECT n.sequence,n.revision,n.authority_revision,n.expires_ms,0,n.withdrawn_ms,a.revision,a.read_ms,a.archived_ms FROM notification_records AS n INDEXED BY notification_records_page LEFT JOIN notification_acknowledgments AS a ON a.principal_key=? AND a.notice_sequence=n.sequence WHERE n.scope=? AND n.audience=?`
	d := func(id stmt.ID, sql string, p ...stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Kind: stmt.Read, Owner: "store/read UO inbox totals", SQL: sql, Params: p, CallSites: []string{"store/read/uo_inbox_totals.go"}, Plan: stmt.Plan{Hot: true, Indexes: []string{"notification_records_page"}}}
	}
	return []stmt.Definition{
		d(uoInboxCountFirst, base+` ORDER BY n.sequence DESC LIMIT ?`, s("principal"), s("scope"), s("audience"), i("limit")),
		d(uoInboxCountTail, base+` AND n.sequence<? ORDER BY n.sequence DESC LIMIT ?`, s("principal"), s("scope"), s("audience"), i("before"), i("limit")),
	}
}
