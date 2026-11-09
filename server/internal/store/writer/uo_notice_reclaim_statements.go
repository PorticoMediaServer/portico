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

// The record byte expressions are the exact variable TEXT a physical delete
// removes; fixed row/table overhead is never refunded to any octet budget.
const (
	uoNoticeReclaimAckOctets       = `length(principal_key)`
	uoNoticeReclaimRecordOctets    = `length(id)+length(scope)+length(audience)+length(severity)+length(source)+length(category)+length(title)+length(body)+length(arguments)+length(actions)+length(dedupe_key)`
	uoNoticeReclaimOperationOctets = `length(scope)+length(operation_id)+length(digest)+length(upper_account)+length(upper_profile)+length(last_account)+length(ids)`
)

// Inert maintenance statements over the adopted personal schema. The expiry
// seek is forced through notification_records_expiry, the ACK victim probe
// through notification_ack_notice, and expired operations through
// notice_broadcast_work_expiry. Each delete is one exact row or one exact
// (principal,notice) child and returns the bytes it actually removed. No
// statement enumerates an inbox, a recipient or a whole table.
func uoNoticeReclaimDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	def := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, indexes ...string) stmt.Definition {
		return stmt.Definition{ID: id, Owner: "store/writer UO notice reclaim", Kind: kind, SQL: sql, Params: params,
			CallSites: []string{"store/writer/uo_notice_reclaim.go"}, Plan: stmt.Plan{Hot: true, Indexes: indexes}}
	}
	return []stmt.Definition{
		def(uoNoticeReclaimExpired, stmt.Read,
			`SELECT sequence,revision FROM notification_records INDEXED BY notification_records_expiry WHERE expires_ms<=? ORDER BY expires_ms,sequence LIMIT 1`,
			[]stmt.Param{i("now")}, "notification_records_expiry"),
		// Bounded probe: 16 principals plus one sentinel row. The caller deletes
		// at most 16 and the sentinel proves more ACKs remain.
		def(uoNoticeReclaimAckVictims, stmt.Read,
			`SELECT principal_key FROM notification_acknowledgments INDEXED BY notification_ack_notice WHERE notice_sequence=? ORDER BY principal_key LIMIT 17`,
			[]stmt.Param{i("sequence")}, "notification_ack_notice"),
		def(uoNoticeReclaimAckDelete, stmt.Write,
			`DELETE FROM notification_acknowledgments WHERE principal_key=? AND notice_sequence=? RETURNING `+uoNoticeReclaimAckOctets,
			[]stmt.Param{s("principal"), i("sequence")}),
		def(uoNoticeReclaimRecordDelete, stmt.Write,
			`DELETE FROM notification_records WHERE sequence=? AND revision=? AND expires_ms<=? RETURNING `+uoNoticeReclaimRecordOctets,
			[]stmt.Param{i("sequence"), i("revision"), i("now")}),
		def(uoNoticeReclaimOperationExpired, stmt.Read,
			`SELECT scope,operation_id FROM notice_broadcast_work INDEXED BY notice_broadcast_work_expiry WHERE expires_ms<=? ORDER BY expires_ms,scope,operation_id LIMIT 1`,
			[]stmt.Param{i("now")}, "notice_broadcast_work_expiry"),
		def(uoNoticeReclaimOperationDelete, stmt.Write,
			`DELETE FROM notice_broadcast_work WHERE scope=? AND operation_id=? AND expires_ms<=? RETURNING `+uoNoticeReclaimOperationOctets,
			[]stmt.Param{s("scope"), s("operation"), i("now")}),
	}
}
