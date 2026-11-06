package writer

import (
	"database/sql"
	"reflect"
	"testing"
)

// Each new intent leaf must fence a previously prepared request. Set fields
// move together in native SQL because the schema requires the nullable triple
// to be coherent; independent per-field decode/hash controls cover each member.
func TestEffectiveBeginFrozenNativeOperationIntent(t *testing.T) {
	w, _, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_publication_sets(id,entity_id,provider,incarnation,revision,generation,subject_kind,provider_id,consent_revision,policy_incarnation,policy_revision)
 SELECT 'unsupported-first-set',entity_id,provider,zeroblob(16),181,generation,'movie','set-opaque:191',consent_revision,policy_incarnation,policy_revision FROM metadata_provider_operations WHERE id='operation-one'`); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]string{
		"query_title":      `query_title='Changed movie request'`,
		"query_artist":     `query_artist='Changed request artist'`,
		"query_year":       `query_year=1987`,
		"query_digest":     `query_digest='changed-query-digest'`,
		"manual":           `manual=0`,
		"review_search":    `review_search=1`,
		"hint_id":          `hint_id='changed-hint-id'`,
		"hint_problem":     `hint_problem='changed-hint-problem'`,
		"qualification":    `qualification='different-qualification'`,
		"episode_order":    `episode_order='aired'`,
		"set_id":           `set_id='unsupported-first-set',set_subject_kind='movie',set_provider_id='set-opaque:191'`,
		"set_subject_kind": `set_id='unsupported-first-set',set_subject_kind='movie',set_provider_id='set-opaque:191'`,
		"set_provider_id":  `set_id='unsupported-first-set',set_subject_kind='movie',set_provider_id='set-opaque:191'`,
	}
	for column, set := range mutations {
		t.Run(column, func(t *testing.T) {
			turn, _ := effectiveBeginWholeTurn(t, w)
			if _, err := turn.transaction.conn.ExecContext(t.Context(), "UPDATE metadata_provider_operations SET "+set+" WHERE id='operation-one'"); err != nil {
				t.Fatal(err)
			}
			_, applyErr := applyEffectiveStageBegin(t.Context(), turn, input)
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if applyErr == nil {
				t.Fatal("fresh BEGIN accepted changed native intent outside the frozen request")
			}
		})
	}
}

// The native schema is the coverage authority. A newly added operation column
// cannot disappear merely because nobody added it to the DTO or reflection walk.
func TestEffectiveBeginOperationSchemaCaptureCoverage(t *testing.T) {
	w, _, request, _ := effectiveBeginWholeFixture(t)
	rows, err := w.db.QueryContext(t.Context(), "PRAGMA table_xinfo(metadata_provider_operations)")
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var cid, notNull, pk, hidden int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk, &hidden); err != nil {
			t.Fatal(err)
		}
		if hidden != 0 {
			t.Fatal("unclassified generated operation column", name)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	// Each non-input has its own contract; custody is not an audit field.
	// CHANGE-005 evidence-header BEGIN creates and pins the evidence build.
	// Effective-image BEGIN neither consumes that pin as authority nor creates,
	// resolves or detaches it. Its source/provenance checks remain independent.
	// Preserve existing build custody through the original result's ACK; the
	// native NULL and retained-pin controls below/alongside this test prove that
	// image BEGIN does not change it. The complete physical snapshot also checks
	// preservation of the other outputs/audit fields.
	nonInputs := map[string]string{
		"result_digest":     "prior provider result output",
		"created_at":        "creation audit",
		"finished_at":       "completion audit",
		"reason":            "completion diagnostic",
		"evidence_build_id": "evidence-header command custody, preserved through source-dependent result ACK; not image-BEGIN authority",
	}
	for _, variant := range []string{"null_set", "distinct_set"} {
		t.Run(variant, func(t *testing.T) {
			if variant == "distinct_set" {
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_publication_sets(id,entity_id,provider,incarnation,revision,generation,subject_kind,provider_id,consent_revision,policy_incarnation,policy_revision)
 SELECT 'set-native:181',entity_id,provider,zeroblob(16),197,generation,'series','set-provider:191',consent_revision,policy_incarnation,policy_revision FROM metadata_provider_operations WHERE id='operation-one'`); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET set_id='set-native:181',set_subject_kind='series',set_provider_id='set-provider:191',episode_order='episode-order:193' WHERE id='operation-one'`); err != nil {
					t.Fatal(err)
				}
			}
			physical := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_provider_operations WHERE id='operation-one'")
			turn, steps := effectiveBeginFactTurn(t, w)
			owner, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], request.ownerID)
			if err != nil {
				t.Fatal(err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			nullableText := func(n sql.NullString) any {
				if n.Valid {
					return n.String
				}
				return nil
			}
			nullableInt := func(n sql.NullInt64) any {
				if n.Valid {
					return n.Int64
				}
				return nil
			}
			captured := map[string]any{
				"id": request.ownerID, "entity_id": owner.entityID, "provider": owner.provider, "phase": owner.phase, "status": owner.status, "generation": owner.generation, "lease": owner.lease, "lease_until": owner.leaseUntil,
				"entity_incarnation": owner.entityIncarnation[:], "source_revision": owner.sourceRevision, "identity_revision": owner.identityRevision, "metadata_revision": owner.metadataRevision, "hierarchy_revision": owner.hierarchyRevision, "selection_revision": owner.selectionRevision, "descriptive_revision": owner.descriptiveRevision,
				"policy_incarnation": owner.policyIncarnation[:], "policy_revision": owner.policyRevision, "consent_revision": owner.consentRevision, "algorithm": owner.algorithm, "refresh_mode": owner.refreshMode,
				"query_title": owner.queryTitle, "query_artist": owner.queryArtist, "query_year": owner.queryYear, "query_digest": owner.queryDigest, "input_digest": owner.inputDigest, "selected_id": owner.selectedID, "manual": owner.manual, "review_search": owner.reviewSearch,
				"hint_id": owner.hintID, "hint_problem": owner.hintProblem, "qualification": owner.qualification, "episode_order": owner.episodeOrder,
				"set_id": nullableText(owner.setID), "set_subject_kind": nullableText(owner.setSubjectKind), "set_provider_id": nullableText(owner.setProviderID),
				"page": owner.page, "after_item": owner.afterItem, "command_incarnation": owner.incarnation[:], "last_command_sequence": owner.lastSequence, "acknowledged_command_sequence": owner.acknowledgedSequence, "credit_image_id": nullableInt(owner.imageID),
			}
			for _, column := range columns {
				got, ok := captured[column]
				if !ok {
					if nonInputs[column] == "" {
						t.Fatal("native operation column lacks capture or explicit non-input contract", column)
					}
					continue
				}
				if !reflect.DeepEqual(got, physical[column]) {
					t.Fatalf("native operation column %s not captured exactly: got %v want %v", column, got, physical[column])
				}
			}
			if len(captured)+len(nonInputs) != len(columns) {
				t.Fatal("stale or duplicate operation coverage", len(captured), len(nonInputs), len(columns))
			}
			// Independently decode the coherent non-NULL triple, then refuse FIRST.
			if variant == "distinct_set" {
				if owner.setID.String != "set-native:181" || owner.setSubjectKind.String != "series" || owner.setProviderID.String != "set-provider:191" || owner.episodeOrder != "episode-order:193" {
					t.Fatal("set/episode native projection swap")
				}
				changed := request
				changed.owner = owner
				if _, err := newEffectiveStageBeginCommand(CommandIdentity{Incarnation: owner.incarnation, Sequence: owner.lastSequence + 1}, changed); err == nil {
					t.Fatal("FIRST accepted native set/episode intent")
				}
			}
		})
	}
}
