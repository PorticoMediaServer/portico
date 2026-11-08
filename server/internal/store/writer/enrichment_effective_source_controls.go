package writer

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	effectiveSourceBuildControl   stmt.ID = "enrichment.effective_source.build_control"
	effectiveSourceCustodyControl stmt.ID = "enrichment.effective_source.custody_control"
)

// Literal same-snapshot lineage points. No lease, expiry or current generation
// of the ORIGINAL evidence owner is treated as fresh authority. Cleanup may
// retire its source side while another title still pins its intact evidence.
func enrichmentEffectiveSourceControlDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	number := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	blob := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: effectiveSourceBuildControl, SQL: `SELECT 1 FROM metadata_evidence_builds AS b INDEXED BY sqlite_autoindex_metadata_evidence_builds_1
INNER JOIN metadata_provider_operations AS o INDEXED BY sqlite_autoindex_metadata_provider_operations_1 ON o.id=b.owner_operation AND o.command_incarnation=b.owner_incarnation
WHERE b.id=? AND b.evidence_id=? AND b.generation=? AND b.owner_operation=? AND b.owner_incarnation=? AND b.owner_generation=?
AND b.parent_build IS NULL AND b.cursor=? AND b.expected_rows=? AND b.materialized_rows=? AND b.content_revision=? AND b.materialized_digest=?
AND o.proof_cleanup_phase IN('idle','source_members','source_header','receipts')`,
			Params: []stmt.Param{text("build"), text("evidence"), number("build_generation"), text("original_owner"), blob("original_incarnation"), number("original_owner_generation"), number("cursor"), number("expected_rows"), number("materialized_rows"), number("content_revision"), text("materialized_digest")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_evidence_builds_1", "sqlite_autoindex_metadata_provider_operations_1"}, AutoIndexes: []stmt.AutoIndexBinding{
				{Table: "metadata_evidence_builds", Name: "sqlite_autoindex_metadata_evidence_builds_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
				{Table: "metadata_provider_operations", Name: "sqlite_autoindex_metadata_provider_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
			}}},
		{ID: effectiveSourceCustodyControl, SQL: `SELECT 1 FROM metadata_provider_operations INDEXED BY sqlite_autoindex_metadata_provider_operations_1
WHERE id=? AND command_incarnation=? AND entity_id=? AND provider=? AND source_publication_id=?
AND source_owner_generation=? AND source_content_revision=? AND source_next_position=? AND source_expected_members=?
AND source_expected_bytes=? AND source_algorithm=? AND source_version=? AND source_expected_commitment=?
AND source_evidence_id=? AND source_subject_id=? AND source_parser_version=?
AND source_evidence_owner=? AND source_evidence_incarnation=? AND source_evidence_sequence=? AND proof_cleanup_phase='idle'`,
			Params: []stmt.Param{text("owner"), blob("incarnation"), number("entity"), text("provider"), text("publication"), number("source_owner_generation"), number("source_content_revision"), number("source_next_position"), number("source_expected_members"), number("source_expected_bytes"), text("source_algorithm"), number("source_version"), blob("source_expected_commitment"), text("evidence"), number("subject"), text("parser"), text("upstream_owner"), blob("upstream_incarnation"), number("upstream_sequence")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{
				{Table: "metadata_provider_operations", Name: "sqlite_autoindex_metadata_provider_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
			}}},
	}
	for i := range defs {
		defs[i].Owner, defs[i].Kind = "writer.enrichment", stmt.Read
		defs[i].CallSites = []string{"writer/enrichment_effective_source_controls.go:checkEffectiveSourceControlPoints"}
		args := make([]any, len(defs[i].Params))
		for j, p := range defs[i].Params {
			switch p.Type {
			case stmt.Text:
				args[j] = "missing"
			case stmt.Integer:
				args[j] = int64(1)
			case stmt.Blob:
				args[j] = make([]byte, 16)
			}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "missing_original_native_lineage", Bindings: args}}
	}
	return defs
}

func checkEffectiveSourceControlPoints(ctx context.Context, turn *commandTurn, c *effectiveStageBeginCommand, e, s effectivePinnedSealReceipt) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != EffectiveStage ||
		c == nil || !c.valid || c.sourceReferences == nil {
		return ErrInvalid
	}
	for _, id := range []stmt.ID{effectiveSourceBuildControl, effectiveSourceCustodyControl} {
		if _, ok := turn.steps[id]; !ok {
			return invalid("effective_source_control_steps")
		}
	}
	ef, sf, refs := e.saved.facts, s.saved.facts, *c.sourceReferences
	if !ef.BuildID.Present || !ef.BuildGeneration.Present || ef.VerifiedRows <= 0 || sf.VerifiedRows < 0 {
		return ErrResolveRequired
	}
	for _, point := range []struct {
		id   stmt.ID
		args []any
	}{
		{effectiveSourceBuildControl, []any{ef.BuildID.Value, ef.EvidenceID, ef.BuildGeneration.Value, ef.ProviderOperationID, refs.evidence.receipt.Identity.Incarnation[:], ef.OwnerGeneration,
			ef.VerifiedRows, ef.VerifiedRows, ef.VerifiedRows, ef.ContentRevision, hex.EncodeToString(ef.Commitment[:])}},
		{effectiveSourceCustodyControl, []any{sf.ProviderOperationID, refs.source.receipt.Identity.Incarnation[:], c.request.entity.id, sf.Provider, sf.PublicationID.Value,
			sf.OwnerGeneration, sf.ContentRevision, sf.VerifiedRows, sf.VerifiedRows, sf.CanonicalBytes, sf.Algorithm, sf.SealVersion, sf.Commitment[:],
			ef.EvidenceID, ef.SubjectID, ef.ParserVersion, refs.evidence.owner, refs.evidence.receipt.Identity.Incarnation[:], refs.evidence.receipt.Identity.Sequence}},
	} {
		rows, e := turn.query(ctx, turn.steps[point.id].statementStep, point.args...)
		if e != nil {
			return e
		}
		if e = effectiveSourceControlOne(rows); e != nil {
			return e
		}
	}
	return nil
}

func effectiveSourceControlOne(rows *commandRows) (err error) {
	if rows == nil {
		return ErrInvalid
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		return errors.Join(ErrConflict, rows.Err())
	}
	var value any
	if err = rows.Scan(&value); err != nil {
		return err
	}
	if n, ok := value.(int64); !ok || n != 1 || rows.Next() {
		return ErrConflict
	}
	return rows.Err()
}
