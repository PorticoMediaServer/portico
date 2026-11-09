package writer

import (
	"errors"
	"testing"
	"time"
)

// SOL-INGEST renew/finish split for the person provider lifecycle. A fresh
// RENEW grants continued work and so requires the current GLOBAL
// metadata_providers.enabled exactly like claim; FINISH is exact-owner custody
// closure and must still settle after a disable without accepting, publishing,
// leasing or starting IO. Person retry differs from the entity job: it records
// only a passive next_attempt_ms on the owner (there is no scheduled job row or
// re-claim of the same owner), so a later run needs a new claim, which remains
// gated by the global enable.
func TestIPPersonProviderGlobalDisableSplitsRenewFinish(t *testing.T) {
	t.Run("fresh_renew_refused_after_disable", func(t *testing.T) {
		w, r, p, base := ipPersonFixture(t)
		first, _ := ipPersonClaimed(t, w, r, p)
		ipPersonAdvance(t, w, base, time.Second)
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonRenew)
		out, err := applyIPPersonRenew(t.Context(), turn, p, t.Context(), b,
			mustIPPersonRenew(t, ipPersonIdentity(2, 0x41), ipPersonRenewFor(first)))
		if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("globally disabled provider renewed", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT revision FROM metadata_person_operations WHERE id='` + first.OperationID + `'`:              1,
			`SELECT lease_generation FROM metadata_person_operations WHERE id='` + first.OperationID + `'`:      1,
			`SELECT lease_until_ms FROM metadata_person_operations WHERE id='` + first.OperationID + `'`:        first.LeaseUntilMS,
			`SELECT last_command_sequence FROM metadata_person_operations WHERE id='` + first.OperationID + `'`: 1,
			`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='renew'`:                          0,
			`SELECT count(*) FROM metadata_person_receipts`:                                                     1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})

	t.Run("exact_finish_after_disable_closes_without_effects", func(t *testing.T) {
		w, r, p, base := ipPersonFixture(t)
		first, _ := ipPersonClaimed(t, w, r, p)
		ipPersonAdvance(t, w, base, time.Second)
		before := map[string]int64{
			"evidence":  ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence`),
			"builds":    ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_builds`),
			"publics":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_publications`),
			"sources":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_sources`),
			"origins":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_field_origins`),
			"catalogue": ipPolicyQueryInt(t, w, `SELECT revision FROM catalog_people WHERE id=42`),
		}
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		out, turn, tx, _ := func() (ipPersonCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonFinish)
			out, err := applyIPPersonFinish(t.Context(), turn, p, t.Context(), b,
				mustIPPersonFinish(t, ipPersonIdentity(2, 0x51), ipPersonFinishFor(first, "done")))
			if err != nil {
				t.Fatal("finish after disable", out, err)
			}
			return out, turn, tx, b
		}()
		now := w.now().UnixMilli()
		if out.Outcome.State != Applied || out.Value.Kind != "finish" || out.Value.Outcome != "done" ||
			out.Value.Revision != 2 || out.Value.LeaseUntilMS != now {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		// Custody closes exactly: the lease is expired (not extended) and the
		// captured token is retained as the closure witness.
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations WHERE id=? AND phase='done' AND lease_owner=? AND lease_generation=1 AND lease_until_ms=? AND next_attempt_ms IS NULL`,
			first.OperationID, first.Lease, now); got != 1 {
			t.Fatal("finish custody closure", got)
		}
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='retry' AND result_count=1`); got != 1 {
			t.Fatal("finish receipt", got)
		}
		// No accepted-source, publication or catalogue effect.
		for name, query := range map[string]string{
			"evidence":  `SELECT count(*) FROM metadata_evidence`,
			"builds":    `SELECT count(*) FROM metadata_person_builds`,
			"publics":   `SELECT count(*) FROM metadata_person_publications`,
			"sources":   `SELECT count(*) FROM metadata_person_sources`,
			"origins":   `SELECT count(*) FROM metadata_person_field_origins`,
			"catalogue": `SELECT revision FROM catalog_people WHERE id=42`,
		} {
			want := before[name]
			if name == "catalogue" {
				want = 1
			}
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(name, got)
			}
		}
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations WHERE lease_until_ms>`+ipWritingInt(now)); got != 0 {
			t.Fatal("finish granted a lease", got)
		}
	})

	t.Run("retry_after_disable_stays_non_running", func(t *testing.T) {
		w, r, p, base := ipPersonFixture(t)
		first, _ := ipPersonClaimed(t, w, r, p)
		ipPersonAdvance(t, w, base, time.Second)
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		out, turn, tx, _ := func() (ipPersonCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonFinish)
			out, err := applyIPPersonFinish(t.Context(), turn, p, t.Context(), b,
				mustIPPersonFinish(t, ipPersonIdentity(2, 0x51), ipPersonFinishFor(first, "retry")))
			if err != nil {
				t.Fatal("retry after disable", out, err)
			}
			return out, turn, tx, b
		}()
		next := w.now().UnixMilli() + ipPersonRetryMS
		if out.Outcome.State != Applied || out.Value.Outcome != "retry" {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		// Retry is passive bookkeeping: the owner keeps its phase, records the
		// next attempt and opens no build.
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations WHERE id=? AND phase='claimed' AND next_attempt_ms=? AND lease_until_ms=?`,
			first.OperationID, next, w.now().UnixMilli()); got != 1 {
			t.Fatal("passive retry", got)
		}
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_builds`); got != 0 {
			t.Fatal("retry began a build", got)
		}
		// A fresh claim (the only way a person operation starts runnable work)
		// is still refused while the provider stays globally disabled.
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonClaim)
		refused, err := applyIPPersonClaim(t.Context(), turn, p, t.Context(), b,
			ipPersonClaimCommand(t, ipPersonIdentity(1, 0x34), nil))
		if refused != (ipPersonCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("disabled claim admitted", refused, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations`); got != 1 {
			t.Fatal("disabled claim wrote an owner", got)
		}
	})

	t.Run("saved_renew_replay_after_disable", func(t *testing.T) {
		w, r, p, base := ipPersonFixture(t)
		first, _ := ipPersonClaimed(t, w, r, p)
		ipPersonAdvance(t, w, base, time.Second)
		q := ipPersonRenewFor(first)
		identity := ipPersonIdentity(2, 0x41)
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonRenew)
		out, err := applyIPPersonRenew(t.Context(), turn, p, t.Context(), b, mustIPPersonRenew(t, identity, q))
		if err != nil {
			t.Fatal(err)
		}
		ipPolicyCommitTurn(t, turn, tx)
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		// Current authority still precedes disclosure, but the saved renew is
		// served before the fresh global-provider acquisition gate.
		turn, tx, b = ipPolicyTurn(t, w, r, IPPersonRenew)
		replay, err := applyIPPersonRenew(t.Context(), turn, p, t.Context(), b, mustIPPersonRenew(t, identity, q))
		if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
			t.Fatal("renew replay after disable", replay, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='renew'`); got != 1 {
			t.Fatal("replay wrote a renew receipt", got)
		}
	})

	t.Run("saved_finish_replay_after_disable", func(t *testing.T) {
		w, r, p, base := ipPersonFixture(t)
		first, _ := ipPersonClaimed(t, w, r, p)
		ipPersonAdvance(t, w, base, time.Second)
		q := ipPersonFinishFor(first, "done")
		identity := ipPersonIdentity(2, 0x51)
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonFinish)
		out, err := applyIPPersonFinish(t.Context(), turn, p, t.Context(), b, mustIPPersonFinish(t, identity, q))
		if err != nil {
			t.Fatal(err)
		}
		ipPolicyCommitTurn(t, turn, tx)
		ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
		turn, tx, b = ipPolicyTurn(t, w, r, IPPersonFinish)
		replay, err := applyIPPersonFinish(t.Context(), turn, p, t.Context(), b, mustIPPersonFinish(t, identity, q))
		if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
			t.Fatal("finish replay after disable", replay, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='retry'`); got != 1 {
			t.Fatal("replay wrote a finish receipt", got)
		}
	})
}
