package writer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group D is not embedded, so the IP proof tuple, the receipt seal unique and
// the evidence proof index are qualified only against the packet's draft
// subset. The provider operation, subject binding, evidence body/build/children
// and acquisition job are the real embedded owners.
func ipEntitySealFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, time.Time) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	base := w.now()
	for _, name := range []string{
		"testdata/ip-policy-draft.sql",
		"testdata/ip-entity-evidence-draft.sql",
		"testdata/ip-entity-seal-draft.sql",
	} {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, query := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider,command_incarnation) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'tvdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(1,'tmdb','series','tmdb-1','en','US','ip.tmdb.series.detail.v1',X'0c000000000000000000000000000000',1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(query, err)
		}
	}
	defs := append(append(append(ipPolicyDefinitions(), ipEntityJobDefinitions()...),
		ipEntityEvidenceDefinitions()...), ipEntitySealDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{registry, keeper}, p, base
}

// ipEntitySealPages is a small complete build: two alias rows then one term
// row, in exact append order.
func ipEntitySealPages() []ipEntitySealPage {
	return []ipEntitySealPage{
		{Family: "alias", AfterOrdinal: 0, rows: 2, aliases: []ipEvidenceAlias{
			{Ordinal: 0, RequestedID: "requested-0", CanonicalID: "tmdb-1", Name: "Alias 0"},
			{Ordinal: 1, RequestedID: "requested-1", CanonicalID: "tmdb-1", Name: "Alias 1"},
		}},
		{Family: "term", AfterOrdinal: 0, rows: 1, terms: []ipEvidenceTerm{
			{Ordinal: 0, Vocabulary: "genre", Value: "Drama"},
		}},
	}
}

// ipEntitySealDeclarations derives the exact begin/seal manifest from the same
// pages, so family digests are meaningful and the chain replays.
func ipEntitySealDeclarations(pages []ipEntitySealPage) []ipEvidenceFamilyDeclaration {
	out := make([]ipEvidenceFamilyDeclaration, len(ipEvidenceFamilySpecs))
	for family, spec := range ipEvidenceFamilySpecs {
		var payloads []any
		var rows int64
		for _, page := range pages {
			if page.Family != spec.name {
				continue
			}
			payload, _ := page.page(family)
			if payload != nil {
				payloads = append(payloads, payload)
				rows += page.rows
			}
		}
		digest, _ := ipEntitySealFamilyDigest(family, payloads)
		out[family] = ipEvidenceFamilyDeclaration{Name: spec.name, Rows: rows, Digest: digest}
	}
	return out
}

func ipEntitySealDeclaredCounts(declarations []ipEvidenceFamilyDeclaration) map[string]int64 {
	out := make(map[string]int64, len(declarations))
	for _, declaration := range declarations {
		out[declaration.Name] = declaration.Rows
	}
	return out
}

func ipEntitySealPageRequest(owner ipEntityJobResult, begin ipEntityEvidenceResult, page ipEntitySealPage,
	familyRows, familyStart, materializedBefore int64) ipEntityEvidenceAppendRequest {
	family := int64(ipEvidenceFamilyIndex(page.Family))
	q := ipEntityEvidenceAppendRequest{operationID: begin.OperationID, provider: begin.Provider,
		purpose: "metadata", selectedID: owner.SelectedID, lease: begin.Lease, entityID: owner.EntityID,
		generation: 1, leaseUntilMS: begin.LeaseUntilMS, evidenceID: begin.EvidenceID,
		subjectID: begin.SubjectID, subjectKind: begin.SubjectKind, providerID: begin.ProviderID,
		parserVersion: begin.ParserVersion, observedAt: "2026-01-01T00:00:00Z", buildID: begin.BuildID,
		buildGeneration: begin.BuildGeneration, buildRevision: begin.Revision, family: page.Family,
		familyIndex: family, familyStart: familyStart, familyRows: familyRows,
		afterOrdinal: page.AfterOrdinal, materializedBefore: materializedBefore}
	switch family {
	case 0:
		q.aliases = page.aliases
	case 1:
		q.external = page.external
	case 2:
		q.terms = page.terms
	case 3:
		q.ratings = page.ratings
	case 4:
		q.credits = page.credits
	case 5:
		q.episodes = page.episodes
	case 6:
		q.media = page.media
	default:
		q.tracks = page.tracks
	}
	return q
}

type ipEntitySealStaged struct {
	owner        ipEntityJobResult
	claim        CommandIdentity
	pages        []ipEntitySealPage
	declarations []ipEvidenceFamilyDeclaration
	begin        ipEntityEvidenceResult
	seal         ipEntitySealRequest
	sequence     int64
}

// ipEntitySealStage creates one complete IP-06 build and the matching complete
// seal request at the next owner sequence.
func ipEntitySealStage(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) ipEntitySealStaged {
	t.Helper()
	owner, claim := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
	pages := ipEntitySealPages()
	declarations := ipEntitySealDeclarations(pages)
	counts := ipEntitySealDeclaredCounts(declarations)
	bq := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", counts)
	bq.families = declarations
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p,
		mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), bq))
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityEvidenceAck(t, w, first.Value, 2)

	current, familyStart, before := first.Value, int64(1), int64(1)
	sequence := int64(3)
	for _, page := range pages {
		request := ipEntitySealPageRequest(owner, current, page, counts[page.Family], familyStart, before)
		out, turn, tx := ipEntityEvidenceApplyAppend(t, w, r, p,
			mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, sequence), request))
		ipPolicyCommitTurn(t, turn, tx)
		ipEntityEvidenceAck(t, w, out.Value, sequence)
		current = out.Value
		before += page.rows
		familyStart = before
		sequence++
	}
	return ipEntitySealStaged{owner: owner, claim: claim, pages: pages, declarations: declarations,
		begin: current, sequence: sequence, seal: ipEntitySealRequestFor(owner, current, declarations, pages)}
}

func ipEntitySealRequestFor(owner ipEntityJobResult, build ipEntityEvidenceResult,
	declarations []ipEvidenceFamilyDeclaration, pages []ipEntitySealPage) ipEntitySealRequest {
	return ipEntitySealRequest{operationID: owner.OperationID, provider: owner.Provider,
		purpose: "metadata", selectedID: owner.SelectedID, lease: owner.Lease, entityID: owner.EntityID,
		generation: 1, leaseUntilMS: owner.LeaseUntilMS, evidenceID: build.EvidenceID,
		buildID: build.BuildID, buildGeneration: build.BuildGeneration, contentRevision: build.Revision,
		subjectID: build.SubjectID, subjectKind: build.SubjectKind, providerID: build.ProviderID,
		parserVersion: build.ParserVersion, language: "en", region: "US",
		requestShape: "ip.tmdb.series.detail.v1", observedAt: "2026-01-01T00:00:00Z",
		observationDigest: ipEntityEvidenceDigest(build.EvidenceID),
		families:          declarations, pages: pages}
}

func ipEntitySealCommandFor(t *testing.T, staged ipEntitySealStaged) *ipEntitySealCommand {
	t.Helper()
	c, err := newIPEntitySealCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence), staged.seal)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipEntitySealApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipEntitySealCommand) (ipEntitySealCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
	out, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("seal", out, err)
	}
	return out, turn, tx
}

func TestIPEntitySealFreshReplayRetiredAndRetainedProof(t *testing.T) {
	w, r, p, _ := ipEntitySealFixture(t)
	staged := ipEntitySealStage(t, w, r, p)
	stage := staged.seal
	out, turn, tx := ipEntitySealApply(t, w, r, p, ipEntitySealCommandFor(t, staged))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Digest == (ResultDigest{}) ||
		out.Value.OperationID != stage.operationID || out.Value.EvidenceID != "evidence-one" ||
		out.Value.BuildID != "build-one" || out.Value.Provider != "tmdb" ||
		out.Value.SubjectKind != "series" || out.Value.ProviderID != "tmdb-1" ||
		out.Value.ParserVersion != "ip.tmdb.series.v1" || out.Value.Language != "en" ||
		out.Value.Region != "US" || out.Value.RequestShape != "ip.tmdb.series.detail.v1" ||
		out.Value.SubjectID != 1 || out.Value.BuildGeneration != 1 || out.Value.OwnerGeneration != 1 ||
		out.Value.ContentRevision != staged.begin.Revision+1 || out.Value.SealVersion != ipSealVersion ||
		out.Value.SealRows != 4 || out.Value.SealBytes <= 0 ||
		out.Value.Commitment == ([32]byte{}) || out.Value.ObservationDigest != stage.observationDigest ||
		out.Value.LeaseUntilMS != staged.owner.LeaseUntilMS || out.Value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	commitment := ipPolicyString(t, w, `SELECT materialized_digest FROM metadata_evidence_builds WHERE id='build-one'`)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_evidence_builds WHERE id='build-one' AND state='ready' AND phase='sealed' AND cursor=4 AND expected_rows=4 AND materialized_rows=4 AND content_revision=4`:                        1,
		`SELECT count(*) FROM metadata_evidence WHERE id='evidence-one' AND sealed=1 AND seal_proof_owner IS NULL AND ip_seal_owner='` + stage.operationID + `' AND ip_seal_sequence=5 AND ip_seal_kind='evidence_seal'`: 1,
		`SELECT length(ip_seal_incarnation) FROM metadata_evidence WHERE id='evidence-one'`:                                                                                                                              16,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND terminal=1 AND result_phase='sealed' AND evidence_id='evidence-one' AND evidence_subject_id=1 AND parser_version='ip.tmdb.series.v1' AND build_id='build-one' AND seal_algorithm='` + ipSealAlgorithm + `' AND seal_version=1 AND seal_rows=4 AND seal_bytes=` + ipPolicyString(t, w, `SELECT seal_bytes FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`) + ` AND proof_pin_count=1`: 1,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + stage.operationID + `'`: 5,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	stored := ipPolicyString(t, w, `SELECT seal_commitment FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`)
	if stored != commitment || stored == "" {
		t.Fatal("commitment not stored", stored, commitment)
	}

	// Replay after unrelated source, lease and policy changes returns the
	// original saved seal before any fresh probe and writes nothing.
	original := out
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending',generation=9,revision=9,lease='other',lease_until_ms=1000000 WHERE entity_id=1`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=5,revision=5 WHERE id=1`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
	replay, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, ipEntitySealCommandFor(t, staged))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("seal replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for _, query := range []string{
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`,
		`SELECT count(*) FROM metadata_evidence WHERE sealed=1`,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != 1 {
			t.Fatal("seal replay wrote rows", query, got)
		}
	}

	// At or below ACK is Retired. The terminal source proof and its downstream
	// pin stay physically retained.
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: stage.operationID, Provider: stage.provider}, 5)
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntitySeal)
	retired, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, ipEntitySealCommandFor(t, staged))
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipEntitySealResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired seal served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND terminal=1 AND proof_pin_count=1`: 1,
		`SELECT count(*) FROM metadata_evidence WHERE sealed=1 AND ip_seal_sequence=5`:                                      1,
		`SELECT count(*) FROM metadata_evidence_builds WHERE state='ready' AND phase='sealed'`:                              1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("retired seal lost its proof", query, got)
		}
	}
}

func TestIPEntitySealShapeRefusals(t *testing.T) {
	w, r, p, _ := ipEntitySealFixture(t)
	staged := ipEntitySealStage(t, w, r, p)
	base := staged.seal
	identity := ipEntityEvidenceIdentity(staged.claim, staged.sequence)
	longID := strings.Repeat("x", ipEvidenceIDMax+1)
	longText := strings.Repeat("x", ipEvidenceTextMax+1)
	mutations := []struct {
		name   string
		mutate func(*ipEntitySealRequest)
	}{
		{"empty_evidence", func(q *ipEntitySealRequest) { q.evidenceID = "" }},
		{"long_evidence", func(q *ipEntitySealRequest) { q.evidenceID = longID }},
		{"empty_build", func(q *ipEntitySealRequest) { q.buildID = "" }},
		{"long_build", func(q *ipEntitySealRequest) { q.buildID = longID }},
		{"zero_build_generation", func(q *ipEntitySealRequest) { q.buildGeneration = 0 }},
		{"zero_content_revision", func(q *ipEntitySealRequest) { q.contentRevision = 0 }},
		{"movie_subject", func(q *ipEntitySealRequest) { q.subjectKind = "movie" }},
		{"person_subject", func(q *ipEntitySealRequest) { q.subjectKind = "person" }},
		{"empty_provider_id", func(q *ipEntitySealRequest) { q.providerID = "" }},
		{"parser_without_ip_namespace", func(q *ipEntitySealRequest) { q.parserVersion = "series.v1" }},
		{"request_without_ip_namespace", func(q *ipEntitySealRequest) { q.requestShape = "series.detail.v1" }},
		{"parser_equals_request", func(q *ipEntitySealRequest) {
			q.parserVersion, q.requestShape = "ip.tmdb.series.v1", "ip.tmdb.series.v1"
		}},
		{"parser_provider_mismatch", func(q *ipEntitySealRequest) { q.parserVersion = "ip.tvdb.series.v1" }},
		{"empty_language", func(q *ipEntitySealRequest) { q.language = "" }},
		{"empty_region", func(q *ipEntitySealRequest) { q.region = "" }},
		{"long_language", func(q *ipEntitySealRequest) { q.language = longText }},
		{"empty_observed_at", func(q *ipEntitySealRequest) { q.observedAt = "" }},
		{"bad_observed_at", func(q *ipEntitySealRequest) { q.observedAt = "tomorrow" }},
		{"zero_observation_digest", func(q *ipEntitySealRequest) { q.observationDigest = [32]byte{} }},
		{"short_families", func(q *ipEntitySealRequest) { q.families = q.families[:len(q.families)-1] }},
		{"wrong_family_name", func(q *ipEntitySealRequest) { q.families[0].Name = "aliases" }},
		{"negative_family_rows", func(q *ipEntitySealRequest) { q.families[0].Rows = -1 }},
		{"over_family_max", func(q *ipEntitySealRequest) {
			q.families[ipEvidenceFamilyIndex("rating")].Rows = int64(ipEvidenceFamilySpecs[ipEvidenceFamilyIndex("rating")].maxRows) + 1
		}},
		{"wrong_family_digest", func(q *ipEntitySealRequest) { q.families[0].Digest = [32]byte{0xee} }},
		{"unknown_page_family", func(q *ipEntitySealRequest) { q.pages[0].Family = "not-a-family" }},
		{"mixed_page_family", func(q *ipEntitySealRequest) {
			q.pages[0].terms = []ipEvidenceTerm{{Ordinal: 0, Vocabulary: "genre", Value: "Drama"}}
		}},
		{"page_ordinal_gap", func(q *ipEntitySealRequest) { q.pages[0].AfterOrdinal = 1 }},
		{"page_rows_mismatch", func(q *ipEntitySealRequest) { q.pages[0].rows = 1 }},
		{"page_duplicate", func(q *ipEntitySealRequest) {
			duplicate := q.pages[0]
			duplicate.AfterOrdinal = 2
			q.pages = append(q.pages, duplicate)
		}},
		{"page_beyond_declared", func(q *ipEntitySealRequest) {
			extra := ipEntitySealPage{Family: "alias", AfterOrdinal: 2, rows: 1,
				aliases: []ipEvidenceAlias{{Ordinal: 2, RequestedID: "requested-2", CanonicalID: "tmdb-1", Name: "Alias 2"}}}
			q.pages = append(q.pages, extra)
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			q.families = append([]ipEvidenceFamilyDeclaration(nil), base.families...)
			q.pages = append([]ipEntitySealPage(nil), base.pages...)
			tc.mutate(&q)
			if c, err := newIPEntitySealCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	// A header-only complete build (all declared families empty) is legal.
	headerOnly := base
	headerOnly.pages = nil
	headerOnly.families = ipEntitySealDeclarations(nil)
	if c, err := newIPEntitySealCommand(identity, headerOnly); err != nil || c == nil {
		t.Fatal("header-only manifest refused", err)
	}
}

// One retained sequence may serve only its ORIGINAL request and operation. A
// different request or a different command operation at the same sequence is a
// conflict, never a second seal.
func TestIPEntitySealConflictingRequestAndSequence(t *testing.T) {
	w, r, p, _ := ipEntitySealFixture(t)
	staged := ipEntitySealStage(t, w, r, p)
	out, turn, tx := ipEntitySealApply(t, w, r, p, ipEntitySealCommandFor(t, staged))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)

	changed := staged.seal
	changed.observedAt = "2026-01-02T00:00:00Z"
	conflicting, err := newIPEntitySealCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence), changed)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
	denied, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipEntitySealCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	otherIdentity := ipEntityEvidenceIdentity(staged.claim, staged.sequence)
	otherIdentity.OperationID = [16]byte{0x6b, 0xff}
	other, err := newIPEntitySealCommand(otherIdentity, staged.seal)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntitySeal)
	denied, err = applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, other)
	if denied != (ipEntitySealCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting operation admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`); got != 1 {
		t.Fatal("conflicting seal wrote a second receipt", got)
	}
}

func TestIPEntitySealRejectsIncompleteAndForeignBuild(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipEntitySealRequest)
		sql    []string
		want   error
	}{
		{"missing_child", nil, []string{`DELETE FROM metadata_evidence_aliases WHERE evidence_id='evidence-one' AND ordinal=1`}, ErrConflict},
		{"extra_child", nil, []string{`INSERT INTO metadata_evidence_aliases(evidence_id,ordinal,requested_id,canonical_id,alias,observed_at) VALUES('evidence-one',2,'extra','tmdb-1','Extra','2026-01-01T00:00:00Z')`}, ErrConflict},
		{"duplicate_page", func(q *ipEntitySealRequest) {
			duplicate := q.pages[0]
			duplicate.AfterOrdinal = 2
			q.pages = []ipEntitySealPage{q.pages[0], duplicate, q.pages[1]}
			q.families = ipEntitySealDeclarations(q.pages)
		}, nil, ErrConflict},
		{"wrong_parser", func(q *ipEntitySealRequest) { q.parserVersion = "ip.tmdb.series.v2" }, nil, ErrConflict},
		{"wrong_language", func(q *ipEntitySealRequest) { q.language = "fr" }, nil, ErrConflict},
		{"wrong_region", func(q *ipEntitySealRequest) { q.region = "GB" }, nil, ErrConflict},
		{"wrong_request_shape", func(q *ipEntitySealRequest) { q.requestShape = "ip.tmdb.series.other.v1" }, nil, ErrConflict},
		{"wrong_observed_at", func(q *ipEntitySealRequest) { q.observedAt = "2026-01-02T00:00:00Z" }, nil, ErrConflict},
		{"wrong_observation_digest", func(q *ipEntitySealRequest) {
			q.observationDigest = ipEntityEvidenceDigest("other")
		}, nil, ErrConflict},
		{"wrong_evidence", func(q *ipEntitySealRequest) { q.evidenceID = "evidence-two" }, nil, ErrConflict},
		{"wrong_build", func(q *ipEntitySealRequest) { q.buildID = "build-two" }, nil, ErrConflict},
		{"wrong_build_generation", func(q *ipEntitySealRequest) { q.buildGeneration = 2 }, nil, ErrConflict},
		{"wrong_content_revision", func(q *ipEntitySealRequest) { q.contentRevision++ }, nil, ErrConflict},
		{"wrong_provider_id", func(q *ipEntitySealRequest) { q.providerID = "tmdb-9" }, nil, ErrConflict},
		{"wrong_subject_kind", func(q *ipEntitySealRequest) { q.subjectKind = "season" }, nil, ErrConflict},
		{"wrong_subject_id", func(q *ipEntitySealRequest) { q.subjectID = 2 }, nil, ErrConflict},
		{"tampered_chain", func(q *ipEntitySealRequest) {
			q.pages = append([]ipEntitySealPage(nil), q.pages...)
			q.pages[0].aliases = append([]ipEvidenceAlias(nil), q.pages[0].aliases...)
			q.pages[0].aliases[0].Name = "Tampered"
			q.families = ipEntitySealDeclarations(q.pages)
		}, nil, ErrConflict},
		{"expired_build", nil, []string{`UPDATE metadata_evidence_builds SET expires_at='2020-01-01T00:00:00Z' WHERE id='build-one'`}, ErrConflict},
		{"sealed_evidence", nil, []string{`UPDATE metadata_evidence SET sealed=1 WHERE id='evidence-one'`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntitySealFixture(t)
			staged := ipEntitySealStage(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := staged.seal
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			c, err := newIPEntitySealCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
			out, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, c)
			if out != (ipEntitySealCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("incomplete/foreign seal admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`:                               0,
				`SELECT count(*) FROM metadata_evidence WHERE ip_seal_owner IS NOT NULL`:                                     0,
				`SELECT count(*) FROM metadata_evidence_builds WHERE state='ready'`:                                          0,
				`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + staged.owner.OperationID + `'`: staged.sequence - 1,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal("refused seal wrote state", query, got)
				}
			}
		})
	}
}

func TestIPEntitySealCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, _ := ipEntitySealFixture(t)
			staged := ipEntitySealStage(t, w, r, p)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
			out, err := applyIPEntitySeal(t.Context(), turn, proof, t.Context(), b, ipEntitySealCommandFor(t, staged))
			if out != (ipEntitySealCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, _ := ipEntitySealFixture(t)
		staged := ipEntitySealStage(t, w, r, p)
		lifetime, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
		out, err := applyIPEntitySeal(lifetime, turn, p, t.Context(), b, ipEntitySealCommandFor(t, staged))
		if out != (ipEntitySealCommandResult{}) || err == nil {
			t.Fatal("cancelled lifetime admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPEntitySealUnknownCommitFreshRead(t *testing.T) {
	w, r, p, _ := ipEntitySealFixture(t)
	staged := ipEntitySealStage(t, w, r, p)
	c := ipEntitySealCommandFor(t, staged)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
	out, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, c)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	// The uncertain frame is closed; it never reserves the saved result.
	if again, e := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, c); again != (ipEntitySealCommandResult{}) || e == nil {
		t.Fatal("uncertain seal frame reserved", again, e)
	}
	// Only a FRESH read-only connection may prove the durable outcome.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND terminal=1 AND result_phase='sealed' AND proof_pin_count=1`: 1,
		`SELECT count(*) FROM metadata_evidence WHERE id='evidence-one' AND sealed=1 AND ip_seal_sequence=5`:                                          1,
		`SELECT count(*) FROM metadata_evidence_builds WHERE id='build-one' AND state='ready' AND phase='sealed'`:                                     1,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + staged.owner.OperationID + `'`:                                  5,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal("durable seal", query, got)
		}
	}
}

func TestIPEntitySealStaleOwner(t *testing.T) {
	cases := []struct {
		name string
		sql  []string
		want error
	}{
		{"new_observation", []string{`UPDATE catalog_entities SET metadata_revision=2,revision=2 WHERE id=1`}, ErrConflict},
		{"new_source_revision", []string{`UPDATE catalog_entities SET source_revision=2 WHERE id=1`}, ErrConflict},
		{"policy_advanced", []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"consent_advanced", []string{`UPDATE screen_metadata_consent SET revision=2 WHERE singleton=1`}, ErrDisclosureDenied},
		{"owner_not_claimed", []string{`UPDATE metadata_provider_operations SET status='staged' WHERE provider='tmdb'`}, ErrConflict},
		{"lease_expired", []string{`UPDATE metadata_provider_operations SET lease_until='2020-01-01T00:00:00Z' WHERE provider='tmdb'`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntitySealFixture(t)
			staged := ipEntitySealStage(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
			out, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, ipEntitySealCommandFor(t, staged))
			if out != (ipEntitySealCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale owner admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`); got != 0 {
				t.Fatal("stale seal wrote a receipt", got)
			}
		})
	}
	t.Run("expired_lease_at_seal_time", func(t *testing.T) {
		w, r, p, base := ipEntitySealFixture(t)
		staged := ipEntitySealStage(t, w, r, p)
		ipEntityJobAdvance(t, w, base, 6*time.Minute)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
		out, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, ipEntitySealCommandFor(t, staged))
		if out != (ipEntitySealCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("expired seal lease admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPEntitySealDescriptors(t *testing.T) {
	expected := Descriptor{ID: IPEntitySeal, Name: "ip_entity_seal", Durability: Normal}
	actual, err := Describe(expected.ID)
	if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
		t.Fatal(actual, err)
	}
}
