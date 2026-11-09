package writer

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Groups D and E are embedded, so the IP receipt/proof tuple, the owner field
// presence columns and the TV set proof are qualified against the canonical
// schema. The provider operation, subject binding, evidence body/build/children,
// publication, subject-provider, field-origin, entity-ID and term bodies are
// the real embedded schema.
func ipEntityAcceptFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, ipEntityAcceptStaged) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, query := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',1,0,1,1,0)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider,command_incarnation) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'tvdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(1,'tmdb','series','tmdb-1','en','US','ip.tmdb.series.evidence.detail+recordings+media+tracks+credits.v2',X'0c000000000000000000000000000000',1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(query, err)
		}
	}
	defs := append(append(append(append(append(ipPolicyDefinitions(), ipEntityJobDefinitions()...),
		ipEntityEvidenceDefinitions()...), ipEntitySealDefinitions()...), ipTVSetDefinitions()...),
		ipEntityAcceptDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	r := &ipPolicyRegistry{registry, keeper}
	staged := ipEntityAcceptStage(t, w, r, p)
	ipEntityAcceptSeedHead(t, w)
	return w, r, p, staged
}

type ipEntityAcceptStaged struct {
	owner        ipEntityJobResult
	claim        CommandIdentity
	pages        []ipEntitySealPage
	declarations []ipEvidenceFamilyDeclaration
	begin        ipEntityEvidenceResult
	seal         ipEntitySealRequest
	sequence     int64
}

// ipEntityAcceptPages is one complete observation: one alias, one external ID,
// one genre term and a two-row cast/crew cohort, in exact append order.
func ipEntityAcceptPages() []ipEntitySealPage {
	return []ipEntitySealPage{
		{Family: "alias", AfterOrdinal: 0, rows: 1, aliases: []ipEvidenceAlias{
			{Ordinal: 0, RequestedID: "requested-0", CanonicalID: "tmdb-1", Name: "Alias 0"},
		}},
		{Family: "external", AfterOrdinal: 0, rows: 1, external: []ipEvidenceExternal{
			{Provider: "tmdb", SubjectKind: "series", ProviderID: "tmdb-1"},
		}},
		{Family: "term", AfterOrdinal: 0, rows: 1, terms: []ipEvidenceTerm{
			{Ordinal: 0, Vocabulary: "genre", Value: "Drama"},
		}},
		{Family: "credit", AfterOrdinal: 0, rows: 2, credits: []ipEvidenceCredit{
			{Ordinal: 0, Class: "cast", ProviderPersonID: "person-1", Name: "Actor One",
				CreditID: "credit-1", Role: "Actor", Department: "Acting", Billing: 1,
				BillingPresent: true, SourceOrdinal: 0, SourceOrdinalPresent: true},
			{Ordinal: 1, Class: "crew", ProviderPersonID: "person-2", Name: "Director One",
				CreditID: "credit-2", Role: "Director", Department: "Directing",
				SourceOrdinal: 1, SourceOrdinalPresent: true},
		}},
	}
}

func ipEntityAcceptEntity(t *testing.T, w *executor) {
	t.Helper()
	public := make([]byte, 16)
	binary.BigEndian.PutUint64(public[8:], 1)
	inc := ipEntityJobIncarnation(1)
	ipPolicyExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1,?,777,2)`, public)
	ipPolicyExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(1,?,777,2,'Entity 1','entity 1','e','entity 1','e',1,?,?,1,1,1)`, public, inc[:], inc[:])
}

func ipEntityAcceptClaimed(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) (ipEntityJobResult, CommandIdentity) {
	t.Helper()
	ipEntityAcceptEntity(t, w)
	ipPolicyExec(t, w, `INSERT INTO metadata_acquisition_jobs(entity_id,provider,purpose,generation,revision,status,selected_id,manual,review_search,query_override,year_override,force_refresh,episode_order,parent_publication,attempts,next_attempt_ms,lease,lease_until_ms,error,page,after_item,staged_bytes,observed_at) VALUES(1,'tmdb','metadata',1,1,'pending','tmdb-1',0,0,'',0,0,'','',0,0,'',0,'',0,'',0,'2026-01-01T00:00:00.000000000Z')`)
	identity := ipEntityJobIdentity(0x21, 1, 0x31)
	q := ipEntityJobClaimFor(1, "tmdb")
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

// ipEntityAcceptStage stages and seals one complete IP-06 build through the
// real IP-05/IP-06/IP-07 commands, leaving the owner ACKed at the seal sequence.
func ipEntityAcceptStage(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) ipEntityAcceptStaged {
	t.Helper()
	owner, claim := ipEntityAcceptClaimed(t, w, r, p)
	pages := ipEntityAcceptPages()
	declarations := ipEntitySealDeclarations(pages)
	counts := ipEntitySealDeclaredCounts(declarations)
	bq := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", counts)
	bq.families = declarations
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p,
		mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), bq))
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
			mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, sequence), request))
		ipPolicyCommitTurn(t, turn, tx)
		ipEntityEvidenceAck(t, w, out.Value, sequence)
		current = out.Value
		before += page.rows
		sequence++
	}
	staged := ipEntityAcceptStaged{owner: owner, claim: claim, pages: pages,
		declarations: declarations, begin: current, sequence: sequence,
		seal: ipEntitySealRequestFor(owner, current, declarations, pages)}
	seal := ipEntitySealCommandFor(t, ipEntitySealStaged{owner: staged.owner, claim: staged.claim,
		pages: staged.pages, declarations: staged.declarations, begin: staged.begin,
		seal: staged.seal, sequence: staged.sequence})
	out, turn, tx := ipEntitySealApply(t, w, r, p, seal)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, staged.sequence)
	return staged
}

// ipEntityAcceptSeedHead seeds the IA accepted-subject seam, the existing search
// intent and one locked explicit-empty owner description (the catalogue
// overview). IP-09 consumes these; it never creates or reinterprets them.
func ipEntityAcceptSeedHead(t *testing.T, w *executor) {
	t.Helper()
	ipPolicyExec(t, w, `INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,status,observed_at) VALUES(1,'tmdb','primary',X'0d000000000000000000000000000000',1,'series','tmdb-1','selected','2025-12-01T00:00:00Z')`)
	ipPolicyExec(t, w, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'bank-one',?,?,'unicode-fixture','active',0)`,
		searchpolicy.RecipeV1, searchpolicy.EngineVersion)
	ipPolicyExec(t, w, `INSERT INTO search_publish_jobs(id,entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,command_incarnation) VALUES(1,1,1,1,X'0e000000000000000000000000000000',1,?,'idle',X'0f000000000000000000000000000000')`,
		searchpolicy.RecipeV1)
	ipPolicyExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('show',1,'description','','',1,1,1,'owner','2025-12-01T00:00:00Z')`)
}

func ipEntityAcceptRequestFor(staged ipEntityAcceptStaged) ipEntityAcceptRequest {
	return ipEntityAcceptRequest{
		operationID: staged.owner.OperationID, provider: staged.owner.Provider,
		purpose: "metadata", selectedID: staged.owner.SelectedID, lease: staged.owner.Lease,
		entityID: staged.owner.EntityID, generation: 1, leaseUntilMS: staged.owner.LeaseUntilMS,
		evidenceID: "evidence-one", buildID: "build-one", publicationID: "publication-one",
		buildGeneration: 1, contentRevision: staged.begin.Revision + 1,
		subjectID: 1, subjectKind: "series", providerID: "tmdb-1",
		parserVersion: ipEntityEvidenceParser("tmdb", "series"), language: "en", region: "US",
		requestShape: ipEntityEvidenceRequest("tmdb", "series"), observedAt: "2026-01-01T00:00:00Z",
		selectionIncarnation: [16]byte{0x0d}, selectionRevision: 1, searchJobID: 1,
		fields: []ipEntityAcceptField{
			{Name: "title", Value: "Accepted Series", Present: true},
			{Name: "language", Value: "en", Present: true},
			{Name: "overview", Value: "Provider overview", Present: true},
			{Name: "releaseDate", Value: "2020-01-01", Present: true},
			{Name: "year", Value: "2020", Present: true},
			{Name: "status", Value: "Returning", Present: true},
		},
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

func ipEntityAcceptCommandFor(t *testing.T, staged ipEntityAcceptStaged, q ipEntityAcceptRequest) *ipEntityAcceptCommand {
	t.Helper()
	c, err := newIPEntityAcceptCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence+1), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipEntityAcceptApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipEntityAcceptCommand) (ipEntityAcceptCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
	out, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("accept", out, err)
	}
	return out, turn, tx
}

func TestIPEntityAcceptFreshReplayRetiredAndRetainedProof(t *testing.T) {
	w, r, p, staged := ipEntityAcceptFixture(t)
	out, turn, tx := ipEntityAcceptApply(t, w, r, p, ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Digest == (ResultDigest{}) ||
		out.Value.OperationID != staged.owner.OperationID || out.Value.PublicationID != "publication-one" ||
		out.Value.EvidenceID != "evidence-one" || out.Value.SetID != "" ||
		out.Value.Provider != "tmdb" || out.Value.SubjectKind != "series" ||
		out.Value.ProviderID != "tmdb-1" || out.Value.ParserVersion != ipEntityEvidenceParser("tmdb", "series") ||
		out.Value.ObservedAt != "2026-01-01T00:00:00Z" || out.Value.EntityID != 1 ||
		out.Value.OwnerGeneration != 1 || out.Value.ContentRevision != staged.begin.Revision+1 ||
		out.Value.AcceptedRevision != 2 || out.Value.SelectionRevision != 1 || out.Value.SearchJobID != 1 ||
		out.Value.FieldCount != 6 || out.Value.ExternalCount != 1 || out.Value.TermCount != 1 ||
		out.Value.CreditCount != 2 || out.Value.LeaseUntilMS != staged.owner.LeaseUntilMS ||
		out.Value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publications WHERE id='publication-one' AND entity_id=1 AND provider='tmdb' AND subject_kind='series' AND provider_id='tmdb-1' AND evidence_id='evidence-one' AND set_id IS NULL AND previous_id IS NULL AND selection_revision=1 AND generation=1 AND decision='accepted' AND observed_at='2026-01-01T00:00:00Z'`: 1,
		`SELECT count(*) FROM metadata_subject_providers WHERE entity_id=1 AND provider='tmdb' AND role='primary' AND accepted_evidence_id='evidence-one' AND accepted_set_id IS NULL AND accepted_publication_id='publication-one' AND revision=2 AND observed_at='2026-01-01T00:00:00Z'`:                                                                1,
		`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Accepted Series' AND overview='' AND release_date='2020-01-01' AND year=2020 AND metadata_language='en' AND revision=2 AND metadata_revision=2`:                                                                                                                                      1,
		`SELECT count(*) FROM metadata_field_origins WHERE entity_id=1 AND publication_id='publication-one' AND evidence_id='evidence-one' AND observed_at='2026-01-01T00:00:00Z' AND language='en' AND region='US'`:                                                                                                                                      6,
		`SELECT count(*) FROM metadata_entity_external_ids WHERE entity_id=1 AND provider='tmdb' AND subject_kind='series' AND provider_id='tmdb-1'`:                                                                                                                                                                                                      1,
		`SELECT count(*) FROM catalog_terms WHERE vocab=1 AND key='drama' AND label='Drama'`:                                                                                                                                         1,
		`SELECT count(*) FROM catalog_entity_terms AS e JOIN catalog_terms AS t ON t.id=e.term_id WHERE e.entity_id=1 AND t.key='drama'`:                                                                                             1,
		`SELECT count(*) FROM catalog_term_sources WHERE entity_id=1 AND provider='tmdb' AND source_id='tmdb:genre:drama' AND ordinal=0`:                                                                                             1,
		`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='' AND value_present=1 AND locked=1 AND automatic_value='Provider overview' AND automatic_present=1`:         1,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept' AND terminal=1 AND result_phase='accepted' AND publication_id='publication-one' AND evidence_id='evidence-one' AND set_id IS NULL AND result_count=6`: 1,
		`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`:                                                                                                                                        2,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + staged.owner.OperationID + `'`:                                                                                                                 staged.sequence + 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	// Replay after unrelated source, lease and policy changes returns the
	// original saved acceptance before any fresh probe and writes nothing.
	original := out
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending',generation=9,revision=9,lease='other',lease_until_ms=1000000 WHERE entity_id=1`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=5,revision=5 WHERE id=1`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
	replay, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("accept replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for _, query := range []string{
		`SELECT count(*) FROM metadata_publications`,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept'`,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != 1 {
			t.Fatal("accept replay wrote rows", query, got)
		}
	}

	// At or below ACK is Retired. The source proof, its pin and the accepted
	// head stay physically retained.
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, staged.sequence+1)
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityAccept)
	retired, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipEntityAcceptResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired accept served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept' AND terminal=1`: 1,
		`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`:  2,
		`SELECT count(*) FROM metadata_publications WHERE id='publication-one'`:                1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("retired accept lost its proof", query, got)
		}
	}
}

func TestIPEntityAcceptShapeRefusals(t *testing.T) {
	w, r, p, staged := ipEntityAcceptFixture(t)
	_ = w
	_ = r
	_ = p
	base := ipEntityAcceptRequestFor(staged)
	longID := strings.Repeat("x", ipEvidenceIDMax+1)
	longField := strings.Repeat("x", ipEntityAcceptFieldMax+1)
	identity := ipEntityEvidenceIdentity(staged.claim, staged.sequence+1)
	mutations := []struct {
		name   string
		mutate func(*ipEntityAcceptRequest)
	}{
		{"empty_evidence", func(q *ipEntityAcceptRequest) { q.evidenceID = "" }},
		{"long_evidence", func(q *ipEntityAcceptRequest) { q.evidenceID = longID }},
		{"empty_build", func(q *ipEntityAcceptRequest) { q.buildID = "" }},
		{"empty_publication", func(q *ipEntityAcceptRequest) { q.publicationID = "" }},
		{"long_publication", func(q *ipEntityAcceptRequest) { q.publicationID = longID }},
		{"episode_subject", func(q *ipEntityAcceptRequest) { q.subjectKind = "episode" }},
		{"movie_subject", func(q *ipEntityAcceptRequest) { q.subjectKind = "movie" }},
		{"person_subject", func(q *ipEntityAcceptRequest) { q.subjectKind = "person" }},
		{"set_on_artist", func(q *ipEntityAcceptRequest) {
			q.subjectKind, q.setID = "artist", "set-one"
		}},
		{"empty_provider_id", func(q *ipEntityAcceptRequest) { q.providerID = "" }},
		{"parser_without_ip_namespace", func(q *ipEntityAcceptRequest) { q.parserVersion = "series.v1" }},
		{"parser_equals_request", func(q *ipEntityAcceptRequest) {
			q.parserVersion, q.requestShape = "ip.tmdb.series.v1", "ip.tmdb.series.v1"
		}},
		{"empty_language", func(q *ipEntityAcceptRequest) { q.language = "" }},
		{"bad_observed_at", func(q *ipEntityAcceptRequest) { q.observedAt = "tomorrow" }},
		{"zero_selection_incarnation", func(q *ipEntityAcceptRequest) { q.selectionIncarnation = [16]byte{} }},
		{"zero_selection_revision", func(q *ipEntityAcceptRequest) { q.selectionRevision = 0 }},
		{"zero_search_job", func(q *ipEntityAcceptRequest) { q.searchJobID = 0 }},
		{"no_fields", func(q *ipEntityAcceptRequest) { q.fields = nil }},
		{"missing_title", func(q *ipEntityAcceptRequest) { q.fields = q.fields[1:] }},
		{"empty_title", func(q *ipEntityAcceptRequest) { q.fields[0].Value = "" }},
		{"missing_language", func(q *ipEntityAcceptRequest) {
			q.fields = append([]ipEntityAcceptField{{Name: "title", Value: "T", Present: true}}, q.fields[2:]...)
		}},
		{"unknown_field", func(q *ipEntityAcceptRequest) {
			q.fields = append(q.fields, ipEntityAcceptField{Name: "genre", Value: "Drama", Present: true})
		}},
		{"duplicate_field", func(q *ipEntityAcceptRequest) {
			q.fields = append(q.fields, ipEntityAcceptField{Name: "title", Value: "Other", Present: true})
		}},
		{"long_field", func(q *ipEntityAcceptRequest) { q.fields[0].Value = longField }},
		{"bad_year", func(q *ipEntityAcceptRequest) {
			for i := range q.fields {
				if q.fields[i].Name == "year" {
					q.fields[i].Value = "20x0"
				}
			}
		}},
		{"duplicate_external", func(q *ipEntityAcceptRequest) { q.external = append(q.external, q.external[0]) }},
		{"unknown_external_provider", func(q *ipEntityAcceptRequest) { q.external[0].Provider = "other" }},
		{"reserved_term_vocabulary", func(q *ipEntityAcceptRequest) { q.terms[0].Vocabulary = "keyword" }},
		{"duplicate_term", func(q *ipEntityAcceptRequest) { q.terms = append(q.terms, q.terms[0]) }},
		{"cast_without_billing", func(q *ipEntityAcceptRequest) { q.credits[0].BillingPresent = false }},
		{"duplicate_credit", func(q *ipEntityAcceptRequest) { q.credits[1].Ordinal = q.credits[0].Ordinal }},
		{"missing_credit_source_ordinal", func(q *ipEntityAcceptRequest) { q.credits[0].SourceOrdinalPresent = false }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			q.fields = append([]ipEntityAcceptField(nil), base.fields...)
			q.external = append([]ipEntityAcceptExternal(nil), base.external...)
			q.terms = append([]ipEntityAcceptTerm(nil), base.terms...)
			q.credits = append([]ipEntityAcceptCredit(nil), base.credits...)
			tc.mutate(&q)
			if c, err := newIPEntityAcceptCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
}

// One retained sequence may serve only its ORIGINAL request and operation. A
// different request or a different command operation at the same sequence is a
// conflict, never a second acceptance.
func TestIPEntityAcceptConflictingRequestAndSequence(t *testing.T) {
	w, r, p, staged := ipEntityAcceptFixture(t)
	out, turn, tx := ipEntityAcceptApply(t, w, r, p, ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)

	changed := ipEntityAcceptRequestFor(staged)
	changed.fields = append([]ipEntityAcceptField(nil), changed.fields...)
	changed.fields[0].Value = "Different Series"
	conflicting, err := newIPEntityAcceptCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence+1), changed)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
	denied, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipEntityAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	otherIdentity := ipEntityEvidenceIdentity(staged.claim, staged.sequence+1)
	otherIdentity.OperationID = [16]byte{0x6b, 0xff}
	other, err := newIPEntityAcceptCommand(otherIdentity, ipEntityAcceptRequestFor(staged))
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityAccept)
	denied, err = applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, other)
	if denied != (ipEntityAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting operation admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept'`); got != 1 {
		t.Fatal("conflicting accept wrote a second receipt", got)
	}
}

func TestIPEntityAcceptRejectsIncompleteForeignAndStaleSource(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipEntityAcceptRequest)
		sql    []string
		want   error
	}{
		{"missing_external", nil, []string{`DELETE FROM metadata_evidence_external_ids WHERE evidence_id='evidence-one'`}, ErrConflict},
		{"extra_external", nil, []string{`INSERT INTO metadata_evidence_external_ids(evidence_id,provider,subject_kind,provider_id) VALUES('evidence-one','tmdb','series','tmdb-2')`}, ErrConflict},
		{"wrong_external", func(q *ipEntityAcceptRequest) { q.external[0].ProviderID = "tmdb-9" }, nil, ErrConflict},
		{"missing_term", nil, []string{`DELETE FROM metadata_evidence_terms WHERE evidence_id='evidence-one'`}, ErrConflict},
		{"wrong_term", func(q *ipEntityAcceptRequest) { q.terms[0].Value = "Other" }, nil, ErrConflict},
		{"missing_credit", nil, []string{`DELETE FROM metadata_evidence_credits WHERE evidence_id='evidence-one' AND ordinal=1`}, ErrConflict},
		{"wrong_credit", func(q *ipEntityAcceptRequest) { q.credits[1].Name = "Other Director" }, nil, ErrConflict},
		{"build_not_ready", nil, []string{`UPDATE metadata_evidence_builds SET state='building',phase='children' WHERE id='build-one'`}, ErrConflict},
		{"seal_pin_lost", nil, []string{`UPDATE metadata_ip_receipts SET proof_pin_count=0 WHERE command_kind='evidence_seal'`}, ErrConflict},
		{"selection_revision", nil, []string{`UPDATE metadata_subject_providers SET revision=2 WHERE entity_id=1`}, ErrConflict},
		{"selection_status", nil, []string{`UPDATE metadata_subject_providers SET status='matched' WHERE entity_id=1`}, ErrConflict},
		{"selection_incarnation", nil, []string{`UPDATE metadata_subject_providers SET incarnation=X'ffffffffffffffffffffffffffffffff' WHERE entity_id=1`}, ErrConflict},
		{"search_missing", nil, []string{`DELETE FROM search_publish_jobs WHERE id=1`}, ErrResolveRequired},
		{"new_observation", nil, []string{`UPDATE catalog_entities SET metadata_revision=2,revision=2 WHERE id=1`}, ErrConflict},
		{"new_source_revision", nil, []string{`UPDATE catalog_entities SET source_revision=2 WHERE id=1`}, ErrConflict},
		{"policy_advanced", nil, []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"consent_advanced", nil, []string{`UPDATE screen_metadata_consent SET revision=2 WHERE singleton=1`}, ErrDisclosureDenied},
		{"owner_not_claimed", nil, []string{`UPDATE metadata_provider_operations SET status='staged' WHERE provider='tmdb'`}, ErrConflict},
		{"lease_expired", nil, []string{`UPDATE metadata_provider_operations SET lease_until='2020-01-01T00:00:00Z' WHERE provider='tmdb'`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, staged := ipEntityAcceptFixture(t)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := ipEntityAcceptRequestFor(staged)
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			c, err := newIPEntityAcceptCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence+1), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
			out, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, c)
			if out != (ipEntityAcceptCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("incomplete/foreign source admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_publications`:                            0,
				`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept'`: 0,
				`SELECT count(*) FROM metadata_field_origins`:                           0,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal("refused accept wrote state", query, got)
				}
			}
		})
	}
	t.Run("expired_lease_at_accept_time", func(t *testing.T) {
		w, r, p, staged := ipEntityAcceptFixture(t)
		base := w.now()
		ipEntityJobAdvance(t, w, base, 6*time.Minute)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
		out, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
		if out != (ipEntityAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("expired accept lease admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPEntityAcceptCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, staged := ipEntityAcceptFixture(t)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
			out, err := applyIPEntityAccept(t.Context(), turn, proof, t.Context(), b, ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
			if out != (ipEntityAcceptCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, staged := ipEntityAcceptFixture(t)
		lifetime, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
		out, err := applyIPEntityAccept(lifetime, turn, p, t.Context(), b, ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
		if out != (ipEntityAcceptCommandResult{}) || err == nil {
			t.Fatal("cancelled lifetime admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPEntityAcceptUnknownCommitFreshRead(t *testing.T) {
	w, r, p, staged := ipEntityAcceptFixture(t)
	c := ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged))
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
	out, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, c)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	// The uncertain frame is closed; it never reserves the saved result.
	if again, e := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, c); again != (ipEntityAcceptCommandResult{}) || e == nil {
		t.Fatal("uncertain accept frame reserved", again, e)
	}
	// Only a FRESH read-only connection may prove the durable outcome.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publications WHERE id='publication-one'`:                                            1,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept' AND terminal=1 AND result_phase='accepted'`: 1,
		`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`:                              2,
		`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Accepted Series' AND revision=2`:                      1,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + staged.owner.OperationID + `'`:       staged.sequence + 1,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal("durable accept", query, got)
		}
	}
}

// A series acceptance may carry its complete sealed IP-08 set. The publication
// and the accepted slot then pin both the evidence and the set proof, and the
// set seal receipt retains one more downstream pin.
func TestIPEntityAcceptSetSource(t *testing.T) {
	w, r, p, staged := ipEntityAcceptFixture(t)
	incarnation := staged.claim.Incarnation
	ipPolicyExec(t, w, `INSERT INTO metadata_publication_sets(member_kind,id,entity_id,provider,incarnation,revision,generation,subject_kind,provider_id,episode_order,consent_revision,policy_incarnation,policy_revision,complete,coverage_source_revision,coverage_hierarchy_revision,coverage_selection_revision,coverage_after_item,coverage_complete,ip_proof_owner,ip_proof_incarnation,ip_proof_sequence,ip_operation_id,ip_operation_incarnation) VALUES('episode','set-one',1,'tmdb',?,2,1,'series','tmdb-1','aired',1,X'09000000000000000000000000000000',1,0,1,0,0,2,0,NULL,NULL,NULL,?,?)`,
		incarnation[:], staged.owner.OperationID, incarnation[:])
	commitment := make([]byte, 32)
	commitment[0] = 0x5a
	ipPolicyExec(t, w, `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id,set_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes,proof_pin_count,entity_id,provider,evidence_subject_kind,evidence_provider_id) VALUES(?,?,?,?,?,'set_seal',2,0,?,'sealed',1,0,'set-digest',2,64,1,'evidence-one',1,'`+ipEntityEvidenceParser("tmdb", "series")+`','build-one','set-one','ip.tv-set',1,?,2,64,1,1,'tmdb','series','tmdb-1')`,
		staged.owner.OperationID, incarnation[:], staged.sequence+1, make([]byte, 16), make([]byte, 32),
		make([]byte, 32), commitment)
	ipPolicyExec(t, w, `UPDATE metadata_provider_operations SET last_command_sequence=?,acknowledged_command_sequence=? WHERE id=?`,
		staged.sequence+1, staged.sequence+1, staged.owner.OperationID)

	q := ipEntityAcceptRequestFor(staged)
	q.setID = "set-one"
	// The accept runs at the sequence after the seeded set seal.
	c, err := newIPEntityAcceptCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence+2), q)
	if err != nil {
		t.Fatal(err)
	}
	// An incomplete set is never accepted as a source.
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
	denied, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, c)
	if denied != (ipEntityAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("incomplete set admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_publications`); got != 0 {
		t.Fatal("incomplete set wrote a publication", got)
	}

	ipPolicyExec(t, w, `UPDATE metadata_publication_sets SET complete=1,coverage_complete=1,ip_proof_owner=?,ip_proof_incarnation=?,ip_proof_sequence=? WHERE id='set-one'`, staged.owner.OperationID, incarnation[:], staged.sequence+1)
	out, turn, tx := ipEntityAcceptApply(t, w, r, p, c)
	if out.Outcome.State != Applied || out.Value.SetID != "set-one" || out.Value.PublicationID != "publication-one" {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publications WHERE id='publication-one' AND set_id='set-one' AND evidence_id='evidence-one'`:                                                             1,
		`SELECT count(*) FROM metadata_subject_providers WHERE entity_id=1 AND accepted_set_id='set-one' AND accepted_evidence_id='evidence-one' AND accepted_publication_id='publication-one'`: 1,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept' AND set_id='set-one'`:                                                                                            1,
		`SELECT proof_pin_count FROM metadata_ip_receipts WHERE command_kind='set_seal'`:                                                                                                        2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

// The retained selector image is still 100 cast + 50 crew, but an IP evidence
// cohort is a different bound: IP-06 allows 1024 evidence credits of any class
// across pages, and contributions stay evidence-pinned.
func TestIPEntityAcceptCreditSelectionCaps(t *testing.T) {
	cast := func(n int) []ScreenCreditCandidate {
		out := make([]ScreenCreditCandidate, n)
		for i := range out {
			out[i] = ScreenCreditCandidate{Class: Cast, Billing: int64(i), SourceOrdinal: int64(i),
				CreditID: "credit", Role: "Actor", Department: "Acting"}
		}
		return out
	}
	crew := func(n, start int) []ScreenCreditCandidate {
		out := make([]ScreenCreditCandidate, n)
		for i := range out {
			out[i] = ScreenCreditCandidate{Class: Crew, SourceOrdinal: int64(start + i),
				CreditID: "credit", Role: "Director", Department: "Directing"}
		}
		return out
	}
	if err := ValidateRetainedCreditShape(append(cast(MaxCastCredits+1), crew(MaxCrewCredits, MaxCastCredits+1)...)); err == nil {
		t.Fatal("101 cast admitted")
	}
	if err := ValidateRetainedCreditShape(append(cast(MaxCastCredits), crew(MaxCrewCredits+1, MaxCastCredits)...)); err == nil {
		t.Fatal("51 crew admitted")
	}
	if err := ValidateRetainedCreditShape(append(cast(MaxCastCredits), crew(MaxCrewCredits, MaxCastCredits)...)); err != nil {
		t.Fatal("100 cast and 50 crew refused", err)
	}
	// The evidence cohort bound is the IP-06 credit family (1024 rows), not the
	// retained selector image, and it carries every class the evidence does.
	credit := func(i int, class string) ipEntityAcceptCredit {
		return ipEntityAcceptCredit{Class: class, Ordinal: int64(i), ProviderPersonID: "person",
			CreditID: "credit", Name: "Artist", Role: "Artist", Department: "",
			SourceOrdinal: int64(i), SourceOrdinalPresent: true}
	}
	for _, tc := range []struct {
		name   string
		class  string
		rows   int
		refuse bool
	}{
		{"artist_credits", "artist", 3, false},
		{"two_hundred_credits", "artist", 200, false},
		{"evidence_bound", "cast", ipEvidenceFamilySpecs[ipEvidenceFamilyIndex("credit")].maxRows, false},
		{"over_evidence_bound", "cast", ipEvidenceFamilySpecs[ipEvidenceFamilyIndex("credit")].maxRows + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credits := make([]ipEntityAcceptCredit, tc.rows)
			for i := range credits {
				credits[i] = credit(i, tc.class)
				if tc.class == "cast" {
					credits[i].Billing, credits[i].BillingPresent = int64(i), true
				}
			}
			err := ipEntityAcceptCreditsShape(credits)
			if tc.refuse && err == nil {
				t.Fatal("over-evidence credit cohort admitted")
			}
			if !tc.refuse && err != nil {
				t.Fatal("evidence credit cohort refused", err)
			}
		})
	}
	// A duplicate (scope, ordinal) would hide a missing evidence row.
	duplicate := []ipEntityAcceptCredit{credit(0, "artist"), credit(0, "artist")}
	if ipEntityAcceptCreditsShape(duplicate) == nil {
		t.Fatal("duplicate evidence credit admitted")
	}
}

// The accept applies provider values as AUTOMATIC observations: a locked owner
// field keeps its effective catalogue value (including a locked explicit
// empty) and receives the provider value only as automatic_value; unlocked
// fields still update the projection.
func TestIPEntityAcceptOwnerLockRespected(t *testing.T) {
	t.Run("locked_title_survives_accept", func(t *testing.T) {
		w, r, p, staged := ipEntityAcceptFixture(t)
		ipPolicyExec(t, w, `UPDATE catalog_entities SET title='Owner Title' WHERE id=1`)
		ipPolicyExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('show',1,'title','Owner Title','',1,0,1,'owner','2025-12-01T00:00:00Z')`)
		out, turn, tx := ipEntityAcceptApply(t, w, r, p,
			ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
		if out.Outcome.State != Applied {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Owner Title' AND metadata_language='en' AND overview=''`:                                                                                                   1,
			`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='title' AND value='Owner Title' AND value_present=1 AND locked=1 AND automatic_value='Accepted Series' AND automatic_present=1`: 1,
			`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='' AND locked=1 AND automatic_value='Provider overview'`:                                                1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
	t.Run("locked_empty_overview_stays_empty", func(t *testing.T) {
		w, r, p, staged := ipEntityAcceptFixture(t)
		out, turn, tx := ipEntityAcceptApply(t, w, r, p,
			ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
		if out.Outcome.State != Applied {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Accepted Series' AND overview=''`); got != 1 {
			t.Fatal("locked-empty overview was overwritten", got)
		}
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='' AND value_present=1 AND locked=1 AND automatic_value='Provider overview' AND automatic_present=1`); got != 1 {
			t.Fatal("provider overview not retained as the automatic observation", got)
		}
	})
	t.Run("unlocked_fields_update", func(t *testing.T) {
		w, r, p, staged := ipEntityAcceptFixture(t)
		ipPolicyExec(t, w, `UPDATE metadata_owner_fields SET locked=0 WHERE kind='show' AND entity_id=1 AND field='description'`)
		out, turn, tx := ipEntityAcceptApply(t, w, r, p,
			ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
		if out.Outcome.State != Applied {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND title='Accepted Series' AND overview='Provider overview' AND release_date='2020-01-01' AND year=2020`:                                                                           1,
			`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='Provider overview' AND value_present=1 AND automatic_value='Provider overview' AND automatic_present=1 AND locked=0`: 1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
}

// An UNLOCKED owner row follows the accepted source exactly as the typed
// ReplaceUnlocked preparation defines it: the effective value moves with the
// automatic value, so the row never goes stale against the catalogue projection
// (a stale effective value makes every later editor edit CAS the wrong Before).
// A locked row keeps its effective value and lock; only automatic moves.
func TestIPEntityAcceptUnlockedOwnerRowFollowsAutomatic(t *testing.T) {
	t.Run("unlocked_row_moves_effective_and_automatic", func(t *testing.T) {
		w, r, p, staged := ipEntityAcceptFixture(t)
		ipPolicyExec(t, w, `UPDATE catalog_entities SET overview='Restored A' WHERE id=1`)
		ipPolicyExec(t, w, `UPDATE metadata_owner_fields SET value='Restored A',value_present=1,automatic_value='Restored A',automatic_present=1,locked=0 WHERE kind='show' AND entity_id=1 AND field='description'`)
		out, turn, tx := ipEntityAcceptApply(t, w, r, p,
			ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
		if out.Outcome.State != Applied {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND overview='Provider overview'`: 1,
			`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='Provider overview' AND value_present=1 AND automatic_value='Provider overview' AND automatic_present=1 AND locked=0`: 1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
	t.Run("locked_row_keeps_effective", func(t *testing.T) {
		w, r, p, staged := ipEntityAcceptFixture(t)
		ipPolicyExec(t, w, `UPDATE catalog_entities SET overview='Restored A' WHERE id=1`)
		ipPolicyExec(t, w, `UPDATE metadata_owner_fields SET value='Restored A',value_present=1,automatic_value='Restored A',automatic_present=1,locked=1 WHERE kind='show' AND entity_id=1 AND field='description'`)
		out, turn, tx := ipEntityAcceptApply(t, w, r, p,
			ipEntityAcceptCommandFor(t, staged, ipEntityAcceptRequestFor(staged)))
		if out.Outcome.State != Applied {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND overview='Restored A'`: 1,
			`SELECT count(*) FROM metadata_owner_fields WHERE kind='show' AND entity_id=1 AND field='description' AND value='Restored A' AND value_present=1 AND automatic_value='Provider overview' AND automatic_present=1 AND locked=1`: 1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
}

func TestIPEntityAcceptDescriptors(t *testing.T) {
	expected := Descriptor{ID: IPEntityAccept, Name: "ip_entity_accept", Durability: Normal}
	actual, err := Describe(expected.ID)
	if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
		t.Fatal(actual, err)
	}
}
