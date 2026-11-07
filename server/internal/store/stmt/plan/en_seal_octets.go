package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Five literal seal/history and Effective provenance byte points, never a generic function allowance.
// Copy/nullness/byte equality and the same-snapshot proof remain owner duties.
type enSealOctetPoint struct {
	id                stmt.ID
	sql, table, index string
	keys              []string
	params            []stmt.Param
	hinted            bool
}

var enSealOctetPoints = []enSealOctetPoint{
	{id: "enrichment.seal.receipt_lengths", sql: "SELECT octet_length(provider_operation_id),octet_length(command_incarnation),octet_length(operation_id),octet_length(request_digest),octet_length(command_kind),octet_length(after_item),octet_length(publication_id),octet_length(result_digest),octet_length(seal_evidence_id),octet_length(seal_parser_version),octet_length(seal_provider),octet_length(seal_subject_kind),octet_length(seal_provider_id),octet_length(seal_language),octet_length(seal_region),octet_length(seal_request_shape),octet_length(seal_observation_digest),octet_length(seal_build_id),octet_length(seal_algorithm),octet_length(seal_commitment),octet_length(upstream_evidence_owner),octet_length(upstream_evidence_incarnation),octet_length(source_dependency_owner),octet_length(source_dependency_incarnation),octet_length(source_dependency_publication_id),octet_length(source_dependency_provider) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?", table: "enrichment_command_receipts", index: "", keys: []string{"provider_operation_id", "command_incarnation", "command_sequence"}, params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}}},
	{id: "enrichment.seal.evidence_header_lengths", hinted: true, sql: "SELECT octet_length(id),octet_length(parser_version),octet_length(provider),octet_length(subject_kind),octet_length(provider_id),octet_length(language),octet_length(region),octet_length(request_shape),octet_length(digest),octet_length(observed_at),octet_length(source_url),octet_length(etag),octet_length(last_modified),octet_length(title),octet_length(original_title),octet_length(overview),octet_length(tagline),octet_length(content_rating),octet_length(studio),octet_length(network),octet_length(country),octet_length(release_date),octet_length(artist),octet_length(artist_type),octet_length(wikidata_id),octet_length(biography),octet_length(biography_source),octet_length(biography_source_url),octet_length(disambiguation),octet_length(release_group_id),octet_length(group_title),octet_length(primary_type),octet_length(barcode),octet_length(seal_proof_owner),octet_length(seal_proof_incarnation) FROM metadata_evidence INDEXED BY sqlite_autoindex_metadata_evidence_1 WHERE id=? AND subject_id=? AND parser_version=?", table: "metadata_evidence", index: "sqlite_autoindex_metadata_evidence_1", keys: []string{"id"}, params: []stmt.Param{{Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text}}},
	{id: "enrichment.effective_source.pin_evidence_lengths", sql: "SELECT octet_length(id),\n    subject_id,\n    octet_length(parser_version),\n    octet_length(provider),\n    sealed,\n    octet_length(seal_proof_owner),\n    octet_length(seal_proof_incarnation),\n    seal_proof_sequence,\n    octet_length(seal_proof_kind)\n    FROM metadata_evidence WHERE id=?", table: "metadata_evidence", index: "sqlite_autoindex_metadata_evidence_1", keys: []string{"id"}, params: []stmt.Param{{Name: "id", Type: stmt.Text}}},
	{id: "enrichment.effective_source.pin_publication_lengths", sql: "SELECT octet_length(publication_id),\n    entity_id,\n    octet_length(provider),\n    member_count,\n    sealed,\n    octet_length(seal_proof_owner),\n    octet_length(seal_proof_incarnation),\n    seal_proof_sequence,\n    octet_length(seal_proof_kind)\n    FROM credit_source_publications WHERE publication_id=?", table: "credit_source_publications", index: "sqlite_autoindex_credit_source_publications_1", keys: []string{"publication_id"}, params: []stmt.Param{{Name: "publication_id", Type: stmt.Text}}},
	{id: "enrichment.effective_source.pin_receipt_lengths", sql: "SELECT octet_length(provider_operation_id),\n    octet_length(command_incarnation),\n    command_sequence,\n    octet_length(operation_id),\n    octet_length(request_digest),\n    octet_length(command_kind),\n    accepted_revision,\n    applied_ms,\n    entity_id,\n    octet_length(after_item),\n    after_ordinal,\n    octet_length(publication_id),\n    effective_image_id,\n    effective_image_revision,\n    result_count,\n    octet_length(result_digest),\n    seal_version,\n    octet_length(seal_evidence_id),\n    seal_subject_id,\n    octet_length(seal_parser_version),\n    octet_length(seal_provider),\n    octet_length(seal_subject_kind),\n    octet_length(seal_provider_id),\n    octet_length(seal_language),\n    octet_length(seal_region),\n    octet_length(seal_request_shape),\n    octet_length(seal_observation_digest),\n    seal_owner_generation,\n    octet_length(seal_build_id),\n    seal_build_generation,\n    seal_content_revision,\n    octet_length(seal_algorithm),\n    octet_length(seal_commitment),\n    seal_verified_rows,\n    seal_canonical_bytes,\n    seal_retained_bytes,\n    octet_length(upstream_evidence_owner),\n    octet_length(upstream_evidence_incarnation),\n    upstream_evidence_sequence,\n    octet_length(source_dependency_owner),\n    octet_length(source_dependency_incarnation),\n    source_dependency_sequence,\n    octet_length(source_dependency_publication_id),\n    octet_length(source_dependency_provider),\n    proof_revision,\n    proof_pin_count,\n    proof_reclaim_due_ms,\n    proof_cleanup_after_sequence\n    FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?", table: "enrichment_command_receipts", index: "", keys: []string{"provider_operation_id", "command_incarnation", "command_sequence"}, params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}}},
}

func enSealOctetPointFor(id stmt.ID) (enSealOctetPoint, bool) {
	for _, p := range enSealOctetPoints {
		if p.id == id {
			return p, true
		}
	}
	return enSealOctetPoint{}, false
}
func enSealOctetSeek(p enSealOctetPoint) string {
	a := make([]string, len(p.keys))
	for i, k := range p.keys {
		a[i] = k + "=?"
	}
	return strings.Join(a, " AND ")
}
func enSealOctetDefinition(d stmt.Definition) bool {
	p, ok := enSealOctetPointFor(d.ID)
	if !ok || d.SQL != p.sql || d.Kind != stmt.Read || !d.Plan.Hot || !slices.Equal(d.Params, p.params) || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	if len(d.Plan.Seeks) != 0 && !slices.Equal(d.Plan.Seeks, []string{enSealOctetSeek(p)}) {
		return false
	}
	if p.index != "" {
		if !slices.Equal(d.Plan.Indexes, []string{p.index}) || len(d.Plan.PrimaryKeys) != 0 {
			return false
		}
		if !p.hinted {
			return len(d.Plan.AutoIndexes) == 0
		}
		return len(d.Plan.AutoIndexes) == 1 &&
			d.Plan.AutoIndexes[0].Table == p.table && d.Plan.AutoIndexes[0].Name == p.index && d.Plan.AutoIndexes[0].Origin == "pk" &&
			slices.Equal(d.Plan.AutoIndexes[0].Columns, []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}})
	}
	return len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Indexes) == 0 && slices.Equal(d.Plan.PrimaryKeys, []string{p.table})
}
func auditENSealOctetPoint(ctx context.Context, conn *sql.Conn, d stmt.Definition) error {
	p, _ := enSealOctetPointFor(d.ID)
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
func checkENSealOctetPoint(d stmt.Definition, eqp Rows) error {
	p, _ := enSealOctetPointFor(d.ID)
	kind := "PRIMARY KEY"
	if p.index != "" {
		kind = "INDEX " + p.index
	}
	want := "SEARCH " + p.table + " USING " + kind + " (" + enSealOctetSeek(p) + ")"
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
		return fmt.Errorf("plan %s: exact native seal point SEARCH required: %s", d.ID, want)
	}
	return nil
}

func init() {
	for _, point := range enSealOctetPoints {
		p := point
		if p.id == "enrichment.effective_source.pin_evidence_lengths" {
			continue
		}
		pointOctets.add(octetShape{
			id: p.id, sql: p.sql, mismatch: "EN seal preflight requires the exact closed ID/SQL/parameters/point plan",
			definition: enSealOctetDefinition,
			audit: func(ctx context.Context, c *sql.Conn) error {
				return auditENSealOctetPoint(ctx, c, stmt.Definition{ID: p.id})
			},
			point: func(rows Rows) error { return checkENSealOctetPoint(stmt.Definition{ID: p.id}, rows) },
		})
	}
}
