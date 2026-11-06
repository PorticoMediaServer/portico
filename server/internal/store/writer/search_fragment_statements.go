package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	searchFragmentInsert      stmt.ID = "search.publish.fragment_stage.fragment"
	searchFragmentPrefix      stmt.ID = "search.publish.fragment_stage.prefix"
	searchFragmentLexical     stmt.ID = "search.publish.fragment_stage.lexical"
	searchFragmentGrams       stmt.ID = "search.publish.fragment_stage.grams"
	searchFragmentSealTotals  stmt.ID = "search.publish.cas.fragment_seal_totals"
	searchFragmentReceiptPut  stmt.ID = "search.publish.receipt.fragment_insert"
	searchFragmentReceiptRead stmt.ID = "search.publish.receipt.fragment_result"
	searchFragmentNoHeadNoDue stmt.ID = "search.publish.cas.fragment_no_head_no_due"
	searchFragmentNoHeadDue   stmt.ID = "search.publish.cas.fragment_no_head_due"
	searchFragmentHeadNoDue   stmt.ID = "search.publish.cas.fragment_head_no_due"
	searchFragmentHeadDue     stmt.ID = "search.publish.cas.fragment_head_due"
	searchFragmentImageRead   stmt.ID = "search.publish.owner.fragment_image"
	searchFragmentSealRead    stmt.ID = "search.publish.owner.fragment_seal"
	searchFragmentHeldRead    stmt.ID = "search.publish.owner.fragment_held_custody"
)

const searchFragmentReceiptKind = "fragment_stage"

// Complete finite SQL family for W's composition. Shared source/history IDs
// retain their existing immutable definitions; no registry or recipe is minted.
// The complete recipe reserves searchImageHeads twice (24 uses / 23 IDs).
func searchFragmentDefinitions() []stmt.Definition {
	defs := searchFragmentStageDefinitions()
	for _, d := range searchPublisherReceiptDefinitions() {
		if d.ID == searchPublisherOwnerID || d.ID == searchPublisherIdentity || d.ID == searchPublisherAck {
			defs = append(defs, d)
		}
	}
	for _, d := range searchImageBeginDefinitions() {
		switch d.ID {
		case searchImageSource, searchImageSubject, searchImageLibrary, searchImageBank, searchImageHeads, searchImageJob:
			defs = append(defs, d)
		}
	}
	return defs
}

// Finite domain definitions only. W composes the registry/complete profile;
// the FTS descriptors require their separately reviewed Capture capability.
func searchFragmentStageDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: searchFragmentImageRead, Kind: stmt.Read,
			SQL: `SELECT id,entity_id,library_id,kind,search_generation_id,generation,source_incarnation,source_revision,recipe_version
FROM search_publications WHERE id=? AND person_id IS NULL AND recipe_version=?`,
			Params: []stmt.Param{i("image_id"), t("recipe")}, Cohorts: []stmt.Cohort{{Name: "captured_staged_image", Bindings: []any{int64(307), searchpolicy.RecipeV1}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_publications"}, Seeks: []string{"rowid=?"}}},
		{ID: searchFragmentSealRead, Kind: stmt.Read,
			SQL: `SELECT algorithm,seed,expected_digest,expected_fragments,expected_bytes,staged_fragments,staged_bytes,
verified_fragments,verified_bytes,chain,after_field,after_ordinal,after_fragment_id,sealed
FROM search_image_seals WHERE publication_id=? AND algorithm=?`,
			Params: []stmt.Param{i("image_id"), t("algorithm")}, Cohorts: []stmt.Cohort{{Name: "independent_seal_checkpoint", Bindings: []any{int64(307), searchmanifest.Algorithm}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_image_seals"}, Seeks: []string{"publication_id=?"}}},
		{ID: searchFragmentHeldRead, Kind: stmt.Read,
			SQL:    `SELECT publication_id,live_publication_id,phase FROM search_reclaim_jobs WHERE publication_id=?`,
			Params: []stmt.Param{i("image_id")}, Cohorts: []stmt.Cohort{{Name: "same_image_held_custody", Bindings: []any{int64(307)}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_search_reclaim_jobs_1"}, Seeks: []string{"publication_id=?"}}},
		{ID: searchFragmentInsert, Kind: stmt.Write,
			SQL: `INSERT INTO search_fragments(owner_entity,owner_person,field,ordinal,source_revision,
prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
			Params: []stmt.Param{i("entity_id"), {Name: "person_id", Type: stmt.Integer, Nullable: true}, i("field"), i("ordinal"), t("source_revision"),
				t("prefix_tokens"), t("lexical_tokens"), t("normalized"), i("input_generation"), i("bank_id"), i("image_id")},
			Cohorts: []stmt.Cohort{{Name: "first_own_title_fragment", Bindings: []any{int64(41), nil, int64(1), int64(0), "11", "000061", "a", "a", int64(5), int64(7), int64(307)}}},
			Plan:    stmt.Plan{Hot: true}},
		{ID: searchFragmentSealTotals, Kind: stmt.Write,
			SQL: `UPDATE search_image_seals SET staged_fragments=?,staged_bytes=?
WHERE publication_id=? AND algorithm=? AND seed=? AND expected_digest=? AND expected_fragments=? AND expected_bytes=?
AND staged_fragments=? AND staged_bytes=? AND verified_fragments=? AND verified_bytes=? AND chain=?
AND after_field=? AND after_ordinal=? AND after_fragment_id=? AND sealed=?`,
			Params: []stmt.Param{i("next_staged_fragments"), i("next_staged_bytes"), i("image_id"), t("algorithm"), b("seed"), b("expected_digest"), i("expected_fragments"), i("expected_bytes"),
				i("staged_fragments"), i("staged_bytes"), i("verified_fragments"), i("verified_bytes"), b("chain"), i("verified_field"), i("verified_ordinal"), i("verified_fragment"), i("sealed")},
			Cohorts: []stmt.Cohort{{Name: "independent_verified_state_preserved", Bindings: []any{int64(1), int64(8), int64(307), searchmanifest.Algorithm, make([]byte, 32), make([]byte, 32), int64(3), int64(10), int64(0), int64(0), int64(0), int64(0), make([]byte, 32), int64(0), int64(0), int64(0), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"search_image_seals"}, Seeks: []string{"publication_id=?"}}},
	}
	for _, slot := range []struct {
		id   stmt.ID
		slot stmt.FTSInsertSlot
	}{{searchFragmentPrefix, stmt.SearchFragmentPrefixV1}, {searchFragmentLexical, stmt.SearchFragmentLexicalV1}, {searchFragmentGrams, stmt.SearchFragmentGramsV1}} {
		spec, _ := slot.slot.Specification()
		defs = append(defs, stmt.Definition{ID: slot.id, Kind: stmt.Write, SQL: spec.SQL,
			Params:  []stmt.Param{i("rowid"), t(spec.Column)},
			Cohorts: []stmt.Cohort{{Name: "nonempty", Bindings: []any{int64(501), "000061"}}, {Name: "empty", Bindings: []any{int64(502), ""}}},
			Plan:    stmt.Plan{Hot: true, NativeInsert: slot.slot}})
	}
	// Receipt storage shape is shared; fragment kind and non-NULL actual ID
	// have their own frozen definitions and saved-result decoder.
	for _, d := range searchPublisherReceiptDefinitions() {
		switch d.ID {
		case searchPublisherImagePut:
			d.ID = searchFragmentReceiptPut
			d.Params[10].Nullable = false
			d.Cohorts = []stmt.Cohort{{Name: "saved_actual_fragment", Bindings: []any{int64(29), make([]byte, 16), int64(8), make([]byte, 16), make([]byte, 32), searchFragmentReceiptKind,
				int64(20), int64(1000), int64(5), int64(307), int64(501), int64(1), int64(0), int64(501), make([]byte, 32), int64(3), int64(1), int64(8), int64(0), int64(0), searchmanifest.Algorithm}}}
			defs = append(defs, d)
		case searchPublisherImageRead:
			d.ID = searchFragmentReceiptRead
			d.Cohorts = []stmt.Cohort{{Name: "saved_fragment_without_native_join", Bindings: []any{int64(29), make([]byte, 16), int64(8), searchmanifest.Algorithm}}}
			defs = append(defs, d)
		}
	}
	for _, v := range []struct {
		id        stmt.ID
		head, due bool
	}{{searchFragmentNoHeadNoDue, false, false}, {searchFragmentNoHeadDue, false, true}, {searchFragmentHeadNoDue, true, false}, {searchFragmentHeadDue, true, true}} {
		q := `UPDATE search_publish_jobs SET staged_fragments=?,staged_bytes=?,after_field=?,after_ordinal=?,after_fragment_id=?,job_revision=?,last_command_sequence=?
WHERE id=? AND entity_id=? AND person_id IS NULL AND search_generation_id=? AND command_incarnation=?
AND job_revision=? AND input_generation=? AND source_incarnation=? AND source_revision=? AND recipe_version=?
AND phase=? AND document_id=? AND published_generation=?`
		params := []stmt.Param{i("next_staged_fragments"), i("next_staged_bytes"), i("next_field"), i("next_ordinal"), i("new_fragment"), i("next_revision"), i("new_sequence"),
			i("job_id"), i("entity_id"), i("bank_id"), b("incarnation"), i("job_revision"), i("input_generation"), b("source_incarnation"), i("source_revision"), t("recipe"), t("phase"), i("image_id"), i("published_generation")}
		args := []any{int64(1), int64(8), int64(1), int64(0), int64(501), int64(20), int64(8), int64(29), int64(41), int64(7), make([]byte, 16), int64(19), int64(5), make([]byte, 16), int64(11), searchpolicy.RecipeV1, "stage", int64(307), int64(0)}
		if v.head {
			q += ` AND published_document_id=?`
			params = append(params, i("published_image"))
			args[len(args)-1] = int64(3)
			args = append(args, int64(13))
		} else {
			q += ` AND published_document_id IS NULL`
		}
		q += ` AND after_field=? AND after_ordinal=? AND after_fragment_id=? AND expected_fragments=? AND staged_fragments=? AND staged_bytes=?
AND content_digest=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=?`
		params = append(params, i("after_field"), i("after_ordinal"), i("after_fragment"), i("expected_fragments"), i("staged_fragments"), i("staged_bytes"), t("content_digest"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"), i("attempts"))
		args = append(args, int64(0), int64(0), int64(0), int64(3), int64(0), int64(0), "", int64(1), int64(2), int64(2100000000000), int64(0))
		if v.due {
			q += ` AND job_next_attempt_ms=?`
			params = append(params, i("next_attempt_ms"))
			args = append(args, int64(1000))
		} else {
			q += ` AND job_next_attempt_ms IS NULL`
		}
		q += ` AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING job_revision`
		params = append(params, i("last_sequence"), i("ack_sequence"))
		args = append(args, int64(7), int64(7))
		defs = append(defs, stmt.Definition{ID: v.id, Kind: stmt.Write, SQL: q, Params: params, Cohorts: []stmt.Cohort{{Name: "exact_staging_owner", Bindings: args}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_publish_jobs"}, Seeks: []string{"rowid=?"}}})
	}
	for n := range defs {
		defs[n].Owner = "writer.search.publisher"
		defs[n].CallSites = []string{"writer/search_fragment_stage.go"}
	}
	return defs
}

func searchFragmentJobValues(c *searchFragmentStageCommand, fragment int64, next searchFragmentTransition) (stmt.ID, []any) {
	o := c.request.snapshot.job
	id := searchFragmentNoHeadNoDue
	if o.publishedImage.Valid {
		id = searchFragmentHeadNoDue
	}
	if o.nextAttempt.Valid {
		if o.publishedImage.Valid {
			id = searchFragmentHeadDue
		} else {
			id = searchFragmentNoHeadDue
		}
	}
	args := []any{next.stagedFragments, next.stagedBytes, next.afterField, next.afterOrdinal, fragment, next.jobRevision, c.identity.Sequence,
		o.id, o.entityID, o.bankID, c.identity.Incarnation[:], o.revision, o.inputGeneration, o.sourceIncarnation[:], o.sourceRevision, o.recipe, o.phase, o.document.Int64, o.publishedGeneration}
	if o.publishedImage.Valid {
		args = append(args, o.publishedImage.Int64)
	}
	args = append(args, o.afterField, o.afterOrdinal, o.afterFragmentID, o.expectedFragments, o.stagedFragments, o.stagedBytes,
		o.contentDigest, o.leaseOwner, o.leaseGeneration, o.leaseExpiresMS, o.attempts)
	if o.nextAttempt.Valid {
		args = append(args, o.nextAttempt.Int64)
	}
	return id, append(args, o.lastSequence, o.acknowledgedSequence)
}
