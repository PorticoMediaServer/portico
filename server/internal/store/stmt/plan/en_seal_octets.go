package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Two literal seal proof byte points, never a generic function allowance.
// Copy/nullness/byte equality and the same-snapshot proof remain owner duties.
type enSealOctetPoint struct {
	id                stmt.ID
	sql, table, index string
	keys              []string
	params            []stmt.Param
}

var enSealOctetPoints = []enSealOctetPoint{
	{id: "enrichment.seal.receipt_lengths", sql: "SELECT octet_length(provider_operation_id),octet_length(command_incarnation),octet_length(operation_id),octet_length(request_digest),octet_length(command_kind),octet_length(after_item),octet_length(publication_id),octet_length(result_digest),octet_length(seal_evidence_id),octet_length(seal_parser_version),octet_length(seal_provider),octet_length(seal_subject_kind),octet_length(seal_provider_id),octet_length(seal_language),octet_length(seal_region),octet_length(seal_request_shape),octet_length(seal_observation_digest),octet_length(seal_build_id),octet_length(seal_algorithm),octet_length(seal_commitment),octet_length(upstream_evidence_owner),octet_length(upstream_evidence_incarnation),octet_length(source_dependency_owner),octet_length(source_dependency_incarnation),octet_length(source_dependency_publication_id),octet_length(source_dependency_provider) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?", table: "enrichment_command_receipts", index: "", keys: []string{"provider_operation_id", "command_incarnation", "command_sequence"}, params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}}},
	{id: "enrichment.seal.evidence_header_lengths", sql: "SELECT octet_length(id),octet_length(parser_version),octet_length(provider),octet_length(subject_kind),octet_length(provider_id),octet_length(language),octet_length(region),octet_length(request_shape),octet_length(digest),octet_length(observed_at),octet_length(source_url),octet_length(etag),octet_length(last_modified),octet_length(title),octet_length(original_title),octet_length(overview),octet_length(tagline),octet_length(content_rating),octet_length(studio),octet_length(network),octet_length(country),octet_length(release_date),octet_length(artist),octet_length(artist_type),octet_length(wikidata_id),octet_length(biography),octet_length(biography_source),octet_length(biography_source_url),octet_length(disambiguation),octet_length(release_group_id),octet_length(group_title),octet_length(primary_type),octet_length(barcode),octet_length(seal_proof_owner),octet_length(seal_proof_incarnation) FROM metadata_evidence INDEXED BY sqlite_autoindex_metadata_evidence_1 WHERE id=? AND subject_id=? AND parser_version=?", table: "metadata_evidence", index: "sqlite_autoindex_metadata_evidence_1", keys: []string{"id"}, params: []stmt.Param{{Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text}}},
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
		return slices.Equal(d.Plan.Indexes, []string{p.index}) && len(d.Plan.PrimaryKeys) == 0 && len(d.Plan.AutoIndexes) == 1 &&
			d.Plan.AutoIndexes[0].Table == p.table && d.Plan.AutoIndexes[0].Name == p.index && d.Plan.AutoIndexes[0].Origin == "pk" &&
			slices.Equal(d.Plan.AutoIndexes[0].Columns, []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}})
	}
	return len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Indexes) == 0 && slices.Equal(d.Plan.PrimaryKeys, []string{p.table})
}
func auditENSealOctetPoint(ctx context.Context, conn *sql.Conn, d stmt.Definition) error {
	p, _ := enSealOctetPointFor(d.ID)
	if err := enAuditOctetKey(ctx, conn, p.table, p.index, p.keys); err != nil {
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

// Callers pass only their compiled ID/SQL-qualified point's native key. This
// shared metadata check admits no function syntax, SQL text or receipt itself.
func enAuditOctetKey(ctx context.Context, conn *sql.Conn, table, indexName string, keyColumns []string) error {
	// Native statement form: SQL objects named pragma_function_list cannot
	// counterfeit the builtin/override inventory.
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("closed octet point: invalid native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("closed octet point: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("closed octet point: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("closed octet point: unshadowed native octet_length required")
	}
	tables, err := query(ctx, conn, "PRAGMA main.table_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	if len(tables.Values) != 1 || len(tables.Values[0]) != 6 ||
		tables.Values[0][0] != "main" || tables.Values[0][1] != table || tables.Values[0][2] != "table" {
		return fmt.Errorf("closed octet point: ordinary MAIN table required")
	}
	columns, err := query(ctx, conn, "PRAGMA main.table_xinfo("+identifier(table)+")")
	if err != nil {
		return err
	}
	// These are complete PRIMARY KEY points, not prefixes that merely SEARCH.
	keys := make([]string, len(keyColumns))
	count := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("closed octet point: invalid native table metadata")
		}
		position, ok := row[5].(int64)
		if !ok {
			return fmt.Errorf("closed octet point: invalid native key position")
		}
		if position != 0 {
			if position < 1 || position > int64(len(keys)) || row[6] != int64(0) {
				return fmt.Errorf("closed octet point: complete native key mismatch")
			}
			name, ok := row[1].(string)
			if !ok || keys[position-1] != "" {
				return fmt.Errorf("closed octet point: invalid native key column")
			}
			keys[position-1] = name
			count++
		}
	}
	if count != len(keyColumns) || !slices.Equal(keys, keyColumns) {
		return fmt.Errorf("closed octet point: complete native key mismatch")
	}
	if indexName == "" {
		if tables.Values[0][4] != int64(1) {
			return fmt.Errorf("closed octet point: WITHOUT ROWID primary key required")
		}
		return nil
	}
	indexes, err := query(ctx, conn, "PRAGMA main.index_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("closed octet point: invalid native index metadata")
		}
		if row[1] == indexName {
			origin, err := autoindexOrigin(row)
			if err != nil || origin != "pk" {
				return fmt.Errorf("closed octet point: pinned native primary key required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("closed octet point: pinned native key absent (possible renumbering)")
	}
	index, err := query(ctx, conn, "PRAGMA main.index_info("+identifier(indexName)+")")
	if err != nil {
		return err
	}
	if len(index.Values) != len(keyColumns) {
		return fmt.Errorf("closed octet point: complete native index key mismatch")
	}
	for i, row := range index.Values {
		if len(row) != 3 || row[0] != int64(i) || row[2] != keyColumns[i] {
			return fmt.Errorf("closed octet point: complete native index key mismatch")
		}
	}
	return nil
}
