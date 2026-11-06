package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	effectiveDisclosureLibrary stmt.ID = "enrichment.effective_stage.disclosure_library"
	effectiveDisclosureSubject stmt.ID = "enrichment.effective_stage.disclosure_subject"
	effectiveReceiptAck        stmt.ID = "enrichment.effective_stage.receipt_ack"
)

// These are fixed scope/ACK points, not fresh source eligibility. In particular
// no lease, source revision, phase or serving-head predicate hides old proof.
func effectiveDispatchDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{
			ID: effectiveDisclosureLibrary, Owner: "writer", Kind: stmt.Read,
			CallSites: []string{"writer/effective_dispatch.go:disclose"},
			SQL:       "SELECT id FROM catalog_libraries WHERE id=? AND library_id=?",
			Params:    []stmt.Param{{Name: "library_id", Type: stmt.Integer}, {Name: "library_key", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "library_present", Bindings: []any{int64(7), "library-native"}}, {Name: "library_missing", Bindings: []any{int64(8), "library-native"}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}},
		},
		{
			ID: effectiveDisclosureSubject, Owner: "writer", Kind: stmt.Read,
			CallSites: []string{"writer/effective_dispatch.go:disclose"},
			SQL:       "SELECT id FROM catalog_subjects WHERE id=? AND library_id=?",
			Params:    []stmt.Param{{Name: "entity_id", Type: stmt.Integer}, {Name: "library_id", Type: stmt.Integer}},
			Cohorts:   []stmt.Cohort{{Name: "subject_present", Bindings: []any{int64(101), int64(7)}}, {Name: "subject_missing", Bindings: []any{int64(102), int64(7)}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_subjects"}, Seeks: []string{"rowid=?"}},
		},
		{
			ID: effectiveReceiptAck, Owner: "writer", Kind: stmt.Write,
			CallSites: []string{"writer/effective_dispatch.go:acknowledgeEffective"},
			SQL: `UPDATE metadata_provider_operations SET acknowledged_command_sequence=?
 WHERE id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?`,
			Params:  []stmt.Param{{Name: "next_ack", Type: stmt.Integer}, {Name: "provider_operation_id", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "last_sequence", Type: stmt.Integer}, {Name: "expected_ack", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "ack_pending", Bindings: []any{int64(12), "operation-one", []byte{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}, int64(12), int64(11)}}, {Name: "ack_stale", Bindings: []any{int64(12), "operation-one", []byte{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}, int64(12), int64(10)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_3"}, Seeks: []string{"id=? AND command_incarnation=?"}},
		},
	}
}
