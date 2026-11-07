package read

import (
	"context"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These four compiled adapters feed the existing governor. No client selects a
// table or priority, and no independent scheduler or timer is created here.
type uoReadyFamily uint8

const (
	uoReadyPersonal uoReadyFamily = 1
	uoReadyWebhook  uoReadyFamily = 2
	uoReadyNotice   uoReadyFamily = 3
	uoReadyService  uoReadyFamily = 4
)

type uoJobReadShape struct{ table, index, key1, key2, revision string }

func uoReadyShape(f uoReadyFamily) (uoJobReadShape, bool) {
	switch f {
	case uoReadyPersonal:
		return uoJobReadShape{"personal_jobs", "personal_jobs_governor_due", "id", "", "revision"}, true
	case uoReadyWebhook:
		return uoJobReadShape{"webhook_deliveries", "webhook_deliveries_governor_due", "id", "", "job_revision"}, true
	case uoReadyNotice:
		return uoJobReadShape{"notice_broadcast_work", "notice_broadcast_work_governor_due", "scope", "operation_id", "revision"}, true
	case uoReadyService:
		return uoJobReadShape{"identity_service_outbox", "identity_service_outbox_governor_due", "notice_id", "", "job_revision"}, true
	default:
		return uoJobReadShape{}, false
	}
}

type uoReadyJob struct {
	family                                                  uoReadyFamily
	key1, key2                                              string
	dueMS, owner, generation, expiresMS, attempts, revision int64
	nextMS                                                  int64
	runnable                                                bool
}

func uoReadyID(f uoReadyFamily, part string) stmt.ID {
	return stmt.ID("read.uo.jobs." + string(rune('0'+f)) + "." + part)
}

// At most one ready candidate per call. Native key lengths and scalar lease
// facts are read before key text, in the SAME admitted snapshot. The indexed
// total order includes rowid, or the WITHOUT ROWID primary key, to resolve equal
// deadlines. The writer still performs current authority and exact lease CAS.
func readUONextJob(ctx context.Context, query uoOutboxQuery, f uoReadyFamily, nowMS int64) (out uoReadyJob, found bool, err error) {
	if ctx == nil || query == nil || nowMS <= 0 {
		return out, false, ErrInput
	}
	s, ok := uoReadyShape(f)
	if !ok {
		return out, false, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return out, false, err
	}
	rows, err := query(uoReadyID(f, "preflight"), nowMS)
	if err != nil {
		return out, false, err
	}
	if len(rows) == 0 {
		return out, false, nil
	}
	if len(rows) != 1 || len(rows[0]) != 9 {
		return out, false, ErrUnqualified
	}
	var values [9]int64
	for i, v := range rows[0] {
		n, ok := v.(int64)
		if !ok || n < 0 {
			return out, false, ErrUnqualified
		}
		values[i] = n
	}
	if values[0] < 1 || values[0] > 128 || values[1] > 128 || (s.key2 != "") != (values[1] > 0) || values[2] > nowMS || values[3] != 0 && values[5] == 0 || values[5] > nowMS || values[6] > 4294967295 || values[8] > nowMS {
		return out, false, ErrUnqualified
	}
	keys, err := query(uoReadyID(f, "keys"), nowMS)
	if err != nil {
		return out, false, err
	}
	if len(keys) != 1 || len(keys[0]) != 2 {
		return out, false, ErrStale
	}
	key1, ok1 := keys[0][0].(string)
	key2, ok2 := keys[0][1].(string)
	if !ok1 || !ok2 || int64(len(key1)) != values[0] || int64(len(key2)) != values[1] || !utf8.ValidString(key1) || !utf8.ValidString(key2) {
		return out, false, ErrStale
	}
	if err := ctx.Err(); err != nil {
		return out, false, err
	}
	return uoReadyJob{family: f, key1: key1, key2: key2, dueMS: values[2], owner: values[3], generation: values[4], expiresMS: values[5], attempts: values[6], revision: values[7], nextMS: values[8], runnable: true}, true, nil
}

// Point status contains fixed scalar facts only; secret payloads, actor names,
// paths and arbitrary error text are never part of this administrative view.
func readUOJobStatus(ctx context.Context, query uoOutboxQuery, f uoReadyFamily, key1, key2 string) (out uoReadyJob, err error) {
	s, ok := uoReadyShape(f)
	if !ok || ctx == nil || query == nil || key1 == "" || len(key1) > 128 || len(key2) > 128 || !utf8.ValidString(key1) || !utf8.ValidString(key2) || (s.key2 != "") != (key2 != "") {
		return out, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	args := []any{key1}
	if s.key2 != "" {
		args = append(args, key2)
	}
	rows, err := query(uoReadyID(f, "status"), args...)
	if err != nil {
		return out, err
	}
	if len(rows) != 1 || len(rows[0]) != 8 {
		return out, ErrDenied
	}
	var v [8]int64
	for i, value := range rows[0] {
		n, ok := value.(int64)
		if !ok || n < 0 {
			return out, ErrUnqualified
		}
		v[i] = n
	}
	if v[4] > 4294967295 || v[6] > 1 || v[6] == 0 && v[7] != 0 {
		return out, ErrUnqualified
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return uoReadyJob{family: f, key1: key1, key2: key2, dueMS: v[0], owner: v[1], generation: v[2], expiresMS: v[3], attempts: v[4], revision: v[5], nextMS: v[7], runnable: v[6] == 1}, nil
}

func uoJobReadDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, f := range []uoReadyFamily{uoReadyPersonal, uoReadyWebhook, uoReadyNotice, uoReadyService} {
		s, _ := uoReadyShape(f)
		key2 := "''"
		order := "job_due_ms,rowid"
		where := s.key1 + "=?"
		params := []stmt.Param{{Name: "key1", Type: stmt.Text}}
		if s.key2 != "" {
			key2 = s.key2
			order = "job_due_ms," + s.key1 + "," + s.key2
			where += " AND " + s.key2 + "=?"
			params = append(params, stmt.Param{Name: "key2", Type: stmt.Text})
		}
		lease := "job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts," + s.revision
		suffix := " FROM " + s.table + " INDEXED BY " + s.index + " WHERE job_due_ms IS NOT NULL AND job_due_ms<=? ORDER BY " + order + " LIMIT 1"
		d := func(id stmt.ID, sql string, p []stmt.Param, plan stmt.Plan) stmt.Definition {
			return stmt.Definition{ID: id, Kind: stmt.Read, Owner: "store/read UO jobs", SQL: sql, Params: p, CallSites: []string{"store/read/uo_jobs.go"}, Plan: plan}
		}
		dueParams := []stmt.Param{{Name: "now", Type: stmt.Integer}}
		out = append(out, d(uoReadyID(f, "preflight"), "SELECT octet_length("+s.key1+"),octet_length("+key2+"),job_due_ms,"+lease+",job_next_attempt_ms"+suffix, dueParams, stmt.Plan{Hot: true, Indexes: []string{s.index}}))
		out = append(out, d(uoReadyID(f, "keys"), "SELECT "+s.key1+","+key2+suffix, dueParams, stmt.Plan{Hot: true, Indexes: []string{s.index}}))
		out = append(out, d(uoReadyID(f, "status"), "SELECT coalesce(job_due_ms,0),"+lease+",(job_next_attempt_ms IS NOT NULL),coalesce(job_next_attempt_ms,0) FROM "+s.table+" WHERE "+where, params, stmt.Plan{Hot: true}))
	}
	return out
}
