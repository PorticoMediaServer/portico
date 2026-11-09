package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoInboxHead            stmt.ID = "read.foreground.uo.inbox.head"
	uoInboxBulk            stmt.ID = "read.foreground.uo.inbox.bulk"
	uoInboxFirstOctets     stmt.ID = "read.foreground.uo.inbox.first_octets"
	uoInboxTailOctets      stmt.ID = "read.foreground.uo.inbox.tail_octets"
	uoInboxPayload         stmt.ID = "read.foreground.uo.inbox.payload"
	uoInboxOperationOctets stmt.ID = "read.foreground.uo.inbox.operation_octets"
	uoInboxOperationPoint  stmt.ID = "read.foreground.uo.inbox.operation_point"
)
const uoInboxOctets = `octet_length(n.id)+octet_length(n.audience)+octet_length(n.severity)+octet_length(n.source)+octet_length(n.category)+octet_length(n.title)+octet_length(n.body)+octet_length(n.arguments)+octet_length(n.actions)+octet_length(n.dedupe_key)`

// Inert metadata only. Capture/EXPLAIN receipts and GV query composition belong
// to integration. Every page seek is forced through notification_records_page;
// the acknowledgement join is the exact (principal_key,notice_sequence) PK and
// the operation point is the notice_broadcast_work PK. No statement scans an
// inbox, enumerates recipients or copies a payload during counting.
func uoInboxDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(id stmt.ID, sql string, params []stmt.Param, indexes ...string) {
		out = append(out, stmt.Definition{ID: id, Owner: "store/read UO inbox", CallSites: []string{"store/read/uo_inbox.go"}, Kind: stmt.Read, SQL: sql, Params: params, Plan: stmt.Plan{Hot: true, Indexes: indexes}})
	}
	add(uoInboxHead, `SELECT revision FROM notification_inbox WHERE scope=? AND audience=?`, []stmt.Param{s("scope"), s("audience")})
	add(uoInboxBulk, `SELECT read_through_sequence,archive_through_sequence,revision,read_through_ms FROM notification_ack_heads WHERE principal_key=?`, []stmt.Param{s("principal")})
	base := `SELECT n.sequence,n.revision,n.authority_revision,n.expires_ms,` + uoInboxOctets + `,n.withdrawn_ms,a.revision,a.read_ms,a.archived_ms FROM notification_records AS n INDEXED BY notification_records_page LEFT JOIN notification_acknowledgments AS a ON a.principal_key=? AND a.notice_sequence=n.sequence WHERE n.scope=? AND n.audience=?`
	add(uoInboxFirstOctets, base+` ORDER BY n.sequence DESC LIMIT ?`, []stmt.Param{s("principal"), s("scope"), s("audience"), i("limit")}, "notification_records_page")
	add(uoInboxTailOctets, base+` AND n.sequence<? ORDER BY n.sequence DESC LIMIT ?`, []stmt.Param{s("principal"), s("scope"), s("audience"), i("before"), i("limit")}, "notification_records_page")
	add(uoInboxPayload, `SELECT n.sequence,n.revision,n.authority_revision,n.created_ms,n.updated_ms,n.expires_ms,n.id,n.audience,n.severity,n.source,n.category,n.title,n.body,n.arguments,n.actions,n.dedupe_key,`+uoInboxOctets+` FROM notification_records AS n INDEXED BY notification_records_page WHERE n.scope=? AND n.audience=? AND n.sequence<=? AND n.sequence>=? ORDER BY n.sequence DESC LIMIT ?`, []stmt.Param{s("scope"), s("audience"), i("upper"), i("lower"), i("limit")}, "notification_records_page")
	add(uoInboxOperationOctets, `SELECT octet_length(upper_account)+octet_length(upper_profile)+octet_length(last_account)+octet_length(ids) FROM notice_broadcast_work WHERE scope=? AND operation_id=?`, []stmt.Param{s("scope"), s("operation")})
	add(uoInboxOperationPoint, `SELECT upper_account,upper_profile,last_account,delivered,created,updated,revision,ids,expires_ms FROM notice_broadcast_work WHERE scope=? AND operation_id=?`, []stmt.Param{s("scope"), s("operation")})
	return out
}
