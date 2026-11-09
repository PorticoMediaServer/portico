package read

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private DRAFT components, not an admitted endpoint or authority capability.
// Composition must supply a SAME-snapshot captured query under GV, bind the
// principal key to EXACT scope/audience/lifetime, reserve actual octets before
// payload copies, and repeat current authority plus exact head/record/ACK facts
// at publication. No handle or Rows crosses this interface. This does not use
// catalogue Browse's library fence for inbox authority.
type uoInboxQuery func(stmt.ID, ...any) ([][]any, error)
type uoInboxInput struct {
	scope, audience, principal, state string
	before, nowMS, byteBudget         int64
	limit                             int
}
type uoInboxCounts struct{ unread, read, archived int }
type uoInboxItem struct {
	sequence, revision, authorityRevision                                             int64
	id, audience, severity, source, category, title, body, arguments, actions, dedupe string
	createdMS, updatedMS, expiresMS                                                   int64
	readMS, archivedMS                                                                sql.NullInt64
}
type uoInboxPage struct {
	items       []uoInboxItem
	counts      uoInboxCounts
	next        int64
	exhausted   bool
	rawRows     int
	actualBytes int64
	fence       uoInboxFence
}
type uoInboxFence struct {
	inboxRevision int64
	bulk          uoNoticeReadThrough
}
type uoInboxPosition struct {
	sequence, revision, authority, expires, bytes int64
	withdrawn                                     sql.NullInt64
	ack                                           uoNoticeIndividualAck
}
type uoInboxWindow struct {
	positions []uoInboxPosition
	fence     uoInboxFence
	next      int64
	exhausted bool
	bytes     int64
}

var errUOInboxResync = errors.New("inbox state requires resync")

func validUOInboxString(v string, n int) bool { return v != "" && len(v) <= n && utf8.ValidString(v) }
func (q uoInboxInput) valid() bool {
	return validUOInboxString(q.scope, 400) && validUOInboxString(q.principal, 512) && (q.audience == "profile" || q.audience == "account-admin" || q.audience == "server-admin") && (q.state == "all" || q.state == "read" || q.state == "unread" || q.state == "archived") && q.before >= 0 && q.nowMS > 0 && q.limit >= 1 && q.limit <= 100 && q.byteBudget > 0 && q.byteBudget <= 64<<10
}
func uoInboxNull(v any) (sql.NullInt64, error) {
	if v == nil {
		return sql.NullInt64{}, nil
	}
	n, ok := v.(int64)
	if !ok || n < 0 {
		return sql.NullInt64{}, ErrInput
	}
	return sql.NullInt64{Int64: n, Valid: true}, nil
}
func loadUOInboxFence(query uoInboxQuery, q uoInboxInput) (f uoInboxFence, err error) {
	rows, err := query(uoInboxHead, q.scope, q.audience)
	if err != nil {
		return f, err
	}
	if len(rows) > 1 {
		return f, ErrInput
	}
	if len(rows) == 1 {
		if len(rows[0]) != 1 {
			return f, ErrInput
		}
		var ok bool
		f.inboxRevision, ok = rows[0][0].(int64)
		if !ok || f.inboxRevision <= 0 {
			return f, ErrInput
		}
	}
	rows, err = query(uoInboxBulk, q.principal)
	if err != nil {
		return f, err
	}
	if len(rows) > 1 {
		return f, ErrInput
	}
	if len(rows) == 0 {
		return f, nil
	}
	if len(rows[0]) != 4 {
		return f, ErrInput
	}
	sequence, sok := rows[0][0].(int64)
	archive, aok := rows[0][1].(int64)
	revision, rok := rows[0][2].(int64)
	ms, mok := rows[0][3].(int64)
	if !sok || !aok || !rok || !mok || sequence < 0 || archive != 0 || revision <= 0 || ms < 0 || sequence > 0 && ms == 0 {
		return f, errUOInboxResync
	}
	// Row presence is NOT bulk coverage. Empty heads cannot synthesize readAt.
	if sequence > 0 {
		f.bulk = uoNoticeReadThrough{exists: true, sequence: sequence, revision: revision, readMS: ms}
	}
	return f, nil
}
func uoInboxPositionValue(row []any) (p uoInboxPosition, err error) {
	if len(row) != 9 {
		return p, ErrInput
	}
	dst := []*int64{&p.sequence, &p.revision, &p.authority, &p.expires, &p.bytes}
	for i, d := range dst {
		v, ok := row[i].(int64)
		if !ok || v < 0 || i < 3 && v == 0 {
			return p, ErrInput
		}
		*d = v
	}
	p.withdrawn, err = uoInboxNull(row[5])
	if err != nil {
		return p, err
	}
	if row[6] != nil {
		rev, ok := row[6].(int64)
		if !ok || rev <= 0 {
			return p, ErrInput
		}
		p.ack.exists, p.ack.revision = true, rev
		p.ack.readMS, err = uoInboxNull(row[7])
		if err != nil {
			return p, err
		}
		p.ack.archivedMS, err = uoInboxNull(row[8])
		if err != nil {
			return p, err
		}
	} else if row[7] != nil || row[8] != nil {
		return p, ErrInput
	}
	return p, nil
}
func uoInboxState(p uoInboxPosition, b uoNoticeReadThrough) (uoNoticeReadState, error) {
	state, err := resolveUONoticeReadState(uoNoticeIdentity{sequence: p.sequence, revision: p.revision}, p.ack, b)
	if err != nil {
		return state, err
	}
	// Parent-owned resolver adoption is independent. Preserve archived unread
	// even when built against the prepared parent with its earlier resolver.
	if p.ack.exists && p.ack.revision >= p.revision && p.ack.archivedMS.Valid && !p.ack.readMS.Valid {
		state.readMS = sql.NullInt64{}
	}
	return state, nil
}

// Every raw window binds ONE row beyond its cap: the extra row proves more work
// exists without copying it into a result. A closure that ignores its LIMIT is
// a resync, never a silently truncated page.
func uoInboxRawWindow(query uoInboxQuery, id stmt.ID, args []any, cap int) ([][]any, bool, error) {
	rows, err := query(id, args...)
	if err != nil {
		return nil, false, err
	}
	if len(rows) > cap+1 {
		return nil, false, ErrUnqualified
	}
	if len(rows) > cap {
		return rows[:cap], true, nil
	}
	return rows, false, nil
}

func preflightUOInbox(ctx context.Context, query uoInboxQuery, q uoInboxInput) (out uoInboxWindow, err error) {
	if ctx == nil || query == nil || !q.valid() {
		return out, ErrInput
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	out.fence, err = loadUOInboxFence(query, q)
	if err != nil {
		return uoInboxWindow{}, err
	}
	id := uoInboxFirstOctets
	args := []any{q.principal, q.scope, q.audience, int64(q.limit + 1)}
	if q.before > 0 {
		id = uoInboxTailOctets
		args = []any{q.principal, q.scope, q.audience, q.before, int64(q.limit + 1)}
	}
	rows, more, err := uoInboxRawWindow(query, id, args, q.limit)
	if err != nil {
		return uoInboxWindow{}, err
	}
	last := q.before
	for _, row := range rows {
		p, e := uoInboxPositionValue(row)
		if e != nil {
			return uoInboxWindow{}, e
		}
		if last > 0 && p.sequence >= last {
			return uoInboxWindow{}, ErrStale
		}
		last = p.sequence
		// Fixed DTO/fence overhead is bounded separately by the parent's recipe.
		// Variable bytes come from THIS snapshot's octet_length, never max columns.
		if p.bytes > q.byteBudget-out.bytes {
			if len(out.positions) == 0 {
				return uoInboxWindow{}, ErrUnqualified
			}
			more = true
			break
		}
		out.positions = append(out.positions, p)
		out.bytes += p.bytes
		out.next = p.sequence
	}
	out.exhausted = !more && len(out.positions) == len(rows)
	if out.exhausted {
		out.next = 0
	}
	if err = ctx.Err(); err != nil {
		return uoInboxWindow{}, err
	}
	return out, nil
}

// Bounded window counts. Only exhausted=true with before=0 proves whole-inbox
// totals. The wire adapter must accumulate/admit all windows or refuse; it must
// never publish these partial counts as the legacy exact-count DTO.
func readUOInbox(ctx context.Context, query uoInboxQuery, q uoInboxInput) (out uoInboxPage, err error) {
	window, err := preflightUOInbox(ctx, query, q)
	if err != nil {
		return out, err
	}
	out.fence, out.next, out.exhausted, out.actualBytes = window.fence, window.next, window.exhausted, window.bytes
	out.rawRows = len(window.positions)
	if len(window.positions) == 0 {
		return out, nil
	}
	positions := window.positions
	rows, err := query(uoInboxPayload, q.scope, q.audience, positions[0].sequence, positions[len(positions)-1].sequence, int64(len(positions)))
	if err != nil {
		return uoInboxPage{}, err
	}
	if len(rows) != len(positions) {
		return uoInboxPage{}, ErrStale
	}
	for i, row := range rows {
		item, e := uoInboxItemValue(row)
		if e != nil {
			return uoInboxPage{}, e
		}
		p := positions[i]
		if item.sequence != p.sequence || item.revision != p.revision || item.authorityRevision != p.authority || item.expiresMS != p.expires || row[16] != p.bytes {
			return uoInboxPage{}, ErrStale
		}
		if p.withdrawn.Valid || p.expires <= q.nowMS {
			continue
		}
		state, e := uoInboxState(p, window.fence.bulk)
		if e != nil {
			return uoInboxPage{}, e
		}
		item.readMS, item.archivedMS = state.readMS, state.archivedMS
		switch {
		case state.archivedMS.Valid:
			out.counts.archived++
		case state.readMS.Valid:
			out.counts.read++
		default:
			out.counts.unread++
		}
		matches := q.state == "all" || q.state == "archived" && state.archivedMS.Valid || q.state == "read" && state.readMS.Valid && !state.archivedMS.Valid || q.state == "unread" && !state.readMS.Valid && !state.archivedMS.Valid
		if matches {
			out.items = append(out.items, item)
		}
	}
	if err = ctx.Err(); err != nil {
		return uoInboxPage{}, err
	}
	return out, nil
}
func uoInboxItemValue(row []any) (out uoInboxItem, err error) {
	if len(row) != 17 {
		return out, ErrInput
	}
	for i, d := range []*int64{&out.sequence, &out.revision, &out.authorityRevision, &out.createdMS, &out.updatedMS, &out.expiresMS} {
		n, ok := row[i].(int64)
		if !ok || n < 0 || i < 3 && n == 0 {
			return out, ErrInput
		}
		*d = n
	}
	for i, d := range []*string{&out.id, &out.audience, &out.severity, &out.source, &out.category, &out.title, &out.body, &out.arguments, &out.actions, &out.dedupe} {
		s, ok := row[6+i].(string)
		if !ok || !utf8.ValidString(s) {
			return out, ErrInput
		}
		*d = s
	}
	// Final native width is returned alongside payload as a defensive equality
	// check; admission itself already happened through the preceding preflight.
	n, ok := row[16].(int64)
	actual := int64(len(out.id) + len(out.audience) + len(out.severity) + len(out.source) + len(out.category) + len(out.title) + len(out.body) + len(out.arguments) + len(out.actions) + len(out.dedupe))
	if !ok || n != actual || !json.Valid([]byte(out.arguments)) || !json.Valid([]byte(out.actions)) {
		return out, ErrInput
	}
	return out, nil
}

// Cheap bounded unread probe. Found proves there is unread; exhausted without
// Found proves none only after traversing from the initial raw position.
type uoInboxUnreadResult struct {
	found, exhausted bool
	next             int64
	rawRows          int
	fence            uoInboxFence
}

func readUOInboxUnread(ctx context.Context, query uoInboxQuery, q uoInboxInput) (out uoInboxUnreadResult, err error) {
	window, err := preflightUOInbox(ctx, query, q)
	if err != nil {
		return out, err
	}
	out.next, out.exhausted, out.rawRows, out.fence = window.next, window.exhausted, len(window.positions), window.fence
	for _, p := range window.positions {
		if p.withdrawn.Valid || p.expires <= q.nowMS {
			continue
		}
		state, e := uoInboxState(p, window.fence.bulk)
		if e != nil {
			return uoInboxUnreadResult{}, e
		}
		if !state.readMS.Valid && !state.archivedMS.Valid {
			out.found = true
			break
		}
	}
	if err = ctx.Err(); err != nil {
		return uoInboxUnreadResult{}, err
	}
	return out, nil
}

// A completed shared operation remains readable even if its notice is later
// withdrawn or reclaimed. This is operation truth, not current notice access.
type uoInboxOperationStatus struct {
	id               string
	revision         int64
	created, updated bool
	expiresMS        int64
}

func readUOInboxOperation(ctx context.Context, query uoInboxQuery, scope, audience, operation string, nowMS int64) (out uoInboxOperationStatus, err error) {
	if ctx == nil || query == nil || !validUOInboxString(scope, 400) || !validUOInboxString(operation, 160) || nowMS <= 0 {
		return out, ErrInput
	}
	if audience != "profile" && audience != "account-admin" && audience != "server-admin" {
		return out, ErrInput
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	rows, err := query(uoInboxOperationOctets, scope, operation)
	if err != nil {
		return out, err
	}
	if len(rows) == 0 {
		return out, ErrNotReady
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return out, ErrInput
	}
	n, ok := rows[0][0].(int64)
	if !ok || n < 0 || n > 2048 {
		return out, ErrUnqualified
	}
	rows, err = query(uoInboxOperationPoint, scope, operation)
	if err != nil {
		return out, err
	}
	if len(rows) != 1 || len(rows[0]) != 9 {
		return out, ErrStale
	}
	r := rows[0]
	if r[0] != "uo.shared.v1" || r[1] != audience || r[2] != "complete" || r[3] != int64(1) {
		return out, errUOInboxResync
	}
	created, cok := r[4].(int64)
	updated, uok := r[5].(int64)
	revision, rok := r[6].(int64)
	ids, iok := r[7].(string)
	expires, eok := r[8].(int64)
	var decoded []string
	if !cok || !uok || !rok || !iok || !eok || created < 0 || updated < 0 || created+updated != 1 || revision <= 0 || json.Unmarshal([]byte(ids), &decoded) != nil || len(decoded) != 1 || !validUOInboxString(decoded[0], 160) {
		return out, ErrInput
	}
	if expires <= nowMS {
		return out, ErrNotReady
	}
	out = uoInboxOperationStatus{decoded[0], revision, created == 1, updated == 1, expires}
	if err = ctx.Err(); err != nil {
		return uoInboxOperationStatus{}, err
	}
	return out, nil
}
