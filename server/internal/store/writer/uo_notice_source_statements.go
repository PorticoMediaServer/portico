package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoNoticeSourceRecord   stmt.ID = "uo.notice.source_record"
	uoNoticeWithdrawTarget stmt.ID = "uo.notice.withdraw_target"
)

// Inert source-side statements over the adopted personal schema. The unique
// (scope,audience,source,dedupe_key) identity is the only way one producer
// record is located, so another inbox is never visible and no recipient is
// enumerated. These are native command steps, not a registered production
// command or a caller-supplied SQL surface.
func uoNoticeSourceDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	def := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, indexes ...string) stmt.Definition {
		return stmt.Definition{ID: id, Owner: "store/writer UO notices", Kind: kind, SQL: sql, Params: params,
			CallSites: []string{"store/writer/uo_notice_source.go:loadUONoticeSourceRecord", "store/writer/uo_notice_publish.go:applyUONoticeWithdraw"},
			Plan:      stmt.Plan{Hot: true, Indexes: indexes}}
	}
	return []stmt.Definition{
		def(uoNoticeSourceRecord, stmt.Read, `SELECT id,sequence,revision,authority_revision,severity,category,title,body,arguments,actions,dedupe_key,expires_ms,withdrawn_ms FROM notification_records WHERE scope=? AND audience=? AND source=? AND dedupe_key=?`,
			[]stmt.Param{s("scope"), s("audience"), s("source"), s("dedupe")}),
		def(uoNoticeWithdrawTarget, stmt.Read, `SELECT sequence,revision,authority_revision,expires_ms,withdrawn_ms FROM notification_records WHERE id=? AND scope=? AND audience=?`,
			[]stmt.Param{s("id"), s("scope"), s("audience")}),
	}
}
