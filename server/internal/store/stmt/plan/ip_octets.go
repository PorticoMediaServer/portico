package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IP's private source/history byte shapes. Each entry pins the exact statement
// ID and SQL that may use the native octet_length builtin, the closed integer/
// text parameter layout, the table whose columns the audit reads, and the exact
// indexed SEARCH detail expected for every bound cohort. Registration is inert;
// Capture still requires the live native function/key/EQP audits.
type ipOctetShape struct {
	id     string
	sql    string
	table  string
	params []stmt.Param
	eqp    []string
}

func ipInteger(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
func ipText(n string) stmt.Param    { return stmt.Param{Name: n, Type: stmt.Text} }

func registerIPOctet(s ipOctetShape) {
	if s.id == "" || s.sql == "" || s.table == "" || len(s.params) == 0 || len(s.eqp) == 0 {
		panic("plan: incomplete IP octet shape")
	}
	id := stmt.ID(s.id)
	pointOctets.add(octetShape{
		id:       id,
		sql:      s.sql,
		mismatch: "IP octet points require the exact closed definition and indexed SEARCH audit",
		definition: func(d stmt.Definition) bool {
			if d.ID != id || d.SQL != s.sql || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(s.params) || len(d.Cohorts) < 2 {
				return false
			}
			for n, p := range s.params {
				if d.Params[n].Name != p.Name || d.Params[n].Type != p.Type || d.Params[n].Nullable {
					return false
				}
			}
			return true
		},
		audit: func(ctx context.Context, conn *sql.Conn) error { return auditIPOctets(ctx, conn, s.table) },
		point: func(rows Rows) error {
			if len(rows.Values) != len(s.eqp) {
				return fmt.Errorf("IP octet point requires %d indexed searches, got %d", len(s.eqp), len(rows.Values))
			}
			for n, row := range rows.Values {
				if len(row) != 4 || row[3] != s.eqp[n] {
					return fmt.Errorf("IP octet point requires exact search %q", s.eqp[n])
				}
			}
			return nil
		},
	})
}

// auditIPOctets proves octet_length is the unshadowed native builtin and that the
// expected table/columns actually exist in the embedded schema. It grants no SQL,
// row or registration capability.
func auditIPOctets(ctx context.Context, conn *sql.Conn, table string) error {
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("IP octets: invalid native functions")
		}
		name, ok := row[0].(string)
		arity, aok := row[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("IP octets: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("IP octets: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("IP octets: native octet_length missing")
	}
	columns, err := query(ctx, conn, "PRAGMA main.table_xinfo("+table+")")
	if err != nil {
		return err
	}
	if len(columns.Values) == 0 {
		return fmt.Errorf("IP octets: missing table %s", table)
	}
	return nil
}

// The exact IP read-side octet SQL is duplicated here; Capture compares the
// registered shape to the executed definition, so drift cannot authorize a
// different statement. This package does not import the harness read package.
const ipPolicyTextOctetsSQL = `SELECT octet_length(pol.algorithm),octet_length(pol.refresh_mode),octet_length(pol.region)
FROM metadata_provider_policy AS pol
WHERE pol.library_id=? AND pol.provider=?`

const ipHeadOctetsSQL = `SELECT octet_length(e.public_id),octet_length(e.title),octet_length(sp.provider),octet_length(sp.role),octet_length(sp.subject_kind),octet_length(sp.provider_id),octet_length(sp.requested_id),octet_length(sp.observed_at),octet_length(p.id),octet_length(p.decision),octet_length(p.observed_at),octet_length(p.operation_id),octet_length(ev.id),octet_length(ev.parser_version),octet_length(ev.digest),octet_length(ev.observed_at),octet_length(ev.ip_seal_owner),octet_length(ev.ip_seal_incarnation),octet_length(ev.release_group_id),octet_length(ev.primary_type),octet_length(op.command_incarnation),octet_length(op.status),octet_length(op.policy_incarnation),octet_length(op.algorithm),octet_length(op.refresh_mode),octet_length(rc.command_kind),octet_length(rc.evidence_subject_kind),octet_length(rc.evidence_provider_id)
FROM catalog_entities AS e
CROSS JOIN metadata_subject_providers AS sp ON sp.entity_id=e.id AND sp.provider=? AND sp.role=?
CROSS JOIN metadata_publications AS p INDEXED BY sqlite_autoindex_metadata_publications_2 ON p.entity_id=e.id AND p.provider=sp.provider AND p.subject_kind=sp.subject_kind AND p.provider_id=sp.provider_id AND p.id=sp.accepted_publication_id AND p.selection_revision=sp.revision AND p.evidence_id=sp.accepted_evidence_id
CROSS JOIN metadata_evidence AS ev INDEXED BY sqlite_autoindex_metadata_evidence_3 ON ev.id=p.evidence_id AND ev.provider=p.provider AND ev.subject_kind=p.subject_kind AND ev.provider_id=p.provider_id AND ev.ip_seal_owner=p.operation_id
CROSS JOIN metadata_provider_operations AS op INDEXED BY sqlite_autoindex_metadata_provider_operations_2 ON op.id=p.operation_id AND op.entity_id=e.id AND op.provider=p.provider AND op.generation=p.generation AND op.command_incarnation=p.ip_proof_incarnation
CROSS JOIN metadata_ip_receipts AS rc INDEXED BY sqlite_autoindex_metadata_ip_receipts_3 ON rc.owner_id=ev.ip_seal_owner AND rc.command_incarnation=ev.ip_seal_incarnation AND rc.command_sequence=ev.ip_seal_sequence AND rc.evidence_id=ev.id AND rc.evidence_subject_id=ev.subject_id AND rc.parser_version=ev.parser_version
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND sp.status='selected' AND p.decision='accepted' AND ev.sealed=1 AND op.status='applied' AND rc.command_kind='evidence_seal' AND p.ip_proof_owner=p.operation_id`

const ipBuildOctetsSQL = `SELECT octet_length(b.id),octet_length(b.state),octet_length(b.owner_operation)
FROM metadata_evidence_builds AS b INDEXED BY sqlite_autoindex_metadata_evidence_builds_2
WHERE b.evidence_id=?`

const ipMaterialOctetsSQL = `SELECT octet_length(s.kind),octet_length(s.role),octet_length(s.choice),octet_length(s.digest),octet_length(s.provider),octet_length(s.image_id),octet_length(s.origin),octet_length(s.attribution),octet_length(s.locale),octet_length(s.effective_language),octet_length(s.source_fence),octet_length(s.source_object_id),octet_length(s.source_object_revision),octet_length(s.actor),octet_length(s.observed_at),octet_length(src.accepted_mime),octet_length(sb.digest),octet_length(sb.mime),octet_length(sb.status),octet_length(lb.digest),octet_length(lb.mime),octet_length(lb.status)
FROM artwork_selections AS s
CROSS JOIN catalog_entities AS e ON e.id=s.entity_id AND e.library_id=? AND e.kind=? AND e.retired=0
LEFT JOIN artwork_sources AS src INDEXED BY sqlite_autoindex_artwork_sources_1 ON src.digest=s.digest
LEFT JOIN artwork_blobs AS sb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON sb.digest=src.small_blob
LEFT JOIN artwork_blobs AS lb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON lb.digest=src.large_blob
WHERE s.kind='entity' AND s.entity_id=? AND s.role=? AND s.choice=?`

const ipOwnerFieldsOctetsSQL = `SELECT octet_length(f.field),octet_length(f.value),octet_length(f.automatic_value),octet_length(f.actor),octet_length(f.observed_at)
FROM metadata_edit_owners AS own INDEXED BY sqlite_autoindex_metadata_edit_owners_1
CROSS JOIN metadata_owner_fields AS f ON f.kind=own.kind AND f.entity_id=own.entity_id
WHERE own.entity_id=? AND own.kind=?
ORDER BY f.field LIMIT ?`

const ipOwnerFieldsPersonOctetsSQL = `SELECT octet_length(f.field),octet_length(f.value),octet_length(f.automatic_value),octet_length(f.actor),octet_length(f.observed_at)
FROM metadata_edit_owners AS own INDEXED BY sqlite_autoindex_metadata_edit_owners_2
CROSS JOIN metadata_owner_fields AS f ON f.kind=own.kind AND f.entity_id=own.person_id
WHERE own.person_id=? AND own.kind='person'
ORDER BY f.field LIMIT ?`

const ipHistoryOctetsSQL = `SELECT octet_length(h.base_revision),octet_length(h.revision),octet_length(h.actor),octet_length(h.observed_at)
FROM metadata_owner_history AS h INDEXED BY metadata_owner_history_entity
WHERE h.kind=? AND h.entity_id=? AND h.id<? ORDER BY h.id DESC LIMIT ?`

const ipHistoryFieldsOctetsSQL = `SELECT octet_length(f.field),octet_length(f.before_value),octet_length(f.before_automatic),octet_length(f.after_value),octet_length(f.after_automatic)
FROM metadata_owner_history AS h INDEXED BY metadata_owner_history_entity
CROSS JOIN metadata_owner_history_fields AS f ON f.history_id=h.id
WHERE h.kind=? AND h.entity_id=? AND h.id BETWEEN ? AND ?
ORDER BY h.id DESC,f.field LIMIT ?`

var ipOctets = []ipOctetShape{
	ipOctetShape{id: "read.ip.policy_text_bytes", sql: ipPolicyTextOctetsSQL, table: "metadata_provider_policy",
		params: []stmt.Param{ipInteger("library"), ipText("provider")},
		eqp:    []string{"SEARCH pol USING PRIMARY KEY (library_id=? AND provider=?)"}},
	ipOctetShape{id: "read.ip.head_bytes", sql: ipHeadOctetsSQL, table: "catalog_entities",
		params: []stmt.Param{ipText("provider"), ipText("role"), ipInteger("entity"), ipInteger("library"), ipInteger("kind")},
		eqp: []string{
			"SEARCH e USING INTEGER PRIMARY KEY (rowid=?)",
			"SEARCH sp USING PRIMARY KEY (entity_id=? AND provider=? AND role=?)",
			"SEARCH p USING INDEX sqlite_autoindex_metadata_publications_2 (entity_id=? AND provider=? AND subject_kind=? AND provider_id=? AND id=?)",
			"SEARCH ev USING INDEX sqlite_autoindex_metadata_evidence_3 (id=? AND provider=? AND subject_kind=? AND provider_id=?)",
			"SEARCH op USING INDEX sqlite_autoindex_metadata_provider_operations_2 (provider=? AND id=?)",
			"SEARCH rc USING INDEX sqlite_autoindex_metadata_ip_receipts_3 (owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? AND evidence_id=? AND evidence_subject_id=? AND parser_version=?)",
		}},
	ipOctetShape{id: "read.ip.build_bytes", sql: ipBuildOctetsSQL, table: "metadata_evidence_builds",
		params: []stmt.Param{ipText("evidence")},
		eqp:    []string{"SEARCH b USING INDEX sqlite_autoindex_metadata_evidence_builds_2 (evidence_id=?)"}},
	ipOctetShape{id: "read.ip.material_bytes", sql: ipMaterialOctetsSQL, table: "artwork_selections",
		params: []stmt.Param{ipInteger("library"), ipInteger("kind"), ipInteger("entity"), ipText("role"), ipText("choice")},
		eqp: []string{
			"SEARCH s USING PRIMARY KEY (kind=? AND entity_id=? AND role=? AND choice=?)",
			"SEARCH e USING INTEGER PRIMARY KEY (rowid=?)",
			"SEARCH src USING INDEX sqlite_autoindex_artwork_sources_1 (digest=?) LEFT-JOIN",
			"SEARCH sb USING INDEX sqlite_autoindex_artwork_blobs_1 (digest=?) LEFT-JOIN",
			"SEARCH lb USING INDEX sqlite_autoindex_artwork_blobs_1 (digest=?) LEFT-JOIN",
		}},
	ipOctetShape{id: "read.ip.owner_fields_bytes", sql: ipOwnerFieldsOctetsSQL, table: "metadata_owner_fields",
		params: []stmt.Param{ipInteger("entity"), ipText("kind"), ipInteger("limit")},
		eqp: []string{
			"SEARCH own USING INDEX sqlite_autoindex_metadata_edit_owners_1 (entity_id=?)",
			"SEARCH f USING INDEX sqlite_autoindex_metadata_owner_fields_1 (kind=? AND entity_id=?)",
		}},
	ipOctetShape{id: "read.ip.owner_fields_person_bytes", sql: ipOwnerFieldsPersonOctetsSQL, table: "metadata_owner_fields",
		params: []stmt.Param{ipInteger("person"), ipInteger("limit")},
		eqp: []string{
			"SEARCH own USING INDEX sqlite_autoindex_metadata_edit_owners_2 (person_id=?)",
			"SEARCH f USING INDEX sqlite_autoindex_metadata_owner_fields_1 (kind=? AND entity_id=?)",
		}},
	ipOctetShape{id: "read.ip.history_bytes", sql: ipHistoryOctetsSQL, table: "metadata_owner_history",
		params: []stmt.Param{ipText("kind"), ipInteger("entity"), ipInteger("after"), ipInteger("limit")},
		eqp:    []string{"SEARCH h USING INDEX metadata_owner_history_entity (kind=? AND entity_id=? AND id<?)"}},
	ipOctetShape{id: "read.ip.history_fields_bytes", sql: ipHistoryFieldsOctetsSQL, table: "metadata_owner_history_fields",
		params: []stmt.Param{ipText("kind"), ipInteger("entity"), ipInteger("low"), ipInteger("high"), ipInteger("limit")},
		eqp: []string{
			"SEARCH h USING COVERING INDEX metadata_owner_history_entity (kind=? AND entity_id=? AND id>? AND id<?)",
			"SEARCH f USING PRIMARY KEY (history_id=?)",
		}},
}

func init() {
	for _, shape := range ipOctets {
		registerIPOctet(shape)
	}
}
