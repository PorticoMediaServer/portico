package writer

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func ipPersonSealFor(begin ipPersonResult, beginReq ipPersonEvidenceBeginRequest, contentRevision int64) ipPersonSealRequest {
	return ipPersonSealRequest{operationID: begin.OperationID, provider: begin.Provider,
		providerID: begin.ProviderID, lease: begin.Lease, personID: begin.PersonID,
		generation: begin.Generation, revision: begin.Revision,
		leaseGeneration: begin.LeaseGeneration, leaseUntilMS: begin.LeaseUntilMS,
		evidenceID: begin.EvidenceID, buildID: begin.BuildID,
		buildGeneration: begin.BuildGeneration, contentRevision: contentRevision,
		header: beginReq.header, birthDate: beginReq.birthDate, deathDate: beginReq.deathDate,
		birthPlace: beginReq.birthPlace, aliases: beginReq.aliases, external: beginReq.external}
}

func mustIPPersonSeal(t *testing.T, identity CommandIdentity, q ipPersonSealRequest) *ipPersonSealCommand {
	t.Helper()
	c, err := newIPPersonSealCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipPersonSealApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipPersonSealCommand) (ipPersonSealCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
	out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("seal", out, err)
	}
	return out, turn, tx
}

func (c *ipPersonEvidenceChain) seal(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof) ipPersonSealCommandResult {
	t.Helper()
	c.sealIdentity = ipPersonIdentity(c.sequence, byte(0x70+c.sequence))
	c.sealCommand = mustIPPersonSeal(t, c.sealIdentity,
		ipPersonSealFor(c.staged.begin, c.staged.beginReq, c.content))
	out, turn, tx := ipPersonSealApply(t, w, r, p, c.sealCommand)
	if out.Outcome.State != Applied || out.Outcome.Replayed {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	c.sealResult = out
	return out
}

func ipPersonSealCompleteChain(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof) *ipPersonEvidenceChain {
	t.Helper()
	chain := ipPersonEvidenceNewChain(t, w, r, p, 2, 2)
	chain.aliasPage(t, w, r, p, chain.aliases[:1])
	chain.aliasPage(t, w, r, p, chain.aliases[1:])
	chain.externalPage(t, w, r, p, chain.external[:1])
	chain.externalPage(t, w, r, p, chain.external[1:])
	return chain
}

func TestIPPersonSealFreshReplayRetiredAndRetainedProof(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonSealCompleteChain(t, w, r, p)
	if chain.content != 5 || chain.sequence != 7 {
		t.Fatal(chain.content, chain.sequence)
	}
	out := chain.seal(t, w, r, p)
	manifestRows := chain.staged.beginReq.expectedRows
	manifestBytes := chain.staged.beginReq.expectedBytes
	if out.Value.Kind != "seal" || out.Value.EvidenceID != chain.staged.begin.EvidenceID ||
		out.Value.BuildID != chain.staged.begin.BuildID || out.Value.SubjectKind != "person" ||
		out.Value.ParserVersion != chain.staged.beginReq.header.ParserVersion ||
		out.Value.MaterializedRows != manifestRows || out.Value.ResultBytes != manifestBytes ||
		out.Value.ResultCount != 1 || out.Value.Commitment != chain.staged.beginReq.expectedCommitment ||
		out.Value.Revision != chain.staged.begin.Revision+1 || out.Value.Generation != 1 ||
		out.Value.Lease != chain.staged.begin.Lease || out.Digest == (ResultDigest{}) ||
		out.Value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(out)
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `' AND state='ready' AND seal_owner='` + chain.staged.begin.OperationID + `' AND seal_sequence=7 AND cursor=5 AND staged_rows=5 AND content_revision=6`: 1,
		`SELECT staged_bytes FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`:        manifestBytes,
		`SELECT materialized_charge FROM metadata_person_builds WHERE id='` + chain.staged.begin.BuildID + `'`: manifestBytes,
		`SELECT count(*) FROM metadata_evidence WHERE id='` + chain.staged.begin.EvidenceID + `' AND sealed=1`: 1,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='evidence_seal' AND after_ordinal=0 AND result_count=1 AND accepted_revision=6 AND seal_version=1 AND seal_rows=5 AND seal_bytes=` + ipWritingInt(manifestBytes) + ` AND seal_algorithm='` + ipPersonSealAlgorithm + `' AND proof_pin_count=1`: 1,
		`SELECT count(*) FROM metadata_person_operations WHERE id='` + chain.staged.begin.OperationID + `' AND phase='sealed' AND revision=3 AND last_command_sequence=7`:                                                                                                                                                1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	if got := ipPolicyString(t, w, `SELECT lower(hex(seal_commitment)) FROM metadata_person_receipts WHERE command_kind='evidence_seal'`); got != ipPersonHexBytes(chain.staged.beginReq.expectedCommitment[:]) {
		t.Fatal("seal commitment not retained", got)
	}
	// Replay after lease, owner, build, evidence and provider changes returns the
	// original saved seal before any fresh probe and writes nothing.
	original := out
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET revision=9,lease_generation=9,lease_until_ms=1 WHERE id='`+chain.staged.begin.OperationID+`'`)
	ipPolicyExec(t, w, `UPDATE metadata_person_builds SET state='done' WHERE id='`+chain.staged.begin.BuildID+`'`)
	ipPolicyExec(t, w, `UPDATE metadata_evidence SET title='changed',sealed=0 WHERE id='`+chain.staged.begin.EvidenceID+`'`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
	replay, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, chain.sealCommand)
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value ||
		replay.Digest != original.Digest {
		t.Fatal("seal replay after lease change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='evidence_seal'`); got != 1 {
		t.Fatal("seal replay wrote a receipt", got)
	}
	// At or below ACK the seal is Retired while the proof pins stay physical.
	// Restore the build state torn for the replay above: retirement must leave
	// the committed sealed proof exactly as the seal wrote it.
	ipPolicyExec(t, w, `UPDATE metadata_person_builds SET state='ready' WHERE id='`+chain.staged.begin.BuildID+`'`)
	ipPersonAck(t, w, chain.staged.begin.OperationID, 7)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonSeal)
	retired, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, chain.sealCommand)
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipPersonResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired seal served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='evidence_seal' AND seal_rows=5 AND proof_pin_count=1`: 1,
		`SELECT count(*) FROM metadata_person_builds WHERE state='ready' AND seal_sequence=7`:                                    1,
		`SELECT count(*) FROM metadata_evidence WHERE sealed=1`:                                                                  0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("retired seal lost its proof", query, got)
		}
	}
	// A sealed owner never appends another page.
	appendReq := ipPersonAppendFor(ipPersonResult{Kind: "seal", OperationID: chain.staged.begin.OperationID,
		PersonID: chain.staged.begin.PersonID, Provider: chain.staged.begin.Provider,
		ProviderID: chain.staged.begin.ProviderID, Lease: chain.staged.begin.Lease,
		Generation: chain.staged.begin.Generation, Revision: chain.staged.begin.Revision + 1,
		LeaseGeneration: chain.staged.begin.LeaseGeneration, LeaseUntilMS: chain.staged.begin.LeaseUntilMS,
		EvidenceID: chain.staged.begin.EvidenceID, BuildID: chain.staged.begin.BuildID,
		BuildGeneration: chain.staged.begin.BuildGeneration},
		ipPersonFamilyAlias, 2, 5, 3, chain.content)
	appendReq.aliases = []ipPersonEvidenceAlias{{RequestedID: "late", CanonicalID: "42",
		Alias: "Late", ObservedAt: "2026-01-01T00:00:00Z"}}
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	out2, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b,
		mustIPPersonAppend(t, ipPersonIdentity(8, 0x79), appendReq))
	if out2 != (ipPersonCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("sealed owner appended", out2, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPPersonSealHeaderOnlyEmptyFamilies(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonEvidenceNewChain(t, w, r, p, 0, 0)
	if chain.staged.beginReq.expectedRows != 1 {
		t.Fatal(chain.staged.beginReq.expectedRows)
	}
	out := chain.seal(t, w, r, p)
	if out.Value.MaterializedRows != 1 ||
		out.Value.ResultBytes != chain.staged.beginReq.expectedBytes ||
		out.Value.ResultBytes <= chain.staged.begin.ResultBytes {
		t.Fatal(out)
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_builds WHERE state='ready' AND cursor=1 AND staged_rows=1`:   1,
		`SELECT count(*) FROM metadata_evidence WHERE sealed=1`:                                            1,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='evidence_seal' AND seal_rows=1`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

func TestIPPersonSealShapeRefusals(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonSealCompleteChain(t, w, r, p)
	base := ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content)
	identity := ipPersonIdentity(chain.sequence, 0x7a)
	longText := strings.Repeat("x", ipPersonTextMax+1)
	mutations := []struct {
		name   string
		mutate func(*ipPersonSealRequest)
	}{
		{"empty_operation", func(q *ipPersonSealRequest) { q.operationID = "" }},
		{"short_operation", func(q *ipPersonSealRequest) { q.operationID = "short" }},
		{"unknown_provider", func(q *ipPersonSealRequest) { q.provider = "plex" }},
		{"empty_provider_id", func(q *ipPersonSealRequest) { q.providerID = "" }},
		{"empty_lease", func(q *ipPersonSealRequest) { q.lease = "" }},
		{"zero_person", func(q *ipPersonSealRequest) { q.personID = 0 }},
		{"zero_generation", func(q *ipPersonSealRequest) { q.generation = 0 }},
		{"max_generation", func(q *ipPersonSealRequest) { q.generation = math.MaxInt64 }},
		{"zero_revision", func(q *ipPersonSealRequest) { q.revision = 0 }},
		{"zero_lease_generation", func(q *ipPersonSealRequest) { q.leaseGeneration = 0 }},
		{"zero_lease_until", func(q *ipPersonSealRequest) { q.leaseUntilMS = 0 }},
		{"empty_evidence", func(q *ipPersonSealRequest) { q.evidenceID = "" }},
		{"empty_build", func(q *ipPersonSealRequest) { q.buildID = "" }},
		{"header_evidence_mismatch", func(q *ipPersonSealRequest) { q.header.ID = "evidence-other" }},
		{"zero_build_generation", func(q *ipPersonSealRequest) { q.buildGeneration = 0 }},
		{"build_generation_mismatch", func(q *ipPersonSealRequest) { q.buildGeneration = 2 }},
		{"zero_content_revision", func(q *ipPersonSealRequest) { q.contentRevision = 0 }},
		{"bad_birth_date", func(q *ipPersonSealRequest) { q.birthDate.Value = "1815-13-10" }},
		{"unexpected_birth", func(q *ipPersonSealRequest) { q.birthDate.Present = false }},
		{"bad_place", func(q *ipPersonSealRequest) { q.birthPlace.Present = false }},
		{"wrong_header_provider", func(q *ipPersonSealRequest) { q.header.Provider = "tvdb" }},
		{"wrong_header_provider_id", func(q *ipPersonSealRequest) { q.header.ProviderID = "99" }},
		{"series_subject", func(q *ipPersonSealRequest) { q.header.SubjectKind = "series" }},
		{"parser_not_person", func(q *ipPersonSealRequest) { q.header.ParserVersion = "tmdb.persondetail.v1" }},
		{"parser_equals_shape", func(q *ipPersonSealRequest) {
			q.header.ParserVersion = q.header.RequestShape
		}},
		{"zero_digest", func(q *ipPersonSealRequest) { q.header.ObservationDigest = [32]byte{} }},
		{"alias_not_canonical", func(q *ipPersonSealRequest) { q.aliases[0].CanonicalID = "99" }},
		{"alias_long_alias", func(q *ipPersonSealRequest) { q.aliases[0].Alias = longText }},
		{"external_kind", func(q *ipPersonSealRequest) { q.external[0].SubjectKind = "series" }},
		{"external_unsorted", func(q *ipPersonSealRequest) {
			q.external[0], q.external[1] = q.external[1], q.external[0]
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			q.aliases = append([]ipPersonEvidenceAlias(nil), base.aliases...)
			q.external = append([]ipPersonEvidenceExternal(nil), base.external...)
			tc.mutate(&q)
			if c, err := newIPPersonSealCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	t.Run("too_many_children", func(t *testing.T) {
		q := base
		q.aliases = ipPersonAliasPageRows(0, ipPersonMaxChildren+1)
		q.external = nil
		if c, err := newIPPersonSealCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(c, err)
		}
	})
	t.Run("over_bytes", func(t *testing.T) {
		q := base
		huge := strings.Repeat("x", ipPersonTextMax)
		q.aliases = make([]ipPersonEvidenceAlias, ipPersonMaxChildren)
		for i := range q.aliases {
			q.aliases[i] = ipPersonEvidenceAlias{RequestedID: "requested", CanonicalID: "42",
				Alias: huge, ObservedAt: "2026-01-01T00:00:00Z"}
		}
		q.external = nil
		if c, err := newIPPersonSealCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(c, err)
		}
	})
}

func TestIPPersonSealRejectsIncompleteForeignAndTampered(t *testing.T) {
	cases := []struct {
		name string
		sql  []string
		want error
	}{
		{"missing_alias", []string{`DELETE FROM metadata_evidence_aliases WHERE ordinal=1`}, ErrConflict},
		{"extra_alias", []string{`INSERT INTO metadata_evidence_aliases(evidence_id,ordinal,requested_id,canonical_id,alias,observed_at) VALUES((SELECT id FROM metadata_evidence LIMIT 1),2,'extra','42','Extra','2026-01-01T00:00:00Z')`}, ErrConflict},
		{"tampered_alias", []string{`UPDATE metadata_evidence_aliases SET alias='Tampered' WHERE ordinal=0`}, ErrConflict},
		{"missing_external", []string{`DELETE FROM metadata_evidence_external_ids WHERE provider_id='nm0000000'`}, ErrConflict},
		{"extra_external", []string{`INSERT INTO metadata_evidence_external_ids(evidence_id,provider,subject_kind,provider_id) SELECT id,'imdb','person','nm0009999' FROM metadata_evidence LIMIT 1`}, ErrConflict},
		{"tampered_commitment", []string{`UPDATE metadata_person_builds SET expected_commitment=X'0000000000000000000000000000000000000000000000000000000000000000'`}, ErrConflict},
		{"staged_bytes_complete", []string{`UPDATE metadata_person_builds SET staged_bytes=expected_bytes`}, ErrConflict},
		{"expected_bytes", []string{`UPDATE metadata_person_builds SET expected_bytes=expected_bytes+1`}, ErrConflict},
		{"evidence_digest", []string{`UPDATE metadata_evidence SET digest=lower(hex(zeroblob(32)))`}, ErrConflict},
		{"evidence_parser", []string{`UPDATE metadata_evidence SET parser_version='tmdb.person.other.v1'`}, ErrConflict},
		{"evidence_title", []string{`UPDATE metadata_evidence SET title='Tampered'`}, ErrConflict},
		{"evidence_biography", []string{`UPDATE metadata_evidence SET biography='Tampered'`}, ErrConflict},
		{"evidence_birth_date", []string{`UPDATE metadata_evidence SET birth_date='1990-01-01'`}, ErrConflict},
		{"evidence_source_url", []string{`UPDATE metadata_evidence SET source_url='https://example.test/x'`}, ErrConflict},
		{"evidence_sealed", []string{`UPDATE metadata_evidence SET sealed=1`}, ErrConflict},
		{"build_not_open", []string{`UPDATE metadata_person_builds SET state='done'`}, ErrConflict},
		{"owner_phase", []string{`UPDATE metadata_person_operations SET phase='sealed'`}, ErrConflict},
		{"stale_person", []string{`UPDATE catalog_people SET revision=2 WHERE id=42`}, ErrConflict},
		{"stale_subject", []string{`UPDATE metadata_provider_subjects SET revision=2 WHERE id=501`}, ErrConflict},
		{"stale_policy", []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"revoked_consent", []string{`UPDATE screen_metadata_consent SET confirmed=0`}, ErrDisclosureDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipPersonFixture(t)
			chain := ipPersonSealCompleteChain(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			// A case may pre-seal the evidence row to force the already-sealed
			// refusal; the refused seal must leave that baseline untouched.
			sealedBefore := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence WHERE sealed=1`)
			command := mustIPPersonSeal(t, ipPersonIdentity(chain.sequence, 0x7a),
				ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content))
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
			out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, command)
			if out != (ipPersonSealCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("incomplete/foreign seal admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='evidence_seal'`: 0,
				`SELECT count(*) FROM metadata_person_builds WHERE state='ready'`:                  0,
				`SELECT count(*) FROM metadata_evidence WHERE sealed=1`:                            sealedBefore,
				`SELECT last_command_sequence FROM metadata_person_operations`:                     6,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal("refused seal wrote state", query, got)
				}
			}
		})
	}
	t.Run("wrong_build_revision", func(t *testing.T) {
		w, r, p, _ := ipPersonFixture(t)
		chain := ipPersonSealCompleteChain(t, w, r, p)
		q := ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content+1)
		command := mustIPPersonSeal(t, ipPersonIdentity(chain.sequence, 0x7a), q)
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
		out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, command)
		if out != (ipPersonSealCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("wrong content revision admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
	t.Run("wrong_subject_id", func(t *testing.T) {
		w, r, p, _ := ipPersonFixture(t)
		chain := ipPersonSealCompleteChain(t, w, r, p)
		q := ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content)
		q.header.SubjectID = 99
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
		out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b,
			mustIPPersonSeal(t, ipPersonIdentity(chain.sequence, 0x7a), q))
		if out != (ipPersonSealCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("wrong subject ID admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
	t.Run("wrong_update_revision", func(t *testing.T) {
		w, r, p, _ := ipPersonFixture(t)
		chain := ipPersonSealCompleteChain(t, w, r, p)
		q := ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content)
		q.revision = 9
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
		out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b,
			mustIPPersonSeal(t, ipPersonIdentity(chain.sequence, 0x7a), q))
		if out != (ipPersonSealCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("wrong owner revision admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPPersonSealStaleOwner(t *testing.T) {
	cases := []struct {
		name string
		sql  []string
		want error
	}{
		{"new_observation", []string{`UPDATE catalog_people SET revision=2 WHERE id=42`}, ErrConflict},
		{"new_subject", []string{`UPDATE metadata_provider_subjects SET revision=2 WHERE id=501`}, ErrConflict},
		{"policy_advanced", []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"consent_advanced", []string{`UPDATE screen_metadata_consent SET revision=2 WHERE singleton=1`}, ErrDisclosureDenied},
		{"lease_expired", []string{`UPDATE metadata_person_operations SET lease_until_ms=1`}, ErrConflict},
		{"owner_not_building", []string{`UPDATE metadata_person_operations SET phase='claimed'`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipPersonFixture(t)
			chain := ipPersonSealCompleteChain(t, w, r, p)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
			out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b,
				mustIPPersonSeal(t, ipPersonIdentity(chain.sequence, 0x7a),
					ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content)))
			if out != (ipPersonSealCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale owner admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("expired_lease_at_seal_time", func(t *testing.T) {
		w, r, p, base := ipPersonFixture(t)
		chain := ipPersonSealCompleteChain(t, w, r, p)
		ipPersonAdvance(t, w, base, 6*time.Minute)
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
		out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b,
			mustIPPersonSeal(t, ipPersonIdentity(chain.sequence, 0x7a),
				ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content)))
		if out != (ipPersonSealCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("expired seal lease admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPPersonSealCurrentAuthorityAndLifetime(t *testing.T) {
	prepare := func(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, *ipPersonSealCommand) {
		t.Helper()
		w, r, p, _ := ipPersonFixture(t)
		chain := ipPersonSealCompleteChain(t, w, r, p)
		return w, r, p, mustIPPersonSeal(t, ipPersonIdentity(chain.sequence, 0x7a),
			ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content))
	}
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, command := prepare(t)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
			out, err := applyIPPersonSeal(t.Context(), turn, proof, t.Context(), b, command)
			if out != (ipPersonSealCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, command := prepare(t)
		lifetime, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
		out, err := applyIPPersonSeal(lifetime, turn, p, t.Context(), b, command)
		if out != (ipPersonSealCommandResult{}) || err == nil {
			t.Fatal("cancelled lifetime admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPPersonSealUnknownCommitFreshRead(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonSealCompleteChain(t, w, r, p)
	command := mustIPPersonSeal(t, ipPersonIdentity(chain.sequence, 0x7a),
		ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content))
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
	out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, command)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if again, e := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, command); again != (ipPersonSealCommandResult{}) || e == nil {
		t.Fatal("uncertain seal frame reserved", again, e)
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='evidence_seal' AND seal_rows=5 AND proof_pin_count=1`: 1,
		`SELECT count(*) FROM metadata_person_builds WHERE state='ready' AND seal_owner IS NOT NULL`:                             1,
		`SELECT count(*) FROM metadata_evidence WHERE sealed=1`:                                                                  1,
		`SELECT count(*) FROM metadata_person_operations WHERE phase='sealed' AND revision=3`:                                    1,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal("durable seal", query, got)
		}
	}
}

func TestIPPersonSealConflictingRequestAndSequence(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	chain := ipPersonSealCompleteChain(t, w, r, p)
	out := chain.seal(t, w, r, p)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	changed := ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content)
	changed.birthPlace = EvidenceText{Present: true, Value: "Paris"}
	conflicting := mustIPPersonSeal(t, chain.sealIdentity, changed)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonSeal)
	denied, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, conflicting)
	if denied != (ipPersonSealCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	other := chain.sealIdentity
	other.OperationID = [16]byte{0x7f, 0x02}
	second := mustIPPersonSeal(t, other,
		ipPersonSealFor(chain.staged.begin, chain.staged.beginReq, chain.content))
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonSeal)
	denied, err = applyIPPersonSeal(t.Context(), turn, p, t.Context(), b, second)
	if denied != (ipPersonSealCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting operation admitted", denied, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='evidence_seal'`); got != 1 {
		t.Fatal("conflicting seal wrote a second receipt", got)
	}
}

func TestIPPersonSealDescriptors(t *testing.T) {
	expected := Descriptor{ID: IPPersonSeal, Name: "ip_person_seal", Durability: Normal}
	actual, err := Describe(expected.ID)
	if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
		t.Fatal(actual, err)
	}
	if IPPersonSeal == IPEntitySeal || IPPersonSeal != 233 {
		t.Fatal("person seal purpose aliased the entity seal purpose")
	}
}
