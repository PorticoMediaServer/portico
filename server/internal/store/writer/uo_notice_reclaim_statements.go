package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoNoticeReclaimExpired          stmt.ID = "uo.notice.reclaim.expired"
	uoNoticeReclaimAckVictims       stmt.ID = "uo.notice.reclaim.ack_victims"
	uoNoticeReclaimAckDelete        stmt.ID = "uo.notice.reclaim.ack_delete"
	uoNoticeReclaimRecordDelete     stmt.ID = "uo.notice.reclaim.record_delete"
	uoNoticeReclaimOperationExpired stmt.ID = "uo.notice.reclaim.operation_expired"
	uoNoticeReclaimOperationDelete  stmt.ID = "uo.notice.reclaim.operation_delete"
)

// The refund is the sum of the variable TEXT columns physically returned by
// the delete, computed by the caller in Go. Fixed row/table overhead is never
// refunded to any octet budget, and no column maximum stands in for an actual
// deleted value.
const (
	uoNoticeReclaimAckColumns       = 1 // principal_key
	uoNoticeReclaimRecordColumns    = 11
	uoNoticeReclaimOperationColumns = 8
)

const (
	uoNoticeReclaimAckReturning       = `principal_key`
	uoNoticeReclaimRecordReturning    = `id,scope,audience,severity,source,category,title,body,arguments,actions,dedupe_key`
	uoNoticeReclaimOperationReturning = `scope,operation_id,digest,upper_account,upper_profile,last_account,last_profile,ids`
)

// Inert maintenance statements over the adopted personal schema. The expiry
// seek is forced through notification_records_expiry, the ACK victim probe
// through notification_ack_notice, and expired operations through
// notice_broadcast_work_expiry. Each delete is one exact row or one exact
// (principal,notice) child and returns the actual TEXT values it removed so the
// caller can refund their exact bytes. No statement enumerates an inbox, a
// recipient or a whole table.
func uoNoticeReclaimDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	def := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, plan stmt.Plan, cohort stmt.Cohort) stmt.Definition {
		return stmt.Definition{ID: id, Owner: "store/writer UO notice reclaim", Kind: kind, SQL: sql, Params: params,
			CallSites: []string{"store/writer/uo_notice_reclaim.go"}, Plan: plan,
			Cohorts: []stmt.Cohort{cohort}}
	}
	return []stmt.Definition{
		def(uoNoticeReclaimExpired, stmt.Read,
			`SELECT sequence,revision FROM notification_records INDEXED BY notification_records_expiry WHERE expires_ms<=? ORDER BY expires_ms,sequence LIMIT 1`,
			[]stmt.Param{i("now")},
			stmt.Plan{Hot: true, Indexes: []string{"notification_records_expiry"}, Seeks: []string{"expires_ms<?"}},
			stmt.Cohort{Name: "expired", Bindings: []any{int64(100)}}),
		// Bounded probe: 16 principals plus one sentinel row. The caller deletes
		// at most 16 and the sentinel proves more ACKs remain.
		def(uoNoticeReclaimAckVictims, stmt.Read,
			`SELECT principal_key FROM notification_acknowledgments INDEXED BY notification_ack_notice WHERE notice_sequence=? ORDER BY principal_key LIMIT 17`,
			[]stmt.Param{i("sequence")},
			stmt.Plan{Hot: true, Indexes: []string{"notification_ack_notice"}, Seeks: []string{"notice_sequence=?"}},
			stmt.Cohort{Name: "ack_victim", Bindings: []any{int64(1)}}),
		def(uoNoticeReclaimAckDelete, stmt.Write,
			`DELETE FROM notification_acknowledgments WHERE principal_key=? AND notice_sequence=? RETURNING `+uoNoticeReclaimAckReturning,
			[]stmt.Param{s("principal"), i("sequence")},
			stmt.Plan{Hot: true, PrimaryKeys: []string{"notification_acknowledgments"}, Seeks: []string{"principal_key=? AND notice_sequence=?"}},
			stmt.Cohort{Name: "ack_principal", Bindings: []any{"p000", int64(1)}}),
		def(uoNoticeReclaimRecordDelete, stmt.Write,
			`DELETE FROM notification_records WHERE sequence=? AND revision=? AND expires_ms<=? RETURNING `+uoNoticeReclaimRecordReturning,
			[]stmt.Param{i("sequence"), i("revision"), i("now")},
			stmt.Plan{Hot: true, PrimaryKeys: []string{"notification_records"}, Seeks: []string{"rowid=?"}},
			stmt.Cohort{Name: "expired_record", Bindings: []any{int64(1), int64(1), int64(100)}}),
		def(uoNoticeReclaimOperationExpired, stmt.Read,
			`SELECT scope,operation_id FROM notice_broadcast_work INDEXED BY notice_broadcast_work_expiry WHERE expires_ms<=? ORDER BY expires_ms,scope,operation_id LIMIT 1`,
			[]stmt.Param{i("now")},
			stmt.Plan{Hot: true, Indexes: []string{"notice_broadcast_work_expiry"}, Seeks: []string{"expires_ms<?"}},
			stmt.Cohort{Name: "expired_operation", Bindings: []any{int64(100)}}),
		def(uoNoticeReclaimOperationDelete, stmt.Write,
			`DELETE FROM notice_broadcast_work WHERE scope=? AND operation_id=? AND expires_ms<=? RETURNING `+uoNoticeReclaimOperationReturning,
			[]stmt.Param{s("scope"), s("operation"), i("now")},
			stmt.Plan{Hot: true, PrimaryKeys: []string{"notice_broadcast_work"}, Seeks: []string{"scope=? AND operation_id=?"}},
			stmt.Cohort{Name: "expired_operation_key", Bindings: []any{"shared:admins", "op", int64(100)}}),
	}
}
