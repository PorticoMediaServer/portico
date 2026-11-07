package writer

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	ipPolicyOwner           stmt.ID = "ip.policy.owner"
	ipPolicyConsent         stmt.ID = "ip.policy.consent"
	ipPolicyOctets          stmt.ID = "ip.policy.octets"
	ipPolicyCurrent         stmt.ID = "ip.policy.current"
	ipPolicyUpdate          stmt.ID = "ip.policy.update"
	ipPolicyReceiptIdentity stmt.ID = "ip.policy.receipt_identity"
	ipPolicyReceiptOctets   stmt.ID = "ip.policy.receipt_octets"
	ipPolicyReceiptResult   stmt.ID = "ip.policy.receipt_result"
	ipPolicyReceiptInsert   stmt.ID = "ip.policy.receipt_insert"
)

// Compact parameter declarations describe only these closed, literal SQL
// shapes. No caller-supplied identifier, SQL fragment or registry is accepted.
// These inert definitions still require stmt.New with Capture receipts in the
// outer dispatcher. Tests use stmttest's explicitly unqualified receipts.
// Qualification targets: ip_policy_set (200), ip_refresh_request (201), and
// ip_refresh_page / ip_refresh_finish_entities (202), each fresh/no-op/replay
// and error class. NORMAL targets p99 <=10ms, max <=25ms; rare refusals <=100ms.
// No fixture timing or native EQP smoke result qualifies any of these targets.
func ipPolicyDefinition(id stmt.ID, kind stmt.Kind, query, types string) stmt.Definition {
	d := stmt.Definition{ID: id, Kind: kind, SQL: query, Owner: "IP", CallSites: []string{"writer/ip_policy_draft.go", "writer/ip_refresh_draft.go"}}
	var args []any
	for i, c := range types {
		p := stmt.Param{Name: fmt.Sprintf("p%d", i)}
		switch c {
		case 'i':
			p.Type = stmt.Integer
			args = append(args, int64(1))
		case 't':
			p.Type = stmt.Text
			args = append(args, "tmdb")
		case 'b':
			p.Type = stmt.Blob
			args = append(args, make([]byte, 16))
		case 'I':
			p.Type = stmt.Integer
			p.Nullable = true
			args = append(args, nil)
		case 'T':
			p.Type = stmt.Text
			p.Nullable = true
			args = append(args, nil)
		default:
			panic("invalid compiled IP parameter")
		}
		d.Params = append(d.Params, p)
	}
	d.Cohorts = []stmt.Cohort{{Name: "draft-native-smoke", Bindings: args}}
	return d
}
func ipPolicyDefinitions() []stmt.Definition {
	return []stmt.Definition{
		ipPolicyDefinition(ipPolicyOwner, stmt.Read, `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM metadata_provider_policy WHERE library_id=? AND provider=?`, "it"),
		ipPolicyDefinition(ipPolicyConsent, stmt.Read, `SELECT revision,confirmed FROM screen_metadata_consent WHERE singleton=1`, ""),
		ipPolicyDefinition(ipPolicyOctets, stmt.Read, `SELECT octet_length(region)+octet_length(algorithm)+octet_length(refresh_mode)+coalesce(octet_length(credit_after_provider),0) FROM metadata_provider_policy WHERE library_id=? AND provider=?`, "it"),
		ipPolicyDefinition(ipPolicyCurrent, stmt.Read, `SELECT incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider FROM metadata_provider_policy WHERE library_id=? AND provider=?`, "it"),
		ipPolicyDefinition(ipPolicyUpdate, stmt.Write, `UPDATE metadata_provider_policy SET revision=?,enabled=?,priority=?,region=?,algorithm=?,refresh_mode=?,credit_enabled=?,last_command_sequence=? WHERE library_id=? AND provider=? AND incarnation=? AND revision=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?`, "iiitttiiitbibii"),
		ipPolicyDefinition(ipPolicyReceiptIdentity, stmt.Read, `SELECT operation_id,request_digest,result_digest FROM metadata_policy_receipts WHERE library_id=? AND provider=? AND command_incarnation=? AND command_sequence=?`, "itbi"),
		ipPolicyDefinition(ipPolicyReceiptOctets, stmt.Read, `SELECT octet_length(region)+octet_length(algorithm)+octet_length(refresh_mode)+coalesce(octet_length(credit_after_provider),0) FROM metadata_policy_receipts WHERE library_id=? AND provider=? AND command_incarnation=? AND command_sequence=?`, "itbi"),
		ipPolicyDefinition(ipPolicyReceiptResult, stmt.Read, `SELECT policy_incarnation,accepted_revision,applied_ms,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider FROM metadata_policy_receipts WHERE library_id=? AND provider=? AND command_incarnation=? AND command_sequence=?`, "itbi"),
		ipPolicyDefinition(ipPolicyReceiptInsert, stmt.Write, `INSERT INTO metadata_policy_receipts(library_id,provider,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,policy_incarnation,accepted_revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "itbibbibbiiitttiIT"),
	}
}
