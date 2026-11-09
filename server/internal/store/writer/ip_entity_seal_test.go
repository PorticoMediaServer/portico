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
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(1,'tmdb','series','tmdb-1','en','US','ip.tmdb.series.evidence.detail+recordings+media+tracks+credits.v2',X'0c000000000000000000000000000000',1)`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(2,'tmdb','release','tmdb-1','en','US','ip.tmdb.release.evidence.detail+recordings+media+tracks+credits.v2',X'0d000000000000000000000000000000',1)`,
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

func ipEntityEvidenceParser(provider, kind string) string {
	parser, _ := ipEntityEvidencePair(provider, kind)
	return parser
}

// ipEntitySealTamperPair rewrites the recorded parser/request pair of one
// evidence row to model a foreign or superseded build. The composite subject FK
// admits only a request shape that the referenced metadata_provider_subjects row
// also carries, so both rows move together in one transaction with deferred
// enforcement; the committed state is still internally coherent.
func ipEntitySealTamperPair(t *testing.T, w *executor, evidenceID, parser, shape string) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `PRAGMA defer_foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	tx, err := w.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(),
		`UPDATE metadata_provider_subjects SET request_shape=? WHERE id=(SELECT subject_id FROM metadata_evidence WHERE id=?)`,
		shape, evidenceID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(),
		`UPDATE metadata_evidence SET parser_version=?,request_shape=? WHERE id=?`, parser, shape,
		evidenceID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `PRAGMA defer_foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
}

func ipEntityEvidenceRequest(provider, kind string) string {
	_, request := ipEntityEvidencePair(provider, kind)
	return request
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
		q.episodes = page.episodes
	case 5:
		q.media = page.media
	case 6:
		q.tracks = page.tracks
	default:
		q.credits = page.credits
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

	current, before := first.Value, int64(1)
	sequence := int64(3)
	for _, page := range pages {
		// familyStart is the global cursor position where this family started,
		// so it is distinct from the family's own after-ordinal once a family
		// spans more than one page.
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
	return ipEntitySealStaged{owner: owner, claim: claim, pages: pages, declarations: declarations,
		begin: current, sequence: sequence, seal: ipEntitySealRequestFor(owner, current, declarations, pages)}
}

func ipEntitySealRequestFor(owner ipEntityJobResult, build ipEntityEvidenceResult,
	declarations []ipEvidenceFamilyDeclaration, pages []ipEntitySealPage) ipEntitySealRequest {
	_, shape := ipEntityEvidencePair(build.Provider, build.SubjectKind)
	return ipEntitySealRequest{operationID: owner.OperationID, provider: owner.Provider,
		purpose: "metadata", selectedID: owner.SelectedID, lease: owner.Lease, entityID: owner.EntityID,
		generation: 1, leaseUntilMS: owner.LeaseUntilMS, evidenceID: build.EvidenceID,
		buildID: build.BuildID, buildGeneration: build.BuildGeneration, contentRevision: build.Revision,
		subjectID: build.SubjectID, subjectKind: build.SubjectKind, providerID: build.ProviderID,
		parserVersion: build.ParserVersion, language: "en", region: "US",
		requestShape: shape, observedAt: "2026-01-01T00:00:00Z",
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
		out.Value.ParserVersion != ipEntityEvidenceParser("tmdb", "series") || out.Value.Language != "en" ||
		out.Value.Region != "US" || out.Value.RequestShape != ipEntityEvidenceRequest("tmdb", "series") ||
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
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND terminal=1 AND result_phase='sealed' AND evidence_id='evidence-one' AND evidence_subject_id=1 AND parser_version='` + ipEntityEvidenceParser("tmdb", "series") + `' AND build_id='build-one' AND seal_algorithm='` + ipSealAlgorithm + `' AND seal_version=2 AND seal_rows=4 AND seal_bytes=` + ipPolicyString(t, w, `SELECT seal_bytes FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`) + ` AND proof_pin_count=1`: 1,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + stage.operationID + `'`: 5,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	stored := ipPolicyString(t, w, `SELECT lower(hex(seal_commitment)) FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`)
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
		{"old_order_pair", func(q *ipEntitySealRequest) {
			q.parserVersion = "ip.tmdb.series.header-complete-children.v1"
			q.requestShape = "ip.tmdb.series.detail+artist-credits+recordings+release-groups.v1"
		}},
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
		tamper func(*testing.T, *executor)
		want   error
	}{
		{"missing_child", nil, []string{`DELETE FROM metadata_evidence_aliases WHERE evidence_id='evidence-one' AND ordinal=1`}, nil, ErrConflict},
		{"extra_child", nil, []string{`INSERT INTO metadata_evidence_aliases(evidence_id,ordinal,requested_id,canonical_id,alias,observed_at) VALUES('evidence-one',2,'extra','tmdb-1','Extra','2026-01-01T00:00:00Z')`}, nil, ErrConflict},
		{"duplicate_page", func(q *ipEntitySealRequest) {
			// Replay the alias page as a coherent second page at the next
			// ordinals: the manifest then declares four alias rows while the
			// build persisted two, which only the fresh count refuses.
			duplicate := q.pages[0]
			duplicate.AfterOrdinal += duplicate.rows
			duplicate.aliases = append([]ipEvidenceAlias(nil), duplicate.aliases...)
			for i := range duplicate.aliases {
				duplicate.aliases[i].Ordinal = duplicate.AfterOrdinal + int64(i)
			}
			q.pages = []ipEntitySealPage{q.pages[0], duplicate, q.pages[1]}
			q.families = ipEntitySealDeclarations(q.pages)
		}, nil, nil, ErrConflict},
		{"wrong_parser", nil, []string{`UPDATE metadata_evidence SET parser_version='ip.tmdb.series.evidence.complete-ordered-families.v3' WHERE id='evidence-one'`}, nil, ErrConflict},
		{"wrong_language", func(q *ipEntitySealRequest) { q.language = "fr" }, nil, nil, ErrConflict},
		{"wrong_region", func(q *ipEntitySealRequest) { q.region = "GB" }, nil, nil, ErrConflict},
		// The composite subject FK admits only a request_shape that the subject
		// row also carries, so the foreign shape is written to both rows.
		{"wrong_request_shape", nil, nil, func(t *testing.T, w *executor) {
			ipEntitySealTamperPair(t, w, "evidence-one", ipEntityEvidenceParser("tmdb", "series"),
				"ip.tmdb.series.other.v1")
		}, ErrConflict},
		{"wrong_observed_at", func(q *ipEntitySealRequest) { q.observedAt = "2026-01-02T00:00:00Z" }, nil, nil, ErrConflict},
		{"wrong_observation_digest", func(q *ipEntitySealRequest) {
			q.observationDigest = ipEntityEvidenceDigest("other")
		}, nil, nil, ErrConflict},
		{"wrong_evidence", func(q *ipEntitySealRequest) { q.evidenceID = "evidence-two" }, nil, nil, ErrConflict},
		{"wrong_build", func(q *ipEntitySealRequest) { q.buildID = "build-two" }, nil, nil, ErrConflict},
		{"wrong_build_generation", func(q *ipEntitySealRequest) { q.buildGeneration = 2 }, nil, nil, ErrConflict},
		{"wrong_content_revision", func(q *ipEntitySealRequest) { q.contentRevision++ }, nil, nil, ErrConflict},
		{"wrong_provider_id", func(q *ipEntitySealRequest) { q.providerID = "tmdb-9" }, nil, nil, ErrConflict},
		{"wrong_subject_kind", func(q *ipEntitySealRequest) {
			// A coherent season request: its own current pair, so the shape is
			// admitted and only the recorded series build can refuse it.
			q.subjectKind = "season"
			q.parserVersion, q.requestShape = ipEntityEvidencePair("tmdb", "season")
		}, nil, nil, ErrConflict},
		{"wrong_subject_id", func(q *ipEntitySealRequest) { q.subjectID = 2 }, nil, nil, ErrConflict},
		{"tampered_chain", func(q *ipEntitySealRequest) {
			q.pages = append([]ipEntitySealPage(nil), q.pages...)
			q.pages[0].aliases = append([]ipEvidenceAlias(nil), q.pages[0].aliases...)
			q.pages[0].aliases[0].Name = "Tampered"
			q.families = ipEntitySealDeclarations(q.pages)
		}, nil, nil, ErrConflict},
		{"expired_build", nil, []string{`UPDATE metadata_evidence_builds SET expires_at='2020-01-01T00:00:00Z' WHERE id='build-one'`}, nil, ErrConflict},
		{"sealed_evidence", nil, []string{`UPDATE metadata_evidence SET sealed=1 WHERE id='evidence-one'`}, nil, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntitySealFixture(t)
			staged := ipEntitySealStage(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			if tc.tamper != nil {
				tc.tamper(t, w)
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

// ipEntitySealSeedMusicBrainz seeds the release and recording provider subjects
// the corrected release shape stages. The writer is provider-agnostic; these
// rows only satisfy the embedded composite subject FKs.
func ipEntitySealSeedMusicBrainz(t *testing.T, w *executor) {
	t.Helper()
	incarnation := make([]byte, 16)
	incarnation[0] = 0x2a
	for _, row := range []struct {
		id         int64
		kind       string
		providerID string
	}{
		{20, "release", "musicbrainz-1"},
		{21, "recording", "rec-1"},
		{22, "recording", "rec-2"},
		{23, "recording", "rec-3"},
		{24, "recording", "rec-4"},
		{25, "recording", "rec-foreign"},
		{26, "release", "musicbrainz-2"},
	} {
		_, shape := ipEntityEvidencePair("musicbrainz", row.kind)
		ipPolicyExec(t, w, `INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(?, 'musicbrainz', ?, ?, 'en', 'US', ?, ?, 1)`,
			row.id, row.kind, row.providerID, shape, incarnation)
	}
}

// ipEntitySealStageCustom stages one complete IP-06 build from an explicit header
// and page list under an already claimed owner, starting at startSequence. It
// returns the staged build and the next free owner sequence.
func ipEntitySealStageCustom(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	owner ipEntityJobResult, claim CommandIdentity, evidenceID, buildID string,
	header EvidenceHeaderInput, pages []ipEntitySealPage, startSequence int64) (ipEntitySealStaged, int64) {
	t.Helper()
	declarations := ipEntitySealDeclarations(pages)
	counts := ipEntitySealDeclaredCounts(declarations)
	bq := ipEntityEvidenceBeginFor(owner, evidenceID, buildID, counts)
	bq.header = header
	bq.families = declarations
	sequence := startSequence
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p,
		mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, sequence), bq))
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityEvidenceAck(t, w, first.Value, sequence)
	sequence++
	current, before := first.Value, int64(1)
	for _, page := range pages {
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
	return ipEntitySealStaged{owner: owner, claim: claim, pages: pages, declarations: declarations,
		begin: current, sequence: sequence,
		seal: ipEntitySealRequestFor(owner, current, declarations, pages)}, sequence
}

// A medium title and format are the provider's exact strings. Each empty
// combination is admitted and sealed unchanged; malformed and oversize rows are
// still refused at admission and at seal.
func TestIPEntitySealMediaEmptyTitleAndFormat(t *testing.T) {
	for _, tc := range []struct{ name, format, title string }{
		{"title_only_empty", "CD", ""},
		{"format_only_empty", "", "Disc One"},
		{"both_empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntitySealFixture(t)
			owner, claim := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
			page := ipEntitySealPage{Family: "media", AfterOrdinal: 0, rows: 1,
				media: []ipEvidenceMedia{{Position: 1, Format: tc.format, Title: tc.title}}}
			staged, _ := ipEntitySealStageCustom(t, w, r, p, owner, claim, "evidence-one", "build-one",
				ipEntityEvidenceReleaseHeader("evidence-one", "tmdb", "tmdb-1", 2),
				[]ipEntitySealPage{page}, 2)
			out, turn, tx := ipEntitySealApply(t, w, r, p, ipEntitySealCommandFor(t, staged))
			if out.Outcome.State != Applied || out.Value.SealRows != 2 {
				t.Fatal(out)
			}
			ipPolicyCommitTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w,
				`SELECT count(*) FROM metadata_evidence_media WHERE evidence_id='evidence-one' AND position=1 AND format=? AND title=?`,
				tc.format, tc.title); got != 1 {
				t.Fatal("exact provider medium strings not preserved", got)
			}
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence WHERE id='evidence-one' AND sealed=1`); got != 1 {
				t.Fatal("sealed medium build", got)
			}
		})
	}
	// Malformed and oversize pages are refused at seal shape, never repaired.
	for _, tc := range []struct {
		name string
		row  ipEvidenceMedia
	}{
		{"invalid_utf8", ipEvidenceMedia{Position: 1, Format: "CD", Title: "\xff"}},
		{"nul_format", ipEvidenceMedia{Position: 1, Format: "C\x00D", Title: "Disc"}},
		{"oversize_title", ipEvidenceMedia{Position: 1, Format: "CD", Title: strings.Repeat("t", ipEvidenceTextMax+1)}},
		{"zero_position", ipEvidenceMedia{Position: 0, Format: "CD", Title: "Disc"}},
	} {
		t.Run("refuse_"+tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntitySealFixture(t)
			owner, claim := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
			pages := []ipEntitySealPage{{Family: "media", AfterOrdinal: 0, rows: 1,
				media: []ipEvidenceMedia{tc.row}}}
			declarations := ipEntitySealDeclarations(pages)
			build := ipEntityEvidenceResult{EvidenceID: "evidence-one", BuildID: "build-one",
				BuildGeneration: 1, SubjectID: 2, SubjectKind: "release", Provider: "tmdb",
				ProviderID: "tmdb-1", ParserVersion: ipEntityEvidenceParser("tmdb", "release"), Revision: 1}
			if c, err := newIPEntitySealCommand(ipEntityEvidenceIdentity(claim, 3),
				ipEntitySealRequestFor(owner, build, declarations, pages)); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed medium seal admitted", c, err)
			}
			_ = w
			_ = r
			_ = p
		})
	}
}

// One release with media, four tracks across two media and both release- and
// track-scoped credits appends and seals with immediate foreign keys ON. The
// complete artist identities and join phrases are preserved beside the joined
// track display, the family append cursor stays distinct from the source credit
// ordinal, and nonexistent or foreign-release tracks are refused.
func TestIPEntitySealReleaseTrackCreditsFKOn(t *testing.T) {
	w, r, p, _ := ipEntitySealFixture(t)
	ipEntitySealSeedMusicBrainz(t, w)
	owner, claim := ipEntityJobClaimed(t, w, r, p, 1, "musicbrainz")

	recording := func(evidenceID, providerID string, subjectID int64) EvidenceHeaderInput {
		return ipEntityEvidenceRecordingHeader(evidenceID, "musicbrainz", providerID, subjectID)
	}
	mediaPage := ipEntitySealPage{Family: "media", AfterOrdinal: 0, rows: 2, media: []ipEvidenceMedia{
		{Position: 1, Format: "CD", Title: ""},
		{Position: 2, Format: "Digital Media", Title: "Disc Two"},
	}}
	trackPage := ipEntitySealPage{Family: "track", AfterOrdinal: 0, rows: 4, tracks: []ipEvidenceTrack{
		{TrackID: "track-a1", MediumPosition: 1, Position: 1, Number: "A1", Title: "First Song",
			Artist: "Artist Two feat. Artist Three", RecordingEvidenceID: "rec-evidence-1",
			Recording: recording("rec-evidence-1", "rec-1", 21)},
		{TrackID: "track-a2", MediumPosition: 1, Position: 2, Number: "A2", Title: "Second Song",
			Artist: "Artist One", RecordingEvidenceID: "rec-evidence-2",
			Recording: recording("rec-evidence-2", "rec-2", 22)},
		{TrackID: "track-b1", MediumPosition: 2, Position: 1, Number: "B1", Title: "Third Song",
			Artist: "Artist One", RecordingEvidenceID: "rec-evidence-3",
			Recording: recording("rec-evidence-3", "rec-3", 23)},
		{TrackID: "track-b2", MediumPosition: 2, Position: 2, Number: "B2", Title: "Fourth Song",
			Artist: "Artist Four", RecordingEvidenceID: "rec-evidence-4",
			Recording: recording("rec-evidence-4", "rec-4", 24)},
	}}
	creditPageOne := ipEntitySealPage{Family: "credit", AfterOrdinal: 0, rows: 3, credits: []ipEvidenceCredit{
		{Ordinal: 0, Class: "artist", ProviderPersonID: "artist-1", Name: "Artist One",
			ArtistName: "Artist One", SortName: "One, Artist", JoinPhrase: "", Role: "primary",
			CreditID: "rel-credit-1", SourceOrdinal: 0, SourceOrdinalPresent: true},
		{Ordinal: 1, Class: "artist", Scope: "track-a1", ProviderPersonID: "artist-2",
			Name: "Artist Two", ArtistName: "Artist Two", SortName: "Two, Artist",
			Disambiguation: "singer", JoinPhrase: " feat. ", Role: "featured",
			CreditID: "trk-credit-1", SourceOrdinal: 1, SourceOrdinalPresent: true},
		{Ordinal: 2, Class: "artist", Scope: "track-a1", ProviderPersonID: "artist-3",
			Name: "Artist Three", ArtistName: "Artist Three", SortName: "Three, Artist",
			JoinPhrase: "", Role: "featured", CreditID: "trk-credit-2",
			SourceOrdinal: 2, SourceOrdinalPresent: true},
	}}
	creditPageTwo := ipEntitySealPage{Family: "credit", AfterOrdinal: 3, rows: 1, credits: []ipEvidenceCredit{
		{Ordinal: 3, Class: "artist", Scope: "track-b2", ProviderPersonID: "artist-4",
			Name: "Artist Four", ArtistName: "Artist Four", SortName: "Four, Artist",
			JoinPhrase: "", Role: "primary", CreditID: "trk-credit-3",
			SourceOrdinal: 9, SourceOrdinalPresent: true},
	}}

	// A second release owns a foreign track. A credit in this build may never
	// point at it: the immediate composite track foreign key includes the
	// evidence ID. The foreign observation is a fixture, not a second build.
	ipPolicyExec(t, w, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url) VALUES('evidence-two',26,?,'musicbrainz','release','musicbrainz-2','en','US',?,?,'2026-01-01T00:00:00Z','https://musicbrainz.org/ws/2/release/2')`,
		ipEntityEvidenceParser("musicbrainz", "release"), ipEntityEvidenceRequest("musicbrainz", "release"), strings.Repeat("2", 64))
	ipPolicyExec(t, w, `INSERT INTO metadata_evidence_media(provider,evidence_kind,evidence_id,position,format,title) VALUES('musicbrainz','release','evidence-two',1,'CD','')`)
	ipPolicyExec(t, w, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url) VALUES('rec-evidence-foreign',25,?,'musicbrainz','recording','rec-foreign','en','US',?,?,'2026-01-01T00:00:00Z','https://musicbrainz.org/ws/2/recording/foreign')`,
		ipEntityEvidenceParser("musicbrainz", "recording"), ipEntityEvidenceRequest("musicbrainz", "recording"), strings.Repeat("3", 64))
	ipPolicyExec(t, w, `INSERT INTO metadata_evidence_tracks(provider,evidence_kind,evidence_id,track_id,medium_position,position,number,title,artist,recording_evidence_id) VALUES('musicbrainz','release','evidence-two','foreign-track',1,1,'1','Foreign','Artist X','rec-evidence-foreign')`)

	pages := []ipEntitySealPage{mediaPage, trackPage, creditPageOne, creditPageTwo}
	declarations := ipEntitySealDeclarations(pages)
	counts := ipEntitySealDeclaredCounts(declarations)
	bq := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", counts)
	bq.header = ipEntityEvidenceReleaseHeader("evidence-one", "musicbrainz", "musicbrainz-1", 20)
	bq.families = declarations
	sequence := int64(2)
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p,
		mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, sequence), bq))
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityEvidenceAck(t, w, first.Value, sequence)
	sequence++
	current, before := first.Value, int64(1)
	appendPage := func(page ipEntitySealPage) {
		t.Helper()
		familyStart := before - page.AfterOrdinal
		request := ipEntitySealPageRequest(owner, current, page, counts[page.Family], familyStart, before)
		out, turn, tx := ipEntityEvidenceApplyAppend(t, w, r, p,
			mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, sequence), request))
		ipPolicyCommitTurn(t, turn, tx)
		ipEntityEvidenceAck(t, w, out.Value, sequence)
		current, before, sequence = out.Value, before+page.rows, sequence+1
	}
	appendPage(mediaPage)
	appendPage(trackPage)
	appendPage(creditPageOne)

	// Nonexistent and foreign-release track scopes are refused by the immediate
	// track foreign key, with no receipt and no credit row.
	for _, scope := range []string{"no-such-track", "foreign-track"} {
		bad := ipEntitySealPage{Family: "credit", AfterOrdinal: 3, rows: 1, credits: []ipEvidenceCredit{
			{Ordinal: 3, Class: "artist", Scope: scope, ProviderPersonID: "artist-9",
				Name: "Artist Nine", Role: "primary", CreditID: "bad-credit",
				SourceOrdinal: 3, SourceOrdinalPresent: true},
		}}
		request := ipEntitySealPageRequest(owner, current, bad, counts["credit"], before-bad.AfterOrdinal, before)
		c, err := newIPEntityEvidenceAppendCommand(ipEntityEvidenceIdentity(claim, sequence), request)
		if err != nil {
			t.Fatal(err)
		}
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
		out, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b, c)
		if out != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("foreign track credit admitted", scope, out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence_credits WHERE evidence_id='evidence-one' AND scope=?`, scope); got != 0 {
			t.Fatal("refused credit persisted", scope, got)
		}
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='append' AND evidence_id='evidence-one'`); got != 3 {
			t.Fatal("refused credit wrote a receipt", scope, got)
		}
	}

	appendPage(creditPageTwo)
	staged := ipEntitySealStaged{owner: owner, claim: claim, pages: pages, declarations: declarations,
		begin: current, sequence: sequence,
		seal: ipEntitySealRequestFor(owner, current, declarations, pages)}
	out, turn, tx := ipEntitySealApply(t, w, r, p, ipEntitySealCommandFor(t, staged))
	if out.Outcome.State != Applied || out.Value.SealRows != 1+2+4+4 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)

	// The recording headers were staged before their tracks and stay unsealed.
	if got := ipPolicyQueryInt(t, w,
		`SELECT count(*) FROM metadata_evidence WHERE provider='musicbrainz' AND subject_kind='recording' AND id IN ('rec-evidence-1','rec-evidence-2','rec-evidence-3','rec-evidence-4') AND sealed=0`); got != 4 {
		t.Fatal("recording headers not pre-staged", got)
	}
	// Exact track identity, order and joined display.
	for _, track := range trackPage.tracks {
		if got := ipPolicyQueryInt(t, w,
			`SELECT count(*) FROM metadata_evidence_tracks WHERE provider='musicbrainz' AND evidence_kind='release' AND evidence_id='evidence-one' AND track_id=? AND medium_position=? AND position=? AND number=? AND title=? AND artist=? AND recording_evidence_id=?`,
			track.TrackID, track.MediumPosition, track.Position, track.Number, track.Title,
			track.Artist, track.RecordingEvidenceID); got != 1 {
			t.Fatal("track identity not preserved", track.TrackID, got)
		}
	}
	// Exact credit identity and join phrases, with the generated track_id equal
	// to the scope and the family ordinal distinct from the source ordinal.
	for _, credit := range append(append([]ipEvidenceCredit(nil), creditPageOne.credits...), creditPageTwo.credits...) {
		if got := ipPolicyQueryInt(t, w,
			`SELECT count(*) FROM metadata_evidence_credits WHERE provider='musicbrainz' AND evidence_kind='release' AND evidence_id='evidence-one' AND scope=? AND ordinal=? AND credit_class=? AND provider_person_id=? AND name=? AND artist_name=? AND sort_name=? AND disambiguation=? AND join_phrase=? AND role=? AND department=? AND credit_id=? AND source_ordinal=? AND billing IS NULL AND track_id IS ?`,
			credit.Scope, credit.Ordinal, credit.Class, credit.ProviderPersonID, credit.Name,
			credit.ArtistName, credit.SortName, credit.Disambiguation, credit.JoinPhrase,
			credit.Role, credit.Department, credit.CreditID, credit.SourceOrdinal,
			nullableIPEvidence(credit.Scope)); got != 1 {
			t.Fatal("credit identity not preserved", credit.Scope, credit.Ordinal, got)
		}
	}
	if got := ipPolicyQueryInt(t, w,
		`SELECT count(*) FROM metadata_evidence_credits WHERE evidence_id='evidence-one' AND scope='track-b2' AND ordinal=3 AND source_ordinal=9`); got != 1 {
		t.Fatal("family cursor conflated with source ordinal", got)
	}
}

func nullableIPEvidence(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// An old-order build is never resumed or reinterpreted under the corrected
// family order: append and seal both refuse with a typed conflict.
func TestIPEntitySealRefusesOldOrderBuild(t *testing.T) {
	w, r, p, _ := ipEntitySealFixture(t)
	owner, claim := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
	counts := map[string]int64{"alias": 3}
	begin := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", counts)
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p,
		mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), begin))
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityEvidenceAck(t, w, first.Value, 2)

	page := ipEntityEvidenceAppendFor(first.Value, "alias", 0, 1, 2)
	page.aliases = ipEntityEvidenceAliasRows(0, 2)
	one, turn, tx := ipEntityEvidenceApplyAppend(t, w, r, p,
		mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 3), page))
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityEvidenceAck(t, w, one.Value, 3)

	ipEntitySealTamperPair(t, w, "evidence-one", "ip.tmdb.series.header-complete-children.v1",
		"ip.tmdb.series.detail+artist-credits+recordings+release-groups.v1")

	next := ipEntityEvidenceAppendFor(one.Value, "alias", 2, 3, 3)
	next.aliases = ipEntityEvidenceAliasRows(2, 1)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
	refused, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b,
		mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 4), next))
	if refused != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("old-order append admitted", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT cursor FROM metadata_evidence_builds WHERE id='build-one'`); got != 3 {
		t.Fatal("old-order append advanced the build", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='append'`); got != 1 {
		t.Fatal("old-order append wrote a receipt", got)
	}

	pages := []ipEntitySealPage{{Family: "alias", AfterOrdinal: 0, rows: 2, aliases: ipEntityEvidenceAliasRows(0, 2)},
		{Family: "alias", AfterOrdinal: 2, rows: 1, aliases: ipEntityEvidenceAliasRows(2, 1)}}
	declarations := ipEntitySealDeclarations(pages)
	build := one.Value
	seal := ipEntitySealRequestFor(owner, build, declarations, pages)
	c, err := newIPEntitySealCommand(ipEntityEvidenceIdentity(claim, 4), seal)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntitySeal)
	out, err := applyIPEntitySeal(t.Context(), turn, p, t.Context(), b, c)
	if out != (ipEntitySealCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("old-order seal admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal'`); got != 0 {
		t.Fatal("old-order seal wrote a receipt", got)
	}
}

func TestIPEntitySealDescriptors(t *testing.T) {
	expected := Descriptor{ID: IPEntitySeal, Name: "ip_entity_seal", Durability: Normal}
	actual, err := Describe(expected.ID)
	if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
		t.Fatal(actual, err)
	}
}
