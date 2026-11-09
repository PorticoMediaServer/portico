package writer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// The person operation, build, receipt header and exact-date carriers are group
// D and fixture-qualified; the alias/external children are the embedded generic
// evidence rows. These helpers stage one exact person observation and its
// append chain without any latest/MAX selection.
type ipPersonEvidenceStaged struct {
	claim    ipPersonResult
	claimID  CommandIdentity
	begin    ipPersonResult
	beginID  CommandIdentity
	beginReq ipPersonEvidenceBeginRequest
}

func ipPersonAliasPageRows(first, count int) []ipPersonEvidenceAlias {
	out := make([]ipPersonEvidenceAlias, count)
	for i := range out {
		out[i] = ipPersonEvidenceAlias{
			RequestedID: fmt.Sprintf("requested-%d", first+i),
			CanonicalID: "42",
			Alias:       fmt.Sprintf("Alias %d", first+i),
			ObservedAt:  "2026-01-01T00:00:00Z",
		}
	}
	return out
}

func ipPersonExternalPageRows(first, count int) []ipPersonEvidenceExternal {
	out := make([]ipPersonEvidenceExternal, count)
	for i := range out {
		out[i] = ipPersonEvidenceExternal{
			Provider: "imdb", SubjectKind: "person",
			ProviderID: fmt.Sprintf("nm%07d", first+i),
		}
	}
	return out
}

func ipPersonBeginRequestFor(t *testing.T, first ipPersonResult, aliases, external int) ipPersonEvidenceBeginRequest {
	t.Helper()
	q := ipPersonEvidenceBeginRequest{
		operationID: first.OperationID, provider: first.Provider, providerID: first.ProviderID,
		language: "en-US", region: "US",
		requestShape: "tmdb.person.detail.header-exact-dates.v1", lease: first.Lease,
		personID: first.PersonID, subjectID: 501, generation: first.Generation, revision: first.Revision,
		leaseGeneration: first.LeaseGeneration, leaseUntilMS: first.LeaseUntilMS,
		buildID: "build-person-1", buildGeneration: first.Generation,
		header:     ipPersonHeaderInput(),
		birthDate:  EvidenceText{Present: true, Value: "1815-12-10"},
		deathDate:  EvidenceText{Present: true, Value: "1852-11-27"},
		birthPlace: EvidenceText{Present: true, Value: "London"},
		aliases:    ipPersonAliasPageRows(0, aliases),
		external:   ipPersonExternalPageRows(0, external),
	}
	header, err := NewEvidenceHeader(q.header)
	if err != nil {
		t.Fatal(err)
	}
	commitment, total, _, err := ipPersonEvidenceManifest(q, header)
	if err != nil {
		t.Fatal(err)
	}
	q.expectedCommitment = commitment
	q.expectedBytes = total
	q.expectedRows = 1 + int64(q.children())
	return q
}

func ipPersonEvidenceStage(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	aliases, external int) ipPersonEvidenceStaged {
	t.Helper()
	claim, claimID := ipPersonClaimed(t, w, r, p)
	q := ipPersonBeginRequestFor(t, claim, aliases, external)
	beginID := ipPersonIdentity(2, 0x61)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
	out, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b,
		mustIPPersonBegin(t, beginID, q))
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPersonAck(t, w, claim.OperationID, 2)
	return ipPersonEvidenceStaged{claim: claim, claimID: claimID, begin: out.Value,
		beginID: beginID, beginReq: q}
}

func ipPersonAppendFor(begin ipPersonResult, family string, after, before, familyRows, buildRevision int64) ipPersonEvidenceAppendRequest {
	return ipPersonEvidenceAppendRequest{
		operationID: begin.OperationID, provider: begin.Provider, providerID: begin.ProviderID,
		lease: begin.Lease, personID: begin.PersonID, generation: begin.Generation,
		revision: begin.Revision, leaseGeneration: begin.LeaseGeneration,
		leaseUntilMS: begin.LeaseUntilMS, evidenceID: begin.EvidenceID, buildID: begin.BuildID,
		buildGeneration: begin.BuildGeneration, buildRevision: buildRevision,
		family: family, familyIndex: int64(ipPersonFamilyIndex(family)),
		familyStart: before - after, familyRows: familyRows,
		afterOrdinal: after, materializedBefore: before,
	}
}

func mustIPPersonAppend(t *testing.T, identity CommandIdentity, q ipPersonEvidenceAppendRequest) *ipPersonCommand {
	t.Helper()
	c, err := newIPPersonEvidenceAppendCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipPersonEvidenceApplyAppend(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipPersonCommand) (ipPersonCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	out, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("append", out, err)
	}
	return out, turn, tx
}

// ipPersonEvidenceChain applies pages in exact append order and keeps the
// caller's declared child lists.
type ipPersonEvidenceChain struct {
	staged       ipPersonEvidenceStaged
	aliases      []ipPersonEvidenceAlias
	external     []ipPersonEvidenceExternal
	content      int64
	sequence     int64
	before       int64
	aliasNext    int64
	extNext      int64
	sealCommand  *ipPersonSealCommand
	sealIdentity CommandIdentity
	sealResult   ipPersonSealCommandResult
}

func ipPersonEvidenceNewChain(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	aliases, external int) *ipPersonEvidenceChain {
	t.Helper()
	staged := ipPersonEvidenceStage(t, w, r, p, aliases, external)
	return &ipPersonEvidenceChain{staged: staged, aliases: staged.beginReq.aliases,
		external: staged.beginReq.external, content: 1, sequence: 3, before: 1}
}

func (c *ipPersonEvidenceChain) aliasPage(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof, rows []ipPersonEvidenceAlias) ipPersonAppendStep {
	t.Helper()
	q := ipPersonAppendFor(c.staged.begin, ipPersonFamilyAlias, c.aliasNext, c.before,
		int64(len(c.aliases)), c.content)
	q.aliases = rows
	return c.apply(t, w, r, p, q)
}

func (c *ipPersonEvidenceChain) externalPage(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof, rows []ipPersonEvidenceExternal) ipPersonAppendStep {
	t.Helper()
	q := ipPersonAppendFor(c.staged.begin, ipPersonFamilyExternal, c.extNext, c.before,
		int64(len(c.external)), c.content)
	q.external = rows
	return c.apply(t, w, r, p, q)
}

type ipPersonAppendStep struct {
	command  *ipPersonCommand
	identity CommandIdentity
	request  ipPersonEvidenceAppendRequest
	value    ipPersonResult
}

func (c *ipPersonEvidenceChain) apply(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof, q ipPersonEvidenceAppendRequest) ipPersonAppendStep {
	t.Helper()
	identity := ipPersonIdentity(c.sequence, byte(0x70+c.sequence))
	command := mustIPPersonAppend(t, identity, q)
	out, turn, tx := ipPersonEvidenceApplyAppend(t, w, r, p, command)
	if out.Outcome.State != Applied || out.Outcome.Replayed {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPersonAck(t, w, c.staged.claim.OperationID, c.sequence)
	rows := q.counts()
	c.aliasNext += int64(rows[0])
	c.extNext += int64(rows[1])
	c.before += int64(rows[0] + rows[1])
	c.content++
	c.sequence++
	return ipPersonAppendStep{command: command, identity: identity, request: q, value: out.Value}
}

func TestIPPersonEvidenceAppendFreshReplayAndRetired(t *testing.T) {
	w, r, p, base := ipPersonFixture(t)
	chain := ipPersonEvidenceNewChain(t, w, r, p, 3, 2)
	ipPersonAdvance(t, w, base, time.Second)
	headerBytes := chain.staged.begin.ResultBytes
	firstPage := chain.aliases[:2]
	// Apply the page without acknowledging it: the replay below exercises the
	// unacknowledged frame, so the chain helper's automatic ACK would retire it
	// before the original page could be served again.
	q := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyAlias, chain.aliasNext, chain.before,
		int64(len(chain.aliases)), chain.content)
	q.aliases = firstPage
	identity := ipPersonIdentity(chain.sequence, 0x73)
	command := mustIPPersonAppend(t, identity, q)
	out, turn, tx := ipPersonEvidenceApplyAppend(t, w, r, p, command)
	step := ipPersonAppendStep{command: command, identity: identity, request: q, value: out.Value}
	if step.value.Kind != "append" || step.value.Revision != 2 ||
		step.value.MaterializedRows != 3 || step.value.ResultCount != 2 ||
		step.value.ResultBytes <= 0 || step.value.SubjectKind != "person" ||
		step.value.EvidenceID != chain.staged.begin.EvidenceID ||
		step.value.BuildID != chain.staged.begin.BuildID ||
		step.value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(step.value)
	}
	ipPolicyCommitTurn(t, turn, tx)
	pageBytes := step.value.ResultBytes
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id='` + chain.staged.begin.EvidenceID + `' AND ordinal IN(0,1)`:                                         2,
		`SELECT count(*) FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `' AND state='building' AND cursor=3 AND staged_rows=3 AND content_revision=2`: 1,
		`SELECT staged_bytes FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`:                                                                        headerBytes + pageBytes,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append' AND after_ordinal=3 AND accepted_revision=2 AND result_count=2`:                             1,
		`SELECT last_command_sequence FROM metadata_person_operations WHERE id='` + chain.staged.begin.OperationID + `'`:                                                       3,
		`SELECT count(*) FROM metadata_person_builds WHERE state<>'building' OR seal_owner IS NOT NULL`:                                                                        0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// The saved page replays before any fresh build, lease, person or policy
	// probe. Every one of those witnesses is deliberately torn here.
	original := step
	ipPolicyExec(t, w, `UPDATE metadata_person_builds SET state='done' WHERE id='`+chain.staged.begin.BuildID+`'`)
	ipPolicyExec(t, w, `UPDATE metadata_evidence SET title='changed' WHERE id='`+chain.staged.begin.EvidenceID+`'`)
	ipPolicyExec(t, w, `UPDATE catalog_people SET revision=9 WHERE id=42`)
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET revision=9,lease_until_ms=1 WHERE id='`+chain.staged.begin.OperationID+`'`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	replay, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, original.command)
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.value ||
		replay.Digest == (ResultDigest{}) {
		t.Fatal("append replay after lease change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append'`: 1,
		`SELECT count(*) FROM metadata_evidence_aliases`:                            2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("append replay wrote rows", query, got)
		}
	}
	// At or below ACK the original append is Retired while its proof remains.
	ipPersonAck(t, w, chain.staged.claim.OperationID, 3)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	retired, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, original.command)
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipPersonResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired append served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append'`); got != 1 {
		t.Fatal("retired append lost its receipt", got)
	}
}

func TestIPPersonEvidenceAppendFullChainContiguous(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonEvidenceNewChain(t, w, r, p, 2, 2)
	if chain.staged.beginReq.expectedRows != 5 || chain.staged.beginReq.expectedBytes <= 0 {
		t.Fatal(chain.staged.beginReq.expectedRows, chain.staged.beginReq.expectedBytes)
	}
	one := chain.aliasPage(t, w, r, p, chain.aliases[:1])
	two := chain.aliasPage(t, w, r, p, chain.aliases[1:])
	three := chain.externalPage(t, w, r, p, chain.external[:1])
	four := chain.externalPage(t, w, r, p, chain.external[1:])
	for i, step := range []ipPersonAppendStep{one, two, three, four} {
		want := ipPersonResult{Kind: "append", Revision: int64(2 + i), MaterializedRows: int64(2 + i),
			ResultCount: 1}
		if step.value.Kind != want.Kind || step.value.Revision != want.Revision ||
			step.value.MaterializedRows != want.MaterializedRows || step.value.ResultCount != 1 {
			t.Fatal(i, step.value)
		}
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id='` + chain.staged.begin.EvidenceID + `' AND ordinal IN(0,1)`:            2,
		`SELECT count(*) FROM metadata_evidence_external_ids WHERE evidence_id='` + chain.staged.begin.EvidenceID + `' AND subject_kind='person'`: 2,
		`SELECT cursor FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`:                                                 5,
		`SELECT staged_rows FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`:                                            5,
		`SELECT content_revision FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`:                                       5,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append' AND after_ordinal IN(2,3,4,5)`:                                 4,
		`SELECT count(*) FROM metadata_person_builds WHERE state<>'building' OR seal_owner IS NOT NULL`:                                           0,
		`SELECT count(*) FROM metadata_evidence WHERE sealed<>0`:                                                                                  0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id=? AND requested_id LIKE 'requested-%'`,
		chain.staged.begin.EvidenceID); got != 2 {
		t.Fatal("alias rows not exact", got)
	}
	// A coherent page whose family position is behind the persisted count is a
	// gap, and a page beyond the declared family count is refused by shape.
	gap := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyAlias, 1, 5, 3, chain.content)
	gap.aliases = chain.aliases[1:2]
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	out, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b,
		mustIPPersonAppend(t, ipPersonIdentity(chain.sequence, 0x7f), gap))
	if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("gapped page admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	beyond := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyAlias, 2, 5, 2, chain.content)
	beyond.aliases = chain.aliases[1:2]
	if c, e := newIPPersonEvidenceAppendCommand(ipPersonIdentity(chain.sequence, 0x7f), beyond); c != nil || !errors.Is(e, ErrInvalid) {
		t.Fatal("page beyond declared family admitted", c, e)
	}
	// A page that repeats an already persisted external tuple cannot be admitted
	// once the declared row count is complete.
	duplicate := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyExternal, 2, 5, 3, chain.content)
	duplicate.external = []ipPersonEvidenceExternal{chain.external[0]}
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	out, err = applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b,
		mustIPPersonAppend(t, ipPersonIdentity(chain.sequence, 0x7e), duplicate))
	if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate external admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_evidence_aliases`:                                          2,
		`SELECT count(*) FROM metadata_evidence_external_ids`:                                     2,
		`SELECT cursor FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`: 5,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append'`:               4,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("refused page changed state", query, got)
		}
	}
}

func TestIPPersonEvidenceAppendShapeRefusals(t *testing.T) {
	begin := ipPersonResult{Kind: "begin", OperationID: strings.Repeat("a", 32), PersonID: 42,
		Provider: "tmdb", ProviderID: "42", Lease: strings.Repeat("b", 32), Generation: 1,
		Revision: 2, LeaseGeneration: 1, LeaseUntilMS: 300000, BuildGeneration: 1,
		EvidenceID: "evidence-one", BuildID: "build-person-1"}
	base := ipPersonAppendFor(begin, ipPersonFamilyAlias, 0, 1, 1, 1)
	base.aliases = ipPersonAliasPageRows(0, 1)
	identity := ipPersonIdentity(3, 0x70)
	mutations := []struct {
		name   string
		mutate func(*ipPersonEvidenceAppendRequest)
	}{
		{"empty_operation", func(q *ipPersonEvidenceAppendRequest) { q.operationID = "" }},
		{"short_operation", func(q *ipPersonEvidenceAppendRequest) { q.operationID = "short" }},
		{"unknown_provider", func(q *ipPersonEvidenceAppendRequest) { q.provider = "plex" }},
		{"empty_provider_id", func(q *ipPersonEvidenceAppendRequest) { q.providerID = "" }},
		{"long_provider_id", func(q *ipPersonEvidenceAppendRequest) { q.providerID = strings.Repeat("p", ipPersonProviderIDMax+1) }},
		{"empty_lease", func(q *ipPersonEvidenceAppendRequest) { q.lease = "" }},
		{"long_lease", func(q *ipPersonEvidenceAppendRequest) { q.lease = strings.Repeat("l", ipPersonPointerMax+1) }},
		{"zero_person", func(q *ipPersonEvidenceAppendRequest) { q.personID = 0 }},
		{"zero_generation", func(q *ipPersonEvidenceAppendRequest) { q.generation = 0 }},
		{"max_generation", func(q *ipPersonEvidenceAppendRequest) { q.generation = math.MaxInt64 }},
		{"zero_revision", func(q *ipPersonEvidenceAppendRequest) { q.revision = 0 }},
		{"zero_lease_generation", func(q *ipPersonEvidenceAppendRequest) { q.leaseGeneration = 0 }},
		{"zero_lease_until", func(q *ipPersonEvidenceAppendRequest) { q.leaseUntilMS = 0 }},
		{"empty_evidence", func(q *ipPersonEvidenceAppendRequest) { q.evidenceID = "" }},
		{"empty_build", func(q *ipPersonEvidenceAppendRequest) { q.buildID = "" }},
		{"zero_build_generation", func(q *ipPersonEvidenceAppendRequest) { q.buildGeneration = 0 }},
		{"build_generation_mismatch", func(q *ipPersonEvidenceAppendRequest) { q.buildGeneration = 2 }},
		{"zero_build_revision", func(q *ipPersonEvidenceAppendRequest) { q.buildRevision = 0 }},
		{"negative_family_index", func(q *ipPersonEvidenceAppendRequest) { q.familyIndex = -1 }},
		{"over_family_index", func(q *ipPersonEvidenceAppendRequest) { q.familyIndex = 2 }},
		{"wrong_family_name", func(q *ipPersonEvidenceAppendRequest) { q.family = "term" }},
		{"zero_family_start", func(q *ipPersonEvidenceAppendRequest) { q.familyStart = 0 }},
		{"negative_family_rows", func(q *ipPersonEvidenceAppendRequest) { q.familyRows = -1 }},
		{"negative_after", func(q *ipPersonEvidenceAppendRequest) { q.afterOrdinal = -1 }},
		{"zero_materialized", func(q *ipPersonEvidenceAppendRequest) { q.materializedBefore = 0 }},
		{"cursor_math", func(q *ipPersonEvidenceAppendRequest) { q.materializedBefore = 3 }},
		{"empty_page", func(q *ipPersonEvidenceAppendRequest) { q.aliases = nil }},
		{"mixed_family", func(q *ipPersonEvidenceAppendRequest) { q.external = ipPersonExternalPageRows(0, 1) }},
		{"alias_not_canonical", func(q *ipPersonEvidenceAppendRequest) { q.aliases[0].CanonicalID = "99" }},
		{"alias_empty_requested", func(q *ipPersonEvidenceAppendRequest) { q.aliases[0].RequestedID = "" }},
		{"alias_empty_observed", func(q *ipPersonEvidenceAppendRequest) { q.aliases[0].ObservedAt = "" }},
		{"alias_bad_observed", func(q *ipPersonEvidenceAppendRequest) { q.aliases[0].ObservedAt = "tomorrow" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			q.aliases = append([]ipPersonEvidenceAlias(nil), base.aliases...)
			q.external = nil
			tc.mutate(&q)
			if c, err := newIPPersonEvidenceAppendCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	t.Run("oversized_page", func(t *testing.T) {
		q := ipPersonAppendFor(begin, ipPersonFamilyAlias, 0, 1, int64(ipPersonMaxChildren+1), 1)
		q.aliases = ipPersonAliasPageRows(0, ipPersonMaxChildren+1)
		if c, err := newIPPersonEvidenceAppendCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(c, err)
		}
	})
	t.Run("oversized_children", func(t *testing.T) {
		q := ipPersonAppendFor(begin, ipPersonFamilyAlias, 0, 1, 1, 1)
		q.aliases = ipPersonAliasPageRows(0, ipPersonMaxChildren)
		q.external = ipPersonExternalPageRows(0, 1)
		if c, err := newIPPersonEvidenceAppendCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(c, err)
		}
	})
	t.Run("external_kind", func(t *testing.T) {
		q := ipPersonAppendFor(begin, ipPersonFamilyExternal, 0, 1, 1, 1)
		q.external = []ipPersonEvidenceExternal{{Provider: "imdb", SubjectKind: "series", ProviderID: "nm1"}}
		if c, err := newIPPersonEvidenceAppendCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(c, err)
		}
	})
	t.Run("external_unsorted", func(t *testing.T) {
		q := ipPersonAppendFor(begin, ipPersonFamilyExternal, 0, 1, 2, 1)
		q.external = ipPersonExternalPageRows(1, 1)
		q.external = append(q.external, ipPersonExternalPageRows(0, 1)...)
		if c, err := newIPPersonEvidenceAppendCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(c, err)
		}
	})
	t.Run("external_empty_provider", func(t *testing.T) {
		q := ipPersonAppendFor(begin, ipPersonFamilyExternal, 0, 1, 1, 1)
		q.external = []ipPersonEvidenceExternal{{SubjectKind: "person", ProviderID: "nm1"}}
		if c, err := newIPPersonEvidenceAppendCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(c, err)
		}
	})
	t.Run("maximum_page", func(t *testing.T) {
		q := ipPersonAppendFor(begin, ipPersonFamilyAlias, 0, 1, ipPersonMaxChildren, 1)
		q.aliases = ipPersonAliasPageRows(0, ipPersonMaxChildren)
		if c, err := newIPPersonEvidenceAppendCommand(identity, q); err != nil || c == nil {
			t.Fatal("maximum page refused", err)
		}
	})
}

func TestIPPersonEvidenceAppendRejectsForeignStaleAndGapped(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipPersonEvidenceAppendRequest)
		sql    []string
		want   error
	}{
		{"evidence", func(q *ipPersonEvidenceAppendRequest) { q.evidenceID = "evidence-other" }, nil, ErrConflict},
		{"build", func(q *ipPersonEvidenceAppendRequest) { q.buildID = "build-other" }, nil, ErrConflict},
		{"build_generation", nil, []string{`UPDATE metadata_person_builds SET generation=2`}, ErrConflict},
		{"build_revision", func(q *ipPersonEvidenceAppendRequest) { q.buildRevision = 1 }, nil, ErrConflict},
		{"provider", func(q *ipPersonEvidenceAppendRequest) { q.provider = "tvdb" }, nil, ErrResolveRequired},
		{"provider_id", func(q *ipPersonEvidenceAppendRequest) { q.providerID = "99" }, nil, ErrResolveRequired},
		{"person", func(q *ipPersonEvidenceAppendRequest) { q.personID = 99 }, nil, ErrResolveRequired},
		{"lease_owner", func(q *ipPersonEvidenceAppendRequest) { q.lease = strings.Repeat("0", 32) }, nil, ErrConflict},
		{"lease_generation", func(q *ipPersonEvidenceAppendRequest) { q.leaseGeneration = 4 }, nil, ErrConflict},
		{"revision", func(q *ipPersonEvidenceAppendRequest) { q.revision = 9 }, nil, ErrConflict},
		{"stale_lease", nil, []string{`UPDATE metadata_person_operations SET lease_until_ms=1`}, ErrConflict},
		{"stale_person", nil, []string{`UPDATE catalog_people SET revision=2 WHERE id=42`}, ErrConflict},
		{"stale_subject", nil, []string{`UPDATE metadata_provider_subjects SET revision=2 WHERE id=501`}, ErrConflict},
		{"stale_policy", nil, []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"revoked_consent", nil, []string{`UPDATE screen_metadata_consent SET confirmed=0`}, ErrDisclosureDenied},
		{"owner_phase", nil, []string{`UPDATE metadata_person_operations SET phase='sealed'`}, ErrConflict},
		{"build_not_open", nil, []string{`UPDATE metadata_person_builds SET state='done'`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipPersonFixture(t)
			chain := ipPersonEvidenceNewChain(t, w, r, p, 1, 1)
			chain.aliasPage(t, w, r, p, chain.aliases)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyExternal, 0, chain.before,
				int64(len(chain.external)), chain.content)
			q.external = chain.external
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			command := mustIPPersonAppend(t, ipPersonIdentity(chain.sequence, 0x7d), q)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
			out, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, command)
			if out != (ipPersonCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("foreign/stale append admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append'`:               1,
				`SELECT count(*) FROM metadata_evidence_external_ids`:                                     0,
				`SELECT cursor FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`: 2,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal("refused append wrote state", query, got)
				}
			}
		})
	}
	t.Run("expired_lease_at_append_time", func(t *testing.T) {
		w, r, p, base := ipPersonFixture(t)
		chain := ipPersonEvidenceNewChain(t, w, r, p, 1, 1)
		chain.aliasPage(t, w, r, p, chain.aliases)
		ipPersonAdvance(t, w, base, 6*time.Minute)
		q := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyExternal, 0, chain.before,
			int64(len(chain.external)), chain.content)
		q.external = chain.external
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
		out, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b,
			mustIPPersonAppend(t, ipPersonIdentity(chain.sequence, 0x7c), q))
		if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("expired append lease admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func ipPersonAppendAuthorityPrepare(t *testing.T) (*executor, *ipPolicyRegistry,
	*usOwnerAuthorityProof, *ipPersonCommand) {
	t.Helper()
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonEvidenceNewChain(t, w, r, p, 1, 1)
	chain.aliasPage(t, w, r, p, chain.aliases)
	q := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyExternal, 0, chain.before,
		int64(len(chain.external)), chain.content)
	q.external = chain.external
	return w, r, p, mustIPPersonAppend(t, ipPersonIdentity(chain.sequence, 0x7b), q)
}

func TestIPPersonEvidenceAppendCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, command := ipPersonAppendAuthorityPrepare(t)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
			out, err := applyIPPersonEvidenceAppend(t.Context(), turn, proof, t.Context(), b, command)
			if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append'`); got != 1 {
				t.Fatal("stale authority wrote a page receipt", got)
			}
		})
	}
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, command := ipPersonAppendAuthorityPrepare(t)
		lifetime, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
		out, err := applyIPPersonEvidenceAppend(lifetime, turn, p, t.Context(), b, command)
		if out != (ipPersonCommandResult{}) || err == nil {
			t.Fatal("cancelled lifetime admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPPersonEvidenceAppendUnknownCommitFreshRead(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonEvidenceNewChain(t, w, r, p, 1, 1)
	q := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyAlias, 0, 1,
		int64(len(chain.aliases)), chain.content)
	q.aliases = chain.aliases
	identity := ipPersonIdentity(3, 0x73)
	command := mustIPPersonAppend(t, identity, q)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	out, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, command)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	// The uncertain frame is closed; it never reserves the saved page.
	if again, e := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, command); again != (ipPersonCommandResult{}) || e == nil {
		t.Fatal("uncertain append frame reserved", again, e)
	}
	// Only a FRESH read-only connection may prove the durable page.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append' AND after_ordinal=2 AND accepted_revision=2`: 1,
		`SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id='` + chain.staged.begin.EvidenceID + `'`:              1,
		`SELECT cursor FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`:                               2,
		`SELECT content_revision FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`:                     2,
		`SELECT last_command_sequence FROM metadata_person_operations WHERE id='` + chain.staged.begin.OperationID + `'`:        3,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal("durable append", query, got)
		}
	}
}

func TestIPPersonEvidenceAppendConflictingRequestAndSequence(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonEvidenceNewChain(t, w, r, p, 1, 1)
	step := chain.aliasPage(t, w, r, p, chain.aliases)
	changed := ipPersonAppendFor(chain.staged.begin, ipPersonFamilyAlias, 0, 1,
		int64(len(chain.aliases)), 1)
	changed.aliases = []ipPersonEvidenceAlias{{
		RequestedID: "other", CanonicalID: "42", Alias: "Other", ObservedAt: "2026-01-01T00:00:00Z"}}
	conflicting := mustIPPersonAppend(t, step.identity, changed)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	out, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, conflicting)
	if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	other := step.identity
	other.OperationID = [16]byte{0x7f, 0x01}
	second := mustIPPersonAppend(t, other, step.request)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	out, err = applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, second)
	if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting operation admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append'`: 1,
		`SELECT count(*) FROM metadata_evidence_aliases`:                            1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("conflicting append wrote state", query, got)
		}
	}
}

func TestIPPersonEvidenceAppendDescriptors(t *testing.T) {
	expected := Descriptor{ID: IPPersonEvidenceAppend, Name: "ip_person_evidence_append", Durability: Normal}
	actual, err := Describe(expected.ID)
	if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
		t.Fatal(actual, err)
	}
	if IPPersonEvidenceAppend == IPEntityEvidenceAppend || IPPersonEvidenceAppend != 232 {
		t.Fatal("person append purpose aliased the entity append purpose")
	}
}
