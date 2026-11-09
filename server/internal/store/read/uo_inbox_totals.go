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
	// notification_revision is the shared mutation ordinal every individual ACK
	// (and notice) write advances. The record/head fence alone cannot see a
	// sparse ACK change mid-traversal, which would publish a torn "exact" total.
	ordinal, err := loadUOInboxOrdinal(query)
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
			currentOrdinal, err := loadUOInboxOrdinal(query)
			if err != nil {
				return uoInboxTotals{}, err
			}
			if current != fence || currentOrdinal != ordinal {
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
	uoInboxOrdinal    stmt.ID = "read.uo.inbox.ordinal"
)

// loadUOInboxOrdinal reads the shared notice/ACK mutation ordinal. A missing
// singleton is zero: no ACK can exist before the first mutation allocated one.
func loadUOInboxOrdinal(query uoInboxQuery) (int64, error) {
	rows, err := query(uoInboxOrdinal)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return 0, ErrInput
	}
	ordinal, ok := rows[0][0].(int64)
	if !ok || ordinal < 0 {
		return 0, errUOInboxResync
	}
	return ordinal, nil
}

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
		// The exact-total fence re-reads this singleton before publishing. It is
		// one PRIMARY KEY point, never a scan or a notice payload.
		{ID: uoInboxOrdinal, Kind: stmt.Read, Owner: "store/read UO inbox totals",
			SQL:       `SELECT value FROM notification_revision WHERE singleton=1`,
			CallSites: []string{"store/read/uo_inbox_totals.go"},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"notification_revision"}, Seeks: []string{"rowid=?"}}},
	}
}
