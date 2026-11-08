package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoNoticeAckHeadRead   stmt.ID = "uo.notice.ack.head_read"
	uoNoticeAckHeadUpsert stmt.ID = "uo.notice.ack.head_upsert"
	uoNoticeAckUpper      stmt.ID = "uo.notice.ack.upper"
	uoNoticeAckTargetRead stmt.ID = "uo.notice.ack.target_read"
	uoNoticeAckRowRead    stmt.ID = "uo.notice.ack.row_read"
	uoNoticeAckRowUpsert  stmt.ID = "uo.notice.ack.row_upsert"
)

// Inert acknowledgement statements over the adopted personal schema. Every
// access is an exact primary-key seek for one principal: notification_ack_heads
// is keyed by principal_key, notification_acknowledgments by
// (principal_key,notice_sequence), and the inbox bound walks
// notification_records_page by (scope,audience,sequence DESC). No statement
// enumerates a principal, a recipient or another inbox.
//
// The head upsert advances read_through_sequence and read_through_ms with
// MAX so a stale or repeated action can never regress a read-through, while a
// new action still takes a newer ordinal and therefore covers an earlier
// explicit unread. archive_through_sequence stays zero: there is no archive-all
// action in this contract, and the merged read rule never interprets one.
func uoNoticeAckDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	def := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, indexes ...string) stmt.Definition {
		return stmt.Definition{ID: id, Owner: "store/writer UO notice acknowledgements", Kind: kind, SQL: sql, Params: params,
			CallSites: []string{"store/writer/uo_notice_ack.go"}, Plan: stmt.Plan{Hot: true, Indexes: indexes}}
	}
	definitions := []stmt.Definition{
		def(uoNoticeAckHeadRead, stmt.Read,
			`SELECT read_through_sequence,archive_through_sequence,revision,read_through_ms FROM notification_ack_heads WHERE principal_key=?`,
			[]stmt.Param{s("principal")}),
		def(uoNoticeAckHeadUpsert, stmt.Write,
			`INSERT INTO notification_ack_heads(principal_key,read_through_sequence,revision,read_through_ms) VALUES(?,?,?,?) ON CONFLICT(principal_key) DO UPDATE SET read_through_sequence=MAX(notification_ack_heads.read_through_sequence,excluded.read_through_sequence),revision=excluded.revision,read_through_ms=MAX(notification_ack_heads.read_through_ms,excluded.read_through_ms) RETURNING revision`,
			[]stmt.Param{s("principal"), i("sequence"), i("revision"), i("read")}),
		def(uoNoticeAckUpper, stmt.Read,
			`SELECT sequence FROM notification_records INDEXED BY notification_records_page WHERE scope=? AND audience=? ORDER BY sequence DESC LIMIT 1`,
			[]stmt.Param{s("scope"), s("audience")}, "notification_records_page"),
		def(uoNoticeAckTargetRead, stmt.Read,
			`SELECT sequence,revision,authority_revision,expires_ms,withdrawn_ms FROM notification_records WHERE id=? AND scope=? AND audience=?`,
			[]stmt.Param{s("id"), s("scope"), s("audience")}),
		def(uoNoticeAckRowRead, stmt.Read,
			`SELECT revision,read_ms,archived_ms FROM notification_acknowledgments WHERE principal_key=? AND notice_sequence=?`,
			[]stmt.Param{s("principal"), i("sequence")}),
		def(uoNoticeAckRowUpsert, stmt.Write,
			`INSERT INTO notification_acknowledgments(principal_key,notice_sequence,read_ms,archived_ms,revision) VALUES(?,?,?,?,?) ON CONFLICT(principal_key,notice_sequence) DO UPDATE SET read_ms=excluded.read_ms,archived_ms=excluded.archived_ms,revision=excluded.revision RETURNING revision`,
			[]stmt.Param{s("principal"), i("sequence"), ni("read"), ni("archive"), i("revision")}),
	}
	// The merged publish/withdraw statements supply the shared ordinal
	// allocator (notification_revision) and the notice source shape this
	// component reads, so one native turn registers exactly one catalogue.
	return append(uoNoticePublishDefinitions(), definitions...)
}
