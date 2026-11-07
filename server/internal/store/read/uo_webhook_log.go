package read

import (
	"context"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type uoWebhookLogCursor struct {
	createdMS int64
	id        string
}
type uoWebhookLogInput struct {
	webhook                         string
	authority, revision, byteBudget int64
	limit                           int
	after                           *uoWebhookLogCursor
}
type uoWebhookLogItem struct {
	id, kind, state, errorCode                                            string
	attempts, createdMS, finishedMS, nextMS, status, durationMS, revision int64
}
type uoWebhookLogPage struct {
	items       []uoWebhookLogItem
	next        *uoWebhookLogCursor
	actualBytes int64
}

// Current US administrative authority is a prerequisite, not supplied by the
// webhook ID/revision. All points are from one admitted snapshot. This log
// intentionally never selects a destination, signing secret or event payload.
func readUOWebhookLog(ctx context.Context, query uoOutboxQuery, q uoWebhookLogInput) (out uoWebhookLogPage, err error) {
	if ctx == nil || query == nil || q.webhook == "" || len(q.webhook) > 128 || !utf8.ValidString(q.webhook) || q.authority <= 0 || q.revision <= 0 || q.limit < 1 || q.limit > 100 || q.byteBudget <= 0 || q.byteBudget > 64<<10 {
		return out, ErrInput
	}
	if q.after != nil && (q.after.createdMS < 0 || q.after.id == "" || len(q.after.id) > 128 || !utf8.ValidString(q.after.id)) {
		return out, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	gate, err := query(uoWebhookLogGate, q.webhook, q.authority, q.revision)
	if err != nil {
		return out, err
	}
	if len(gate) != 1 || len(gate[0]) != 1 || gate[0][0] != q.revision {
		return out, ErrDenied
	}
	id := uoWebhookLogFirst
	args := []any{q.webhook, int64(q.limit + 1)}
	if q.after != nil {
		id = uoWebhookLogTail
		args = []any{q.webhook, q.after.createdMS, q.after.id, int64(q.limit + 1)}
	}
	positions, err := query(id, args...)
	if err != nil {
		return out, err
	}
	if len(positions) > q.limit+1 {
		return out, ErrUnqualified
	}
	for n, p := range positions {
		if n == q.limit {
			break
		}
		if len(p) != 7 {
			return uoWebhookLogPage{}, ErrUnqualified
		}
		var v [7]int64
		for i, value := range p {
			n, ok := value.(int64)
			if !ok || n < 0 {
				return uoWebhookLogPage{}, ErrUnqualified
			}
			v[i] = n
		}
		if v[0] <= 0 || v[6] <= 0 || v[2] < 1 || v[2] > 128 || v[3] > 96 || v[4] > 9 || v[5] > 64 {
			return uoWebhookLogPage{}, ErrUnqualified
		}
		bytes := v[2] + v[3] + v[4] + v[5]
		if bytes > q.byteBudget-out.actualBytes {
			if n == 0 {
				return uoWebhookLogPage{}, ErrUnqualified
			}
			break
		}
		rows, err := query(uoWebhookLogPoint, v[0], q.webhook, v[6])
		if err != nil {
			return uoWebhookLogPage{}, err
		}
		if len(rows) != 1 || len(rows[0]) != 12 {
			return uoWebhookLogPage{}, ErrStale
		}
		row := rows[0]
		item := uoWebhookLogItem{}
		for i, dst := range []*string{&item.id, &item.kind, &item.state, &item.errorCode} {
			s, ok := row[i].(string)
			if !ok || int64(len(s)) != v[i+2] || !utf8.ValidString(s) {
				return uoWebhookLogPage{}, ErrStale
			}
			*dst = s
		}
		if item.state != "pending" && item.state != "delivered" && item.state != "failed" {
			return uoWebhookLogPage{}, ErrUnqualified
		}
		for i, dst := range []*int64{&item.attempts, &item.createdMS, &item.finishedMS, &item.nextMS, &item.status, &item.durationMS, &item.revision} {
			n, ok := row[i+4].(int64)
			if !ok || n < 0 {
				return uoWebhookLogPage{}, ErrUnqualified
			}
			*dst = n
		}
		if row[11] != bytes || item.createdMS != v[1] || item.revision != v[6] || item.attempts > 4294967295 || item.status > 599 {
			return uoWebhookLogPage{}, ErrStale
		}
		if q.after != nil && !(item.createdMS < q.after.createdMS || item.createdMS == q.after.createdMS && item.id < q.after.id) {
			return uoWebhookLogPage{}, ErrStale
		}
		if len(out.items) > 0 {
			prev := out.items[len(out.items)-1]
			if !(item.createdMS < prev.createdMS || item.createdMS == prev.createdMS && item.id < prev.id) {
				return uoWebhookLogPage{}, ErrStale
			}
		}
		out.items = append(out.items, item)
		out.actualBytes += bytes
	}
	if len(positions) > len(out.items) && len(out.items) > 0 {
		last := out.items[len(out.items)-1]
		out.next = &uoWebhookLogCursor{createdMS: last.createdMS, id: last.id}
	}
	if err := ctx.Err(); err != nil {
		return uoWebhookLogPage{}, err
	}
	return out, nil
}

const (
	uoWebhookLogGate  stmt.ID = "read.uo.webhook_log.gate"
	uoWebhookLogFirst stmt.ID = "read.uo.webhook_log.first"
	uoWebhookLogTail  stmt.ID = "read.uo.webhook_log.tail"
	uoWebhookLogPoint stmt.ID = "read.uo.webhook_log.point"
)

func uoWebhookLogDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	d := func(id stmt.ID, sql string, params []stmt.Param, p stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: id, Kind: stmt.Read, SQL: sql, Params: params, Owner: "store/read UO webhook log", CallSites: []string{"store/read/uo_webhook_log.go"}, Plan: p}
	}
	base := `SELECT rowid,created_ms,octet_length(id),octet_length(type),octet_length(state),octet_length(error),job_revision FROM webhook_deliveries INDEXED BY webhook_deliveries_log WHERE webhook_id=?`
	return []stmt.Definition{
		d(uoWebhookLogGate, `SELECT revision FROM webhooks WHERE id=? AND authority_generation=? AND revision=?`, []stmt.Param{s("webhook"), i("authority"), i("revision")}, stmt.Plan{Hot: true}),
		d(uoWebhookLogFirst, base+` ORDER BY created_ms DESC,id DESC LIMIT ?`, []stmt.Param{s("webhook"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"webhook_deliveries_log"}}),
		d(uoWebhookLogTail, base+` AND (created_ms,id)<(?,?) ORDER BY created_ms DESC,id DESC LIMIT ?`, []stmt.Param{s("webhook"), i("before_ms"), s("before_id"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"webhook_deliveries_log"}}),
		d(uoWebhookLogPoint, `SELECT id,type,state,error,job_attempts,created_ms,finished_ms,coalesce(job_next_attempt_ms,0),status,duration_ms,job_revision,octet_length(id)+octet_length(type)+octet_length(state)+octet_length(error) FROM webhook_deliveries WHERE rowid=? AND webhook_id=? AND job_revision=?`, []stmt.Param{i("rowid"), s("webhook"), i("revision")}, stmt.Plan{Hot: true, PrimaryKeys: []string{"webhook_deliveries"}}),
	}
}
