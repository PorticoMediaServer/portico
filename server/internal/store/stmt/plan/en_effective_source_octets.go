package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exactly three Effective native-pin byte points approved in packet33ba0d73.
// Capture can qualify only these reads; this grants no verifier authority.
type enEffectiveSourceOctetPoint struct {
	id                stmt.ID
	sql, table, index string
	keys              []string
	params            []stmt.Param
}

var enEffectiveSourceOctetPoints = []enEffectiveSourceOctetPoint{
	{id: "enrichment.effective_source.pin_evidence_lengths", sql: "SELECT octet_length(id),\n    subject_id,\n    octet_length(parser_version),\n    octet_length(provider),\n    sealed,\n    octet_length(seal_proof_owner),\n    octet_length(seal_proof_incarnation),\n    seal_proof_sequence,\n    octet_length(seal_proof_kind)\n    FROM metadata_evidence WHERE id=?", table: "metadata_evidence", index: "sqlite_autoindex_metadata_evidence_1", keys: []string{"id"}, params: []stmt.Param{{Name: "id", Type: stmt.Text}}},
	{id: "enrichment.effective_source.pin_publication_lengths", sql: "SELECT octet_length(publication_id),\n    entity_id,\n    octet_length(provider),\n    member_count,\n    sealed,\n    octet_length(seal_proof_owner),\n    octet_length(seal_proof_incarnation),\n    seal_proof_sequence,\n    octet_length(seal_proof_kind)\n    FROM credit_source_publications WHERE publication_id=?", table: "credit_source_publications", index: "sqlite_autoindex_credit_source_publications_1", keys: []string{"publication_id"}, params: []stmt.Param{{Name: "publication_id", Type: stmt.Text}}},
	{id: "enrichment.effective_source.pin_receipt_lengths", sql: "SELECT octet_length(provider_operation_id),\n    octet_length(command_incarnation),\n    command_sequence,\n    octet_length(operation_id),\n    octet_length(request_digest),\n    octet_length(command_kind),\n    accepted_revision,\n    applied_ms,\n    entity_id,\n    octet_length(after_item),\n    after_ordinal,\n    octet_length(publication_id),\n    effective_image_id,\n    effective_image_revision,\n    result_count,\n    octet_length(result_digest),\n    seal_version,\n    octet_length(seal_evidence_id),\n    seal_subject_id,\n    octet_length(seal_parser_version),\n    octet_length(seal_provider),\n    octet_length(seal_subject_kind),\n    octet_length(seal_provider_id),\n    octet_length(seal_language),\n    octet_length(seal_region),\n    octet_length(seal_request_shape),\n    octet_length(seal_observation_digest),\n    seal_owner_generation,\n    octet_length(seal_build_id),\n    seal_build_generation,\n    seal_content_revision,\n    octet_length(seal_algorithm),\n    octet_length(seal_commitment),\n    seal_verified_rows,\n    seal_canonical_bytes,\n    seal_retained_bytes,\n    octet_length(upstream_evidence_owner),\n    octet_length(upstream_evidence_incarnation),\n    upstream_evidence_sequence,\n    octet_length(source_dependency_owner),\n    octet_length(source_dependency_incarnation),\n    source_dependency_sequence,\n    octet_length(source_dependency_publication_id),\n    octet_length(source_dependency_provider),\n    proof_revision,\n    proof_pin_count,\n    proof_reclaim_due_ms,\n    proof_cleanup_after_sequence\n    FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?", table: "enrichment_command_receipts", index: "", keys: []string{"provider_operation_id", "command_incarnation", "command_sequence"}, params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}}},
}

func enEffectiveSourceOctetPointFor(id stmt.ID) (enEffectiveSourceOctetPoint, bool) {
	for _, p := range enEffectiveSourceOctetPoints {
		if p.id == id {
			return p, true
		}
	}
	return enEffectiveSourceOctetPoint{}, false
}
func enEffectiveSourceOctetSeek(p enEffectiveSourceOctetPoint) string {
	a := make([]string, len(p.keys))
	for i, k := range p.keys {
		a[i] = k + "=?"
	}
	return strings.Join(a, " AND ")
}
func enEffectiveSourceOctetDefinition(d stmt.Definition) bool {
	p, ok := enEffectiveSourceOctetPointFor(d.ID)
	if !ok || d.SQL != p.sql || d.Kind != stmt.Read || !d.Plan.Hot || !slices.Equal(d.Params, p.params) || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	if !slices.Equal(d.Plan.Seeks, []string{enEffectiveSourceOctetSeek(p)}) {
		return false
	}
	if len(d.Plan.AutoIndexes) != 0 {
		return false
	}
	if p.index != "" {
		return slices.Equal(d.Plan.Indexes, []string{p.index}) && len(d.Plan.PrimaryKeys) == 0
	}
	return len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Indexes) == 0 && slices.Equal(d.Plan.PrimaryKeys, []string{p.table})
}
func auditENEffectiveSourceOctetPoint(ctx context.Context, conn *sql.Conn, d stmt.Definition) error {
	p, _ := enEffectiveSourceOctetPointFor(d.ID)
	if err := auditClosedOctetKey(ctx, conn, p.table, p.index, p.keys); err != nil {
		return err
	}
	name := p.index
	if name == "" {
		indexes, err := query(ctx, conn, "PRAGMA main.index_list("+identifier(p.table)+")")
		if err != nil {
			return err
		}
		for _, row := range indexes.Values {
			if len(row) != 5 {
				return fmt.Errorf("seal byte point: native index metadata")
			}
			if row[3] == "pk" {
				name, _ = row[1].(string)
			}
		}
		if name == "" {
			return fmt.Errorf("seal byte point: native PK missing")
		}
	}
	info, err := query(ctx, conn, "PRAGMA main.index_xinfo("+identifier(name)+")")
	if err != nil {
		return err
	}
	for _, row := range info.Values {
		if len(row) != 6 {
			return fmt.Errorf("seal byte point: native key metadata")
		}
		if row[5] == int64(1) && (row[3] != int64(0) || row[4] != "BINARY") {
			return fmt.Errorf("seal byte point: native BINARY ASC key required")
		}
	}
	return nil
}
func checkENEffectiveSourceOctetPoint(d stmt.Definition, eqp Rows) error { return nil }

func init() {
	for _, point := range enEffectiveSourceOctetPoints {
		p := point
		pointOctets.add(octetShape{
			id: p.id, sql: p.sql, mismatch: "EN seal preflight requires the exact closed ID/SQL/parameters/point plan",
			definition: enEffectiveSourceOctetDefinition,
			audit: func(ctx context.Context, c *sql.Conn) error {
				return auditENEffectiveSourceOctetPoint(ctx, c, stmt.Definition{ID: p.id})
			},
			point: func(rows Rows) error { return checkENEffectiveSourceOctetPoint(stmt.Definition{ID: p.id}, rows) },
		})
	}
}
