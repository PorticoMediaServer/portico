package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Groups D and E are embedded, so the IP receipt/proof tuple, the owner field
// presence columns and the TV set proof are qualified against the canonical
// schema. The provider operation, subject binding, evidence body/build/children,
// publication, subject-provider, field-origin, entity-ID and term bodies are
// the real embedded schema.
type ipEntityDeltaSetup struct {
	first    ipEntityAcceptStaged
	accepted ipEntityAcceptResult
	owner    ipEntityJobResult
	claim    CommandIdentity
}

func ipEntityDeltaFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, ipEntityDeltaSetup) {
	t.Helper()
	return ipEntityDeltaFixtureAccepting(t, ipEntityDeltaAcceptedFields())
}

// ipEntityDeltaFixtureAccepting runs the baseline acceptance with an explicit
// typed field cohort, so a test can choose a cohort whose fields all have a
// durable witness. The owner-field-edit tables are created here because the
// unlock/edit regression goes through the real IP-24 edit command.
func ipEntityDeltaFixtureAccepting(t *testing.T, acceptedFields []ipEntityAcceptField) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, ipEntityDeltaSetup) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, query := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',1,0,1,1,0)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider,command_incarnation) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'tvdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(1,'tmdb','series','tmdb-1','en','US','` + ipEntityEvidenceRequest("tmdb", "series") + `',X'0c000000000000000000000000000000',1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(query, err)
		}
	}
	defs := append(append(append(append(append(ipPolicyDefinitions(), ipEntityJobDefinitions()...),
		ipEntityEvidenceDefinitions()...), ipEntitySealDefinitions()...), ipTVSetDefinitions()...),
		ipEntityAcceptDefinitions()...)
	defs = append(defs, ipEntityDeltaDefinitions()...)
	defs = append(defs, ipTextEditDefinitions()...)
	registry, err := stmttest.New(t, ipEntityDeltaDedupe(defs))
	if err != nil {
		t.Fatal(err)
	}
	r := &ipPolicyRegistry{registry, keeper}
	staged := ipEntityAcceptStage(t, w, r, p)
	ipEntityAcceptSeedHead(t, w)
	acceptedRequest := ipEntityAcceptRequestFor(staged)
	acceptedRequest.fields = acceptedFields
	accepted, turn, tx := ipEntityAcceptApply(t, w, r, p,
		ipEntityAcceptCommandFor(t, staged, acceptedRequest))
	if accepted.Outcome.State != Applied || accepted.Value.PublicationID != "publication-one" {
		t.Fatal("baseline accept", accepted)
	}
	ipPolicyCommitTurn(t, turn, tx)
	owner, claim := ipEntityDeltaSecondClaim(t, w, r, p)
	return w, r, p, ipEntityDeltaSetup{first: staged, accepted: accepted.Value, owner: owner, claim: claim}
}

// ipEntityDeltaDedupe keeps one definition per statement ID. ipTextEditDefinitions
// embeds the IP entry and US authority families, which the accept/delta fixture
// already registers; the duplicated definitions are byte-identical.
func ipEntityDeltaDedupe(defs []stmt.Definition) []stmt.Definition {
	seen := make(map[stmt.ID]bool, len(defs))
	out := make([]stmt.Definition, 0, len(defs))
	for _, def := range defs {
		if seen[def.ID] {
			continue
		}
		seen[def.ID] = true
		out = append(out, def)
	}
	return out
}

// ipEntityDeltaSecondClaim claims one NEW observation owner after the baseline
// acceptance moved the entity revision. It reuses IP-05's exact claim path.
func ipEntityDeltaSecondClaim(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof) (ipEntityJobResult, CommandIdentity) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending',generation=2,revision=2,lease='',lease_until_ms=0,next_attempt_ms=0,attempts=1,error='' WHERE entity_id=1 AND provider='tmdb'`)
	identity := ipEntityJobIdentity(0x41, 1, 0x51)
	q := ipEntityJobClaimFor(1, "tmdb")
	q.jobGeneration, q.jobRevision, q.entityRevision, q.metadataRevision = 2, 2, 2, 2
	c, err := newIPEntityClaimCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	out, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, c)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, out.Value, 1)
	return out.Value, identity
}

// ipEntityDeltaEvidenceIdentity is the delta fixture's second evidence chain and
// delta-command identity: the shared evidence identity with a distinct
// operation-ID salt (0x6c), so the second chain can never collide with the first
// accept chain's operation IDs in one fixture database.
func ipEntityDeltaEvidenceIdentity(claim CommandIdentity, sequence int64) CommandIdentity {
	return ipEntityEvidenceChainIdentity(claim, sequence, 0x6c)
}

// ipEntityDeltaStageSecond stages and seals one second observation under the
// new owner. observationDigest is the provider content digest of that separate
// observation; no-op identity is the typed cohort, because the schema keeps one
// evidence row per content digest.
func ipEntityDeltaStageSecond(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	setup ipEntityDeltaSetup, evidenceID, buildID string, pages []ipEntitySealPage,
	observationDigest [32]byte) ipEntityAcceptStaged {
	t.Helper()
	owner, claim := setup.owner, setup.claim
	declarations := ipEntitySealDeclarations(pages)
	counts := ipEntitySealDeclaredCounts(declarations)
	bq := ipEntityEvidenceBeginFor(owner, evidenceID, buildID, counts)
	bq.families = declarations
	bq.header = ipEntityEvidenceHeader(evidenceID)
	bq.header.ObservationDigest = observationDigest
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p,
		mustEvidenceBegin(t, ipEntityDeltaEvidenceIdentity(claim, 2), bq))
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityEvidenceAck(t, w, first.Value, 2)

	current, before := first.Value, int64(1)
	sequence := int64(3)
	for _, page := range pages {
		// The closed order is alias, external, term, rating, episode, media,
		// track, credit; familyStart is the global cursor start and stays
		// distinct from the family after-ordinal for a multi-page family.
		familyStart := before - page.AfterOrdinal
		request := ipEntitySealPageRequest(owner, current, page, counts[page.Family], familyStart, before)
		out, turn, tx := ipEntityEvidenceApplyAppend(t, w, r, p,
			mustEvidenceAppend(t, ipEntityDeltaEvidenceIdentity(claim, sequence), request))
		ipPolicyCommitTurn(t, turn, tx)
		ipEntityEvidenceAck(t, w, out.Value, sequence)
		current = out.Value
		before += page.rows
		sequence++
	}
	seal := ipEntitySealRequestFor(owner, current, declarations, pages)
	seal.observationDigest = observationDigest
	staged := ipEntityAcceptStaged{owner: owner, claim: claim, pages: pages,
		declarations: declarations, begin: current, sequence: sequence, seal: seal}
	sealCommand, err := newIPEntitySealCommand(ipEntityDeltaEvidenceIdentity(claim, sequence), seal)
	if err != nil {
		t.Fatal(err)
	}
	out, turn, tx := ipEntitySealApply(t, w, r, p, sealCommand)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: owner.OperationID, Provider: owner.Provider}, sequence)
	return staged
}

// ipEntityDeltaAcceptedFields is the baseline accepted cohort. Every name has a
// durable witness: the four projection fields are catalog columns (overview is
// the locked description owner row in the fixture) and the rest are catalog
// columns too. Provenance-only names are deliberately absent, because accept
// records no value for them.
func ipEntityDeltaAcceptedFields() []ipEntityAcceptField {
	return []ipEntityAcceptField{
		{Name: "title", Value: "Accepted Series", Present: true},
		{Name: "language", Value: "en", Present: true},
		{Name: "overview", Value: "Provider overview", Present: true},
		{Name: "releaseDate", Value: "2020-01-01", Present: true},
		{Name: "year", Value: "2020", Present: true},
	}
}

// ipEntityDeltaBaseFields is the SAME accepted cohort a second observation may
// present as an exact no-op. No provenance-only field is present, so every
// value can be proved against a durable witness.
func ipEntityDeltaBaseFields() []ipEntityAcceptField {
	return ipEntityDeltaAcceptedFields()
}

func ipEntityDeltaSecondFields() []ipEntityAcceptField {
	return []ipEntityAcceptField{
		{Name: "title", Value: "Replacement Series", Present: true},
		{Name: "language", Value: "en", Present: true},
		{Name: "overview", Value: "Replacement overview", Present: true},
		{Name: "releaseDate", Value: "2021-02-02", Present: true},
		{Name: "year", Value: "2021", Present: true},
	}
}

// ipEntityDeltaStatusFields adds one provenance-only field (status) to a cohort.
// Accept stores no durable value for that name, so the delta can never prove it
// identical: this cohort is the conservative-publish regression case.
func ipEntityDeltaStatusFields() []ipEntityAcceptField {
	return append(ipEntityDeltaAcceptedFields(), ipEntityAcceptField{Name: "status", Value: "Returning", Present: true})
}

func ipEntityDeltaObservationFor(staged ipEntityAcceptStaged, evidenceID, buildID, publicationID string,
	selectionRevision int64, fields []ipEntityAcceptField) ipEntityAcceptRequest {
	return ipEntityAcceptRequest{
		operationID: staged.owner.OperationID, provider: staged.owner.Provider,
		purpose: "metadata", selectedID: staged.owner.SelectedID, lease: staged.owner.Lease,
		entityID: staged.owner.EntityID, generation: 1, leaseUntilMS: staged.owner.LeaseUntilMS,
		evidenceID: evidenceID, buildID: buildID, publicationID: publicationID,
		buildGeneration: 1, contentRevision: staged.begin.Revision + 1,
		subjectID: 1, subjectKind: "series", providerID: "tmdb-1",
		parserVersion: ipEntityEvidenceParser("tmdb", "series"), language: "en", region: "US",
		requestShape: ipEntityEvidenceRequest("tmdb", "series"), observedAt: "2026-01-01T00:00:00Z",
		selectionIncarnation: [16]byte{0x0d}, selectionRevision: selectionRevision, searchJobID: 1,
		fields: fields,
		external: []ipEntityAcceptExternal{
			{Provider: "tmdb", SubjectKind: "series", ProviderID: "tmdb-1"},
		},
		terms: []ipEntityAcceptTerm{
			{Vocabulary: "genre", Ordinal: 0, Value: "Drama"},
		},
		credits: []ipEntityAcceptCredit{
			{Class: "cast", Ordinal: 0, ProviderPersonID: "person-1", CreditID: "credit-1",
				Name: "Actor One", Role: "Actor", Department: "Acting", Billing: 1,
				BillingPresent: true, SourceOrdinal: 0, SourceOrdinalPresent: true},
			{Class: "crew", Ordinal: 1, ProviderPersonID: "person-2", CreditID: "credit-2",
				Name: "Director One", Role: "Director", Department: "Directing",
				SourceOrdinal: 1, SourceOrdinalPresent: true},
		},
	}
}

func ipEntityDeltaRequestFor(t *testing.T, staged ipEntityAcceptStaged, setup ipEntityDeltaSetup,
	publicationID string, fields []ipEntityAcceptField) ipEntityDeltaRequest {
	t.Helper()
	return ipEntityDeltaRequest{
		observation: ipEntityDeltaObservationFor(staged, "evidence-two", "build-two", publicationID,
			setup.accepted.SelectionRevision+1, fields),
		previousPublicationID: setup.accepted.PublicationID,
		previousEvidenceID:    setup.accepted.EvidenceID,
		previousSetID:         setup.accepted.SetID,
	}
}

// ipEntityDeltaPagesWithTerm replaces the term page by FAMILY NAME, never by a
// hard-coded family index, so an upstream family re-order keeps this test real.
func ipEntityDeltaPagesWithTerm(value string) []ipEntitySealPage {
	pages := ipEntityAcceptPages()
	for i := range pages {
		if pages[i].Family == "term" {
			pages[i].terms = []ipEvidenceTerm{{Ordinal: 0, Vocabulary: "genre", Value: value}}
		}
	}
	return pages
}

func ipEntityDeltaCommandFor(t *testing.T, staged ipEntityAcceptStaged,
	q ipEntityDeltaRequest) *ipEntityDeltaCommand {
	t.Helper()
	c, err := newIPEntityDeltaCommand(ipEntityDeltaEvidenceIdentity(staged.claim, staged.sequence+1), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipEntityDeltaApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipEntityDeltaCommand) (ipEntityDeltaCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
	out, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("delta", out, err)
	}
	return out, turn, tx
}

func TestIPEntityDeltaReplaceRetainsOldSourceAndReplays(t *testing.T) {
	w, r, p, setup := ipEntityDeltaFixture(t)
	second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
		ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
	out, turn, tx := ipEntityDeltaApply(t, w, r, p,
		ipEntityDeltaCommandFor(t, second, ipEntityDeltaRequestFor(t, second, setup,
			"publication-two", ipEntityDeltaSecondFields())))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Digest == (ResultDigest{}) ||
		out.Value.OperationID != setup.owner.OperationID ||
		out.Value.PublicationID != "publication-two" ||
		out.Value.PreviousPublicationID != "publication-one" ||
		out.Value.EvidenceID != "evidence-two" || out.Value.PreviousEvidenceID != "evidence-one" ||
		out.Value.NoOp || out.Value.SetID != "" || out.Value.PreviousSetID != "" ||
		out.Value.Provider != "tmdb" || out.Value.SubjectKind != "series" ||
		out.Value.ProviderID != "tmdb-1" || out.Value.ParserVersion != ipEntityEvidenceParser("tmdb", "series") ||
		out.Value.ObservedAt != "2026-01-01T00:00:00Z" || out.Value.EntityID != 1 ||
		out.Value.OwnerGeneration != 1 || out.Value.ContentRevision != second.begin.Revision+1 ||
		out.Value.AcceptedRevision != 3 || out.Value.SelectionRevision != 2 ||
		out.Value.SearchJobID != 1 || out.Value.FieldCount != 5 ||
		out.Value.ExternalCount != 1 || out.Value.TermCount != 1 ||
		out.Value.CreditCount != 2 || out.Value.LeaseUntilMS != setup.owner.LeaseUntilMS ||
		out.Value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publications WHERE id='publication-two' AND entity_id=1 AND provider='tmdb' AND subject_kind='series' AND provider_id='tmdb-1' AND evidence_id='evidence-two' AND set_id IS NULL AND previous_id='publication-one' AND previous_provider='tmdb' AND previous_subject_kind='series' AND previous_provider_id='tmdb-1' AND selection_revision=2 AND generation=1 AND decision='accepted' AND operation_id='` + setup.owner.OperationID + `'`: 1,
		`SELECT count(*) FROM metadata_subject_providers WHERE entity_id=1 AND provider='tmdb' AND role='primary' AND accepted_evidence_id='evidence-two' AND accepted_set_id IS NULL AND accepted_publication_id='publication-two' AND revision=3`:                                                                                                                                                                                                                               1,
		`SELECT count(*) FROM metadata_publications WHERE id='publication-one' AND evidence_id='evidence-one' AND selection_revision=1 AND decision='accepted'`:                                                                                                                                                                                                                                                                                                                   1,
		`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND owner_id='` + setup.first.owner.OperationID + `'`:                                                                                                                                                                                                                                                                                                                                2,
		`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND owner_id='` + setup.owner.OperationID + `'`:                                                                                                                                                                                                                                                                                                                                      2,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta' AND publication_id='publication-two' AND evidence_id='evidence-two' AND terminal=1 AND result_phase='accepted' AND result_count=5`:                                                                                                                                                                                                                                                                  1,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept'`: 1,
		// The fixture locks the owner 'description' field (the catalogue overview)
		// to an explicit empty, so the replacement keeps that locked effective
		// value in the projection and retains the provider observation only as the
		// automatic value; the unlocked title and year are replaced in place.
		`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Replacement Series' AND overview='' AND year=2021 AND revision=3 AND metadata_revision=3`:                                                                  1,
		`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='' AND value_present=1 AND locked=1 AND automatic_value='Replacement overview' AND automatic_present=1`: 1,
		`SELECT count(*) FROM metadata_field_origins WHERE entity_id=1 AND publication_id='publication-two' AND evidence_id='evidence-two'`:                                                                                     5,
		`SELECT count(*) FROM metadata_field_origins WHERE entity_id=1 AND publication_id='publication-one'`:                                                                                                                    0,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + setup.owner.OperationID + `'`:                                                                                                             second.sequence + 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	// Replay after unrelated source, lease, policy and entity changes returns
	// the original saved replacement before any fresh probe and writes nothing.
	original := out
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending',generation=9,revision=9,lease='other',lease_until_ms=1000000 WHERE entity_id=1`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=9,revision=9 WHERE id=1`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
	replay, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b,
		ipEntityDeltaCommandFor(t, second, ipEntityDeltaRequestFor(t, second, setup,
			"publication-two", ipEntityDeltaSecondFields())))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value ||
		replay.Digest != original.Digest {
		t.Fatal("delta replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publications`:                           2,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta'`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("delta replay wrote rows", query, got)
		}
	}

	// At or below ACK is Retired. Both the new and the retained old source, and
	// the old pin, stay physically present.
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: setup.owner.OperationID,
		Provider: setup.owner.Provider}, second.sequence+1)
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityDelta)
	retired, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b,
		ipEntityDeltaCommandFor(t, second, ipEntityDeltaRequestFor(t, second, setup,
			"publication-two", ipEntityDeltaSecondFields())))
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipEntityDeltaResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired delta served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta' AND terminal=1`:                                                      1,
		`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND owner_id='` + setup.first.owner.OperationID + `'`: 2,
		`SELECT count(*) FROM metadata_publications WHERE id='publication-one'`:                                                                    1,
		`SELECT count(*) FROM metadata_publications WHERE id='publication-two'`:                                                                    1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("retired delta lost a proof", query, got)
		}
	}
}

// An observation carrying the exact accepted typed cohort is an exact no-op: the
// accepted publication and selection revision stay put, the new source is not
// charged as a downstream proof, and only one terminal delta receipt records the
// attempt. A changed typed value with the same observation shape is NOT a no-op.
func TestIPEntityDeltaExactNoOpVersusNewObservation(t *testing.T) {
	t.Run("exact_noop", func(t *testing.T) {
		w, r, p, setup := ipEntityDeltaFixture(t)
		second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
			ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
		out, turn, tx := ipEntityDeltaApply(t, w, r, p, ipEntityDeltaCommandFor(t, second,
			ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaBaseFields())))
		if out.Outcome.State != Applied || !out.Value.NoOp ||
			out.Value.PublicationID != "publication-one" ||
			out.Value.PreviousPublicationID != "publication-one" ||
			out.Value.EvidenceID != "evidence-one" || out.Value.AcceptedRevision != 2 {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM metadata_publications`: 1,
			`SELECT count(*) FROM metadata_subject_providers WHERE entity_id=1 AND accepted_publication_id='publication-one' AND accepted_evidence_id='evidence-one' AND revision=2`:     1,
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Accepted Series' AND revision=2 AND metadata_revision=2`:                                                        1,
			`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND owner_id='` + setup.first.owner.OperationID + `'`:                                   2,
			`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND owner_id='` + setup.owner.OperationID + `'`:                                         1,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta' AND publication_id='publication-one' AND evidence_id='evidence-two' AND terminal=1 AND result_count=5`: 1,
			`SELECT count(*) FROM metadata_field_origins WHERE publication_id='publication-one'`:                                                                                         5,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
	t.Run("changed_owner_automatic_is_new", func(t *testing.T) {
		w, r, p, setup := ipEntityDeltaFixture(t)
		second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
			ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
		fields := ipEntityDeltaBaseFields()
		// The fixture's description row is locked, so only its automatic
		// observation moves; the newer provider value still makes this a new
		// observation rather than an exact no-op.
		fields[2].Value = "Changed overview"
		out, turn, tx := ipEntityDeltaApply(t, w, r, p, ipEntityDeltaCommandFor(t, second,
			ipEntityDeltaRequestFor(t, second, setup, "publication-two", fields)))
		if out.Outcome.State != Applied || out.Value.NoOp ||
			out.Value.PublicationID != "publication-two" {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
	})
	// A provenance-only field (status, sortName, disambiguation, type, isbn,
	// pageCount) has no durable accepted-value witness, so the delta cannot prove
	// its value identical even when the sealed child cohort is byte-identical.
	// The conservative answer is a replacement head, never a claimed no-op that
	// could drop a changed value. SOL-INGEST owns the witness decision.
	t.Run("provenance_only_field_cannot_prove_noop", func(t *testing.T) {
		w, r, p, setup := ipEntityDeltaFixtureAccepting(t, ipEntityDeltaStatusFields())
		second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
			ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
		out, turn, tx := ipEntityDeltaApply(t, w, r, p, ipEntityDeltaCommandFor(t, second,
			ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaStatusFields())))
		if out.Outcome.State != Applied || out.Value.NoOp ||
			out.Value.PublicationID != "publication-two" ||
			out.Value.PreviousPublicationID != "publication-one" {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='status'`); got != 0 {
			t.Fatal("provenance-only field gained a phantom owner row", got)
		}
	})
	t.Run("changed_sealed_cohort_is_new", func(t *testing.T) {
		w, r, p, setup := ipEntityDeltaFixture(t)
		second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
			ipEntityDeltaPagesWithTerm("Comedy"), ipEntityEvidenceDigest("evidence-two"))
		q := ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaBaseFields())
		q.observation.terms[0].Value = "Comedy"
		out, turn, tx := ipEntityDeltaApply(t, w, r, p, ipEntityDeltaCommandFor(t, second, q))
		if out.Outcome.State != Applied || out.Value.NoOp ||
			out.Value.PublicationID != "publication-two" {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
	})
	t.Run("changed_typed_value_is_new", func(t *testing.T) {
		w, r, p, setup := ipEntityDeltaFixture(t)
		second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
			ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
		out, turn, tx := ipEntityDeltaApply(t, w, r, p, ipEntityDeltaCommandFor(t, second,
			ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaSecondFields())))
		if out.Outcome.State != Applied || out.Value.NoOp ||
			out.Value.PublicationID != "publication-two" {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM metadata_publications WHERE id='publication-two' AND previous_id='publication-one'`: 1,
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Replacement Series' AND revision=3`:          1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
}

func TestIPEntityDeltaWrongPreviousHeadRefused(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*ipEntityDeltaRequest)
		sql       []string
		unchecked bool
		want      error
	}{
		{"wrong_publication_id", func(q *ipEntityDeltaRequest) { q.previousPublicationID = "publication-x" }, nil, false, ErrConflict},
		{"wrong_previous_evidence", func(q *ipEntityDeltaRequest) { q.previousEvidenceID = "evidence-x" }, nil, false, ErrConflict},
		{"stale_selection_revision", func(q *ipEntityDeltaRequest) { q.observation.selectionRevision = 1 }, nil, false, ErrConflict},
		{"selection_revision_advanced", nil, []string{`UPDATE metadata_subject_providers SET revision=3 WHERE entity_id=1`}, false, ErrConflict},
		{"selection_evidence_moved", nil, []string{`UPDATE metadata_subject_providers SET accepted_evidence_id='evidence-two' WHERE entity_id=1`}, true, ErrConflict},
		{"prior_superseded", nil, []string{`UPDATE metadata_publications SET decision='superseded' WHERE id='publication-one'`}, false, ErrConflict},
		{"prior_selection_revision", nil, []string{`UPDATE metadata_publications SET selection_revision=0 WHERE id='publication-one'`}, false, ErrConflict},
		{"prior_evidence_mismatch", nil, []string{`UPDATE metadata_publications SET evidence_id=NULL,ip_proof_owner=NULL,ip_proof_incarnation=NULL,ip_proof_sequence=NULL WHERE id='publication-one'`}, true, ErrConflict},
		{"prior_evidence_unsealed", nil, []string{`UPDATE metadata_evidence SET sealed=0,ip_seal_owner=NULL,ip_seal_incarnation=NULL,ip_seal_sequence=NULL WHERE id='evidence-one'`}, false, ErrConflict},
		{"prior_evidence_foreign", nil, []string{`UPDATE metadata_evidence SET region='CA' WHERE id='evidence-one'`}, true, ErrConflict},
		{"prior_pin_drained", nil, []string{`UPDATE metadata_ip_receipts SET proof_pin_count=0 WHERE command_kind='evidence_seal' AND owner_id=(SELECT operation_id FROM metadata_publications WHERE id='publication-one')`}, false, ErrConflict},
		{"prior_seal_not_terminal", nil, []string{`UPDATE metadata_ip_receipts SET terminal=0 WHERE command_kind='evidence_seal' AND owner_id=(SELECT operation_id FROM metadata_publications WHERE id='publication-one')`}, false, ErrConflict},
		{"prior_seal_sequence_moved", nil, []string{`UPDATE metadata_ip_receipts SET command_sequence=999 WHERE command_kind='evidence_seal' AND owner_id=(SELECT operation_id FROM metadata_publications WHERE id='publication-one')`}, true, ErrConflict},
		{"search_intent_missing", nil, []string{`DELETE FROM search_publish_jobs WHERE id=1`}, false, ErrResolveRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, setup := ipEntityDeltaFixture(t)
			second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
				ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
			if tc.unchecked {
				ipEntityDeltaExecUnchecked(t, w, tc.sql...)
			} else {
				for _, query := range tc.sql {
					ipPolicyExec(t, w, query)
				}
			}
			q := ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaSecondFields())
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			c, err := newIPEntityDeltaCommand(
				ipEntityDeltaEvidenceIdentity(second.claim, second.sequence+1), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
			want := tc.want
			if want == nil {
				want = ErrConflict
			}
			out, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b, c)
			if out != (ipEntityDeltaCommandResult{}) || !errors.Is(err, want) {
				t.Fatal("wrong previous head admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_publications`:                                   1,
				`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta'`:         0,
				`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Accepted Series'`: 1,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal("refused delta wrote state", query, got)
				}
			}
		})
	}
}

// A late failure after the new publication and the selection CAS rolls the
// WHOLE replacement back: the old accepted head stays the visible one and the
// entity projection keeps its old automatic values.
func TestIPEntityDeltaRollbackKeepsOldAcceptedVisibility(t *testing.T) {
	w, r, p, setup := ipEntityDeltaFixture(t)
	second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
		ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
	// The new source pin cannot be charged: the retain statement matches no row
	// after the publication insert, the CAS and the projection write.
	ipPolicyExec(t, w, `UPDATE metadata_ip_receipts SET proof_pin_count=9223372036854775807 WHERE command_kind='evidence_seal' AND owner_id='`+setup.owner.OperationID+`'`)
	c := ipEntityDeltaCommandFor(t, second, ipEntityDeltaRequestFor(t, second, setup,
		"publication-two", ipEntityDeltaSecondFields()))
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
	out, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b, c)
	if out != (ipEntityDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("uncharged new source admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publications WHERE id='publication-two'`:                                                                                                  0,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta'`:                                                                                                   0,
		`SELECT count(*) FROM metadata_subject_providers WHERE entity_id=1 AND accepted_publication_id='publication-one' AND accepted_evidence_id='evidence-one' AND revision=2`: 1,
		`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Accepted Series' AND revision=2 AND metadata_revision=2`:                                                    1,
		`SELECT count(*) FROM metadata_field_origins WHERE publication_id='publication-two'`:                                                                                     0,
		`SELECT count(*) FROM metadata_field_origins WHERE publication_id='publication-one'`:                                                                                     5,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("delta rollback lost the old head", query, got)
		}
	}
}

func TestIPEntityDeltaShapeRefusals(t *testing.T) {
	w, r, p, setup := ipEntityDeltaFixture(t)
	_ = w
	_ = r
	_ = p
	second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
		ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
	base := ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaSecondFields())
	longID := strings.Repeat("x", ipEntityAcceptIDMax+1)
	identity := ipEntityDeltaEvidenceIdentity(second.claim, second.sequence+1)
	mutations := []struct {
		name   string
		mutate func(*ipEntityDeltaRequest)
	}{
		{"empty_previous_publication", func(q *ipEntityDeltaRequest) { q.previousPublicationID = "" }},
		{"long_previous_publication", func(q *ipEntityDeltaRequest) { q.previousPublicationID = longID }},
		{"empty_previous_evidence", func(q *ipEntityDeltaRequest) { q.previousEvidenceID = "" }},
		{"long_previous_evidence", func(q *ipEntityDeltaRequest) { q.previousEvidenceID = longID }},
		{"previous_evidence_is_new", func(q *ipEntityDeltaRequest) { q.previousEvidenceID = q.observation.evidenceID }},
		{"previous_publication_is_new", func(q *ipEntityDeltaRequest) { q.previousPublicationID = q.observation.publicationID }},
		{"previous_set_on_artist", func(q *ipEntityDeltaRequest) {
			q.observation.subjectKind = "artist"
			q.previousSetID = "set-one"
		}},
		{"long_previous_set", func(q *ipEntityDeltaRequest) { q.previousSetID = longID }},
		{"new_observation_invalid", func(q *ipEntityDeltaRequest) { q.observation.evidenceID = "" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			tc.mutate(&q)
			if c, err := newIPEntityDeltaCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
}

// One retained sequence may serve only its ORIGINAL request and operation. A
// different request or a different command operation at the same sequence is a
// conflict, never a second replacement.
func TestIPEntityDeltaConflictingRequestAndSequence(t *testing.T) {
	w, r, p, setup := ipEntityDeltaFixture(t)
	second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
		ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
	out, turn, tx := ipEntityDeltaApply(t, w, r, p, ipEntityDeltaCommandFor(t, second,
		ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaSecondFields())))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)

	changed := ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaSecondFields())
	changed.observation.fields = append([]ipEntityAcceptField(nil), changed.observation.fields...)
	changed.observation.fields[0].Value = "Different Series"
	conflicting, err := newIPEntityDeltaCommand(
		ipEntityDeltaEvidenceIdentity(second.claim, second.sequence+1), changed)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
	denied, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipEntityDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	otherIdentity := ipEntityDeltaEvidenceIdentity(second.claim, second.sequence+1)
	otherIdentity.OperationID = [16]byte{0x6b, 0xff}
	other, err := newIPEntityDeltaCommand(otherIdentity,
		ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaSecondFields()))
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityDelta)
	denied, err = applyIPEntityDelta(t.Context(), turn, p, t.Context(), b, other)
	if denied != (ipEntityDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting operation admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta'`); got != 1 {
		t.Fatal("conflicting delta wrote a second receipt", got)
	}
}

func TestIPEntityDeltaCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, setup := ipEntityDeltaFixture(t)
			second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
				ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
			out, err := applyIPEntityDelta(t.Context(), turn, proof, t.Context(), b,
				ipEntityDeltaCommandFor(t, second, ipEntityDeltaRequestFor(t, second, setup,
					"publication-two", ipEntityDeltaSecondFields())))
			if out != (ipEntityDeltaCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, setup := ipEntityDeltaFixture(t)
		second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
			ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
		lifetime, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
		out, err := applyIPEntityDelta(lifetime, turn, p, t.Context(), b,
			ipEntityDeltaCommandFor(t, second, ipEntityDeltaRequestFor(t, second, setup,
				"publication-two", ipEntityDeltaSecondFields())))
		if out != (ipEntityDeltaCommandResult{}) || err == nil {
			t.Fatal("cancelled lifetime admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPEntityDeltaUnknownCommitFreshRead(t *testing.T) {
	w, r, p, setup := ipEntityDeltaFixture(t)
	second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
		ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
	c := ipEntityDeltaCommandFor(t, second, ipEntityDeltaRequestFor(t, second, setup,
		"publication-two", ipEntityDeltaSecondFields()))
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
	out, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b, c)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	// The uncertain frame is closed; it never reserves the saved result.
	if again, e := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b, c); again != (ipEntityDeltaCommandResult{}) || e == nil {
		t.Fatal("uncertain delta frame reserved", again, e)
	}
	// Only a FRESH read-only connection may prove the durable outcome.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publications WHERE id='publication-two' AND previous_id='publication-one'`:                                  1,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta' AND terminal=1 AND result_phase='accepted'`:                          1,
		`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND owner_id='` + setup.first.owner.OperationID + `'`: 2,
		`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Replacement Series' AND revision=3`:                                           1,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + setup.owner.OperationID + `'`:                                second.sequence + 1,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal("durable delta", query, got)
		}
	}
}

// Lease expiry at the replacement's own clock is a conflict; the old accepted
// head stays visible and the new source is never published.
func TestIPEntityDeltaExpiredLeaseRefused(t *testing.T) {
	w, r, p, setup := ipEntityDeltaFixture(t)
	second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
		ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
	base := w.now()
	ipEntityJobAdvance(t, w, base, 6*time.Minute)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
	out, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b,
		ipEntityDeltaCommandFor(t, second, ipEntityDeltaRequestFor(t, second, setup,
			"publication-two", ipEntityDeltaSecondFields())))
	if out != (ipEntityDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("expired delta lease admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_publications`); got != 1 {
		t.Fatal("expired delta wrote a publication", got)
	}
}

// ipEntityDeltaExecUnchecked seeds one deliberately foreign or incomplete prior
// head that no real writer can produce (the normal FKs would reject it), so the
// delta's own exact-old-head guards are what the case exercises. Enforcement is
// restored on the same pinned connection immediately after the seed.
func ipEntityDeltaExecUnchecked(t *testing.T, w *executor, queries ...string) {
	t.Helper()
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	statements := append([]string{`PRAGMA foreign_keys=OFF`}, queries...)
	statements = append(statements, `PRAGMA foreign_keys=ON`)
	for _, query := range statements {
		if _, err := conn.ExecContext(t.Context(), query); err != nil {
			t.Fatal(query, err)
		}
	}
}

// ipEntityDeltaSeedSet inserts one complete TV set header and its exact IP-08
// set-seal receipt under one owner. It is test fixture state, not a command.
func ipEntityDeltaSeedSet(t *testing.T, w *executor, owner ipEntityJobResult, incarnation []byte,
	setID, evidenceID, buildID string, sequence, pins int64) {
	t.Helper()
	ipPolicyExec(t, w, `INSERT INTO metadata_publication_sets(member_kind,id,entity_id,provider,incarnation,revision,generation,subject_kind,provider_id,episode_order,consent_revision,policy_incarnation,policy_revision,complete,coverage_source_revision,coverage_hierarchy_revision,coverage_selection_revision,coverage_after_item,coverage_complete,ip_proof_owner,ip_proof_incarnation,ip_proof_sequence,ip_operation_id,ip_operation_incarnation) VALUES('episode',?,1,'tmdb',?,2,1,'series','tmdb-1','aired',1,X'09000000000000000000000000000000',1,1,1,0,0,2,1,NULL,NULL,NULL,?,?)`,
		setID, incarnation, owner.OperationID, incarnation)
	commitment := make([]byte, 32)
	commitment[0] = 0x5a
	// metadata_ip_receipts.operation_id is globally unique, so each seeded set
	// seal derives its own operation identity from its set ID.
	setOperation := ipEntityEvidenceDigest(setID)
	ipPolicyExec(t, w, `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id,set_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes,proof_pin_count,entity_id,provider,evidence_subject_kind,evidence_provider_id) VALUES(?,?,?,?,?,'set_seal',2,0,?,'sealed',1,0,'set-digest',2,64,1,?,1,'`+ipEntityEvidenceParser("tmdb", "series")+`',?,?,'ip.tv-set',1,?,2,64,?,1,'tmdb','series','tmdb-1')`,
		owner.OperationID, incarnation, sequence, setOperation[:16], make([]byte, 32),
		make([]byte, 32), evidenceID, buildID, setID, commitment, pins)
	// The set proof pins the receipt just inserted; the canonical foreign key
	// cannot be satisfied in the opposite order without a deferred frame.
	ipPolicyExec(t, w, `UPDATE metadata_publication_sets SET ip_proof_owner=?,ip_proof_incarnation=?,ip_proof_sequence=? WHERE id=?`,
		owner.OperationID, incarnation, sequence, setID)
}

// A replacement may move from one complete TV set proof to the next. The old
// set and its seal pin stay charged, the new set proof is pinned, and the exact
// old set is part of the required previous head.
func TestIPEntityDeltaRetainedAndNewSetProof(t *testing.T) {
	cases := []struct {
		name      string
		sql       []string
		refuse    bool
		unchecked bool
	}{
		{"replace_set", nil, false, false},
		{"prior_set_incomplete", []string{`UPDATE metadata_publication_sets SET complete=0,ip_proof_owner=NULL,ip_proof_incarnation=NULL,ip_proof_sequence=NULL WHERE id='set-one'`}, true, false},
		{"prior_set_pin_drained", []string{`UPDATE metadata_ip_receipts SET proof_pin_count=0 WHERE command_kind='set_seal' AND set_id='set-one'`}, true, false},
		// A set receipt whose evidence disagrees with its sealed build is not a
		// durable row the real schema can produce; the fixture disables FK
		// enforcement only for this deliberate corruption.
		{"prior_set_evidence_mismatch", []string{`UPDATE metadata_ip_receipts SET evidence_id='evidence-two' WHERE command_kind='set_seal' AND set_id='set-one'`}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, setup := ipEntityDeltaFixture(t)
			ipEntityDeltaSeedSet(t, w, setup.first.owner, setup.first.claim.Incarnation[:],
				"set-one", "evidence-one", "build-one", 40, 1)
			ipPolicyExec(t, w, `UPDATE metadata_publications SET set_id='set-one' WHERE id='publication-one'`)
			ipPolicyExec(t, w, `UPDATE metadata_subject_providers SET accepted_set_id='set-one' WHERE entity_id=1`)
			second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
				ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
			ipEntityDeltaSeedSet(t, w, setup.owner, second.claim.Incarnation[:],
				"set-two", "evidence-two", "build-two", second.sequence+1, 1)
			ipEntityJobAck(t, w, ipEntityJobResult{OperationID: setup.owner.OperationID,
				Provider: setup.owner.Provider}, second.sequence+1)
			if tc.unchecked {
				ipEntityDeltaExecUnchecked(t, w, tc.sql...)
			} else {
				for _, query := range tc.sql {
					ipPolicyExec(t, w, query)
				}
			}
			q := ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaSecondFields())
			q.previousSetID = "set-one"
			q.observation.setID = "set-two"
			c, err := newIPEntityDeltaCommand(
				ipEntityDeltaEvidenceIdentity(second.claim, second.sequence+2), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityDelta)
			out, err := applyIPEntityDelta(t.Context(), turn, p, t.Context(), b, c)
			if tc.refuse {
				if out != (ipEntityDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
					t.Fatal("broken set proof admitted", out, err)
				}
				ipPolicyRollbackTurn(t, turn, tx)
				if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_publications`); got != 1 {
					t.Fatal("refused set delta wrote a publication", got)
				}
				return
			}
			if err != nil || out.Outcome.State != Applied || out.Value.NoOp ||
				out.Value.SetID != "set-two" || out.Value.PreviousSetID != "set-one" {
				t.Fatal(out, err)
			}
			ipPolicyCommitTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_publications WHERE id='publication-two' AND set_id='set-two' AND previous_id='publication-one'`:                  1,
				`SELECT count(*) FROM metadata_publications WHERE id='publication-one' AND set_id='set-one'`:                                                    1,
				`SELECT count(*) FROM metadata_subject_providers WHERE entity_id=1 AND accepted_set_id='set-two' AND accepted_publication_id='publication-two'`: 1,
				`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='set_seal' AND set_id='set-one'`:                                           1,
				`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='set_seal' AND set_id='set-two'`:                                           2,
				`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta' AND set_id='set-two' AND publication_id='publication-two'`:                1,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal(query, got)
				}
			}
		})
	}
}

// A replacement respects the owner lock exactly as the descriptive lock
// commands define it, and the exact no-op compares the COMPLETE sealed evidence
// cohort, so a change to any family — even one the accepted typed cohort does
// not carry — publishes a replacement.
func TestIPEntityDeltaOwnerLockAndExactCohort(t *testing.T) {
	t.Run("locked_title_survives_delta", func(t *testing.T) {
		w, r, p, setup := ipEntityDeltaFixture(t)
		ipPolicyExec(t, w, `UPDATE catalog_entities SET title='Owner Title' WHERE id=1`)
		ipPolicyExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('show',1,'title','Owner Title','Accepted Series',1,1,1,'owner','2025-12-01T00:00:00Z')`)
		second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
			ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
		out, turn, tx := ipEntityDeltaApply(t, w, r, p, ipEntityDeltaCommandFor(t, second,
			ipEntityDeltaRequestFor(t, second, setup, "publication-two", ipEntityDeltaSecondFields())))
		if out.Outcome.State != Applied || out.Value.NoOp || out.Value.PublicationID != "publication-two" {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			// The locked title keeps the owner's effective value; the provider
			// value lands only in the automatic observation.
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Owner Title' AND overview='' AND revision=3`:                                                                                                                  1,
			`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='title' AND value='Owner Title' AND value_present=1 AND locked=1 AND automatic_value='Replacement Series' AND automatic_present=1`: 1,
			// The locked-empty description stays empty and also records the
			// provider observation.
			`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='' AND value_present=1 AND locked=1 AND automatic_value='Replacement overview' AND automatic_present=1`: 1,
			`SELECT count(*) FROM metadata_publications WHERE id='publication-two' AND previous_id='publication-one'`:                                                                                                               1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})

	for _, tc := range []struct {
		name   string
		change func(*ipEntityMBContent)
	}{
		{"only_track_title_changed_is_new", func(c *ipEntityMBContent) { c.trackTitle = "Safe from Harm (remaster)" }},
		{"only_media_title_changed_is_new", func(c *ipEntityMBContent) { c.mediaTitle = "Disc One" }},
		{"only_alias_changed_is_new", func(c *ipEntityMBContent) { c.alias = "Collected (deluxe)" }},
		{"only_rating_changed_is_new", func(c *ipEntityMBContent) { c.rating = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := ipEntityMBDefaultContent()
			w, r, p, staged := ipEntityMBFixture(t, ipEntityMBPages(content))
			ipEntityMBSeedAcceptedHead(t, w)
			accepted := ipEntityMBAccept(t, w, r, p, staged, "publication-one")
			changed := content
			tc.change(&changed)
			second := ipEntityMBSecondEvidence(t, w, r, p, changed)
			q := ipEntityMBDeltaRequest(second, accepted.Value)
			c, err := newIPEntityDeltaCommand(
				ipEntityEvidenceChainIdentity(second.claim, second.sequence+1, 0x6c), q)
			if err != nil {
				t.Fatal(err)
			}
			out, turn, tx := ipEntityDeltaApply(t, w, r, p, c)
			if out.Outcome.State != Applied || out.Value.NoOp ||
				out.Value.PublicationID != "publication-two" {
				t.Fatal(out)
			}
			ipPolicyCommitTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_publications WHERE id='publication-two' AND previous_id='publication-one' AND evidence_id='evidence-two'`: 1,
				`SELECT count(*) FROM metadata_publications WHERE id='publication-one'`:                                                                  1,
				`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta' AND publication_id='publication-two'`:                              1,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal(query, got)
				}
			}
		})
	}

	t.Run("identical_cohort_is_exact_noop", func(t *testing.T) {
		content := ipEntityMBDefaultContent()
		w, r, p, staged := ipEntityMBFixture(t, ipEntityMBPages(content))
		ipEntityMBSeedAcceptedHead(t, w)
		accepted := ipEntityMBAccept(t, w, r, p, staged, "publication-one")
		second := ipEntityMBSecondEvidence(t, w, r, p, content)
		q := ipEntityMBDeltaRequest(second, accepted.Value)
		c, err := newIPEntityDeltaCommand(
			ipEntityEvidenceChainIdentity(second.claim, second.sequence+1, 0x6c), q)
		if err != nil {
			t.Fatal(err)
		}
		out, turn, tx := ipEntityDeltaApply(t, w, r, p, c)
		if out.Outcome.State != Applied || !out.Value.NoOp ||
			out.Value.PublicationID != "publication-one" {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM metadata_publications`: 1,
			`SELECT count(*) FROM metadata_subject_providers WHERE entity_id=1 AND accepted_publication_id='publication-one' AND accepted_evidence_id='evidence-one' AND revision=2`: 1,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='delta' AND publication_id='publication-one' AND evidence_id='evidence-two'`:                               1,
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Collected' AND revision=2`:                                                                                  1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
}

// An UNLOCKED owner row must follow a replacement source exactly as the typed
// ReplaceUnlocked preparation does: the effective value moves with the automatic
// value. Before the fix accept/delta moved only automatic_value, so a restored
// row kept A in value while the catalogue showed the new B; a real IP-24 edit
// that correctly sent Before=B then failed the catalogue CAS with ErrConflict.
func TestIPEntityDeltaUnlockedOwnerRowTracksReplacement(t *testing.T) {
	w, r, p, setup := ipEntityDeltaFixture(t)
	// The baseline accept leaves the description row locked. Model lock followed
	// by RestoreAutomatic: an unlocked row whose effective and automatic values
	// are both the restored value A.
	ipPolicyExec(t, w, `UPDATE catalog_entities SET overview='Restored A' WHERE id=1`)
	ipPolicyExec(t, w, `UPDATE metadata_owner_fields SET value='Restored A',value_present=1,automatic_value='Restored A',automatic_present=1,locked=0 WHERE kind='show' AND entity_id=1 AND field='description'`)
	second := ipEntityDeltaStageSecond(t, w, r, p, setup, "evidence-two", "build-two",
		ipEntityAcceptPages(), ipEntityEvidenceDigest("evidence-two"))
	fields := ipEntityDeltaBaseFields()
	fields[2].Value = "Provider B"
	out, turn, tx := ipEntityDeltaApply(t, w, r, p, ipEntityDeltaCommandFor(t, second,
		ipEntityDeltaRequestFor(t, second, setup, "publication-two", fields)))
	if out.Outcome.State != Applied || out.Value.NoOp || out.Value.PublicationID != "publication-two" {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM catalog_entities WHERE id=1 AND overview='Provider B'`: 1,
		`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='Provider B' AND value_present=1 AND automatic_value='Provider B' AND automatic_present=1 AND locked=0`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	// The real IP-24/IP-25 field edit takes the row's Before cohort (B/B,
	// unlocked). It must pass the owner CAS and the catalogue scalar CAS.
	edit := ipTextEditRequest{Kind: IPFieldEdit, Source: ipTextEditSource{
		EntityID: 1, Kind: "show", TargetKind: 2, LibraryID: 777, LibraryKey: "ip-library",
		Actor: "Owner", OwnerRevision: 1, SubjectRevision: out.Value.AcceptedRevision,
		MetadataRevision: out.Value.AcceptedRevision, SourceRevision: 1, IdentityRevision: 1,
		MetadataIncarnation: ipEntityJobIncarnation(1),
	}, Fields: []ipTextEditField{{Field: fieldmeta.Description,
		Before: fieldmeta.TextState{
			Effective: fieldmeta.Text{Value: "Provider B", Present: true},
			Automatic: fieldmeta.Text{Value: "Provider B", Present: true},
		},
		Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "Owner Final"}}}}
	cmd, err := newIPTextEditCommand(ipTextEditIdentity(1, 0x70), edit)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, _ = ipPolicyTurn(t, w, r, IPFieldEdit)
	applied, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), ipEntryBudget{4 << 20}, cmd)
	if err != nil || applied.Outcome.State != Applied || applied.Outcome.Replayed ||
		applied.Value.Fields[0].Field != fieldmeta.Description || !applied.Value.Fields[0].Change.Changed {
		t.Fatal("edit after replacement", applied, err)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM catalog_entities WHERE id=1 AND overview='Owner Final'`:                                                                                                                    1,
		`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='Owner Final' AND value_present=1 AND automatic_value='Provider B' AND locked=1`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

func TestIPEntityDeltaDescriptors(t *testing.T) {
	expected := Descriptor{ID: IPEntityDelta, Name: "ip_entity_delta", Durability: Normal}
	actual, err := Describe(expected.ID)
	if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
		t.Fatal(actual, err)
	}
}
