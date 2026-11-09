package writer

import (
	"errors"
	"testing"
	"time"
)

// SOL-INGEST renew/finish split for the entity acquisition lifecycle. A fresh
// RENEW grants continued work and so requires the current GLOBAL
// metadata_providers.enabled, exactly like claim; FINISH is exact-owner custody
// closure and must still settle after a disable without accepting, publishing,
// leasing or starting IO.
func TestIPEntityJobGlobalDisableSplitsRenewFinish(t *testing.T) {
	t.Run("fresh_renew_refused_after_disable", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipEntityJobAdvance(t, w, base, time.Minute)
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
		out, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b,
			mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), ipEntityJobRenewFor(first)))
		if out != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("globally disabled provider renewed", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT revision FROM metadata_acquisition_jobs WHERE entity_id=1`:                                    2,
			`SELECT lease_until_ms FROM metadata_acquisition_jobs WHERE entity_id=1`:                              first.LeaseUntilMS,
			`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + first.OperationID + `'`: 1,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='renew'`:                                0,
			`SELECT count(*) FROM metadata_ip_receipts`:                                                           1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})

	t.Run("exact_finish_after_disable_closes_without_effects", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipEntityJobAdvance(t, w, base, time.Minute)
		before := map[string]int64{
			"evidence": ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence`),
			"builds":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence_builds`),
			"publics":  ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_publications`),
			"subjects": ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_subject_providers`),
		}
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		out, turn, tx, _ := ipEntityJobApplyFinish(t, w, r, p,
			mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), ipEntityJobFinishFor(first)))
		now := w.now().UnixMilli()
		if out.Outcome.State != Applied || out.Value.Kind != "finish" || out.Value.Outcome != "done" ||
			out.Value.Revision != 3 || out.Value.Lease != "" || out.Value.LeaseUntilMS != now {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND status='done' AND lease='' AND attempts=2 AND next_attempt_ms=` + ipWritingInt(now):                    1,
			`SELECT count(*) FROM metadata_provider_operations WHERE id='` + first.OperationID + `' AND status='staged' AND lease='' AND lease_until='` + ipEntityJobTimeText(now) + `'`: 1,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='finish' AND result_phase='done' AND terminal=1`:                                                               1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
		// Custody closure publishes and accepts nothing: no accepted-source or
		// publication row changed, and no new lease was granted.
		for name, query := range map[string]string{
			"evidence": `SELECT count(*) FROM metadata_evidence`,
			"builds":   `SELECT count(*) FROM metadata_evidence_builds`,
			"publics":  `SELECT count(*) FROM metadata_publications`,
			"subjects": `SELECT count(*) FROM metadata_subject_providers`,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != before[name] {
				t.Fatal(name, got)
			}
		}
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND lease_until_ms>`+ipWritingInt(now)); got != 0 {
			t.Fatal("finish granted a lease", got)
		}
	})

	t.Run("retry_after_disable_stays_non_running", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipEntityJobAdvance(t, w, base, time.Minute)
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		q := ipEntityJobFinishFor(first)
		q.outcome, q.reason = "retry", "provider timeout"
		out, turn, tx, _ := ipEntityJobApplyFinish(t, w, r, p,
			mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), q))
		next := w.now().UnixMilli() + ipEntityJobRetryMS
		if out.Outcome.State != Applied || out.Value.Outcome != "retry" {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND status='pending' AND lease='' AND error='provider timeout' AND next_attempt_ms=`+ipWritingInt(next)); got != 1 {
			t.Fatal("passive retry", got)
		}
		// The passive retry is due, but the job cannot be claimed while the
		// provider stays globally disabled.
		ipEntityJobAdvance(t, w, base, 6*time.Minute)
		claim := func() *ipEntityJobCommand {
			return ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x24, 1, 0x34),
				func(q *ipEntityJobClaimRequest) { q.jobGeneration, q.jobRevision = 2, 3 })
		}
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityClaim)
		blocked, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, claim())
		if blocked != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("disabled retry admitted", blocked, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_operations`); got != 1 {
			t.Fatal("disabled retry claimed", got)
		}
		// Re-enabling lets the same due retry run.
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=1 WHERE code='tmdb'`)
		second, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x24, 1, 0x34),
			func(q *ipEntityJobClaimRequest) { q.jobGeneration, q.jobRevision = 2, 3 }))
		if second.Value.Generation != 3 || second.Value.Revision != 4 {
			t.Fatal("re-enabled retry claim", second)
		}
		ipPolicyCommitTurn(t, turn, tx)
	})

	t.Run("saved_renew_replay_after_disable", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipEntityJobAdvance(t, w, base, time.Minute)
		renew := ipEntityJobRenewFor(first)
		identity := ipEntityJobIdentity(0x21, 2, 0x41)
		out, turn, tx, _ := ipEntityJobApplyRenew(t, w, r, p, mustRenew(t, identity, renew))
		ipPolicyCommitTurn(t, turn, tx)
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		// Current authority still precedes disclosure, but the saved renew is
		// served before the fresh global-provider acquisition gate.
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
		replay, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, mustRenew(t, identity, renew))
		if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
			t.Fatal("renew replay after disable", replay, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='renew'`); got != 1 {
			t.Fatal("replay wrote a renew receipt", got)
		}
	})

	t.Run("saved_finish_replay_after_disable", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipEntityJobAdvance(t, w, base, time.Minute)
		finish := ipEntityJobFinishFor(first)
		identity := ipEntityJobIdentity(0x21, 2, 0x51)
		out, turn, tx, _ := ipEntityJobApplyFinish(t, w, r, p, mustFinish(t, identity, finish))
		ipPolicyCommitTurn(t, turn, tx)
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityFinish)
		replay, err := applyIPEntityFinish(t.Context(), turn, p, t.Context(), b, mustFinish(t, identity, finish))
		if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
			t.Fatal("finish replay after disable", replay, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='finish'`); got != 1 {
			t.Fatal("replay wrote a finish receipt", got)
		}
	})

	t.Run("claim_still_refused_while_disabled", func(t *testing.T) {
		w, r, p, _ := ipEntityJobFixture(t)
		ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{})
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityClaim)
		out, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b,
			ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x21, 1, 0x31), nil))
		if out != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("disabled provider claimed", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		for _, query := range []string{
			`SELECT count(*) FROM metadata_provider_operations`,
			`SELECT count(*) FROM metadata_operation_subjects`,
			`SELECT count(*) FROM metadata_ip_receipts`,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != 0 {
				t.Fatal(query, got)
			}
		}
	})
}
