package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// ipPersonDeltaSetup is one accepted person head followed by a NEW claimed
// observation owner. The second claim captures the accepted person revision.
type ipPersonDeltaSetup struct {
	first       *ipPersonChainStaged
	firstAccept ipPersonAcceptCommandResult
	firstID     CommandIdentity
	second      *ipPersonChainStaged
	secondClaim ipPersonResult
}

func ipPersonSecondClaimed(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof, personRevision int64) (ipPersonResult, CommandIdentity) {
	t.Helper()
	identity := ipPersonIdentity(1, 0x32)
	q := ipPersonClaimFor()
	q.personRevision = personRevision
	c, err := newIPPersonClaimCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonClaim)
	out, err := applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, c)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPersonAck(t, w, out.Value.OperationID, 1)
	return out.Value, identity
}

func ipPersonSecondHeader() EvidenceHeaderInput {
	return EvidenceHeaderInput{
		ID: "evidence-person-2", ParserVersion: "tmdb.person.header-exact-dates.v1",
		Provider: "tmdb", SubjectKind: "person", ProviderID: "42", SubjectID: 501,
		Language: "en-US", Region: "US",
		RequestShape:      "tmdb.person.detail.header-exact-dates.v1",
		ObservationDigest: [32]byte{8}, ObservedAt: "2026-02-02T00:00:00Z",
		SourceURL: "https://api.themoviedb.org/3/person/42",
		Title:     "Augusta Ada King", Biography: "Mathematician and writer.",
		BiographySource: "tmdb", BiographySourceURL: "https://api.themoviedb.org/3/person/42",
	}
}

func ipPersonStageSecond(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	header EvidenceHeaderInput, birthDate, deathDate, birthPlace EvidenceText,
	aliases []ipPersonEvidenceAlias, external []ipPersonEvidenceExternal) (*ipPersonChainStaged, ipPersonResult, CommandIdentity) {
	t.Helper()
	claim, claimID := ipPersonSecondClaimed(t, w, r, p, 2)
	chain := ipPersonStageChain(t, w, r, p, claim, claimID, header.ID, "build-person-2",
		header, birthDate, deathDate, birthPlace, aliases, external)
	return chain, claim, claimID
}

func ipPersonDeltaFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, ipPersonDeltaSetup) {
	t.Helper()
	w, r, p, _ := ipPersonAcceptFixture(t)
	first := ipPersonFirstChain(t, w, r, p)
	firstReq := ipPersonAcceptRequest{observation: ipPersonObservationFor(first, "publication-person-1", 1)}
	firstID := ipEntityEvidenceIdentity(first.claimID, first.sequence+1)
	firstCommand, err := newIPPersonAcceptCommand(firstID, firstReq)
	if err != nil {
		t.Fatal(err)
	}
	accepted, turn, tx := ipPersonAcceptApply(t, w, r, p, firstCommand)
	if accepted.Outcome.State != Applied {
		t.Fatal(accepted)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPersonAck(t, w, first.begin.OperationID, first.sequence+1)
	return w, r, p, ipPersonDeltaSetup{first: first, firstAccept: accepted, firstID: firstID}
}

func ipPersonDeltaRequestFor(t *testing.T, setup ipPersonDeltaSetup, second *ipPersonChainStaged,
	publicationID string, fields []ipPersonAcceptField) ipPersonDeltaRequest {
	t.Helper()
	observation := ipPersonObservationFor(second, publicationID, 2)
	observation.fields = append([]ipPersonAcceptField(nil), fields...)
	return ipPersonDeltaRequest{
		observation:               observation,
		previousPublicationID:     setup.firstAccept.Value.PublicationID,
		previousEvidenceID:        setup.firstAccept.Value.EvidenceID,
		previousSourceIncarnation: setup.firstID.OperationID,
		previousSourceRevision:    setup.firstAccept.Value.SourceRevision,
	}
}

func ipPersonDeltaApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipPersonDeltaCommand) (ipPersonDeltaCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
	out, err := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("delta", out, err)
	}
	return out, turn, tx
}

func ipPersonDeltaCommandFor(t *testing.T, chain *ipPersonChainStaged,
	q ipPersonDeltaRequest) *ipPersonDeltaCommand {
	t.Helper()
	c, err := newIPPersonDeltaCommand(ipEntityEvidenceIdentity(chain.claimID, chain.sequence+1), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// baselineSecondObservation advances one NEW sealed observation under a new
// owner. digest selects an exact no-op; everything else is a new observation.
func ipPersonDeltaSecondObservation(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) *ipPersonChainStaged {
	t.Helper()
	chain, _, _ := ipPersonStageSecond(t, w, r, p, ipPersonSecondHeader(),
		EvidenceText{Present: true, Value: "1815-12-11"},
		EvidenceText{Present: true, Value: "1852-11-28"},
		EvidenceText{Present: true, Value: "Paris"},
		ipPersonAliasPageRows(10, 1), ipPersonExternalPageRows(10, 1))
	return chain
}

func ipPersonDeltaSecondFacts() []ipPersonAcceptField {
	return []ipPersonAcceptField{
		{Name: "name", Value: "Augusta Ada King", Present: true},
		{Name: "biography", Value: "Mathematician and writer.", Present: true},
		{Name: "birthDate", Value: "1815-12-11", Present: true},
		{Name: "deathDate", Value: "1852-11-28", Present: true},
		{Name: "birthPlace", Value: "Paris", Present: true},
	}
}

func ipPersonDeltaFirstFacts() []ipPersonAcceptField {
	return []ipPersonAcceptField{
		{Name: "name", Value: "Ada Lovelace", Present: true},
		{Name: "biography", Value: "Mathematician.", Present: true},
		{Name: "birthDate", Value: "1815-12-10", Present: true},
		{Name: "deathDate", Value: "1852-11-27", Present: true},
		{Name: "birthPlace", Value: "London", Present: true},
	}
}

func TestIPPersonDeltaReplaceRetainsOldSourceAndReplays(t *testing.T) {
	w, r, p, setup := ipPersonDeltaFixture(t)
	second := ipPersonDeltaSecondObservation(t, w, r, p)
	out, turn, tx := ipPersonDeltaApply(t, w, r, p,
		ipPersonDeltaCommandFor(t, second, ipPersonDeltaRequestFor(t, setup, second,
			"publication-person-2", ipPersonDeltaSecondFacts())))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Digest == (ResultDigest{}) ||
		out.Value.OperationID != second.begin.OperationID ||
		out.Value.PublicationID != "publication-person-2" ||
		out.Value.PreviousPublicationID != "publication-person-1" ||
		out.Value.EvidenceID != "evidence-person-2" ||
		out.Value.PreviousEvidenceID != "evidence-person-1" || out.Value.NoOp ||
		out.Value.Name != "Augusta Ada King" || out.Value.Biography != "Mathematician and writer." ||
		out.Value.BirthDate != "1815-12-11" || out.Value.BirthPlace != "Paris" ||
		out.Value.PersonID != 42 || out.Value.PersonRevision != 3 ||
		out.Value.AcceptedRevision != 3 || out.Value.PreviousSourceRevision != 1 ||
		out.Value.SourceRevision != 2 || out.Value.AliasCount != 1 ||
		out.Value.ExternalCount != 1 || out.Value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_publications WHERE id='publication-person-2' AND person_id=42 AND provider='tmdb' AND subject_kind='person' AND provider_id='42' AND evidence_id='evidence-person-2' AND operation_id='` + second.begin.OperationID + `' AND proof_sequence=` + ipWritingInt(second.sequence) + ` AND accepted_revision=3 AND observed_at='2026-02-02T00:00:00Z'`: 1,
		`SELECT count(*) FROM metadata_person_publications WHERE id='publication-person-1' AND evidence_id='evidence-person-1' AND accepted_revision=2`:                                                                                                                                                   1,
		`SELECT count(*) FROM metadata_person_sources WHERE person_id=42 AND provider='tmdb' AND role='primary' AND revision=2 AND accepted_publication_id='publication-person-2' AND accepted_evidence_id='evidence-person-2' AND incarnation=X'` + ipPersonHexBytes(setup.firstID.OperationID[:]) + `'`: 1,
		`SELECT count(*) FROM catalog_people WHERE id=42 AND name='Augusta Ada King' AND biography='Mathematician and writer.' AND birth_place='Paris' AND revision=3`:                                                                                                                                    1,
		`SELECT count(*) FROM metadata_person_field_origins WHERE person_id=42 AND publication_id='publication-person-2' AND evidence_id='evidence-person-2' AND selection_revision=2`:                                                                                                                    5,
		`SELECT count(*) FROM metadata_person_field_origins WHERE publication_id='publication-person-1'`:                                                                                                                                                                                                  0,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='delta' AND publication_id='publication-person-2' AND evidence_id='evidence-person-2' AND accepted_revision=3 AND result_count=5`:                                                                                               1,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='accept'`:                                                                                                                                                                                                                       1,
		`SELECT proof_pin_count FROM metadata_person_receipts WHERE command_kind='evidence_seal' AND owner_id='` + setup.first.begin.OperationID + `'`:                                                                                                                                                    2,
		`SELECT proof_pin_count FROM metadata_person_receipts WHERE command_kind='evidence_seal' AND owner_id='` + second.begin.OperationID + `'`:                                                                                                                                                         2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	// Replay after unrelated source, lease, person, provider and slot changes
	// returns the original saved replacement before any fresh probe.
	original := out
	ipPolicyExec(t, w, `UPDATE catalog_people SET revision=9,name='Changed' WHERE id=42`)
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET revision=99,lease_generation=99,lease_until_ms=1 WHERE id='`+second.begin.OperationID+`'`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	ipPolicyExec(t, w, `DELETE FROM metadata_person_sources`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
	replay, err := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b,
		ipPersonDeltaCommandFor(t, second, ipPersonDeltaRequestFor(t, setup, second,
			"publication-person-2", ipPersonDeltaSecondFacts())))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value ||
		replay.Digest != original.Digest {
		t.Fatal("delta replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_publications`); got != 2 {
		t.Fatal("delta replay wrote a publication", got)
	}

	// At or below ACK is Retired while both sources and their pins stay.
	ipPersonAck(t, w, second.begin.OperationID, second.sequence+1)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonDelta)
	retired, err := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b,
		ipPersonDeltaCommandFor(t, second, ipPersonDeltaRequestFor(t, setup, second,
			"publication-person-2", ipPersonDeltaSecondFacts())))
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipPersonDeltaResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired delta served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_publications`:                                                                                            2,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='delta' AND terminal IS NULL`:                                                1,
		`SELECT proof_pin_count FROM metadata_person_receipts WHERE command_kind='evidence_seal' AND owner_id='` + setup.first.begin.OperationID + `'`: 2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("retired delta lost retained state", query, got)
		}
	}
}

// The embedded metadata_evidence unique key includes the sealed content digest,
// so a SECOND row carrying the accepted head's exact digest cannot physically
// coexist; the exact-no-op decision is proven at its boundary, and the saved
// no-op result mapping is proven from receipt witnesses alone.
func TestIPPersonDeltaNoOpDecision(t *testing.T) {
	w, r, p, setup := ipPersonDeltaFixture(t)
	second := ipPersonStageSecondCohort(t, w, r, p)
	q := ipPersonDeltaRequestFor(t, setup, second, "publication-person-2", ipPersonDeltaFirstFacts())
	turn, tx, _ := ipPolicyTurn(t, w, r, IPPersonDelta)
	defer ipPolicyRollbackTurn(t, turn, tx)
	prior, err := ipPersonDeltaPriorEvidenceRead(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := ipPersonFactsRead(t.Context(), turn, 42, "person-token-42")
	if err != nil {
		t.Fatal(err)
	}
	newest, err := ipPersonSealedObservationRead(t.Context(), turn, q.observation,
		ipEntityEvidenceIdentity(second.claimID, second.sequence+1))
	if err != nil {
		t.Fatal(err)
	}
	newest.digest = prior.digest
	noOp, err := ipPersonDeltaNoOp(t.Context(), turn, q, newest, prior, facts)
	if err != nil || !noOp {
		t.Fatal("exact no-op decision", noOp, err)
	}
	changedDigest := newest
	changedDigest.digest = prior.digest + "0"
	if noOp, err = ipPersonDeltaNoOp(t.Context(), turn, q, changedDigest, prior, facts); err != nil || noOp {
		t.Fatal("changed digest no-op", noOp, err)
	}
	changedFacts := q
	changedFacts.observation.fields = append([]ipPersonAcceptField(nil), q.observation.fields...)
	changedFacts.observation.fields[1].Value = "Changed"
	if noOp, err = ipPersonDeltaNoOp(t.Context(), turn, changedFacts, newest, prior, facts); err != nil || noOp {
		t.Fatal("changed facts no-op", noOp, err)
	}
	changedCohort := q
	changedCohort.observation.aliases = ipPersonAliasPageRows(10, 1)
	if noOp, err = ipPersonDeltaNoOp(t.Context(), turn, changedCohort, newest, prior, facts); err != nil || noOp {
		t.Fatal("changed cohort no-op", noOp, err)
	}
}

// The no-op saved result names the retained old publication and source
// revision and never derives either from current mutable state.
func TestIPPersonDeltaSavedNoOpMapping(t *testing.T) {
	observation := ipPersonObservation{
		operationID: strings.Repeat("a", 32), provider: "tmdb", providerID: "42",
		lease: strings.Repeat("b", 32), personID: 42, personRevision: 2,
		generation: 1, revision: 4, leaseGeneration: 1, leaseUntilMS: 400000,
		evidenceID: "evidence-person-2", buildID: "build-person-2",
		publicationID: "publication-person-2", buildGeneration: 1, contentRevision: 4,
		searchJobID: 1, observedAt: "2026-02-02T00:00:00Z", fields: ipPersonDeltaFirstFacts(),
	}
	q := ipPersonDeltaRequest{observation: observation, previousPublicationID: "publication-person-1",
		previousEvidenceID: "evidence-person-1", previousSourceIncarnation: [16]byte{1},
		previousSourceRevision: 1}
	identity := ipEntityEvidenceIdentity(ipPersonIdentity(1, 0x31), 8)
	r := ipPersonReceipt{kind: "delta", incarnation: identity.Incarnation, sequence: 8,
		operationID: identity.OperationID, evidenceID: "evidence-person-2",
		buildID: "build-person-2", publicationID: "publication-person-1",
		acceptedRevision: 2, resultCount: 5, appliedMS: 1000}
	value, err := ipPersonDeltaSaved(q, identity, r)
	if err != nil || !value.NoOp || value.PublicationID != "publication-person-1" ||
		value.PersonRevision != 2 || value.AcceptedRevision != 2 ||
		value.SourceRevision != 1 || value.PreviousSourceRevision != 1 {
		t.Fatal(value, err)
	}
	r.publicationID = "publication-person-2"
	r.acceptedRevision = 3
	value, err = ipPersonDeltaSaved(q, identity, r)
	if err != nil || value.NoOp || value.PublicationID != "publication-person-2" ||
		value.PersonRevision != 3 || value.SourceRevision != 2 {
		t.Fatal(value, err)
	}
}

// A same-cohort later observation (different content digest) is a NEW
// observation and publishes a replacement head.
func ipPersonStageSecondCohort(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof) *ipPersonChainStaged {
	t.Helper()
	header := ipPersonHeaderInput()
	header.ID = "evidence-person-2"
	header.ObservedAt = "2026-02-02T00:00:00Z"
	header.ObservationDigest = [32]byte{8}
	chain, _, _ := ipPersonStageSecond(t, w, r, p, header,
		EvidenceText{Present: true, Value: "1815-12-10"},
		EvidenceText{Present: true, Value: "1852-11-27"},
		EvidenceText{Present: true, Value: "London"},
		ipPersonAliasPageRows(0, 1), ipPersonExternalPageRows(0, 1))
	return chain
}

func TestIPPersonDeltaWrongPreviousHeadRefused(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipPersonDeltaRequest)
		sql    []string
		want   error
	}{
		{"wrong_publication", func(q *ipPersonDeltaRequest) { q.previousPublicationID = "publication-x" }, nil, ErrConflict},
		{"wrong_evidence", func(q *ipPersonDeltaRequest) { q.previousEvidenceID = "evidence-x" }, nil, ErrConflict},
		{"wrong_source_incarnation", func(q *ipPersonDeltaRequest) { q.previousSourceIncarnation = [16]byte{0xee} }, nil, ErrConflict},
		{"wrong_source_revision", func(q *ipPersonDeltaRequest) { q.previousSourceRevision = 2 }, nil, ErrConflict},
		{"slot_revision_advanced", nil, []string{`UPDATE metadata_person_sources SET revision=2`}, ErrConflict},
		{"prior_evidence_unsealed", nil, []string{`UPDATE metadata_evidence SET sealed=0 WHERE id='evidence-person-1'`}, ErrConflict},
		{"prior_pin_drained", nil, []string{`UPDATE metadata_person_receipts SET proof_pin_count=0 WHERE command_kind='evidence_seal' AND owner_id='` + "PLACEHOLDER" + `'`}, ErrConflict},
		{"prior_publication_revision", nil, []string{`UPDATE metadata_person_publications SET accepted_revision=1 WHERE id='publication-person-1'`}, ErrConflict},
		{"search_missing", nil, []string{`DELETE FROM search_publish_jobs WHERE id=1`}, ErrResolveRequired},
		{"new_person_revision", nil, []string{`UPDATE catalog_people SET revision=3 WHERE id=42`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, setup := ipPersonDeltaFixture(t)
			second := ipPersonDeltaSecondObservation(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, strings.ReplaceAll(query, "PLACEHOLDER", setup.first.begin.OperationID))
			}
			q := ipPersonDeltaRequestFor(t, setup, second, "publication-person-2", ipPersonDeltaSecondFacts())
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			c, err := newIPPersonDeltaCommand(
				ipEntityEvidenceIdentity(second.claimID, second.sequence+1), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
			out, err := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b, c)
			if out != (ipPersonDeltaCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("wrong previous head admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_person_publications`:                        1,
				`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='delta'`: 0,
				`SELECT count(*) FROM catalog_people WHERE id=42 AND name='Ada Lovelace'`:  1,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal("refused delta wrote state", query, got)
				}
			}
		})
	}
}

// A late failure after the new publication and the source CAS rolls the WHOLE
// replacement back: the old accepted head stays visible and the person keeps
// its accepted automatic values.
func TestIPPersonDeltaRollbackKeepsOldAcceptedVisibility(t *testing.T) {
	w, r, p, setup := ipPersonDeltaFixture(t)
	second := ipPersonDeltaSecondObservation(t, w, r, p)
	ipPolicyExec(t, w, `UPDATE metadata_person_receipts SET proof_pin_count=9223372036854775807 WHERE command_kind='evidence_seal' AND owner_id='`+second.begin.OperationID+`'`)
	c := ipPersonDeltaCommandFor(t, second, ipPersonDeltaRequestFor(t, setup, second,
		"publication-person-2", ipPersonDeltaSecondFacts()))
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
	out, err := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b, c)
	if out != (ipPersonDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("uncharged new source admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_publications WHERE id='publication-person-2'`:                                0,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='delta'`:                                         0,
		`SELECT count(*) FROM metadata_person_sources WHERE revision=1 AND accepted_publication_id='publication-person-1'`: 1,
		`SELECT count(*) FROM catalog_people WHERE id=42 AND name='Ada Lovelace' AND revision=2`:                           1,
		`SELECT count(*) FROM metadata_person_field_origins WHERE publication_id='publication-person-2'`:                   0,
		`SELECT count(*) FROM metadata_person_field_origins WHERE publication_id='publication-person-1'`:                   5,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("delta rollback lost the old head", query, got)
		}
	}
}

func TestIPPersonDeltaShapeRefusals(t *testing.T) {
	w, r, p, setup := ipPersonDeltaFixture(t)
	second := ipPersonDeltaSecondObservation(t, w, r, p)
	base := ipPersonDeltaRequestFor(t, setup, second, "publication-person-2", ipPersonDeltaSecondFacts())
	longID := strings.Repeat("x", ipPersonPointerMax+1)
	identity := ipEntityEvidenceIdentity(second.claimID, second.sequence+1)
	mutations := []struct {
		name   string
		mutate func(*ipPersonDeltaRequest)
	}{
		{"empty_previous_publication", func(q *ipPersonDeltaRequest) { q.previousPublicationID = "" }},
		{"long_previous_publication", func(q *ipPersonDeltaRequest) { q.previousPublicationID = longID }},
		{"empty_previous_evidence", func(q *ipPersonDeltaRequest) { q.previousEvidenceID = "" }},
		{"previous_evidence_is_new", func(q *ipPersonDeltaRequest) { q.previousEvidenceID = q.observation.evidenceID }},
		{"previous_publication_is_new", func(q *ipPersonDeltaRequest) { q.previousPublicationID = q.observation.publicationID }},
		{"zero_source_incarnation", func(q *ipPersonDeltaRequest) { q.previousSourceIncarnation = [16]byte{} }},
		{"zero_source_revision", func(q *ipPersonDeltaRequest) { q.previousSourceRevision = 0 }},
		{"new_observation_invalid", func(q *ipPersonDeltaRequest) { q.observation.evidenceID = "" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			q.observation.fields = append([]ipPersonAcceptField(nil), base.observation.fields...)
			q.observation.aliases = append([]ipPersonEvidenceAlias(nil), base.observation.aliases...)
			q.observation.external = append([]ipPersonEvidenceExternal(nil), base.observation.external...)
			tc.mutate(&q)
			if c, err := newIPPersonDeltaCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
}

func TestIPPersonDeltaConflictingRequestAndSequence(t *testing.T) {
	w, r, p, setup := ipPersonDeltaFixture(t)
	second := ipPersonDeltaSecondObservation(t, w, r, p)
	q := ipPersonDeltaRequestFor(t, setup, second, "publication-person-2", ipPersonDeltaSecondFacts())
	out, turn, tx := ipPersonDeltaApply(t, w, r, p, ipPersonDeltaCommandFor(t, second, q))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)

	changed := q
	changed.observation.fields = append([]ipPersonAcceptField(nil), q.observation.fields...)
	changed.observation.fields[1].Value = "Different"
	conflicting, err := newIPPersonDeltaCommand(
		ipEntityEvidenceIdentity(second.claimID, second.sequence+1), changed)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
	denied, err := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipPersonDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	otherIdentity := ipEntityEvidenceIdentity(second.claimID, second.sequence+1)
	otherIdentity.OperationID = [16]byte{0x6c, 0xff}
	other, err := newIPPersonDeltaCommand(otherIdentity, q)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonDelta)
	denied, err = applyIPPersonDelta(t.Context(), turn, p, t.Context(), b, other)
	if denied != (ipPersonDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting operation admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='delta'`); got != 1 {
		t.Fatal("conflicting delta wrote a second receipt", got)
	}
}

func TestIPPersonDeltaCurrentAuthorityAndLifetime(t *testing.T) {
	prepare := func(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, *ipPersonDeltaCommand) {
		t.Helper()
		w, r, p, setup := ipPersonDeltaFixture(t)
		second := ipPersonDeltaSecondObservation(t, w, r, p)
		return w, r, p, ipPersonDeltaCommandFor(t, second,
			ipPersonDeltaRequestFor(t, setup, second, "publication-person-2", ipPersonDeltaSecondFacts()))
	}
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, command := prepare(t)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
			out, err := applyIPPersonDelta(t.Context(), turn, proof, t.Context(), b, command)
			if out != (ipPersonDeltaCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, command := prepare(t)
		lifetime, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
		out, err := applyIPPersonDelta(lifetime, turn, p, t.Context(), b, command)
		if out != (ipPersonDeltaCommandResult{}) || err == nil {
			t.Fatal("cancelled lifetime admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPPersonDeltaUnknownCommitFreshRead(t *testing.T) {
	w, r, p, setup := ipPersonDeltaFixture(t)
	second := ipPersonDeltaSecondObservation(t, w, r, p)
	c := ipPersonDeltaCommandFor(t, second, ipPersonDeltaRequestFor(t, setup, second,
		"publication-person-2", ipPersonDeltaSecondFacts()))
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
	out, err := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b, c)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if again, e := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b, c); again != (ipPersonDeltaCommandResult{}) || e == nil {
		t.Fatal("uncertain delta frame reserved", again, e)
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_publications WHERE id='publication-person-2'`:                                1,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='delta'`:                                         1,
		`SELECT count(*) FROM catalog_people WHERE id=42 AND name='Augusta Ada King' AND revision=3`:                       1,
		`SELECT count(*) FROM metadata_person_sources WHERE revision=2 AND accepted_publication_id='publication-person-2'`: 1,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal("durable delta", query, got)
		}
	}
}

// Lease expiry at the replacement's own clock is a conflict; the old accepted
// head stays visible and no new source is published.
func TestIPPersonDeltaExpiredLeaseRefused(t *testing.T) {
	w, r, p, setup := ipPersonDeltaFixture(t)
	second := ipPersonDeltaSecondObservation(t, w, r, p)
	base := w.now()
	ipPersonAdvance(t, w, base, 6*time.Minute)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonDelta)
	out, err := applyIPPersonDelta(t.Context(), turn, p, t.Context(), b,
		ipPersonDeltaCommandFor(t, second, ipPersonDeltaRequestFor(t, setup, second,
			"publication-person-2", ipPersonDeltaSecondFacts())))
	if out != (ipPersonDeltaCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("expired delta lease admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_publications`); got != 1 {
		t.Fatal("expired delta wrote a publication", got)
	}
}
