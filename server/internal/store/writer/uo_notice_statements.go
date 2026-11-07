package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoNoticeReserve          stmt.ID = "uo.notice.reserve_ordinals"
	uoNoticeDedupe           stmt.ID = "uo.notice.dedupe"
	uoNoticeInboxUpsert      stmt.ID = "uo.notice.inbox_upsert"
	uoNoticeInsert           stmt.ID = "uo.notice.insert"
	uoNoticeRestate          stmt.ID = "uo.notice.restate"
	uoNoticeTarget           stmt.ID = "uo.notice.target"
	uoNoticeWithdrawUpdate   stmt.ID = "uo.notice.withdraw"
	uoNoticeBulkRead         stmt.ID = "uo.notice.bulk_read"
	uoNoticeBulkUpsert       stmt.ID = "uo.notice.bulk_upsert"
	uoNoticeUpper            stmt.ID = "uo.notice.upper"
	uoNoticeAckRead          stmt.ID = "uo.notice.ack_read"
	uoNoticeAckUpsert        stmt.ID = "uo.notice.ack_upsert"
	uoNoticeOperationInsert  stmt.ID = "uo.notice.operation_insert"
	uoNoticeExpired          stmt.ID = "uo.notice.expired"
	uoNoticeAckVictims       stmt.ID = "uo.notice.ack_victims"
	uoNoticeAckDelete        stmt.ID = "uo.notice.ack_delete"
	uoNoticeDelete           stmt.ID = "uo.notice.delete"
	uoNoticeOperationExpired stmt.ID = "uo.notice.operation_expired"
	uoNoticeOperationDelete  stmt.ID = "uo.notice.operation_delete"
)

// Inert DRAFT domain metadata. The timestamp column is installed ONLY in
// disposable fixtures until INT adopts UO-001. No shared registry is changed.
func uoNoticeDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	var out []stmt.Definition
	add := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, indexes ...string) {
		out = append(out, stmt.Definition{ID: id, Owner: "store/writer UO notices DRAFT", CallSites: []string{"store/writer/uo_notice.go", "store/writer/uo_notice_reclaim.go"}, Kind: kind, SQL: sql, Params: params, Plan: stmt.Plan{Hot: true, Indexes: indexes}})
	}
	add(uoNoticeReserve, stmt.Write, `INSERT INTO notification_revision(singleton,value) VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET value=value+? WHERE value>=0 AND value<=? RETURNING value`, []stmt.Param{i("initial"), i("count"), i("ceiling")})
	add(uoNoticeDedupe, stmt.Read, `SELECT id,sequence,revision,authority_revision FROM notification_records WHERE scope=? AND audience=? AND source=? AND dedupe_key=?`, []stmt.Param{s("scope"), s("audience"), s("source"), s("dedupe")})
	add(uoNoticeInboxUpsert, stmt.Write, `INSERT INTO notification_inbox(scope,audience,revision,updated_ms) VALUES(?,?,?,?) ON CONFLICT(scope,audience) DO UPDATE SET revision=excluded.revision,updated_ms=excluded.updated_ms RETURNING revision`, []stmt.Param{s("scope"), s("audience"), i("revision"), i("now")})
	add(uoNoticeInsert, stmt.Write, `INSERT INTO notification_records(id,scope,audience,severity,source,category,title,body,arguments,actions,dedupe_key,revision,created_ms,updated_ms,expires_ms,authority_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING sequence`, []stmt.Param{s("id"), s("scope"), s("audience"), s("severity"), s("source"), s("category"), s("title"), s("body"), s("arguments"), s("actions"), s("dedupe"), i("revision"), i("created"), i("updated"), i("expires"), i("authority")})
	add(uoNoticeRestate, stmt.Write, `UPDATE notification_records SET severity=?,category=?,title=?,body=?,arguments=?,actions=?,revision=?,updated_ms=?,expires_ms=?,authority_revision=?,withdrawn_ms=NULL WHERE sequence=? AND revision=? AND authority_revision=? RETURNING sequence`, []stmt.Param{s("severity"), s("category"), s("title"), s("body"), s("arguments"), s("actions"), i("revision"), i("now"), i("expires"), i("authority"), i("sequence"), i("old_revision"), i("old_authority")})
	add(uoNoticeTarget, stmt.Read, `SELECT sequence,revision,authority_revision,expires_ms,withdrawn_ms FROM notification_records WHERE id=? AND scope=? AND audience=?`, []stmt.Param{s("id"), s("scope"), s("audience")})
	add(uoNoticeWithdrawUpdate, stmt.Write, `UPDATE notification_records SET revision=?,updated_ms=?,withdrawn_ms=? WHERE sequence=? AND revision=? AND authority_revision=? AND withdrawn_ms IS NULL RETURNING sequence`, []stmt.Param{i("revision"), i("now"), i("withdrawn"), i("sequence"), i("old_revision"), i("authority")})
	add(uoNoticeBulkRead, stmt.Read, `SELECT read_through_sequence,archive_through_sequence,revision,read_through_ms FROM notification_ack_heads WHERE principal_key=?`, []stmt.Param{s("principal")})
	add(uoNoticeBulkUpsert, stmt.Write, `INSERT INTO notification_ack_heads(principal_key,read_through_sequence,revision,read_through_ms) VALUES(?,?,?,?) ON CONFLICT(principal_key) DO UPDATE SET read_through_sequence=excluded.read_through_sequence,revision=excluded.revision,read_through_ms=excluded.read_through_ms RETURNING revision`, []stmt.Param{s("principal"), i("sequence"), i("revision"), i("now")})
	add(uoNoticeUpper, stmt.Read, `SELECT sequence FROM notification_records INDEXED BY notification_records_page WHERE scope=? AND audience=? ORDER BY sequence DESC LIMIT 1`, []stmt.Param{s("scope"), s("audience")}, "notification_records_page")
	add(uoNoticeAckRead, stmt.Read, `SELECT revision,read_ms,archived_ms FROM notification_acknowledgments WHERE principal_key=? AND notice_sequence=?`, []stmt.Param{s("principal"), i("sequence")})
	add(uoNoticeAckUpsert, stmt.Write, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,read_ms,archived_ms,revision) VALUES(?,?,?,?,?) ON CONFLICT(principal_key,notice_sequence) DO UPDATE SET read_ms=excluded.read_ms,archived_ms=excluded.archived_ms,revision=excluded.revision RETURNING revision`, []stmt.Param{s("principal"), i("sequence"), ni("read"), ni("archive"), i("revision")})
	// No recipient cursor: this record describes ONE committed shared audience.
	// The sentinel distinguishes this draft shape from retained legacy work.
	add(uoNoticeOperationInsert, stmt.Write, `INSERT INTO notice_broadcast_work(scope,operation_id,digest,upper_account,upper_profile,last_account,delivered,created,updated,revision,ids,expires_ms) VALUES(?,?,?,'uo.shared.v1',?,'complete',1,?,?,?,?,?) RETURNING revision`, []stmt.Param{s("scope"), s("operation"), s("digest"), s("audience"), i("created"), i("updated"), i("revision"), s("ids"), i("expires")})
	add(uoNoticeExpired, stmt.Read, `SELECT sequence,revision FROM notification_records INDEXED BY notification_records_expiry WHERE expires_ms<=? ORDER BY expires_ms,sequence LIMIT 1`, []stmt.Param{i("now")}, "notification_records_expiry")
	add(uoNoticeAckVictims, stmt.Read, `SELECT principal_key FROM notification_acknowledgments INDEXED BY notification_ack_notice WHERE notice_sequence=? ORDER BY principal_key LIMIT 17`, []stmt.Param{i("sequence")}, "notification_ack_notice")
	add(uoNoticeAckDelete, stmt.Write, `DELETE FROM notification_acknowledgments WHERE principal_key=? AND notice_sequence=? RETURNING notice_sequence`, []stmt.Param{s("principal"), i("sequence")})
	add(uoNoticeDelete, stmt.Write, `DELETE FROM notification_records WHERE sequence=? AND revision=? AND expires_ms<=? RETURNING sequence`, []stmt.Param{i("sequence"), i("revision"), i("now")})
	add(uoNoticeOperationExpired, stmt.Read, `SELECT scope,operation_id FROM notice_broadcast_work INDEXED BY notice_broadcast_work_expiry WHERE expires_ms<=? ORDER BY expires_ms,scope,operation_id LIMIT 1`, []stmt.Param{i("now")}, "notice_broadcast_work_expiry")
	add(uoNoticeOperationDelete, stmt.Write, `DELETE FROM notice_broadcast_work WHERE scope=? AND operation_id=? AND expires_ms<=? RETURNING revision`, []stmt.Param{s("scope"), s("operation"), i("now")})
	return out
}
