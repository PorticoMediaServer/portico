package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoNoticeReserve         stmt.ID = "uo.notice.publish.reserve_ordinals"
	uoNoticeInboxUpsert     stmt.ID = "uo.notice.publish.inbox_upsert"
	uoNoticeInsert          stmt.ID = "uo.notice.publish.insert"
	uoNoticeRestate         stmt.ID = "uo.notice.publish.restate"
	uoNoticeWithdrawUpdate  stmt.ID = "uo.notice.publish.withdraw"
	uoNoticeOperationInsert stmt.ID = "uo.notice.publish.operation"
)

// Inert publish/withdraw statements over the adopted personal schema. One
// inbox head row is upserted per (scope,audience); the notification_revision
// singleton allocates the mutation ordinals. No statement enumerates an inbox
// and the record identity is the unique source dedupe key.
func uoNoticePublishDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	def := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Owner: "store/writer UO notices", Kind: kind, SQL: sql, Params: params,
			CallSites: []string{"store/writer/uo_notice_publish.go"}, Plan: stmt.Plan{Hot: true}}
	}
	definitions := []stmt.Definition{
		def(uoNoticeReserve, stmt.Write,
			`INSERT INTO notification_revision(singleton,value) VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET value=value+? WHERE value>=0 AND value<=? RETURNING value`,
			[]stmt.Param{i("initial"), i("count"), i("ceiling")}),
		def(uoNoticeInboxUpsert, stmt.Write,
			`INSERT INTO notification_inbox(scope,audience,revision,updated_ms) VALUES(?,?,?,?) ON CONFLICT(scope,audience) DO UPDATE SET revision=excluded.revision,updated_ms=excluded.updated_ms RETURNING revision`,
			[]stmt.Param{s("scope"), s("audience"), i("revision"), i("now")}),
		def(uoNoticeInsert, stmt.Write,
			`INSERT INTO notification_records(id,scope,audience,severity,source,category,title,body,arguments,actions,dedupe_key,revision,created_ms,updated_ms,expires_ms,authority_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING sequence`,
			[]stmt.Param{s("id"), s("scope"), s("audience"), s("severity"), s("source"), s("category"), s("title"), s("body"), s("arguments"), s("actions"), s("dedupe"), i("revision"), i("created"), i("updated"), i("expires"), i("authority")}),
		def(uoNoticeRestate, stmt.Write,
			`UPDATE notification_records SET severity=?,category=?,title=?,body=?,arguments=?,actions=?,revision=?,updated_ms=?,expires_ms=?,authority_revision=?,withdrawn_ms=NULL WHERE sequence=? AND revision=? AND authority_revision=? RETURNING sequence`,
			[]stmt.Param{s("severity"), s("category"), s("title"), s("body"), s("arguments"), s("actions"), i("revision"), i("now"), i("expires"), i("authority"), i("sequence"), i("old_revision"), i("old_authority")}),
		def(uoNoticeWithdrawUpdate, stmt.Write,
			`UPDATE notification_records SET revision=?,updated_ms=?,withdrawn_ms=? WHERE sequence=? AND revision=? AND authority_revision=? AND withdrawn_ms IS NULL RETURNING sequence`,
			[]stmt.Param{i("revision"), i("now"), i("withdrawn"), i("sequence"), i("old_revision"), i("authority")}),
		// One shared operation marker: a sentinel upper bound and the completed
		// flag describe this single shared audience record. There is no
		// recipient cursor and no per-admin row, so nothing here can fan out.
		def(uoNoticeOperationInsert, stmt.Write,
			`INSERT INTO notice_broadcast_work(scope,operation_id,digest,upper_account,upper_profile,last_account,delivered,created,updated,revision,ids,expires_ms) VALUES(?,?,?,'uo.shared.v1',?,'complete',1,?,?,?,?,?) RETURNING revision`,
			[]stmt.Param{s("scope"), s("operation"), s("digest"), s("audience"), i("created"), i("updated"), i("revision"), s("ids"), i("expires")}),
	}
	return append(uoNoticeSourceDefinitions(), definitions...)
}
