package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Groups D and E are not embedded, so the IP set proof tuple and the receipt set
// reverse index are qualified only against the packet's draft subset. The
// provider operation, publication set/page/member body, evidence/build owners
// and acquisition job are the real embedded schema.
func ipTVSetFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, ipEntitySealStaged) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, name := range []string{
		"testdata/ip-policy-draft.sql",
		"testdata/ip-entity-evidence-draft.sql",
		"testdata/ip-entity-seal-draft.sql",
		"testdata/ip-tv-set-draft.sql",
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
	defs := append(append(append(append(ipPolicyDefinitions(), ipEntityJobDefinitions()...),
		ipEntityEvidenceDefinitions()...), ipEntitySealDefinitions()...), ipTVSetDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	r := &ipPolicyRegistry{registry, keeper}
	staged := ipEntitySealStage(t, w, r, p)
	out, turn, tx := ipEntitySealApply(t, w, r, p, ipEntitySealCommandFor(t, staged))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, staged.sequence)
	return w, r, p, staged
}

type ipTVSetEpisodeSeed struct {
	EvidenceID                                  string
	SeasonNumber, EpisodeNumber, AbsoluteNumber int64
}

// ipTVSetSeedEpisodes materializes the already-sealed IP-07 episode evidence
// rows the set publishes. The set writer never acquires or normalizes them; it
// only proves the exact sealed provider/episode tuple is the declared member.
func ipTVSetSeedEpisodes(t *testing.T, w *executor, staged ipEntitySealStaged, episodes []ipTVSetEpisodeSeed) []string {
	t.Helper()
	ids := make([]string, 0, len(episodes))
	for i, episode := range episodes {
		subjectID := int64(100 + i)
		providerID := fmt.Sprintf("tmdb-episode-%d", i+1)
		digest := sha256.Sum256([]byte(episode.EvidenceID))
		buildID := fmt.Sprintf("episode-build-%d", i+1)
		sequence := int64(1000 + i)
		incarnation := ipEntityJobIncarnation(subjectID)
		ipPolicyExec(t, w, `INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(?, 'tmdb','episode',?,'en','US','ip.tmdb.episode.detail.v1',?,1)`,
			subjectID, providerID, incarnation[:])
		ipPolicyExec(t, w, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,sealed,ip_seal_owner,ip_seal_incarnation,ip_seal_sequence) VALUES(?,?,'ip.tmdb.episode.v1','tmdb','episode',?,'en','US','ip.tmdb.episode.detail.v1',?,'2026-01-01T00:00:00Z',?,1,?,?,?)`,
			episode.EvidenceID, subjectID, providerID, hex.EncodeToString(digest[:]),
			fmt.Sprintf("https://api.themoviedb.org/3/tv/1/season/%d/episode/%d", episode.SeasonNumber, episode.EpisodeNumber),
			staged.owner.OperationID, staged.claim.Incarnation[:], sequence)
		ipPolicyExec(t, w, `INSERT INTO metadata_evidence_builds(id,evidence_id,generation,owner_operation,owner_incarnation,owner_generation,state,phase,cursor,expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at,content_revision) VALUES(?,?,1,?,?,1,'ready','sealed',1,1,1,?,?,1,'2030-01-01T00:00:00Z',1)`,
			buildID, episode.EvidenceID, staged.owner.OperationID, staged.claim.Incarnation[:],
			hex.EncodeToString(digest[:]), hex.EncodeToString(digest[:]))
		operationID := make([]byte, 16)
		operationID[0], operationID[1] = 0x7e, byte(i+1)
		request := make([]byte, 32)
		request[0], request[1] = 0x7d, byte(i+1)
		result := make([]byte, 32)
		result[0], result[1] = 0x7c, byte(i+1)
		commitment := make([]byte, 32)
		commitment[0], commitment[1] = 0x7b, byte(i+1)
		ipPolicyExec(t, w, `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes,proof_pin_count) VALUES(?,?,?,?,?,'evidence_seal',1,0,?,'sealed',0,0,'',0,1,1,?,?,?,?,'ip-evidence',1,?,1,1,1)`,
			staged.owner.OperationID, staged.claim.Incarnation[:], sequence, operationID, request,
			result, episode.EvidenceID, subjectID, "ip.tmdb.episode.v1", buildID, commitment)
		ids = append(ids, episode.EvidenceID)
	}
	return ids
}

func ipTVSetBeginFor(staged ipEntitySealStaged, ids []string) ipTVSetBeginRequest {
	digest, _ := TVSetMemberDigest(ids)
	return ipTVSetBeginRequest{operationID: staged.owner.OperationID, provider: staged.owner.Provider,
		purpose: "metadata", selectedID: staged.owner.SelectedID, lease: staged.owner.Lease,
		entityID: staged.owner.EntityID, generation: 1, leaseUntilMS: staged.owner.LeaseUntilMS,
		setID: "tv-set-one", subjectKind: "series", providerID: "tmdb-1", episodeOrder: "aired",
		evidenceID: "evidence-one", buildID: "build-one", buildGeneration: 1,
		buildRevision:  staged.seal.contentRevision + 1,
		sourceRevision: 1, hierarchyRevision: 0, selectionRevision: 0,
		memberCount: int64(len(ids)), memberDigest: digest}
}

func ipTVSetNextPage(value int64) *int64 { return &value }

func ipTVSetPageFor(staged ipEntitySealStaged, ids []string, page, start, revision int64, next *int64) ipTVSetPageRequest {
	return ipTVSetPageRequest{operationID: staged.owner.OperationID, provider: staged.owner.Provider,
		purpose: "metadata", selectedID: staged.owner.SelectedID, lease: staged.owner.Lease,
		entityID: staged.owner.EntityID, generation: 1, leaseUntilMS: staged.owner.LeaseUntilMS,
		setID: "tv-set-one", subjectKind: "series", providerID: "tmdb-1", episodeOrder: "aired",
		setRevision: revision, memberStart: start, page: page, nextPage: next, members: ids,
		sourceRevision: 1, hierarchyRevision: 0, selectionRevision: 0}
}

func ipTVSetSealFor(staged ipEntitySealStaged, pages []ipTVSetSealPage) ipTVSetSealRequest {
	return ipTVSetSealRequest{operationID: staged.owner.OperationID, provider: staged.owner.Provider,
		purpose: "metadata", selectedID: staged.owner.SelectedID, lease: staged.owner.Lease,
		entityID: staged.owner.EntityID, generation: 1, leaseUntilMS: staged.owner.LeaseUntilMS,
		setID: "tv-set-one", subjectKind: "series", providerID: "tmdb-1", episodeOrder: "aired",
		setRevision: int64(len(pages)) + 1, evidenceID: "evidence-one", buildID: "build-one",
		buildGeneration: 1, buildRevision: staged.seal.contentRevision + 1,
		sourceRevision: 1, hierarchyRevision: 0, selectionRevision: 0, pages: pages}
}

func ipTVSetBeginCommandFor(t *testing.T, staged ipEntitySealStaged, sequence int64, q ipTVSetBeginRequest) *ipTVSetCommand {
	t.Helper()
	c, err := newIPTVSetBeginCommand(ipEntityEvidenceIdentity(staged.claim, sequence), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipTVSetPageCommandFor(t *testing.T, staged ipEntitySealStaged, sequence int64, q ipTVSetPageRequest) *ipTVSetCommand {
	t.Helper()
	c, err := newIPTVSetPageCommand(ipEntityEvidenceIdentity(staged.claim, sequence), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipTVSetSealCommandFor(t *testing.T, staged ipEntitySealStaged, sequence int64, q ipTVSetSealRequest) *ipTVSetCommand {
	t.Helper()
	c, err := newIPTVSetSealCommand(ipEntityEvidenceIdentity(staged.claim, sequence), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipTVSetApplyBegin(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipTVSetCommand) (ipTVSetCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetBegin)
	out, err := applyIPTVSetBegin(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("set begin", out, err)
	}
	return out, turn, tx
}

func ipTVSetApplyPage(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipTVSetCommand) (ipTVSetCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetPage)
	out, err := applyIPTVSetPage(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("set page", out, err)
	}
	return out, turn, tx
}

func ipTVSetApplySeal(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipTVSetCommand) (ipTVSetCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetSeal)
	out, err := applyIPTVSetSeal(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("set seal", out, err)
	}
	return out, turn, tx
}

// ipTVSetPartial is one three-member set after begin and page 0 only, both
// committed and acknowledged. Page 1 is ready to apply at the next sequence.
type ipTVSetPartial struct {
	owner    ipEntitySealStaged
	ids      []string
	begin    ipTVSetBeginRequest
	page0    ipTVSetPageRequest
	page1    ipTVSetPageRequest
	sequence int64
}

func ipTVSetStagePartial(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	staged ipEntitySealStaged) ipTVSetPartial {
	t.Helper()
	episodes := []ipTVSetEpisodeSeed{
		{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1},
		{EvidenceID: "episode-2", SeasonNumber: 1, EpisodeNumber: 2, AbsoluteNumber: 2},
		{EvidenceID: "episode-3", SeasonNumber: 2, EpisodeNumber: 1, AbsoluteNumber: 3},
	}
	ids := ipTVSetSeedEpisodes(t, w, staged, episodes)
	begin := ipTVSetBeginFor(staged, ids)
	out, turn, tx := ipTVSetApplyBegin(t, w, r, p, ipTVSetBeginCommandFor(t, staged, 6, begin))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 6)
	page0 := ipTVSetPageFor(staged, ids[:2], 0, 0, 1, ipTVSetNextPage(1))
	out, turn, tx = ipTVSetApplyPage(t, w, r, p, ipTVSetPageCommandFor(t, staged, 7, page0))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 7)
	return ipTVSetPartial{owner: staged, ids: ids, begin: begin, page0: page0,
		page1: ipTVSetPageFor(staged, ids[2:], 1, 2, 2, nil), sequence: 8}
}

// ipTVSetStaged is one complete three-member set with two pages, after begin and
// both page commits and acknowledgements. It is ready for seal at the next
// sequence.
type ipTVSetStaged struct {
	owner    ipEntitySealStaged
	ids      []string
	begin    ipTVSetBeginRequest
	page0    ipTVSetPageRequest
	page1    ipTVSetPageRequest
	seal     ipTVSetSealRequest
	pages    []ipTVSetSealPage
	sequence int64
}

func ipTVSetStage(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	staged ipEntitySealStaged) ipTVSetStaged {
	t.Helper()
	partial := ipTVSetStagePartial(t, w, r, p, staged)
	out, turn, tx := ipTVSetApplyPage(t, w, r, p,
		ipTVSetPageCommandFor(t, staged, partial.sequence, partial.page1))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, partial.sequence)
	pages := []ipTVSetSealPage{
		{Page: 0, NextPage: ipTVSetNextPage(1), Members: partial.ids[:2]},
		{Page: 1, NextPage: nil, Members: partial.ids[2:]},
	}
	return ipTVSetStaged{owner: staged, ids: partial.ids, begin: partial.begin,
		page0: partial.page0, page1: partial.page1, seal: ipTVSetSealFor(staged, pages),
		pages: pages, sequence: partial.sequence + 1}
}

func TestIPTVSetFreshReplayRetiredAndRetainedProof(t *testing.T) {
	w, r, p, staged := ipTVSetFixture(t)
	set := ipTVSetStage(t, w, r, p, staged)
	out, turn, tx := ipTVSetApplySeal(t, w, r, p, ipTVSetSealCommandFor(t, staged, set.sequence, set.seal))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Digest == (ResultDigest{}) ||
		out.Value.Kind != "seal" || out.Value.SetID != "tv-set-one" ||
		out.Value.Provider != "tmdb" || out.Value.SubjectKind != "series" ||
		out.Value.ProviderID != "tmdb-1" || out.Value.EpisodeOrder != "aired" ||
		out.Value.EvidenceID != "evidence-one" || out.Value.BuildID != "build-one" ||
		out.Value.MemberCount != 3 || out.Value.SealRows != 3 || out.Value.SealBytes <= 0 ||
		out.Value.SealVersion != ipTVSetVersion || out.Value.Commitment == ([32]byte{}) ||
		out.Value.Revision != 4 || out.Value.OwnerGeneration != 1 ||
		out.Value.BuildGeneration != 1 || out.Value.LeaseUntilMS != staged.owner.LeaseUntilMS ||
		out.Value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publication_sets WHERE id='tv-set-one' AND complete=1 AND revision=4 AND coverage_source_revision=1 AND coverage_hierarchy_revision=0 AND coverage_selection_revision=0 AND coverage_after_item=3 AND coverage_complete=1`: 1,
		`SELECT count(*) FROM metadata_publication_sets WHERE id='tv-set-one' AND ip_proof_owner='` + set.owner.owner.OperationID + `' AND ip_proof_sequence=9 AND ip_proof_kind='set_seal'`:                                                                      1,
		`SELECT length(ip_proof_incarnation) FROM metadata_publication_sets WHERE id='tv-set-one'`:                                                                                                                                                                16,
		`SELECT count(*) FROM metadata_publication_pages WHERE set_id='tv-set-one'`:                                                                                                                                                                               2,
		`SELECT count(*) FROM metadata_publication_page_members WHERE set_id='tv-set-one'`:                                                                                                                                                                        3,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='set_seal' AND terminal=1 AND result_phase='sealed' AND set_id='tv-set-one' AND seal_algorithm='` + ipTVSetAlgorithm + `' AND seal_version=1 AND seal_rows=3 AND proof_pin_count=1`:         1,
		`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + set.owner.owner.OperationID + `'`:                                                                                                                                           9,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	original := out

	// Replay after unrelated source, lease, policy and provider changes returns
	// the original saved seal before any fresh probe and writes nothing.
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending',generation=9,revision=9,lease='other',lease_until_ms=1000000 WHERE entity_id=1`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=5,revision=5 WHERE id=1`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetSeal)
	replay, err := applyIPTVSetSeal(t.Context(), turn, p, t.Context(), b,
		ipTVSetSealCommandFor(t, staged, set.sequence, set.seal))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("set seal replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for _, query := range []string{
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='set_seal'`,
		`SELECT count(*) FROM metadata_publication_sets WHERE complete=1`,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != 1 {
			t.Fatal("set seal replay wrote rows", query, got)
		}
	}

	// At or below ACK is Retired. The terminal set proof and its member rows
	// stay physically retained.
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: set.owner.owner.OperationID, Provider: set.owner.owner.Provider}, set.sequence)
	for _, replayCommand := range []struct {
		name     string
		sequence int64
		apply    func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context, ipPolicyBudget, *ipTVSetCommand) (ipTVSetCommandResult, error)
		command  *ipTVSetCommand
		purpose  CommandID
	}{
		{"begin", 6, applyIPTVSetBegin, ipTVSetBeginCommandFor(t, staged, 6, set.begin), IPTVSetBegin},
		{"page0", 7, applyIPTVSetPage, ipTVSetPageCommandFor(t, staged, 7, set.page0), IPTVSetPage},
		{"page1", 8, applyIPTVSetPage, ipTVSetPageCommandFor(t, staged, 8, set.page1), IPTVSetPage},
		{"seal", 9, applyIPTVSetSeal, ipTVSetSealCommandFor(t, staged, 9, set.seal), IPTVSetSeal},
	} {
		turn, tx, b := ipPolicyTurn(t, w, r, replayCommand.purpose)
		retired, err := replayCommand.apply(t.Context(), turn, p, t.Context(), b, replayCommand.command)
		if err != nil || retired.Outcome.State != ResultRetired ||
			retired.Value != (ipTVSetResult{}) || retired.Digest != (ResultDigest{}) {
			t.Fatal("retired set command served", replayCommand.name, retired, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='set_seal' AND terminal=1 AND proof_pin_count=1`: 1,
		`SELECT count(*) FROM metadata_publication_sets WHERE complete=1 AND ip_proof_sequence=9`:                      1,
		`SELECT count(*) FROM metadata_publication_page_members WHERE set_id='tv-set-one'`:                             3,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("retired set lost its proof", query, got)
		}
	}
}

func TestIPTVSetReplayAfterSourceAndLeaseChanges(t *testing.T) {
	w, r, p, staged := ipTVSetFixture(t)
	episodes := []ipTVSetEpisodeSeed{
		{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1},
		{EvidenceID: "episode-2", SeasonNumber: 1, EpisodeNumber: 2, AbsoluteNumber: 2},
		{EvidenceID: "episode-3", SeasonNumber: 2, EpisodeNumber: 1, AbsoluteNumber: 3},
	}
	ids := ipTVSetSeedEpisodes(t, w, staged, episodes)
	change := func() {
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		ipPolicyExec(t, w, `UPDATE metadata_provider_operations SET lease='other',lease_until='2020-01-01T00:00:00Z' WHERE id=? AND provider=?`,
			staged.owner.OperationID, staged.owner.Provider)
	}
	restore := func() {
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=1 WHERE code='tmdb'`)
		ipPolicyExec(t, w, `UPDATE metadata_provider_operations SET lease=?,lease_until=? WHERE id=? AND provider=?`,
			staged.owner.Lease, ipEntityJobTimeText(staged.owner.LeaseUntilMS), staged.owner.OperationID, staged.owner.Provider)
	}
	begin := ipTVSetBeginFor(staged, ids)
	out, turn, tx := ipTVSetApplyBegin(t, w, r, p, ipTVSetBeginCommandFor(t, staged, 6, begin))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	change()
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetBegin)
	replay, err := applyIPTVSetBegin(t.Context(), turn, p, t.Context(), b,
		ipTVSetBeginCommandFor(t, staged, 6, begin))
	if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal("begin replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	restore()
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 6)

	page0 := ipTVSetPageFor(staged, ids[:2], 0, 0, 1, ipTVSetNextPage(1))
	out, turn, tx = ipTVSetApplyPage(t, w, r, p, ipTVSetPageCommandFor(t, staged, 7, page0))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	change()
	turn, tx, b = ipPolicyTurn(t, w, r, IPTVSetPage)
	replay, err = applyIPTVSetPage(t.Context(), turn, p, t.Context(), b,
		ipTVSetPageCommandFor(t, staged, 7, page0))
	if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal("page replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	restore()
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 7)

	page1 := ipTVSetPageFor(staged, ids[2:], 1, 2, 2, nil)
	out, turn, tx = ipTVSetApplyPage(t, w, r, p, ipTVSetPageCommandFor(t, staged, 8, page1))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	change()
	turn, tx, b = ipPolicyTurn(t, w, r, IPTVSetPage)
	replay, err = applyIPTVSetPage(t.Context(), turn, p, t.Context(), b,
		ipTVSetPageCommandFor(t, staged, 8, page1))
	if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal("page1 replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	restore()
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 8)

	pages := []ipTVSetSealPage{
		{Page: 0, NextPage: ipTVSetNextPage(1), Members: ids[:2]},
		{Page: 1, NextPage: nil, Members: ids[2:]},
	}
	seal := ipTVSetSealFor(staged, pages)
	out, turn, tx = ipTVSetApplySeal(t, w, r, p, ipTVSetSealCommandFor(t, staged, 9, seal))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	change()
	turn, tx, b = ipPolicyTurn(t, w, r, IPTVSetSeal)
	replay, err = applyIPTVSetSeal(t.Context(), turn, p, t.Context(), b,
		ipTVSetSealCommandFor(t, staged, 9, seal))
	if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal("seal replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPTVSetBeginAndPageConflictingRequestAndSequence(t *testing.T) {
	w, r, p, staged := ipTVSetFixture(t)
	episodes := []ipTVSetEpisodeSeed{
		{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1},
		{EvidenceID: "episode-2", SeasonNumber: 1, EpisodeNumber: 2, AbsoluteNumber: 2},
	}
	ids := ipTVSetSeedEpisodes(t, w, staged, episodes)
	begin := ipTVSetBeginFor(staged, ids)
	out, turn, tx := ipTVSetApplyBegin(t, w, r, p, ipTVSetBeginCommandFor(t, staged, 6, begin))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	changedBegin := begin
	changedBegin.buildRevision++
	conflicting, err := newIPTVSetBeginCommand(ipEntityEvidenceIdentity(staged.claim, 6), changedBegin)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetBegin)
	denied, err := applyIPTVSetBegin(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting begin admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 6)

	page0 := ipTVSetPageFor(staged, ids, 0, 0, 1, nil)
	out, turn, tx = ipTVSetApplyPage(t, w, r, p, ipTVSetPageCommandFor(t, staged, 7, page0))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	changedPage := page0
	changedPage.members = []string{ids[0]}
	conflicting, err = newIPTVSetPageCommand(ipEntityEvidenceIdentity(staged.claim, 7), changedPage)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPTVSetPage)
	denied, err = applyIPTVSetPage(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting page admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind IN('set_begin','set_page')`); got != 2 {
		t.Fatal("conflicting set commands wrote a receipt", got)
	}
}

func TestIPTVSetBeginShapeRefusals(t *testing.T) {
	w, _, _, staged := ipTVSetFixture(t)
	ids := ipTVSetSeedEpisodes(t, w, staged, []ipTVSetEpisodeSeed{
		{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1},
	})
	base := ipTVSetBeginFor(staged, ids)
	identity := ipEntityEvidenceIdentity(staged.claim, 6)
	longID := strings.Repeat("x", ipTVSetIDMax+1)
	mutations := []struct {
		name   string
		mutate func(*ipTVSetBeginRequest)
	}{
		{"empty_set", func(q *ipTVSetBeginRequest) { q.setID = "" }},
		{"long_set", func(q *ipTVSetBeginRequest) { q.setID = longID }},
		{"movie_subject", func(q *ipTVSetBeginRequest) { q.subjectKind = "movie" }},
		{"person_subject", func(q *ipTVSetBeginRequest) { q.subjectKind = "person" }},
		{"bad_order", func(q *ipTVSetBeginRequest) { q.episodeOrder = "production" }},
		{"empty_provider_id", func(q *ipTVSetBeginRequest) { q.providerID = "" }},
		{"empty_evidence", func(q *ipTVSetBeginRequest) { q.evidenceID = "" }},
		{"empty_build", func(q *ipTVSetBeginRequest) { q.buildID = "" }},
		{"zero_build_generation", func(q *ipTVSetBeginRequest) { q.buildGeneration = 0 }},
		{"zero_build_revision", func(q *ipTVSetBeginRequest) { q.buildRevision = 0 }},
		{"zero_source_revision", func(q *ipTVSetBeginRequest) { q.sourceRevision = 0 }},
		{"negative_hierarchy", func(q *ipTVSetBeginRequest) { q.hierarchyRevision = -1 }},
		{"negative_selection", func(q *ipTVSetBeginRequest) { q.selectionRevision = -1 }},
		{"zero_members", func(q *ipTVSetBeginRequest) { q.memberCount = 0 }},
		{"over_members", func(q *ipTVSetBeginRequest) { q.memberCount = ipTVSetMembersMax + 1 }},
		{"zero_digest", func(q *ipTVSetBeginRequest) { q.memberDigest = [32]byte{} }},
		{"wrong_purpose", func(q *ipTVSetBeginRequest) { q.purpose = "artwork" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			tc.mutate(&q)
			if c, err := newIPTVSetBeginCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
}

func TestIPTVSetPageAndSealShapeRefusals(t *testing.T) {
	w, _, _, staged := ipTVSetFixture(t)
	ids := ipTVSetSeedEpisodes(t, w, staged, []ipTVSetEpisodeSeed{
		{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1},
		{EvidenceID: "episode-2", SeasonNumber: 1, EpisodeNumber: 2, AbsoluteNumber: 2},
	})
	identity := ipEntityEvidenceIdentity(staged.claim, 7)
	page := ipTVSetPageFor(staged, ids, 0, 0, 1, ipTVSetNextPage(1))
	pageMutations := []struct {
		name   string
		mutate func(*ipTVSetPageRequest)
	}{
		{"empty_set", func(q *ipTVSetPageRequest) { q.setID = "" }},
		{"bad_order", func(q *ipTVSetPageRequest) { q.episodeOrder = "production" }},
		{"zero_revision", func(q *ipTVSetPageRequest) { q.setRevision = 0 }},
		{"negative_start", func(q *ipTVSetPageRequest) { q.memberStart = -1 }},
		{"negative_page", func(q *ipTVSetPageRequest) { q.page = -1 }},
		{"page_over_max", func(q *ipTVSetPageRequest) { q.page = ipTVSetPagesMax }},
		{"empty_members", func(q *ipTVSetPageRequest) { q.members = nil }},
		{"duplicate_member", func(q *ipTVSetPageRequest) { q.members = []string{ids[0], ids[0]} }},
		{"bad_next", func(q *ipTVSetPageRequest) { q.nextPage = ipTVSetNextPage(2) }},
		{"next_over_max", func(q *ipTVSetPageRequest) { q.nextPage = ipTVSetNextPage(ipTVSetPagesMax) }},
		{"start_over_max", func(q *ipTVSetPageRequest) { q.memberStart = ipTVSetMembersMax }},
	}
	for _, tc := range pageMutations {
		t.Run("page/"+tc.name, func(t *testing.T) {
			q := page
			tc.mutate(&q)
			if c, err := newIPTVSetPageCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	// The declared maximum page is accepted; the next page is not.
	maxMembers := make([]string, 0, ipTVSetPageMax)
	for i := 0; i < ipTVSetPageMax; i++ {
		maxMembers = append(maxMembers, fmt.Sprintf("episode-max-%d", i))
	}
	maxPage := ipTVSetPageFor(staged, maxMembers, 0, 0, 1, nil)
	if c, err := newIPTVSetPageCommand(identity, maxPage); err != nil || c == nil {
		t.Fatal("max page refused", err)
	}
	overPage := ipTVSetPageFor(staged, append(maxMembers, "episode-over"), 0, 0, 1, nil)
	if c, err := newIPTVSetPageCommand(identity, overPage); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("over page admitted", c, err)
	}

	sealPages := []ipTVSetSealPage{
		{Page: 0, NextPage: ipTVSetNextPage(1), Members: ids},
		{Page: 1, NextPage: nil, Members: []string{"episode-3"}},
	}
	seal := ipTVSetSealFor(staged, sealPages)
	sealIdentity := ipEntityEvidenceIdentity(staged.claim, 9)
	sealMutations := []struct {
		name   string
		mutate func(*ipTVSetSealRequest)
	}{
		{"empty_pages", func(q *ipTVSetSealRequest) { q.pages = nil }},
		{"wrong_page_number", func(q *ipTVSetSealRequest) { q.pages[0].Page = 1 }},
		{"first_no_next", func(q *ipTVSetSealRequest) { q.pages[0].NextPage = nil }},
		{"last_has_next", func(q *ipTVSetSealRequest) { q.pages[1].NextPage = ipTVSetNextPage(2) }},
		{"revision_mismatch", func(q *ipTVSetSealRequest) { q.setRevision = 9 }},
		{"duplicate_member", func(q *ipTVSetSealRequest) { q.pages[1].Members = []string{ids[0]} }},
		{"empty_page", func(q *ipTVSetSealRequest) { q.pages[1].Members = nil }},
		{"bad_order", func(q *ipTVSetSealRequest) { q.episodeOrder = "production" }},
		{"empty_evidence", func(q *ipTVSetSealRequest) { q.evidenceID = "" }},
		{"zero_build_revision", func(q *ipTVSetSealRequest) { q.buildRevision = 0 }},
		{"negative_hierarchy", func(q *ipTVSetSealRequest) { q.hierarchyRevision = -1 }},
	}
	for _, tc := range sealMutations {
		t.Run("seal/"+tc.name, func(t *testing.T) {
			q := seal
			q.pages = append([]ipTVSetSealPage(nil), seal.pages...)
			for i := range q.pages {
				q.pages[i].Members = append([]string(nil), seal.pages[i].Members...)
			}
			tc.mutate(&q)
			if c, err := newIPTVSetSealCommand(sealIdentity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
}

func TestIPTVSetMissingPageAndTruthfulPartialAck(t *testing.T) {
	w, r, p, staged := ipTVSetFixture(t)
	episodes := []ipTVSetEpisodeSeed{
		{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1},
		{EvidenceID: "episode-2", SeasonNumber: 1, EpisodeNumber: 2, AbsoluteNumber: 2},
	}
	ids := ipTVSetSeedEpisodes(t, w, staged, episodes)
	begin := ipTVSetBeginFor(staged, ids)
	out, turn, tx := ipTVSetApplyBegin(t, w, r, p, ipTVSetBeginCommandFor(t, staged, 6, begin))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 6)
	page0 := ipTVSetPageFor(staged, ids[:1], 0, 0, 1, ipTVSetNextPage(1))
	out, turn, tx = ipTVSetApplyPage(t, w, r, p, ipTVSetPageCommandFor(t, staged, 7, page0))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	// A page ACK does not complete the set and does not refund the missing page.
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 7)
	partial := ipTVSetSealFor(staged, []ipTVSetSealPage{
		{Page: 0, NextPage: ipTVSetNextPage(1), Members: ids[:1]},
		{Page: 1, NextPage: nil, Members: ids[1:]},
	})
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetSeal)
	denied, err := applyIPTVSetSeal(t.Context(), turn, p, t.Context(), b,
		ipTVSetSealCommandFor(t, staged, 8, partial))
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("partial page sealed", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_publication_sets WHERE id='tv-set-one' AND complete=0 AND revision=2`: 1,
		`SELECT count(*) FROM metadata_publication_page_members WHERE set_id='tv-set-one'`:                   1,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='set_seal'`:                            0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("partial page wrote completion", query, got)
		}
	}
	// The missing page is still required, even if the manifest declares no next.
	noNext := ipTVSetSealFor(staged, []ipTVSetSealPage{
		{Page: 0, NextPage: nil, Members: ids[:1]},
	})
	turn, tx, b = ipPolicyTurn(t, w, r, IPTVSetSeal)
	denied, err = applyIPTVSetSeal(t.Context(), turn, p, t.Context(), b,
		ipTVSetSealCommandFor(t, staged, 8, noNext))
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("no-next partial sealed", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// The truthful second page and complete manifest seal.
	page1 := ipTVSetPageFor(staged, ids[1:], 1, 1, 2, nil)
	out, turn, tx = ipTVSetApplyPage(t, w, r, p, ipTVSetPageCommandFor(t, staged, 8, page1))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 8)
	complete := ipTVSetSealFor(staged, []ipTVSetSealPage{
		{Page: 0, NextPage: ipTVSetNextPage(1), Members: ids[:1]},
		{Page: 1, NextPage: nil, Members: ids[1:]},
	})
	out, turn, tx = ipTVSetApplySeal(t, w, r, p, ipTVSetSealCommandFor(t, staged, 9, complete))
	if out.Outcome.State != Applied || out.Value.MemberCount != 2 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_publication_sets WHERE id='tv-set-one' AND complete=1 AND coverage_after_item=2`); got != 1 {
		t.Fatal("complete set not sealed", got)
	}
}

func TestIPTVSetDuplicateAndForeignEpisodes(t *testing.T) {
	w, r, p, staged := ipTVSetFixture(t)
	episodes := []ipTVSetEpisodeSeed{
		{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1},
		{EvidenceID: "episode-2", SeasonNumber: 1, EpisodeNumber: 2, AbsoluteNumber: 2},
		{EvidenceID: "episode-3", SeasonNumber: 1, EpisodeNumber: 3, AbsoluteNumber: 3},
	}
	ids := ipTVSetSeedEpisodes(t, w, staged, episodes)
	begin := ipTVSetBeginFor(staged, ids)
	out, turn, tx := ipTVSetApplyBegin(t, w, r, p, ipTVSetBeginCommandFor(t, staged, 6, begin))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 6)
	page0 := ipTVSetPageFor(staged, ids[:2], 0, 0, 1, ipTVSetNextPage(1))
	out, turn, tx = ipTVSetApplyPage(t, w, r, p, ipTVSetPageCommandFor(t, staged, 7, page0))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 7)

	// A duplicate across pages is refused by the set/evidence unique.
	duplicate := ipTVSetPageFor(staged, []string{ids[0]}, 1, 2, 2, nil)
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetPage)
	denied, err := applyIPTVSetPage(t.Context(), turn, p, t.Context(), b,
		ipTVSetPageCommandFor(t, staged, 8, duplicate))
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate episode page admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_publication_page_members WHERE set_id='tv-set-one'`); got != 2 {
		t.Fatal("duplicate page wrote members", got)
	}

	// An unsealed episode evidence body cannot enter the set.
	ipPolicyExec(t, w, `UPDATE metadata_evidence SET sealed=0 WHERE id='episode-3'`)
	unsealed := ipTVSetPageFor(staged, ids[2:], 1, 2, 2, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPTVSetPage)
	denied, err = applyIPTVSetPage(t.Context(), turn, p, t.Context(), b,
		ipTVSetPageCommandFor(t, staged, 8, unsealed))
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("unsealed episode admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `UPDATE metadata_evidence SET sealed=1 WHERE id='episode-3'`)

	// A foreign-provider episode is not this set's member namespace.
	foreignDigest := sha256.Sum256([]byte("episode-foreign"))
	foreignIncarnation := ipEntityJobIncarnation(900)
	ipPolicyExec(t, w, `INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(900,'tvdb','episode','tvdb-episode','en','US','ip.tvdb.episode.detail.v1',?,1)`,
		foreignIncarnation[:])
	ipPolicyExec(t, w, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,sealed,ip_seal_owner,ip_seal_incarnation,ip_seal_sequence) VALUES('episode-foreign',900,'ip.tvdb.episode.v1','tvdb','episode','tvdb-episode','en','US','ip.tvdb.episode.detail.v1',?,'2026-01-01T00:00:00Z','https://api.thetvdb.com/series/1/episodes/1',1,?,?,1001)`,
		hex.EncodeToString(foreignDigest[:]), staged.owner.OperationID, staged.claim.Incarnation[:])
	foreign := ipTVSetPageFor(staged, []string{"episode-foreign"}, 1, 2, 2, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPTVSetPage)
	denied, err = applyIPTVSetPage(t.Context(), turn, p, t.Context(), b,
		ipTVSetPageCommandFor(t, staged, 8, foreign))
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("foreign episode admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// A seal manifest with a duplicate member is a shape refusal.
	seal := ipTVSetSealFor(staged, []ipTVSetSealPage{
		{Page: 0, NextPage: ipTVSetNextPage(1), Members: ids[:2]},
		{Page: 1, NextPage: nil, Members: []string{ids[0]}},
	})
	if c, err := newIPTVSetSealCommand(ipEntityEvidenceIdentity(staged.claim, 9), seal); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate seal admitted", c, err)
	}
}

func TestIPTVSetSourceAndHierarchyChanges(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want error
	}{
		{"source_revision", `UPDATE catalog_entities SET source_revision=2 WHERE id=1`, ErrConflict},
		{"metadata_revision", `UPDATE catalog_entities SET metadata_revision=2,revision=2 WHERE id=1`, ErrConflict},
		{"hierarchy_revision", `UPDATE metadata_provider_operations SET hierarchy_revision=1 WHERE provider='tmdb'`, ErrConflict},
		{"selection_revision", `UPDATE metadata_provider_operations SET selection_revision=1 WHERE provider='tmdb'`, ErrConflict},
		{"policy_revision", `UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, staged := ipTVSetFixture(t)
			episodes := []ipTVSetEpisodeSeed{
				{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1},
			}
			ids := ipTVSetSeedEpisodes(t, w, staged, episodes)
			begin := ipTVSetBeginFor(staged, ids)
			out, turn, tx := ipTVSetApplyBegin(t, w, r, p, ipTVSetBeginCommandFor(t, staged, 6, begin))
			if out.Outcome.State != Applied {
				t.Fatal(out)
			}
			ipPolicyCommitTurn(t, turn, tx)
			ipEntityJobAck(t, w, ipEntityJobResult{OperationID: staged.owner.OperationID, Provider: staged.owner.Provider}, 6)
			ipPolicyExec(t, w, tc.sql)
			page := ipTVSetPageFor(staged, ids, 0, 0, 1, nil)
			turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetPage)
			denied, err := applyIPTVSetPage(t.Context(), turn, p, t.Context(), b,
				ipTVSetPageCommandFor(t, staged, 7, page))
			if denied != (ipTVSetCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale source/hierarchy page admitted", denied, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_publication_pages`); got != 0 {
				t.Fatal("stale page wrote", got)
			}
		})
	}
}

func TestIPTVSetCurrentAuthorityAndLifetime(t *testing.T) {
	commands := []struct {
		name    string
		purpose CommandID
		prepare func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, staged ipEntitySealStaged) (*ipTVSetCommand, int64)
		apply   func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context, ipPolicyBudget, *ipTVSetCommand) (ipTVSetCommandResult, error)
	}{
		{"begin", IPTVSetBegin, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, staged ipEntitySealStaged) (*ipTVSetCommand, int64) {
			ids := ipTVSetSeedEpisodes(t, w, staged, []ipTVSetEpisodeSeed{{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1}})
			return ipTVSetBeginCommandFor(t, staged, 6, ipTVSetBeginFor(staged, ids)), 6
		}, applyIPTVSetBegin},
		{"page", IPTVSetPage, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, staged ipEntitySealStaged) (*ipTVSetCommand, int64) {
			partial := ipTVSetStagePartial(t, w, r, p, staged)
			return ipTVSetPageCommandFor(t, staged, partial.sequence, partial.page1), partial.sequence
		}, applyIPTVSetPage},
		{"seal", IPTVSetSeal, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, staged ipEntitySealStaged) (*ipTVSetCommand, int64) {
			set := ipTVSetStage(t, w, r, p, staged)
			return ipTVSetSealCommandFor(t, staged, set.sequence, set.seal), set.sequence
		}, applyIPTVSetSeal},
	}
	for _, command := range commands {
		for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
			t.Run(command.name+"/"+mode, func(t *testing.T) {
				w, r, p, staged := ipTVSetFixture(t)
				c, _ := command.prepare(t, w, r, p, staged)
				proof := ipPolicyRevoke(t, w, p, mode)
				turn, tx, b := ipPolicyTurn(t, w, r, command.purpose)
				out, err := command.apply(t.Context(), turn, proof, t.Context(), b, c)
				if out != (ipTVSetCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal("stale current authority admitted", out, err)
				}
				ipPolicyRollbackTurn(t, turn, tx)
			})
		}
		t.Run(command.name+"/cancelled_lifetime", func(t *testing.T) {
			w, r, p, staged := ipTVSetFixture(t)
			c, _ := command.prepare(t, w, r, p, staged)
			lifetime, cancel := context.WithCancel(t.Context())
			cancel()
			turn, tx, b := ipPolicyTurn(t, w, r, command.purpose)
			out, err := command.apply(lifetime, turn, p, t.Context(), b, c)
			if out != (ipTVSetCommandResult{}) || err == nil {
				t.Fatal("cancelled lifetime admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPTVSetUnknownCommitFreshRead(t *testing.T) {
	cases := []struct {
		name    string
		purpose CommandID
		stage   func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, staged ipEntitySealStaged) *ipTVSetCommand
		apply   func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context, ipPolicyBudget, *ipTVSetCommand) (ipTVSetCommandResult, error)
		queries map[string]int64
	}{
		{"begin", IPTVSetBegin, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, staged ipEntitySealStaged) *ipTVSetCommand {
			ids := ipTVSetSeedEpisodes(t, w, staged, []ipTVSetEpisodeSeed{{EvidenceID: "episode-1", SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1}})
			return ipTVSetBeginCommandFor(t, staged, 6, ipTVSetBeginFor(staged, ids))
		}, applyIPTVSetBegin, map[string]int64{
			`SELECT count(*) FROM metadata_publication_sets WHERE id='tv-set-one' AND complete=0 AND revision=1`: 1,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='set_begin'`:                           1,
		}},
		{"page", IPTVSetPage, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, staged ipEntitySealStaged) *ipTVSetCommand {
			partial := ipTVSetStagePartial(t, w, r, p, staged)
			return ipTVSetPageCommandFor(t, staged, partial.sequence, partial.page1)
		}, applyIPTVSetPage, map[string]int64{
			`SELECT count(*) FROM metadata_publication_pages WHERE set_id='tv-set-one' AND page=1`: 1,
			`SELECT count(*) FROM metadata_publication_page_members WHERE set_id='tv-set-one'`:     3,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='set_page'`:              2,
		}},
		{"seal", IPTVSetSeal, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, staged ipEntitySealStaged) *ipTVSetCommand {
			set := ipTVSetStage(t, w, r, p, staged)
			return ipTVSetSealCommandFor(t, staged, set.sequence, set.seal)
		}, applyIPTVSetSeal, map[string]int64{
			`SELECT count(*) FROM metadata_publication_sets WHERE id='tv-set-one' AND complete=1`:                          1,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='set_seal' AND terminal=1 AND proof_pin_count=1`: 1,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, staged := ipTVSetFixture(t)
			c := tc.stage(t, w, r, p, staged)
			path := ipPolicyDatabasePath(t, w)
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
			turn, tx, b := ipPolicyTurn(t, w, r, tc.purpose)
			out, err := tc.apply(t.Context(), turn, p, t.Context(), b, c)
			if err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			result, err := tx.commit()
			if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
				t.Fatal(result, err)
			}
			// The uncertain frame is closed; it never reserves the saved result.
			if again, e := tc.apply(t.Context(), turn, p, t.Context(), b, c); again != (ipTVSetCommandResult{}) || e == nil {
				t.Fatal("uncertain set frame reserved", again, e)
			}
			for query, want := range tc.queries {
				if got := ipPolicyFreshInt(t, path, query); got != want {
					t.Fatal("durable set outcome", query, got)
				}
			}
		})
	}
}

func TestIPTVSetConflictingRequestAndSequence(t *testing.T) {
	w, r, p, staged := ipTVSetFixture(t)
	set := ipTVSetStage(t, w, r, p, staged)
	out, turn, tx := ipTVSetApplySeal(t, w, r, p, ipTVSetSealCommandFor(t, staged, set.sequence, set.seal))
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)

	changed := set.seal
	changed.buildRevision++
	conflicting, err := newIPTVSetSealCommand(ipEntityEvidenceIdentity(staged.claim, set.sequence), changed)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPTVSetSeal)
	denied, err := applyIPTVSetSeal(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting set request admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	otherIdentity := ipEntityEvidenceIdentity(staged.claim, set.sequence)
	otherIdentity.OperationID = [16]byte{0x6b, 0xff}
	other, err := newIPTVSetSealCommand(otherIdentity, set.seal)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPTVSetSeal)
	denied, err = applyIPTVSetSeal(t.Context(), turn, p, t.Context(), b, other)
	if denied != (ipTVSetCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting set operation admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='set_seal'`); got != 1 {
		t.Fatal("conflicting set seal wrote a second receipt", got)
	}
}

func TestIPTVSetDescriptors(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: IPTVSetBegin, Name: "ip_tv_set_begin", Durability: Normal},
		{ID: IPTVSetPage, Name: "ip_tv_set_page", Durability: Normal},
		{ID: IPTVSetSeal, Name: "ip_tv_set_seal", Durability: Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
			t.Fatal(actual, err)
		}
	}
}
