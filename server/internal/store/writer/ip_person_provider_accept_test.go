package writer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group D is not embedded, so person custody, the accepted publication/source/
// origin owners and the person receipt tuple are qualified only against the
// exact draft subset. The catalogue person, provider subject, policy, generic
// evidence body/children and search intent rows are the real embedded schema.
// The fixture also seeds the PB People source helper output the packet names
// but this train does not contain: the catalog_people row and its
// search_publish_jobs(person_id) intent row.
func ipPersonAcceptFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, time.Time) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	base := w.now()
	ddl, err := os.ReadFile("testdata/ip-person-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing'),(777,'tvdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing'),(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
		`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(42,'person-token-42','ik-42','Ada Lovelace','Lovelace, Ada',X'01000000000000000000000000000000')`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(501,'tmdb','person','42','en-US','US','tmdb.person.detail.header-exact-dates.v1',X'0b000000000000000000000000000000',1),(502,'musicbrainz','person','42','en-US','US','tmdb.person.detail.header-exact-dates.v1',X'0c000000000000000000000000000000',1),(503,'tmdb','person','42','en-US','FR','tmdb.person.detail.header-exact-dates.v1',X'0d000000000000000000000000000000',1)`,
	} {
		ipPolicyExec(t, w, query)
	}
	ipPolicyExec(t, w, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'bank-one',?,?,'unicode-fixture','active',0)`,
		searchpolicy.RecipeV1, searchpolicy.EngineVersion)
	ipPolicyExec(t, w, `INSERT INTO search_publish_jobs(id,person_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase) VALUES(1,42,1,1,X'0e000000000000000000000000000000',1,?,'idle')`,
		searchpolicy.RecipeV1)
	defs := append(ipPolicyDefinitions(), ipPersonDefinitions()...)
	defs = append(defs, ipPersonEvidenceAppendDefinitions()...)
	defs = append(defs, ipPersonEvidenceSealDefinitions()...)
	defs = append(defs, ipPersonAcceptDefinitions()...)
	defs = append(defs, ipPersonDeltaDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{registry, keeper}, p, base
}

// ipPersonChainStaged is one complete person observation advanced through the
// real IP-14/IP-15 commands: claim, exact BEGIN, alias/external pages and seal.
type ipPersonChainStaged struct {
	claim    ipPersonResult
	claimID  CommandIdentity
	begin    ipPersonResult
	beginReq ipPersonEvidenceBeginRequest
	seal     ipPersonSealCommandResult
	sealID   CommandIdentity
	aliases  []ipPersonEvidenceAlias
	external []ipPersonEvidenceExternal
	content  int64
	sequence int64
}

func ipPersonStageChain(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	claim ipPersonResult, claimID CommandIdentity, evidenceID, buildID string,
	header EvidenceHeaderInput, birthDate, deathDate, birthPlace EvidenceText,
	aliases []ipPersonEvidenceAlias, external []ipPersonEvidenceExternal) *ipPersonChainStaged {
	t.Helper()
	q := ipPersonEvidenceBeginRequest{
		operationID: claim.OperationID, provider: claim.Provider, providerID: claim.ProviderID,
		language: "en-US", region: "US",
		requestShape: "tmdb.person.detail.header-exact-dates.v1", lease: claim.Lease,
		personID: claim.PersonID, subjectID: 501, generation: claim.Generation,
		revision: claim.Revision, leaseGeneration: claim.LeaseGeneration,
		leaseUntilMS: claim.LeaseUntilMS, buildID: buildID, buildGeneration: claim.Generation,
		header: header, birthDate: birthDate, deathDate: deathDate, birthPlace: birthPlace,
		aliases: aliases, external: external,
	}
	headerValue, err := NewEvidenceHeader(q.header)
	if err != nil {
		t.Fatal(err)
	}
	commitment, total, _, err := ipPersonEvidenceManifest(q, headerValue)
	if err != nil {
		t.Fatal(err)
	}
	q.expectedCommitment, q.expectedBytes = commitment, total
	q.expectedRows = 1 + int64(q.children())
	beginID := ipEntityEvidenceIdentity(claimID, 2)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
	begin, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b,
		mustIPPersonBegin(t, beginID, q))
	if err != nil || begin.Outcome.State != Applied {
		t.Fatal(begin, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPersonAck(t, w, claim.OperationID, 2)

	current, before, content, sequence := begin.Value, int64(1), int64(1), int64(3)
	if len(aliases) > 0 {
		request := ipPersonAppendFor(current, ipPersonFamilyAlias, 0, before,
			int64(len(aliases)), content)
		request.aliases = aliases
		out, turn, tx := ipPersonEvidenceApplyAppend(t, w, r, p,
			mustIPPersonAppend(t, ipPersonIdentity(sequence, byte(0x70+sequence)), request))
		ipPolicyCommitTurn(t, turn, tx)
		ipPersonAck(t, w, claim.OperationID, sequence)
		current = out.Value
		before += int64(len(aliases))
		content++
		sequence++
	}
	if len(external) > 0 {
		request := ipPersonAppendFor(current, ipPersonFamilyExternal, 0, before,
			int64(len(external)), content)
		request.external = external
		out, turn, tx := ipPersonEvidenceApplyAppend(t, w, r, p,
			mustIPPersonAppend(t, ipPersonIdentity(sequence, byte(0x70+sequence)), request))
		ipPolicyCommitTurn(t, turn, tx)
		ipPersonAck(t, w, claim.OperationID, sequence)
		current = out.Value
		before += int64(len(external))
		content++
		sequence++
	}
	sealID := ipPersonIdentity(sequence, byte(0x70+sequence))
	seal := mustIPPersonSeal(t, sealID, ipPersonSealFor(current, q, content))
	out, turn, tx := ipPersonSealApply(t, w, r, p, seal)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPersonAck(t, w, claim.OperationID, sequence)
	return &ipPersonChainStaged{claim: claim, claimID: claimID, begin: current, beginReq: q,
		seal: out, sealID: sealID, aliases: aliases, external: external, content: content,
		sequence: sequence}
}

// ipPersonObservationFor builds the complete accept observation for one sealed
// chain. The typed facts are exactly the sealed header/date carriers.
func ipPersonObservationFor(chain *ipPersonChainStaged, publicationID string,
	personRevision int64) ipPersonObservation {
	return ipPersonObservation{
		operationID: chain.begin.OperationID, provider: chain.begin.Provider,
		providerID: chain.begin.ProviderID, lease: chain.begin.Lease,
		personID: chain.begin.PersonID, personRevision: personRevision,
		generation: chain.begin.Generation, revision: chain.seal.Value.Revision,
		leaseGeneration: chain.begin.LeaseGeneration, leaseUntilMS: chain.begin.LeaseUntilMS,
		evidenceID: chain.beginReq.header.ID, buildID: chain.begin.BuildID,
		publicationID: publicationID, buildGeneration: chain.begin.BuildGeneration,
		contentRevision: chain.content + 1, searchJobID: 1,
		observedAt: chain.beginReq.header.ObservedAt,
		fields: []ipPersonAcceptField{
			{Name: "name", Value: chain.beginReq.header.Title, Present: true},
			{Name: "biography", Value: chain.beginReq.header.Biography, Present: true},
			{Name: "birthDate", Value: chain.beginReq.birthDate.Value, Present: true},
			{Name: "deathDate", Value: chain.beginReq.deathDate.Value, Present: true},
			{Name: "birthPlace", Value: chain.beginReq.birthPlace.Value, Present: true},
		},
		aliases: chain.aliases, external: chain.external,
	}
}

func ipPersonFirstChain(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof) *ipPersonChainStaged {
	t.Helper()
	claim, claimID := ipPersonClaimed(t, w, r, p)
	return ipPersonStageChain(t, w, r, p, claim, claimID, "evidence-person-1", "build-person-1",
		ipPersonHeaderInput(),
		EvidenceText{Present: true, Value: "1815-12-10"},
		EvidenceText{Present: true, Value: "1852-11-27"},
		EvidenceText{Present: true, Value: "London"},
		ipPersonAliasPageRows(0, 1), ipPersonExternalPageRows(0, 1))
}

func ipPersonAcceptApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipPersonAcceptCommand) (ipPersonAcceptCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
	out, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("accept", out, err)
	}
	return out, turn, tx
}

func ipPersonAcceptCommandFor(t *testing.T, chain *ipPersonChainStaged,
	q ipPersonAcceptRequest) *ipPersonAcceptCommand {
	t.Helper()
	c, err := newIPPersonAcceptCommand(ipEntityEvidenceIdentity(chain.claimID, chain.sequence+1), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIPPersonAcceptFreshReplayRetiredAndRetainedProof(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	chain := ipPersonFirstChain(t, w, r, p)
	q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
	out, turn, tx := ipPersonAcceptApply(t, w, r, p, ipPersonAcceptCommandFor(t, chain, q))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Digest == (ResultDigest{}) ||
		out.Value.OperationID != chain.begin.OperationID ||
		out.Value.PublicationID != "publication-person-1" ||
		out.Value.EvidenceID != "evidence-person-1" || out.Value.Provider != "tmdb" ||
		out.Value.ProviderID != "42" || out.Value.ObservedAt != "2026-01-01T00:00:00Z" ||
		out.Value.Name != "Ada Lovelace" || out.Value.Biography != "Mathematician." ||
		out.Value.BirthDate != "1815-12-10" || out.Value.DeathDate != "1852-11-27" ||
		out.Value.BirthPlace != "London" || out.Value.PersonID != 42 ||
		out.Value.PersonRevision != 2 || out.Value.AcceptedRevision != 2 ||
		out.Value.SourceRevision != 1 || out.Value.OwnerGeneration != 1 ||
		out.Value.ContentRevision != chain.content+1 || out.Value.SearchJobID != 1 ||
		out.Value.AliasCount != 1 || out.Value.ExternalCount != 1 ||
		out.Value.LeaseUntilMS != chain.begin.LeaseUntilMS ||
		out.Value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_publications WHERE id='publication-person-1' AND person_id=42 AND provider='tmdb' AND subject_kind='person' AND provider_id='42' AND evidence_id='evidence-person-1' AND operation_id='` + chain.begin.OperationID + `' AND proof_owner='` + chain.begin.OperationID + `' AND proof_sequence=` + ipWritingInt(chain.sequence) + ` AND proof_kind='evidence_seal' AND accepted_revision=2 AND observed_at='2026-01-01T00:00:00Z'`: 1,
		`SELECT count(*) FROM metadata_person_sources WHERE person_id=42 AND provider='tmdb' AND role='primary' AND provider_id='42' AND revision=1 AND accepted_publication_id='publication-person-1' AND accepted_evidence_id='evidence-person-1' AND incarnation=X'` + ipPersonHexBytes(ipEntityEvidenceIdentity(chain.claimID, chain.sequence+1).OperationID[:]) + `'`:                                                                                                     1,
		`SELECT count(*) FROM catalog_people WHERE id=42 AND token='person-token-42' AND name='Ada Lovelace' AND biography='Mathematician.' AND birth_date='1815-12-10' AND death_date='1852-11-27' AND birth_place='London' AND biography_source='tmdb' AND revision=2`:                                                                                                                                                                                                       1,
		`SELECT count(*) FROM catalog_people_aliases WHERE alias='name:alias 0' AND person_id=42`:                                                                                                                                                                  1,
		`SELECT count(*) FROM metadata_person_external_ids WHERE person_id=42 AND provider='imdb' AND provider_id='nm0000001'`:                                                                                                                                     1,
		`SELECT count(*) FROM metadata_person_field_origins WHERE person_id=42 AND publication_id='publication-person-1' AND evidence_id='evidence-person-1' AND observed_at='2026-01-01T00:00:00Z' AND language='en-US' AND region='US' AND selection_revision=1`: 5,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='accept' AND evidence_id='evidence-person-1' AND build_id='build-person-1' AND publication_id='publication-person-1' AND accepted_revision=2 AND result_count=5 AND proof_pin_count=1`:   1,
		`SELECT proof_pin_count FROM metadata_person_receipts WHERE command_kind='evidence_seal'`:                                                                                                                                                                  2,
		`SELECT count(*) FROM metadata_person_operations WHERE id='` + chain.begin.OperationID + `' AND phase='accepted' AND revision=` + ipWritingInt(chain.seal.Value.Revision+1) + ` AND last_command_sequence=` + ipWritingInt(chain.sequence+1):               1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	// Replay after unrelated source, lease, person, provider and slot changes
	// returns the original saved acceptance before any fresh probe.
	original := out
	ipPolicyExec(t, w, `UPDATE catalog_people SET revision=9,name='Changed' WHERE id=42`)
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET revision=99,lease_generation=99,lease_until_ms=1 WHERE id='`+chain.begin.OperationID+`'`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	ipPolicyExec(t, w, `DELETE FROM metadata_person_sources`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
	replay, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, ipPersonAcceptCommandFor(t, chain, q))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("accept replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_publications`); got != 1 {
		t.Fatal("accept replay wrote a publication", got)
	}

	// At or below ACK is Retired while the publication, source proof, pin and
	// origins stay physically retained.
	ipPersonAck(t, w, chain.begin.OperationID, chain.sequence+1)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonAccept)
	retired, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, ipPersonAcceptCommandFor(t, chain, q))
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipPersonAcceptResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired accept served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='accept'`:               1,
		`SELECT proof_pin_count FROM metadata_person_receipts WHERE command_kind='evidence_seal'`: 2,
		`SELECT count(*) FROM metadata_person_publications WHERE id='publication-person-1'`:       1,
		`SELECT count(*) FROM metadata_person_field_origins WHERE person_id=42`:                   5,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("retired accept lost retained state", query, got)
		}
	}
}

func TestIPPersonAcceptShapeRefusals(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	chain := ipPersonFirstChain(t, w, r, p)
	base := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
	longText := strings.Repeat("x", ipPersonTextMax+1)
	identity := ipEntityEvidenceIdentity(chain.claimID, chain.sequence+1)
	mutations := []struct {
		name   string
		mutate func(*ipPersonAcceptRequest)
	}{
		{"empty_operation", func(q *ipPersonAcceptRequest) { q.observation.operationID = "" }},
		{"unknown_provider", func(q *ipPersonAcceptRequest) { q.observation.provider = "plex" }},
		{"empty_provider_id", func(q *ipPersonAcceptRequest) { q.observation.providerID = "" }},
		{"empty_lease", func(q *ipPersonAcceptRequest) { q.observation.lease = "" }},
		{"zero_person", func(q *ipPersonAcceptRequest) { q.observation.personID = 0 }},
		{"zero_person_revision", func(q *ipPersonAcceptRequest) { q.observation.personRevision = 0 }},
		{"zero_generation", func(q *ipPersonAcceptRequest) { q.observation.generation = 0 }},
		{"zero_revision", func(q *ipPersonAcceptRequest) { q.observation.revision = 0 }},
		{"zero_lease_generation", func(q *ipPersonAcceptRequest) { q.observation.leaseGeneration = 0 }},
		{"zero_lease_until", func(q *ipPersonAcceptRequest) { q.observation.leaseUntilMS = 0 }},
		{"empty_evidence", func(q *ipPersonAcceptRequest) { q.observation.evidenceID = "" }},
		{"empty_build", func(q *ipPersonAcceptRequest) { q.observation.buildID = "" }},
		{"empty_publication", func(q *ipPersonAcceptRequest) { q.observation.publicationID = "" }},
		{"build_generation", func(q *ipPersonAcceptRequest) { q.observation.buildGeneration = 2 }},
		{"zero_content_revision", func(q *ipPersonAcceptRequest) { q.observation.contentRevision = 0 }},
		{"zero_search_job", func(q *ipPersonAcceptRequest) { q.observation.searchJobID = 0 }},
		{"bad_observed_at", func(q *ipPersonAcceptRequest) { q.observation.observedAt = "tomorrow" }},
		{"no_fields", func(q *ipPersonAcceptRequest) { q.observation.fields = nil }},
		{"reordered_fields", func(q *ipPersonAcceptRequest) {
			q.observation.fields[0], q.observation.fields[1] = q.observation.fields[1], q.observation.fields[0]
		}},
		{"absent_fact", func(q *ipPersonAcceptRequest) { q.observation.fields[1].Present = false }},
		{"empty_name", func(q *ipPersonAcceptRequest) { q.observation.fields[0].Value = "" }},
		{"bad_birth_date", func(q *ipPersonAcceptRequest) { q.observation.fields[2].Value = "1815-13-10" }},
		{"long_fact", func(q *ipPersonAcceptRequest) { q.observation.fields[1].Value = longText }},
		{"alias_not_canonical", func(q *ipPersonAcceptRequest) { q.observation.aliases[0].CanonicalID = "99" }},
		{"external_kind", func(q *ipPersonAcceptRequest) { q.observation.external[0].SubjectKind = "series" }},
		{"too_many_children", func(q *ipPersonAcceptRequest) {
			q.observation.aliases = ipPersonAliasPageRows(0, ipPersonMaxChildren+1)
		}},
		{"negative_source_revision", func(q *ipPersonAcceptRequest) { q.sourceRevision = -1 }},
		{"source_incarnation_without_revision", func(q *ipPersonAcceptRequest) { q.sourceIncarnation = [16]byte{1} }},
		{"source_revision_without_incarnation", func(q *ipPersonAcceptRequest) { q.sourceRevision = 2 }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			q.observation.fields = append([]ipPersonAcceptField(nil), base.observation.fields...)
			q.observation.aliases = append([]ipPersonEvidenceAlias(nil), base.observation.aliases...)
			q.observation.external = append([]ipPersonEvidenceExternal(nil), base.observation.external...)
			tc.mutate(&q)
			if c, err := newIPPersonAcceptCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
}

func TestIPPersonAcceptConflictingRequestAndSequence(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	chain := ipPersonFirstChain(t, w, r, p)
	q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
	out, turn, tx := ipPersonAcceptApply(t, w, r, p, ipPersonAcceptCommandFor(t, chain, q))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)

	changed := q
	changed.observation.fields = append([]ipPersonAcceptField(nil), q.observation.fields...)
	changed.observation.fields[1].Value = "Different"
	conflicting, err := newIPPersonAcceptCommand(ipPersonAcceptIdentityWith(chain.claimID, chain.sequence+1), changed)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
	denied, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipPersonAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	otherIdentity := ipEntityEvidenceIdentity(chain.claimID, chain.sequence+1)
	otherIdentity.OperationID = [16]byte{0x6b, 0xff}
	other, err := newIPPersonAcceptCommand(otherIdentity, q)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonAccept)
	denied, err = applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, other)
	if denied != (ipPersonAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting operation admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='accept'`); got != 1 {
		t.Fatal("conflicting accept wrote a second receipt", got)
	}
}

func ipPersonAcceptIdentityWith(claim CommandIdentity, sequence int64) CommandIdentity {
	out := claim
	out.Sequence = sequence
	out.OperationID = [16]byte{0xaa, 0xbb}
	return out
}

func TestIPPersonAcceptRejectsIncompleteForeignAndStaleSource(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipPersonAcceptRequest)
		sql    []string
		want   error
	}{
		{"missing_alias", nil, []string{`DELETE FROM metadata_evidence_aliases`}, ErrConflict},
		{"extra_alias", nil, []string{`INSERT INTO metadata_evidence_aliases(evidence_id,ordinal,requested_id,canonical_id,alias,observed_at) VALUES('evidence-person-1',2,'extra','42','Extra','2026-01-01T00:00:00Z')`}, ErrConflict},
		{"tampered_alias", nil, []string{`UPDATE metadata_evidence_aliases SET alias='Tampered'`}, ErrConflict},
		{"missing_external", nil, []string{`DELETE FROM metadata_evidence_external_ids`}, ErrConflict},
		{"extra_external", nil, []string{`INSERT INTO metadata_evidence_external_ids(evidence_id,provider,subject_kind,provider_id) VALUES('evidence-person-1','tvdb','person','tv-9')`}, ErrConflict},
		{"wrong_fact", func(q *ipPersonAcceptRequest) { q.observation.fields[1].Value = "Other" }, nil, ErrConflict},
		{"wrong_observed_at", func(q *ipPersonAcceptRequest) { q.observation.observedAt = "2026-01-02T00:00:00Z" }, nil, ErrConflict},
		{"build_not_ready", nil, []string{`UPDATE metadata_person_builds SET state='building' WHERE id='build-person-1'`}, ErrConflict},
		{"build_content_revision", nil, []string{`UPDATE metadata_person_builds SET content_revision=content_revision+1 WHERE id='build-person-1'`}, ErrConflict},
		{"seal_pin_lost", nil, []string{`UPDATE metadata_person_receipts SET proof_pin_count=0 WHERE command_kind='evidence_seal'`}, ErrConflict},
		{"seal_rows", nil, []string{`UPDATE metadata_person_receipts SET seal_rows=seal_rows+1 WHERE command_kind='evidence_seal'`}, ErrConflict},
		{"seal_algorithm", nil, []string{`UPDATE metadata_person_receipts SET seal_algorithm='other' WHERE command_kind='evidence_seal'`}, ErrConflict},
		{"evidence_unsealed", nil, []string{`UPDATE metadata_evidence SET sealed=0 WHERE id='evidence-person-1'`}, ErrConflict},
		{"search_missing", nil, []string{`DELETE FROM search_publish_jobs WHERE id=1`}, ErrResolveRequired},
		{"new_person_revision", nil, []string{`UPDATE catalog_people SET revision=2 WHERE id=42`}, ErrConflict},
		{"disabled_provider", nil, []string{`UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`}, ErrDisclosureDenied},
		{"policy_advanced", nil, []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"consent_advanced", nil, []string{`UPDATE screen_metadata_consent SET revision=2 WHERE singleton=1`}, ErrDisclosureDenied},
		{"owner_not_sealed", nil, []string{`UPDATE metadata_person_operations SET phase='building'`}, ErrConflict},
		{"lease_expired", nil, []string{`UPDATE metadata_person_operations SET lease_until_ms=1`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipPersonAcceptFixture(t)
			chain := ipPersonFirstChain(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			c, err := newIPPersonAcceptCommand(ipEntityEvidenceIdentity(chain.claimID, chain.sequence+1), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
			out, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, c)
			if out != (ipPersonAcceptCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("incomplete/foreign source admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_person_publications`:                         0,
				`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='accept'`: 0,
				`SELECT count(*) FROM metadata_person_field_origins`:                        0,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal("refused accept wrote state", query, got)
				}
			}
		})
	}
	t.Run("expired_lease_at_accept_time", func(t *testing.T) {
		w, r, p, base := ipPersonAcceptFixture(t)
		chain := ipPersonFirstChain(t, w, r, p)
		ipPersonAdvance(t, w, base, 6*time.Minute)
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
		q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
		out, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, ipPersonAcceptCommandFor(t, chain, q))
		if out != (ipPersonAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("expired accept lease admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPPersonAcceptCurrentAuthorityAndLifetime(t *testing.T) {
	prepare := func(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, *ipPersonAcceptCommand) {
		t.Helper()
		w, r, p, _ := ipPersonAcceptFixture(t)
		chain := ipPersonFirstChain(t, w, r, p)
		q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
		return w, r, p, ipPersonAcceptCommandFor(t, chain, q)
	}
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, command := prepare(t)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
			out, err := applyIPPersonAccept(t.Context(), turn, proof, t.Context(), b, command)
			if out != (ipPersonAcceptCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, command := prepare(t)
		lifetime, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
		out, err := applyIPPersonAccept(lifetime, turn, p, t.Context(), b, command)
		if out != (ipPersonAcceptCommandResult{}) || err == nil {
			t.Fatal("cancelled lifetime admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPPersonAcceptUnknownCommitFreshRead(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	chain := ipPersonFirstChain(t, w, r, p)
	q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
	c := ipPersonAcceptCommandFor(t, chain, q)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
	out, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, c)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if again, e := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, c); again != (ipPersonAcceptCommandResult{}) || e == nil {
		t.Fatal("uncertain accept frame reserved", again, e)
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_publications WHERE id='publication-person-1'`:             1,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='accept'`:                     1,
		`SELECT proof_pin_count FROM metadata_person_receipts WHERE command_kind='evidence_seal'`:       2,
		`SELECT count(*) FROM catalog_people WHERE id=42 AND revision=2 AND biography='Mathematician.'`: 1,
		`SELECT count(*) FROM metadata_person_field_origins WHERE person_id=42`:                         5,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal("durable accept", query, got)
		}
	}
}

// A person observation with no alias/external children is still a complete
// typed source; the empty families are explicit, not absent state.
func TestIPPersonAcceptHeaderOnlyEmptyFamilies(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	claim, claimID := ipPersonClaimed(t, w, r, p)
	chain := ipPersonStageChain(t, w, r, p, claim, claimID, "evidence-person-empty", "build-person-empty",
		EvidenceHeaderInput{ID: "evidence-person-empty", ParserVersion: "tmdb.person.header-exact-dates.v1",
			Provider: "tmdb", SubjectKind: "person", ProviderID: "42", SubjectID: 501,
			Language: "en-US", Region: "US", RequestShape: "tmdb.person.detail.header-exact-dates.v1",
			ObservationDigest: [32]byte{9}, ObservedAt: "2026-01-01T00:00:00Z",
			SourceURL: "https://api.themoviedb.org/3/person/42", Title: "Ada Lovelace"},
		EvidenceText{Present: true, Value: ""}, EvidenceText{Present: true, Value: ""},
		EvidenceText{Present: true, Value: ""}, nil, nil)
	q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-empty", 1)}
	out, turn, tx := ipPersonAcceptApply(t, w, r, p, ipPersonAcceptCommandFor(t, chain, q))
	if out.Outcome.State != Applied || out.Value.AliasCount != 0 || out.Value.ExternalCount != 0 ||
		out.Value.BirthDate != "" || out.Value.DeathDate != "" || out.Value.BirthPlace != "" {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM catalog_people WHERE id=42 AND birth_date='' AND death_date='' AND birth_place='' AND revision=2`: 1,
		`SELECT count(*) FROM metadata_person_field_origins WHERE person_id=42`:                                                 5,
		`SELECT count(*) FROM catalog_people_aliases`:                                                                           0,
		`SELECT count(*) FROM metadata_person_external_ids`:                                                                     0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

// The accepted head is terminal for its owner: a second acceptance with a
// different request at the next sequence is refused, and a replayed command
// keeps its original result.
func TestIPPersonAcceptOwnerLockAndCapturedRevision(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	chain := ipPersonFirstChain(t, w, r, p)
	q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
	out, turn, tx := ipPersonAcceptApply(t, w, r, p, ipPersonAcceptCommandFor(t, chain, q))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	// The captured pre-accept revision is the publication's exact CAS witness,
	// not a re-read current person revision.
	if got := ipPolicyQueryInt(t, w, `SELECT accepted_revision FROM metadata_person_publications WHERE id='publication-person-1'`); got != 2 {
		t.Fatal("accepted revision", got)
	}
	ipPersonAck(t, w, chain.begin.OperationID, chain.sequence+1)
	second := q
	second.observation.publicationID = "publication-person-2"
	second.observation.fields = append([]ipPersonAcceptField(nil), q.observation.fields...)
	second.observation.fields[1].Value = "Second"
	secondCommand, err := newIPPersonAcceptCommand(
		ipEntityEvidenceIdentity(chain.claimID, chain.sequence+2), second)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
	refused, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, secondCommand)
	if refused != (ipPersonAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("terminal owner accepted again", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_publications`); got != 1 {
		t.Fatal("second acceptance wrote a head", got)
	}
}

// A caller-named captured revision that is not the owner's captured value is
// refused before any fresh source is read.
func TestIPPersonAcceptCapturedRevisionMismatch(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	chain := ipPersonFirstChain(t, w, r, p)
	q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 2)}
	c, err := newIPPersonAcceptCommand(
		ipEntityEvidenceIdentity(chain.claimID, chain.sequence+1), q)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
	out, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, c)
	if out != (ipPersonAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("wrong captured revision admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_publications`); got != 0 {
		t.Fatal("wrong captured revision wrote a head", got)
	}
}

// A retained person receipt of a DIFFERENT person command kind (here the
// append kind an entity path would also use) never serves an acceptance and is
// a conflict, even with the same owner, incarnation, sequence and evidence.
func TestIPPersonAcceptWrongKindReceiptConflicts(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	chain := ipPersonFirstChain(t, w, r, p)
	q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
	identity := ipEntityEvidenceIdentity(chain.claimID, chain.sequence+1)
	c := ipPersonAcceptCommandFor(t, chain, q)
	// The owner floor admits the sequence, but the stored receipt is an append.
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET last_command_sequence=? WHERE id=?`,
		identity.Sequence, chain.begin.OperationID)
	ipPolicyExec(t, w, `INSERT INTO metadata_person_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,evidence_id,build_id) VALUES(?,?,?,?,?,0,?,'append',2,1,'evidence-person-1','build-person-1')`,
		chain.begin.OperationID, identity.Incarnation[:], identity.Sequence,
		identity.OperationID[:], c.digest[:], make([]byte, 32))
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
	out, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, c)
	if out != (ipPersonAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("foreign person receipt kind served the accept", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='accept'`); got != 0 {
		t.Fatal("wrong-kind accept wrote a receipt", got)
	}
}

func TestIPPersonAcceptDescriptors(t *testing.T) {
	for _, cell := range []struct {
		id   CommandID
		name string
	}{
		{IPPersonAccept, "ip_person_accept"},
		{IPPersonDelta, "ip_person_delta"},
	} {
		expected := Descriptor{ID: cell.id, Name: cell.name, Durability: Normal}
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
			t.Fatal(actual, err)
		}
	}
	if IPPersonAccept != 234 || IPPersonDelta != 235 ||
		IPPersonAccept == IPEntityAccept || IPPersonDelta == IPEntityDelta {
		t.Fatal("person accept/delta purposes aliased the entity purposes")
	}
}

func TestIPPersonAcceptStaleOwnerAndPolicyChanges(t *testing.T) {
	cases := []struct {
		name string
		sql  []string
		want error
	}{
		{"new_observation", []string{`UPDATE catalog_people SET revision=3 WHERE id=42`}, ErrConflict},
		{"new_subject", []string{`UPDATE metadata_provider_subjects SET revision=2 WHERE id=501`}, ErrConflict},
		{"policy_advanced", []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"consent_advanced", []string{`UPDATE screen_metadata_consent SET revision=2 WHERE singleton=1`}, ErrDisclosureDenied},
		{"lease_expired", []string{`UPDATE metadata_person_operations SET lease_until_ms=1`}, ErrConflict},
		{"owner_not_sealed", []string{`UPDATE metadata_person_operations SET phase='claimed'`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipPersonAcceptFixture(t)
			chain := ipPersonFirstChain(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
			q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
			out, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, ipPersonAcceptCommandFor(t, chain, q))
			if out != (ipPersonAcceptCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale owner admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

// An existing ACCEPTED person/provider source slot is never re-accepted by the
// birth command: a replacement must name the exact old head through IP-16's
// delta. The empty-slot witness is the only source CAS accept may move.
func TestIPPersonAcceptRefusesAnAcceptedSourceSlot(t *testing.T) {
	w, r, p, _ := ipPersonAcceptFixture(t)
	chain := ipPersonFirstChain(t, w, r, p)
	ipPolicyExec(t, w, `INSERT INTO metadata_person_publications(id,person_id,provider,subject_kind,provider_id,evidence_id,operation_id,proof_owner,proof_incarnation,proof_sequence,accepted_revision,observed_at) SELECT 'publication-old',42,'tmdb','person','42','evidence-person-1',o.id,o.id,r.command_incarnation,r.command_sequence,1,'2026-01-01T00:00:00Z' FROM metadata_person_operations o JOIN metadata_person_receipts r ON r.owner_id=o.id AND r.command_kind='evidence_seal' LIMIT 1`)
	ipPolicyExec(t, w, `INSERT INTO metadata_person_sources(person_id,provider,role,provider_id,incarnation,revision,accepted_publication_id,accepted_evidence_id) SELECT 42,'tmdb','primary','42',command_incarnation,1,'publication-old','evidence-person-1' FROM metadata_person_operations LIMIT 1`)
	q := ipPersonAcceptRequest{observation: ipPersonObservationFor(chain, "publication-person-1", 1)}
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonAccept)
	out, err := applyIPPersonAccept(t.Context(), turn, p, t.Context(), b, ipPersonAcceptCommandFor(t, chain, q))
	if out != (ipPersonAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("accepted source slot re-accepted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_publications`); got != 1 {
		t.Fatal("re-accept wrote a head", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_sources WHERE accepted_publication_id='publication-old'`); got != 1 {
		t.Fatal("re-accept moved the accepted slot", got)
	}
}
