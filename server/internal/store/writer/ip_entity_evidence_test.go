package writer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group D is not embedded, so the IP receipt header and its exact reverse
// indexes are qualified only against the packet's draft subset. The provider
// operation, subject binding, evidence body/build/children and acquisition job
// are the real embedded owners.
func ipEntityEvidenceFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, time.Time) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	base := w.now()
	for _, name := range []string{"testdata/ip-policy-draft.sql", "testdata/ip-entity-evidence-draft.sql"} {
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
	defs := append(append(ipPolicyDefinitions(), ipEntityJobDefinitions()...), ipEntityEvidenceDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{registry, keeper}, p, base
}

func ipEntityEvidenceIdentity(claim CommandIdentity, sequence int64) CommandIdentity {
	var token [16]byte
	token[0], token[1] = 0x6b, byte(sequence)
	return CommandIdentity{Incarnation: claim.Incarnation, Sequence: sequence, OperationID: token}
}

// ipEntityEvidenceChainIdentity is ipEntityEvidenceIdentity under one explicit
// operation-ID salt. A fixture that stages two independent evidence chains in
// one database (both beginning at sequence 2) must not reuse the first chain's
// globally unique operation IDs; the salt keeps each chain's operation
// namespace distinct while the single-chain helper above keeps its exact output.
func ipEntityEvidenceChainIdentity(claim CommandIdentity, sequence int64, salt byte) CommandIdentity {
	var token [16]byte
	token[0], token[1], token[2] = 0x6b, salt, byte(sequence)
	return CommandIdentity{Incarnation: claim.Incarnation, Sequence: sequence, OperationID: token}
}

func ipEntityEvidenceDigest(value string) [32]byte {
	return sha256.Sum256([]byte(value))
}

func ipEntityEvidenceHeader(evidenceID string) EvidenceHeaderInput {
	parser, shape := ipEntityEvidencePair("tmdb", "series")
	return EvidenceHeaderInput{ID: evidenceID, SubjectID: 1, ParserVersion: parser,
		Provider: "tmdb", SubjectKind: "series", ProviderID: "tmdb-1", Language: "en", Region: "US",
		RequestShape: shape, ObservationDigest: ipEntityEvidenceDigest(evidenceID),
		ObservedAt: "2026-01-01T00:00:00Z", SourceURL: "https://api.themoviedb.org/3/tv/1",
		Title: "Series One", Year: 2020}
}

// ipEntityEvidenceReleaseHeader is one complete release observation header under
// the corrected ordered-family shape. MusicBrainz evidence is locale-neutral
// and carries the canonical empty language/region pair.
func ipEntityEvidenceReleaseHeader(evidenceID, provider, providerID string, subjectID int64) EvidenceHeaderInput {
	parser, shape := ipEntityEvidencePair(provider, "release")
	language, region := "en", "US"
	if provider == "musicbrainz" {
		language, region = "", ""
	}
	return EvidenceHeaderInput{ID: evidenceID, SubjectID: subjectID, ParserVersion: parser,
		Provider: provider, SubjectKind: "release", ProviderID: providerID, Language: language, Region: region,
		RequestShape: shape, ObservationDigest: ipEntityEvidenceDigest(evidenceID),
		ObservedAt: "2026-01-01T00:00:00Z", SourceURL: "https://musicbrainz.org/ws/2/release/1",
		Title: "Release One", Artist: "Artist One", Year: 2020}
}

// ipEntityEvidenceRecordingHeader is one pre-staged recording header for the
// provider subject identified by subjectID. MusicBrainz evidence carries the
// canonical empty locale pair.
func ipEntityEvidenceRecordingHeader(evidenceID, provider, providerID string, subjectID int64) EvidenceHeaderInput {
	parser, shape := ipEntityEvidencePair(provider, "recording")
	language, region := "en", "US"
	if provider == "musicbrainz" {
		language, region = "", ""
	}
	return EvidenceHeaderInput{ID: evidenceID, SubjectID: subjectID, ParserVersion: parser,
		Provider: provider, SubjectKind: "recording", ProviderID: providerID, Language: language, Region: region,
		RequestShape: shape, ObservationDigest: ipEntityEvidenceDigest(evidenceID),
		ObservedAt: "2026-01-01T00:00:00Z", SourceURL: "https://musicbrainz.org/ws/2/recording/1",
		Title: "Recording One", Artist: "Artist One", LengthMillis: EvidenceInteger{Present: true, Value: 180000}}
}

func ipEntityEvidenceDeclarations(counts map[string]int64) []ipEvidenceFamilyDeclaration {
	out := make([]ipEvidenceFamilyDeclaration, len(ipEvidenceFamilySpecs))
	for i, spec := range ipEvidenceFamilySpecs {
		out[i] = ipEvidenceFamilyDeclaration{Name: spec.name, Rows: counts[spec.name], Digest: [32]byte{byte(i + 1)}}
	}
	return out
}

func ipEntityEvidenceBeginFor(owner ipEntityJobResult, evidenceID, buildID string, counts map[string]int64) ipEntityEvidenceBeginRequest {
	return ipEntityEvidenceBeginRequest{operationID: owner.OperationID, provider: owner.Provider,
		purpose: "metadata", selectedID: owner.SelectedID, lease: owner.Lease, entityID: owner.EntityID,
		generation: 1, leaseUntilMS: owner.LeaseUntilMS, buildID: buildID, buildGeneration: 1,
		expiresAt: "2030-01-01T00:00:00Z", header: ipEntityEvidenceHeader(evidenceID),
		families: ipEntityEvidenceDeclarations(counts)}
}

func ipEntityEvidenceAppendFor(begin ipEntityEvidenceResult, family string, after, before, rows int64) ipEntityEvidenceAppendRequest {
	return ipEntityEvidenceAppendRequest{operationID: begin.OperationID, provider: begin.Provider,
		purpose: "metadata", selectedID: "tmdb-1", lease: begin.Lease, entityID: 1, generation: 1,
		leaseUntilMS: begin.LeaseUntilMS, evidenceID: begin.EvidenceID, subjectID: begin.SubjectID,
		subjectKind: begin.SubjectKind, providerID: begin.ProviderID, parserVersion: begin.ParserVersion,
		observedAt: "2026-01-01T00:00:00Z", buildID: begin.BuildID, buildGeneration: begin.BuildGeneration,
		buildRevision: begin.Revision, family: family, familyIndex: int64(ipEvidenceFamilyIndex(family)),
		familyStart: 1, familyRows: rows, afterOrdinal: after, materializedBefore: before}
}

func ipEntityEvidenceOwner(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) (ipEntityJobResult, CommandIdentity) {
	t.Helper()
	return ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
}

func ipEntityEvidenceApplyBegin(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipEntityEvidenceCommand) (ipEntityEvidenceCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceBegin)
	out, err := applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("begin", out, err)
	}
	return out, turn, tx
}

func ipEntityEvidenceApplyAppend(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipEntityEvidenceCommand) (ipEntityEvidenceCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
	out, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("append", out, err)
	}
	return out, turn, tx
}

func ipEntityEvidenceAck(t *testing.T, w *executor, value ipEntityEvidenceResult, sequence int64) {
	t.Helper()
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: value.OperationID, Provider: value.Provider}, sequence)
}

func mustEvidenceBegin(t *testing.T, identity CommandIdentity, q ipEntityEvidenceBeginRequest) *ipEntityEvidenceCommand {
	t.Helper()
	c, err := newIPEntityEvidenceBeginCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustEvidenceAppend(t *testing.T, identity CommandIdentity, q ipEntityEvidenceAppendRequest) *ipEntityEvidenceCommand {
	t.Helper()
	c, err := newIPEntityEvidenceAppendCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIPEntityEvidenceBeginFreshReplayAndRetired(t *testing.T) {
	w, r, p, _ := ipEntityEvidenceFixture(t)
	owner, claim := ipEntityEvidenceOwner(t, w, r, p)
	q := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"alias": 2})
	out, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), q))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Kind != "begin" ||
		out.Value.EvidenceID != "evidence-one" || out.Value.BuildID != "build-one" ||
		out.Value.SubjectKind != "series" || out.Value.SubjectID != 1 ||
		out.Value.BuildGeneration != 1 || out.Value.Revision != 1 || out.Value.MaterializedRows != 1 ||
		out.Value.ResultCount != 1 || out.Value.ResultBytes <= 0 ||
		out.Value.LeaseUntilMS != owner.LeaseUntilMS || out.Value.AppliedMS != w.now().UnixMilli() ||
		out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	charge := out.Value.ResultBytes
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_evidence WHERE id='evidence-one' AND subject_id=1 AND provider='tmdb' AND subject_kind='series' AND provider_id='tmdb-1' AND sealed=0`:                                                                                                   1,
		`SELECT count(*) FROM metadata_evidence_builds WHERE id='build-one' AND evidence_id='evidence-one' AND generation=1 AND owner_operation='` + owner.OperationID + `' AND state='building' AND phase='children' AND cursor=1 AND expected_rows=3 AND materialized_rows=1`: 1,
		`SELECT materialized_charge FROM metadata_evidence_builds WHERE id='build-one'`:                                                                                                                   charge,
		`SELECT content_revision FROM metadata_evidence_builds WHERE id='build-one'`:                                                                                                                      1,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='begin' AND evidence_id='evidence-one' AND build_id='build-one' AND result_phase='building' AND terminal=0 AND accepted_revision=1`: 1,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + owner.OperationID + `'`:                                                                                             2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// Replay after unrelated source/lease/policy changes returns the original
	// saved begin before any fresh probe and writes nothing.
	original := out
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending',generation=9,revision=9,lease='other',lease_until_ms=1000000 WHERE entity_id=1`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=5,revision=5 WHERE id=1`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceBegin)
	replay, err := applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b,
		mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), q))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("begin replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// The claim receipt and the begin receipt share the IP receipt header; a
	// replay may add no receipt of its own.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_evidence`:        1,
		`SELECT count(*) FROM metadata_evidence_builds`: 1,
		`SELECT count(*) FROM metadata_ip_receipts`:     2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("begin replay wrote rows", query, got)
		}
	}
	// At or below ACK is Retired even while the receipt and build remain
	// physically present.
	ipEntityEvidenceAck(t, w, original.Value, 2)
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityEvidenceBegin)
	retired, err := applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b,
		mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), q))
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipEntityEvidenceResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired begin served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence WHERE sealed=0`); got != 1 {
		t.Fatal("retired begin lost its unsealed evidence", got)
	}
}

func TestIPEntityEvidenceBeginNamespacesAndIdentity(t *testing.T) {
	w, r, p, _ := ipEntityEvidenceFixture(t)
	owner, claim := ipEntityEvidenceOwner(t, w, r, p)
	construction := []struct {
		name   string
		mutate func(*ipEntityEvidenceBeginRequest)
	}{
		{"en_first_parser", func(q *ipEntityEvidenceBeginRequest) {
			q.header.ParserVersion = "tmdb.movie.header-selected-credits.v1"
		}},
		{"en_first_request", func(q *ipEntityEvidenceBeginRequest) {
			q.header.RequestShape = "tmdb.movie.detail+credits.header-selected-credits.v1"
		}},
		{"parser_without_ip_namespace", func(q *ipEntityEvidenceBeginRequest) { q.header.ParserVersion = "series.v1" }},
		{"request_without_ip_namespace", func(q *ipEntityEvidenceBeginRequest) { q.header.RequestShape = "series.detail.v1" }},
		{"parser_equals_request", func(q *ipEntityEvidenceBeginRequest) {
			q.header.ParserVersion, q.header.RequestShape = "ip.tmdb.series.v1", "ip.tmdb.series.v1"
		}},
		{"pair_of_another_kind", func(q *ipEntityEvidenceBeginRequest) {
			q.header.ParserVersion, q.header.RequestShape = ipEntityEvidencePair("tmdb", "recording")
		}},
		{"movie", func(q *ipEntityEvidenceBeginRequest) { q.header.SubjectKind = "movie" }},
		{"person", func(q *ipEntityEvidenceBeginRequest) { q.header.SubjectKind = "person" }},
		{"unknown_provider", func(q *ipEntityEvidenceBeginRequest) { q.header.Provider, q.provider = "plex", "plex" }},
		{"header_provider_mismatch", func(q *ipEntityEvidenceBeginRequest) { q.header.Provider = "tvdb" }},
		{"provider_id_mismatch", func(q *ipEntityEvidenceBeginRequest) { q.header.ProviderID = "tmdb-9" }},
		{"empty_evidence", func(q *ipEntityEvidenceBeginRequest) { q.header.ID = "" }},
		{"empty_build", func(q *ipEntityEvidenceBeginRequest) { q.buildID = "" }},
		{"long_build", func(q *ipEntityEvidenceBeginRequest) { q.buildID = strings.Repeat("b", ipEvidenceIDMax+1) }},
		{"zero_generation", func(q *ipEntityEvidenceBeginRequest) { q.buildGeneration = 0 }},
		{"bad_expiry", func(q *ipEntityEvidenceBeginRequest) { q.expiresAt = "tomorrow" }},
		{"short_families", func(q *ipEntityEvidenceBeginRequest) { q.families = q.families[:len(q.families)-1] }},
		{"wrong_family_name", func(q *ipEntityEvidenceBeginRequest) { q.families[0].Name = "aliases" }},
		{"negative_family_rows", func(q *ipEntityEvidenceBeginRequest) { q.families[0].Rows = -1 }},
		{"over_family_max", func(q *ipEntityEvidenceBeginRequest) {
			q.families[ipEvidenceFamilyIndex("rating")].Rows = int64(ipEvidenceFamilySpecs[ipEvidenceFamilyIndex("rating")].maxRows) + 1
		}},
	}
	for _, tc := range construction {
		t.Run("refuse_"+tc.name, func(t *testing.T) {
			q := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", nil)
			tc.mutate(&q)
			if c, err := newIPEntityEvidenceBeginCommand(ipEntityEvidenceIdentity(claim, 2), q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	t.Run("duplicate_evidence_and_build", func(t *testing.T) {
		w, r, p, _ := ipEntityEvidenceFixture(t)
		owner, claim := ipEntityEvidenceOwner(t, w, r, p)
		q := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", nil)
		out, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), q))
		ipPolicyCommitTurn(t, turn, tx)
		ipEntityEvidenceAck(t, w, out.Value, 2)
		// The exact evidence ID is already owned: the reverse receipt index
		// refuses a second fresh begin before any insert.
		second := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-two", nil)
		second.header.ObservationDigest = ipEntityEvidenceDigest("second")
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceBegin)
		dup, err := applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b,
			mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 3), second))
		if dup != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("duplicate evidence admitted", dup, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		// A second evidence may not reuse an existing build ID.
		third := ipEntityEvidenceBeginFor(owner, "evidence-two", "build-one", nil)
		turn, tx, b = ipPolicyTurn(t, w, r, IPEntityEvidenceBegin)
		dup, err = applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b,
			mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 3), third))
		if dup != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("duplicate build admitted", dup, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence`); got != 1 {
			t.Fatal("refused begins wrote evidence", got)
		}
		// The claim and the first begin each saved one receipt; neither refused
		// begin may add a third.
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts`); got != 2 {
			t.Fatal("refused begins wrote receipts", got)
		}
	})
}

// The superseded v1 spellings encode the old credit-before-media/track order.
// They are refused outright, so no old-order build can begin, resume or be
// reinterpreted under the corrected family indexes.
func TestIPEntityEvidenceHeaderPairRefusals(t *testing.T) {
	current := ipEntityEvidenceReleaseHeader("evidence-one", "musicbrainz", "mb-release-1", 2)
	if !ipEntityEvidenceHeaderAllowed(current) {
		t.Fatal("current ordered-family pair refused")
	}
	old := current
	old.ParserVersion = "ip.musicbrainz.release.header-complete-children.v1"
	old.RequestShape = "ip.musicbrainz.release.detail+artist-credits+recordings+release-groups.v1"
	if ipEntityEvidenceHeaderAllowed(old) {
		t.Fatal("superseded v1 release pair admitted")
	}
	mixed := current
	mixed.ParserVersion = "ip.musicbrainz.release.header-complete-children.v1"
	if ipEntityEvidenceHeaderAllowed(mixed) {
		t.Fatal("superseded v1 parser admitted")
	}
	otherKind := current
	otherKind.ParserVersion, otherKind.RequestShape = ipEntityEvidencePair("musicbrainz", "recording")
	if ipEntityEvidenceHeaderAllowed(otherKind) {
		t.Fatal("another kind's pair admitted for a release")
	}
}

func ipEntityEvidenceAliasRows(first int64, count int) []ipEvidenceAlias {
	out := make([]ipEvidenceAlias, count)
	for i := range out {
		ordinal := first + int64(i)
		out[i] = ipEvidenceAlias{Ordinal: ordinal, RequestedID: fmt.Sprintf("requested-%d", ordinal),
			CanonicalID: "tmdb-1", Name: fmt.Sprintf("Alias %d", ordinal)}
	}
	return out
}

func TestIPEntityEvidenceAppendAliasChain(t *testing.T) {
	w, r, p, _ := ipEntityEvidenceFixture(t)
	owner, claim := ipEntityEvidenceOwner(t, w, r, p)
	begin := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"alias": 4})
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), begin))
	ipPolicyCommitTurn(t, turn, tx)
	headerCharge := first.Value.ResultBytes
	initialDigest := ipPolicyString(t, w, `SELECT materialized_digest FROM metadata_evidence_builds WHERE id='build-one'`)
	ipEntityEvidenceAck(t, w, first.Value, 2)

	page := ipEntityEvidenceAppendFor(first.Value, "alias", 0, 1, 4)
	page.aliases = ipEntityEvidenceAliasRows(0, 2)
	one, turn, tx := ipEntityEvidenceApplyAppend(t, w, r, p, mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 3), page))
	if one.Outcome.State != Applied || one.Value.Kind != "append" || one.Value.Family != "alias" ||
		one.Value.Revision != 2 || one.Value.MaterializedRows != 3 || one.Value.ResultCount != 2 ||
		one.Value.ResultBytes <= 0 || one.Digest == (ResultDigest{}) {
		t.Fatal(one)
	}
	ipPolicyCommitTurn(t, turn, tx)
	oneDigest := ipPolicyString(t, w, `SELECT materialized_digest FROM metadata_evidence_builds WHERE id='build-one'`)
	if oneDigest == initialDigest || oneDigest == "" {
		t.Fatal("append did not advance the digest chain", oneDigest)
	}
	// The committed page replays before any build/lease/current probe. The
	// mutable build state is deliberately torn; a replay must not read it.
	ipPolicyExec(t, w, `UPDATE metadata_evidence_builds SET state='done',phase='done' WHERE id='build-one'`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=5,revision=5 WHERE id=1`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
	replay, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b,
		mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 3), page))
	if err != nil || !replay.Outcome.Replayed || replay.Value != one.Value || replay.Digest != one.Digest {
		t.Fatal("append replay after build change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// Restore the torn mutable source too: the replay above proved it is not
	// read, but the next fresh page is a current-source CAS and must not be
	// stale.
	ipPolicyExec(t, w, `UPDATE metadata_evidence_builds SET state='building',phase='children' WHERE id='build-one'`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=1,revision=1 WHERE id=1`)
	ipEntityEvidenceAck(t, w, one.Value, 3)
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
	retired, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b,
		mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 3), page))
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipEntityEvidenceResult{}) {
		t.Fatal("retired append served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	second := ipEntityEvidenceAppendFor(one.Value, "alias", 2, 3, 4)
	second.aliases = ipEntityEvidenceAliasRows(2, 2)
	two, turn, tx := ipEntityEvidenceApplyAppend(t, w, r, p, mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 4), second))
	if two.Value.Revision != 3 || two.Value.MaterializedRows != 5 || two.Value.ResultCount != 2 {
		t.Fatal(two)
	}
	ipPolicyCommitTurn(t, turn, tx)
	twoDigest := ipPolicyString(t, w, `SELECT materialized_digest FROM metadata_evidence_builds WHERE id='build-one'`)
	if twoDigest == oneDigest || twoDigest == initialDigest {
		t.Fatal("second page reused a digest", twoDigest)
	}
	wantCharge := headerCharge + one.Value.ResultBytes + two.Value.ResultBytes
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id='evidence-one'`:                                   4,
		`SELECT cursor FROM metadata_evidence_builds WHERE id='build-one'`:                                                  5,
		`SELECT materialized_rows FROM metadata_evidence_builds WHERE id='build-one'`:                                       5,
		`SELECT materialized_charge FROM metadata_evidence_builds WHERE id='build-one'`:                                     wantCharge,
		`SELECT content_revision FROM metadata_evidence_builds WHERE id='build-one'`:                                        3,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='append'`:                                             2,
		`SELECT count(*) FROM metadata_evidence_builds WHERE state<>'building' OR phase<>'children'`:                        0,
		`SELECT count(*) FROM metadata_evidence WHERE sealed<>0`:                                                            0,
		`SELECT after_ordinal FROM metadata_ip_receipts WHERE command_kind='append' ORDER BY command_sequence DESC LIMIT 1`: 4,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// Reaching the declared row count must never mark the build ready or the
	// evidence sealed. One extra row is refused by the exact expected-row CAS.
	extra := ipEntityEvidenceAppendFor(two.Value, "alias", 4, 5, 5)
	extra.aliases = ipEntityEvidenceAliasRows(4, 1)
	ipEntityEvidenceAck(t, w, two.Value, 4)
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
	refused, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b,
		mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 5), extra))
	if refused != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("append past the declared count admitted", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT cursor FROM metadata_evidence_builds WHERE id='build-one'`:                5,
		`SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id='evidence-one'`: 4,
		`SELECT count(*) FROM metadata_evidence_builds WHERE state='ready'`:               0,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='append'`:           2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("refused append changed the build", query, got)
		}
	}
}

func TestIPEntityEvidenceAppendMaximumAndEmptyPages(t *testing.T) {
	w, r, p, _ := ipEntityEvidenceFixture(t)
	owner, claim := ipEntityEvidenceOwner(t, w, r, p)
	begin := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", nil)
	// A release subject carries media, tracks and credits; the corrected family
	// order materializes media, then track, then credit.
	begin.header = ipEntityEvidenceReleaseHeader("evidence-one", "tmdb", "tmdb-1", 2)
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), begin))
	ipPolicyCommitTurn(t, turn, tx)
	_ = w
	_ = r
	_ = p
	recording := ipEntityEvidenceRecordingHeader("recording-evidence", "tmdb", "recording-1", 3)
	sentinels := map[string]func(int) any{
		"alias": func(n int) any {
			return ipEvidenceAlias{Ordinal: int64(n), RequestedID: "r", CanonicalID: "tmdb-1", Name: "n"}
		},
		"external": func(n int) any {
			return ipEvidenceExternal{Provider: "tmdb", SubjectKind: "series", ProviderID: fmt.Sprintf("x-%d", n)}
		},
		"term": func(n int) any { return ipEvidenceTerm{Ordinal: int64(n), Vocabulary: "genre", Value: "Drama"} },
		"rating": func(n int) any {
			return ipEvidenceRating{Role: "audience", Value: 8, Scale: 10, Votes: 1, SourceURL: "https://example.test/r"}
		},
		"credit": func(n int) any {
			return ipEvidenceCredit{Ordinal: int64(n), Class: "crew", ProviderPersonID: "42", Name: "Name"}
		},
		"episode": func(n int) any {
			return ipEvidenceEpisode{Ordinal: int64(n), EpisodeEvidenceID: "episode-evidence", EpisodeOrder: "1"}
		},
		"media": func(n int) any { return ipEvidenceMedia{Position: int64(n) + 1, Format: "CD", Title: "Disc"} },
		"track": func(n int) any {
			return ipEvidenceTrack{TrackID: fmt.Sprintf("track-%d", n), MediumPosition: 1, Position: int64(n) + 1,
				Number: "1", Title: "Track", RecordingEvidenceID: "recording-evidence", Recording: recording}
		},
	}
	for family := range ipEvidenceFamilySpecs {
		spec := ipEvidenceFamilySpecs[family]
		t.Run(spec.name, func(t *testing.T) {
			build := func(count int) ipEntityEvidenceAppendRequest {
				q := ipEntityEvidenceAppendFor(first.Value, spec.name, 0, 1, int64(spec.maxPage))
				rows := make([]any, count)
				for i := range rows {
					rows[i] = sentinels[spec.name](i)
				}
				switch family {
				case 0:
					q.aliases = make([]ipEvidenceAlias, count)
					for i := range rows {
						q.aliases[i] = rows[i].(ipEvidenceAlias)
					}
				case 1:
					q.external = make([]ipEvidenceExternal, count)
					for i := range rows {
						q.external[i] = rows[i].(ipEvidenceExternal)
					}
				case 2:
					q.terms = make([]ipEvidenceTerm, count)
					for i := range rows {
						q.terms[i] = rows[i].(ipEvidenceTerm)
					}
				case 3:
					q.ratings = make([]ipEvidenceRating, count)
					for i := range rows {
						q.ratings[i] = rows[i].(ipEvidenceRating)
					}
				case 4:
					q.episodes = make([]ipEvidenceEpisode, count)
					for i := range rows {
						q.episodes[i] = rows[i].(ipEvidenceEpisode)
					}
				case 5:
					q.media = make([]ipEvidenceMedia, count)
					for i := range rows {
						q.media[i] = rows[i].(ipEvidenceMedia)
					}
				case 6:
					q.tracks = make([]ipEvidenceTrack, count)
					for i := range rows {
						q.tracks[i] = rows[i].(ipEvidenceTrack)
					}
				case 7:
					q.credits = make([]ipEvidenceCredit, count)
					for i := range rows {
						q.credits[i] = rows[i].(ipEvidenceCredit)
					}
				}
				return q
			}
			identity := ipEntityEvidenceIdentity(claim, 3)
			if c, err := newIPEntityEvidenceAppendCommand(identity, build(spec.maxPage)); err != nil || c == nil {
				t.Fatalf("maximum %s page refused: %v", spec.name, err)
			}
			if c, err := newIPEntityEvidenceAppendCommand(identity, build(spec.maxPage+1)); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("oversized page admitted", c, err)
			}
			if c, err := newIPEntityEvidenceAppendCommand(identity, build(0)); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("empty page admitted", c, err)
			}
			wrong := build(1)
			wrong.family = "not-a-family"
			if c, err := newIPEntityEvidenceAppendCommand(identity, wrong); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("foreign family admitted", c, err)
			}
			mixed := build(1)
			mixed.terms = []ipEvidenceTerm{{Ordinal: 0, Vocabulary: "genre", Value: "Drama"}}
			if family == 2 {
				mixed.aliases = []ipEvidenceAlias{{Ordinal: 0, RequestedID: "r", CanonicalID: "tmdb-1", Name: "n"}}
			}
			if c, err := newIPEntityEvidenceAppendCommand(identity, mixed); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("mixed-family page admitted", c, err)
			}
		})
	}
}

func TestIPEntityEvidenceAppendRejectsForeignAndStaleBuild(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipEntityEvidenceAppendRequest)
		sql    []string
		want   error
	}{
		{"evidence", func(q *ipEntityEvidenceAppendRequest) { q.evidenceID = "evidence-two" }, nil, ErrConflict},
		{"subject_kind", func(q *ipEntityEvidenceAppendRequest) { q.subjectKind = "season" }, nil, ErrConflict},
		{"subject_id", func(q *ipEntityEvidenceAppendRequest) { q.subjectID = 2 }, nil, ErrConflict},
		{"provider_id", func(q *ipEntityEvidenceAppendRequest) { q.providerID = "tmdb-9" }, nil, ErrConflict},
		{"parser", func(q *ipEntityEvidenceAppendRequest) { q.parserVersion = "ip.tmdb.series.v2" }, nil, ErrConflict},
		{"observed_at", func(q *ipEntityEvidenceAppendRequest) { q.observedAt = "2026-01-02T00:00:00Z" }, nil, ErrConflict},
		{"build_revision", func(q *ipEntityEvidenceAppendRequest) { q.buildRevision = 2 }, nil, ErrConflict},
		{"builder_cursor", func(q *ipEntityEvidenceAppendRequest) {
			// Coherent with the request's own cursors, but ahead of the open
			// build, so the first ordinal must move with it.
			q.afterOrdinal, q.materializedBefore = 1, 2
			q.aliases = ipEntityEvidenceAliasRows(1, 1)
		}, nil, ErrConflict},
		{"owner_generation", func(q *ipEntityEvidenceAppendRequest) { q.generation = 2 }, nil, ErrConflict},
		{"owner_provider", func(q *ipEntityEvidenceAppendRequest) { q.provider, q.selectedID = "tvdb", "tvdb-1" }, nil, ErrResolveRequired},
		{"expired_build", nil, []string{`UPDATE metadata_evidence_builds SET expires_at='2020-01-01T00:00:00Z' WHERE id='build-one'`}, ErrConflict},
		{"sealed_evidence", nil, []string{`UPDATE metadata_evidence SET sealed=1 WHERE id='evidence-one'`}, ErrResolveRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntityEvidenceFixture(t)
			owner, claim := ipEntityEvidenceOwner(t, w, r, p)
			begin := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"alias": 2})
			first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), begin))
			ipPolicyCommitTurn(t, turn, tx)
			ipEntityEvidenceAck(t, w, first.Value, 2)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := ipEntityEvidenceAppendFor(first.Value, "alias", 0, 1, 2)
			q.aliases = ipEntityEvidenceAliasRows(0, 1)
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			c, err := newIPEntityEvidenceAppendCommand(ipEntityEvidenceIdentity(claim, 3), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
			out, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b, c)
			if out != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("foreign/stale append admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence_aliases`); got != 0 {
				t.Fatal("refused append wrote children", got)
			}
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='append'`); got != 0 {
				t.Fatal("refused append wrote a receipt", got)
			}
			if got := ipPolicyQueryInt(t, w, `SELECT cursor FROM metadata_evidence_builds WHERE id='build-one'`); got != 1 {
				t.Fatal("refused append advanced the build", got)
			}
		})
	}
}

func TestIPEntityEvidenceCurrentAuthorityAndLifetime(t *testing.T) {
	type prepared struct {
		w       *executor
		r       *ipPolicyRegistry
		p       *usOwnerAuthorityProof
		command *ipEntityEvidenceCommand
		purpose CommandID
	}
	prepare := func(t *testing.T, kind string) prepared {
		t.Helper()
		w, r, p, _ := ipEntityEvidenceFixture(t)
		owner, claim := ipEntityEvidenceOwner(t, w, r, p)
		begin := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"alias": 2})
		c := mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), begin)
		if kind == "begin" {
			return prepared{w, r, p, c, IPEntityEvidenceBegin}
		}
		first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, c)
		ipPolicyCommitTurn(t, turn, tx)
		ipEntityEvidenceAck(t, w, first.Value, 2)
		q := ipEntityEvidenceAppendFor(first.Value, "alias", 0, 1, 2)
		q.aliases = ipEntityEvidenceAliasRows(0, 1)
		return prepared{w, r, p, mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 3), q), IPEntityEvidenceAppend}
	}
	apply := func(ctx context.Context, purpose CommandID, turn *commandTurn, p *usOwnerAuthorityProof,
		b ipPolicyBudget, c *ipEntityEvidenceCommand) (ipEntityEvidenceCommandResult, error) {
		if purpose == IPEntityEvidenceBegin {
			return applyIPEntityEvidenceBegin(ctx, turn, p, ctx, b, c)
		}
		return applyIPEntityEvidenceAppend(ctx, turn, p, ctx, b, c)
	}
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		for _, kind := range []string{"begin", "append"} {
			t.Run(kind+"_"+mode, func(t *testing.T) {
				state := prepare(t, kind)
				proof := ipPolicyRevoke(t, state.w, state.p, mode)
				turn, tx, b := ipPolicyTurn(t, state.w, state.r, state.purpose)
				out, err := apply(t.Context(), state.purpose, turn, proof, b, state.command)
				if out != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal("stale current authority admitted", out, err)
				}
				ipPolicyRollbackTurn(t, turn, tx)
			})
		}
	}
	for _, kind := range []string{"begin", "append"} {
		t.Run(kind+"_cancelled_lifetime", func(t *testing.T) {
			state := prepare(t, kind)
			lifetime, cancel := context.WithCancel(t.Context())
			cancel()
			turn, tx, b := ipPolicyTurn(t, state.w, state.r, state.purpose)
			out, err := apply(lifetime, state.purpose, turn, state.p, b, state.command)
			if out != (ipEntityEvidenceCommandResult{}) || err == nil {
				t.Fatal("cancelled lifetime admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPEntityEvidenceUnknownCommitFreshRead(t *testing.T) {
	for _, kind := range []string{"begin", "append"} {
		t.Run(kind, func(t *testing.T) {
			w, r, p, _ := ipEntityEvidenceFixture(t)
			owner, claim := ipEntityEvidenceOwner(t, w, r, p)
			var command *ipEntityEvidenceCommand
			var purpose CommandID
			if kind == "begin" {
				command = mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2),
					ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"alias": 1}))
				purpose = IPEntityEvidenceBegin
			} else {
				begin := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"alias": 1})
				first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), begin))
				ipPolicyCommitTurn(t, turn, tx)
				ipEntityEvidenceAck(t, w, first.Value, 2)
				q := ipEntityEvidenceAppendFor(first.Value, "alias", 0, 1, 1)
				q.aliases = ipEntityEvidenceAliasRows(0, 1)
				command = mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 3), q)
				purpose = IPEntityEvidenceAppend
			}
			path := ipPolicyDatabasePath(t, w)
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
			turn, tx, b := ipPolicyTurn(t, w, r, purpose)
			var out ipEntityEvidenceCommandResult
			if purpose == IPEntityEvidenceBegin {
				out, err = applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b, command)
			} else {
				out, err = applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b, command)
			}
			if err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			result, err := tx.commit()
			if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
				t.Fatal(result, err)
			}
			// The uncertain frame is closed; it never reserves the saved result.
			if purpose == IPEntityEvidenceBegin {
				if again, e := applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b, command); again != (ipEntityEvidenceCommandResult{}) || e == nil {
					t.Fatal("uncertain begin frame reserved", again, e)
				}
			} else {
				if again, e := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b, command); again != (ipEntityEvidenceCommandResult{}) || e == nil {
					t.Fatal("uncertain append frame reserved", again, e)
				}
			}
			// Only a FRESH read-only connection may prove the durable outcome.
			wantKind := kind
			if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='`+wantKind+`' AND result_phase='building' AND terminal=0`); got != 1 {
				t.Fatal("durable receipt", got)
			}
			if kind == "begin" {
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_evidence WHERE id='evidence-one' AND sealed=0`); got != 1 {
					t.Fatal("durable evidence", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT cursor FROM metadata_evidence_builds WHERE id='build-one'`); got != 1 {
					t.Fatal("durable build", got)
				}
			} else {
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id='evidence-one'`); got != 1 {
					t.Fatal("durable child", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT cursor FROM metadata_evidence_builds WHERE id='build-one'`); got != 2 {
					t.Fatal("durable cursor", got)
				}
			}
		})
	}
}

func TestIPEntityEvidenceStaleOwner(t *testing.T) {
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
		t.Run("begin_"+tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntityEvidenceFixture(t)
			owner, claim := ipEntityEvidenceOwner(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceBegin)
			out, err := applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b,
				mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2),
					ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", nil)))
			if out != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale begin owner admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence`); got != 0 {
				t.Fatal("stale begin wrote evidence", got)
			}
		})
		t.Run("append_"+tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntityEvidenceFixture(t)
			owner, claim := ipEntityEvidenceOwner(t, w, r, p)
			begin := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"alias": 1})
			first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), begin))
			ipPolicyCommitTurn(t, turn, tx)
			ipEntityEvidenceAck(t, w, first.Value, 2)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := ipEntityEvidenceAppendFor(first.Value, "alias", 0, 1, 1)
			q.aliases = ipEntityEvidenceAliasRows(0, 1)
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
			out, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b,
				mustEvidenceAppend(t, ipEntityEvidenceIdentity(claim, 3), q))
			if out != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale append owner admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence_aliases`); got != 0 {
				t.Fatal("stale append wrote children", got)
			}
		})
	}
	t.Run("expired_lease_at_begin_time", func(t *testing.T) {
		w, r, p, base := ipEntityEvidenceFixture(t)
		owner, claim := ipEntityEvidenceOwner(t, w, r, p)
		ipEntityJobAdvance(t, w, base, 6*time.Minute)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityEvidenceBegin)
		out, err := applyIPEntityEvidenceBegin(t.Context(), turn, p, t.Context(), b,
			mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2),
				ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", nil)))
		if out != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("expired begin lease admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPEntityEvidenceBounds(t *testing.T) {
	w, r, p, _ := ipEntityEvidenceFixture(t)
	owner, claim := ipEntityEvidenceOwner(t, w, r, p)
	long := strings.Repeat("t", ipEvidenceTextMax)
	q := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"alias": 1})
	q.header.Title = long
	q.header.Overview = long
	q.header.SourceURL = "https://api.themoviedb.org/3/tv/1"
	if c, err := newIPEntityEvidenceBeginCommand(ipEntityEvidenceIdentity(claim, 2), q); err != nil || c == nil {
		t.Fatal("maximum header text refused", err)
	}
	over := q
	over.header.ID = strings.Repeat("e", ipEvidenceIDMax+1)
	if c, err := newIPEntityEvidenceBeginCommand(ipEntityEvidenceIdentity(claim, 2), over); err == nil || c != nil {
		t.Fatal("oversized evidence identity admitted", err)
	}
	zeroLease := q
	zeroLease.leaseUntilMS = 0
	if c, err := newIPEntityEvidenceBeginCommand(ipEntityEvidenceIdentity(claim, 2), zeroLease); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("zero lease admitted", c, err)
	}
	// The maximum declared family page remains bounded by each family's own
	// total count, not by the page size.
	for i := range ipEvidenceFamilySpecs {
		decl := append([]ipEvidenceFamilyDeclaration(nil), q.families...)
		decl[i].Rows = int64(ipEvidenceFamilySpecs[i].maxRows)
		value := q
		value.families = decl
		if c, err := newIPEntityEvidenceBeginCommand(ipEntityEvidenceIdentity(claim, 2), value); err != nil || c == nil {
			t.Fatalf("maximum %s rows refused: %v", ipEvidenceFamilySpecs[i].name, err)
		}
	}
	_ = w
	_ = r
	_ = p
}

// A medium's title and format are the provider's exact strings and may each be
// empty; presentation fallback belongs outside evidence. Malformed, NUL,
// oversize and non-contiguous positions are still refused.
func TestIPEntityEvidenceMediaEmptyAndMalformed(t *testing.T) {
	w, r, p, _ := ipEntityEvidenceFixture(t)
	owner, claim := ipEntityEvidenceOwner(t, w, r, p)
	begin := ipEntityEvidenceBeginFor(owner, "evidence-one", "build-one", map[string]int64{"media": 2})
	begin.header = ipEntityEvidenceReleaseHeader("evidence-one", "tmdb", "tmdb-1", 2)
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p, mustEvidenceBegin(t, ipEntityEvidenceIdentity(claim, 2), begin))
	ipPolicyCommitTurn(t, turn, tx)
	page := func(row ipEvidenceMedia) ipEntityEvidenceAppendRequest {
		q := ipEntityEvidenceAppendFor(first.Value, "media", 0, 1, 1)
		q.media = []ipEvidenceMedia{row}
		return q
	}
	for _, tc := range []struct{ name, format, title string }{
		{"title_only_empty", "CD", ""},
		{"format_only_empty", "", "Disc One"},
		{"both_empty", "", ""},
	} {
		t.Run("admit_"+tc.name, func(t *testing.T) {
			c, err := newIPEntityEvidenceAppendCommand(ipEntityEvidenceIdentity(claim, 3),
				page(ipEvidenceMedia{Position: 1, Format: tc.format, Title: tc.title}))
			if err != nil || c == nil {
				t.Fatalf("empty medium field refused: %v", err)
			}
		})
	}
	malformed := []struct {
		name string
		row  ipEvidenceMedia
	}{
		{"invalid_utf8_format", ipEvidenceMedia{Position: 1, Format: "\xff", Title: "Disc"}},
		{"invalid_utf8_title", ipEvidenceMedia{Position: 1, Format: "CD", Title: "\xff"}},
		{"nul_title", ipEvidenceMedia{Position: 1, Format: "CD", Title: "a\x00b"}},
		{"oversize_format", ipEvidenceMedia{Position: 1, Format: strings.Repeat("f", ipEvidenceTextMax+1), Title: "Disc"}},
		{"oversize_title", ipEvidenceMedia{Position: 1, Format: "CD", Title: strings.Repeat("t", ipEvidenceTextMax+1)}},
		{"zero_position", ipEvidenceMedia{Position: 0, Format: "CD", Title: "Disc"}},
		{"noncontiguous_position", ipEvidenceMedia{Position: 2, Format: "CD", Title: "Disc"}},
	}
	for _, tc := range malformed {
		t.Run("refuse_"+tc.name, func(t *testing.T) {
			c, err := newIPEntityEvidenceAppendCommand(ipEntityEvidenceIdentity(claim, 3), page(tc.row))
			if c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed medium admitted", c, err)
			}
		})
	}
}

func TestIPEntityEvidenceDescriptors(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: IPEntityEvidenceBegin, Name: "ip_entity_evidence_begin", Durability: Normal},
		{ID: IPEntityEvidenceAppend, Name: "ip_entity_evidence_append", Durability: Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
			t.Fatal(actual, err)
		}
	}
}
