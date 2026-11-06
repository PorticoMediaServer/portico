package writer

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func evidenceProducerFixture(t *testing.T) (*executor, sealOwnerScope, EvidenceStagePayload) {
	t.Helper()
	w, scope := sealEffectFixture(t)
	// An actual native TMDB namespace. Inert synthetic data/codec values here are
	// NOT provider acquisition, a source-sealer proof or a production admission.
	for _, q := range []string{
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1)`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(151,'tmdb','movie','42','en-CA','CA','tmdb.movie.detail+credits.header-selected-credits.v1',zeroblob(16),1)`,
		`DELETE FROM metadata_subject_providers WHERE entity_id=101`,
		`UPDATE metadata_provider_operations SET provider='tmdb',selected_id='42',evidence_build_id=NULL WHERE id='operation-one'`,
		`DELETE FROM metadata_evidence_builds WHERE id='build-original:211'`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	scope.provider = "tmdb"
	scope.selectedID = "42"
	scope.selection.ID = "42"
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,status,observed_at) VALUES(101,'tmdb','primary',?,73,'movie','42','selected',?)`, scope.selection.Incarnation[:], scope.selectionObserved); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_evidence_totals(provider,releases,recordings,other_evidence,charge) VALUES('tmdb',0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	p, err := PrepareEvidenceStagePayload(draftMovieStageHeader(t), draftMovieStageCredits())
	if err != nil {
		t.Fatal(err)
	}
	return w, scope, p
}
func evidenceProducerDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, d := range enrichmentEvidenceDefinitions() {
		if d.ID == enrichmentEvidenceInsert {
			defs = append(defs, d)
		}
	}
	defs = append(defs, enrichmentEvidencePinDefinitions()...)
	defs = append(defs, enrichmentEvidenceStageEffectDefinitions()...)
	defs = append(defs, enrichmentSealAdvanceDefinitions()...)
	defs = append(defs, enrichmentSealReceiptDefinitions()...)
	return defs
}
func TestEvidenceProducerPrefixSealReceiptAndSingleCharge(t *testing.T) {
	w, scope, payload := evidenceProducerFixture(t)
	turn := sealEffectTurnPurpose(t, w, evidenceProducerDefinitions(), EvidenceStage)
	m := normalizedSealManifest{normalizedEvidenceAlgorithm, 1, 4, 1600, normalizedSealCommitment{17}}
	p := evidenceStagePrefix{root: evidenceSealRoot{buildID: "build-producer-original", evidenceID: payload.header.ID(), parser: payload.header.input.ParserVersion, buildGeneration: 89, subjectID: 151, rows: 4}, manifest: m, expires: "2100-11-05T13:00:00Z"}
	got, err := createEvidenceBuild(t.Context(), turn, scope, p, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.root.revision != 1 || got.next != 1 || got.providerOther != 1 || got.charge != got.providerCharge {
		t.Fatal("new root/header accounting", got)
	}
	got, err = stageEvidenceCredits(t.Context(), turn, scope, got, payload.Credits())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := turn.transaction.conn.QueryContext(t.Context(), `SELECT provider,evidence_kind,credit_class,evidence_id,scope,ordinal,provider_person_id,name,artist_name,sort_name,disambiguation,join_phrase,role,department,credit_id,source_ordinal,billing FROM metadata_evidence_credits WHERE evidence_id=? ORDER BY ordinal`, payload.header.ID())
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range payload.Credits() {
		if !stored.Next() {
			t.Fatal("missing persisted child", i)
		}
		var provider, kind, class, id, scopeValue, person, name, artist, sort, disambiguation, join, role, department, creditID string
		var ordinal, raw int64
		var billing sql.NullInt64
		if err := stored.Scan(&provider, &kind, &class, &id, &scopeValue, &ordinal, &person, &name, &artist, &sort, &disambiguation, &join, &role, &department, &creditID, &raw, &billing); err != nil {
			t.Fatal(err)
		}
		if provider != "tmdb" || kind != "movie" || class != string(want.Class) || id != payload.header.ID() || scopeValue != "" || ordinal != want.Ordinal || person != want.ProviderPersonID || name != want.Name || artist != "" || sort != "" || disambiguation != "" || join != "" || role != want.Role || department != want.Department || creditID != want.CreditID || raw != want.SourceOrdinal || billing.Valid != want.Billing.Present || billing.Int64 != want.Billing.Value {
			t.Fatal("native17-column credit projection drift", i)
		}
	}
	if stored.Next() {
		t.Fatal("extra persisted credit")
	}
	if err := stored.Err(); err != nil {
		t.Fatal(err)
	}
	if err := stored.Close(); err != nil {
		t.Fatal(err)
	}

	beforeSeal := got
	facts, err := advanceEvidenceSeal(t.Context(), turn, scope, got.root)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately inert synthetic result supplied at the W-owned seam. This
	// verifies SQL readback only, never W result encoding/production source proof.
	result := ResultDigest{193}
	content := sealReceiptContent{payload.header.input, m, 8876032}
	if err := writeEvidenceSealReceipt(t.Context(), turn, scope, got.root, facts, content, result); err != nil {
		t.Fatal(err)
	}
	if err := accountEvidenceSealReference(t.Context(), turn, scope, got.root, facts, m, got.charge, got.providerCharge, got.providerOther); err != nil {
		t.Fatal(err)
	}
	var sealed, sequence, charge, total, prefixCharge, rows, revision, pins int64
	var digest, buildPin string
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT e.sealed,e.seal_proof_sequence,a.charge,t.charge,b.materialized_charge,b.materialized_rows,b.content_revision,b.materialized_digest,o.evidence_build_id,r.proof_pin_count
 FROM metadata_evidence e JOIN metadata_evidence_accounting a ON a.evidence_id=e.id JOIN metadata_evidence_totals t ON t.provider=e.provider JOIN metadata_evidence_builds b ON b.evidence_id=e.id JOIN metadata_provider_operations o ON o.id=b.owner_operation JOIN enrichment_command_receipts r ON r.provider_operation_id=e.seal_proof_owner AND r.command_incarnation=e.seal_proof_incarnation AND r.command_sequence=e.seal_proof_sequence WHERE e.id=?`, payload.header.ID()).Scan(&sealed, &sequence, &charge, &total, &prefixCharge, &rows, &revision, &digest, &buildPin, &pins); err != nil {
		t.Fatal(err)
	}
	forecast, err := forecastEvidenceStage(payload, scope.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if sealed != 1 || sequence != 12 || charge != int64(forecast.charge) || total != charge || prefixCharge != charge || rows != 4 || revision != 3 || buildPin != p.root.buildID || pins != 1 || digest != "1100000000000000000000000000000000000000000000000000000000000000" {
		t.Fatal("double debit, missing proof bytes or native linkage", sealed, sequence, charge, total, prefixCharge, forecast, rows, revision, digest, buildPin, pins)
	}
	if beforeSeal.charge+8*int64(len(scope.ownerID)+16) != charge {
		t.Fatal("proof-reference delta", charge)
	}
	// No duplicate Q debit from retrying a stale accounting witness.
	if err := accountEvidenceSealReference(t.Context(), turn, scope, got.root, facts, m, got.charge, got.providerCharge, got.providerOther); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate debit accepted", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM metadata_evidence WHERE id=?`, payload.header.ID()).Scan(&n); err != nil || n != 0 {
		t.Fatal("whole producer/seal escaped rollback", n, err)
	}
}
func TestEvidenceProducerStaleAccountingRequiresWholeRollback(t *testing.T) {
	w, scope, payload := evidenceProducerFixture(t)
	turn := sealEffectTurnPurpose(t, w, evidenceProducerDefinitions(), EvidenceStage)
	p := evidenceStagePrefix{root: evidenceSealRoot{buildID: "build-producer-original", evidenceID: payload.header.ID(), parser: payload.header.input.ParserVersion, buildGeneration: 89, subjectID: 151, rows: 4}, manifest: normalizedSealManifest{normalizedEvidenceAlgorithm, 1, 4, 1600, normalizedSealCommitment{17}}, expires: "2100-11-05T13:00:00Z", providerCharge: 1}
	if out, err := createEvidenceBuild(t.Context(), turn, scope, p, payload); !errors.Is(err, ErrConflict) || out.root.buildID != "" {
		t.Fatal("stale provider total accepted", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM metadata_evidence WHERE id=?`, payload.header.ID()).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial failed allocation survived", n, err)
	}
}
