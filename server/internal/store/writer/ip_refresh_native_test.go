package writer

import (
	"errors"
	"math"
	"testing"
)

func ipRefreshFixture(t *testing.T, n int) *executor {
	t.Helper()
	w := ipPolicyFixture(t)
	ipPolicyTestExec(t, w, `INSERT INTO metadata_refresh_sweeps(provider,purpose,generation,revision,epoch_ms,phase,command_incarnation,job_lease_owner,job_lease_generation,job_lease_expires_ms) VALUES('tmdb','metadata',7,11,90000,'entities',?,41,13,200000)`, ipPolicyTestOwner[:])
	for i := 1; i <= n; i++ {
		pub := [16]byte{byte(i), 70}
		ipPolicyTestExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,7,1)`, int64(i), pub[:])
		ipPolicyTestExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(?,?,7,1,'fixture','fixture','f','fixture','f',1,zeroblob(16),zeroblob(16))`, int64(i), pub[:])
		ipPolicyTestExec(t, w, `INSERT INTO metadata_acquisition_jobs(entity_id,provider,purpose,generation,revision,status,selected_id,manual,review_search,query_override,year_override,force_refresh,episode_order,parent_publication,attempts,next_attempt_ms,lease,lease_until_ms,error,page,after_item,staged_bytes) VALUES(?,'tmdb','metadata',3,5,'matched','identity-kept',0,0,'',0,0,'aired','',0,0,'',0,'',0,'',0)`, int64(i))
	}
	return w
}
func ipRefreshTestRequest(kind ipRefreshKind) ipRefreshRequest {
	return ipRefreshRequest{provider: "tmdb", purpose: "metadata", identity: ipPolicyTestIdentity(1), kind: kind, generation: 7, revision: 11, leaseOwner: 41, leaseGeneration: 13, leaseExpiresMS: 200000, consentRevision: 3, nowMS: 100000}
}
func ipRefreshNextRequest(t *testing.T, w *executor, r ipRefreshRequest, out ipRefreshResult, kind ipRefreshKind) ipRefreshRequest {
	t.Helper()
	// Synthetic fixture ACK, NOT a production ACK implementation or proof.
	ipPolicyTestExec(t, w, `UPDATE metadata_refresh_sweeps SET acknowledged_command_sequence=last_command_sequence`)
	r.identity = ipPolicyTestIdentity(r.identity.Sequence + 1)
	r.kind = kind
	r.generation = out.generation
	r.revision = out.revision
	r.afterID = out.afterID
	return r
}
func ipRefreshTestApply(t *testing.T, w *executor, r ipRefreshRequest) ipRefreshResult {
	t.Helper()
	turn := ipPolicyTestTurn(t, w, r.kind.command())
	out, err := ipRefreshBody(t.Context(), turn, r, ipPolicyTestDecode())
	if err != nil {
		t.Fatal(err)
	}
	ipPolicyTestCommit(t, turn)
	return out
}

func TestIPRefreshNativeRequest(t *testing.T) {
	for _, name := range []string{"fresh_replay", "conflict", "stale_generation", "overflow", "consent", "byte_reserve"} {
		t.Run(name, func(t *testing.T) {
			w := ipRefreshFixture(t, 0)
			r := ipRefreshTestRequest(ipRefreshRequestKind)
			switch name {
			case "stale_generation":
				r.generation--
			case "overflow":
				ipPolicyTestExec(t, w, `UPDATE metadata_refresh_sweeps SET generation=?`, int64(math.MaxInt64))
				r.generation = math.MaxInt64
			case "consent":
				ipPolicyTestExec(t, w, `UPDATE screen_metadata_consent SET confirmed=0`)
			}
			b := ipPolicyTestDecode()
			if name == "byte_reserve" {
				b.remaining = 0
			}
			turn := ipPolicyTestTurn(t, w, IPRefreshRequest)
			out, err := ipRefreshBody(t.Context(), turn, r, b)
			if name != "fresh_replay" && name != "conflict" {
				if err == nil || out != (ipRefreshResult{}) {
					t.Fatal(out, err)
				}
				_, _ = turn.transaction.rollback(err)
				if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_refresh_receipts`) != 0 {
					t.Fatal("refusal receipt")
				}
				return
			}
			if err != nil || out.generation != 8 || out.revision != 12 || out.phase != "entities" || out.afterID != 0 || out.waiting != 0 {
				t.Fatal(out, err)
			}
			ipPolicyTestCommit(t, turn)
			if ipPolicyTestScalar(t, w, `SELECT job_lease_generation FROM metadata_refresh_sweeps`) != 14 || ipPolicyTestScalar(t, w, `SELECT job_lease_owner FROM metadata_refresh_sweeps`) != 0 {
				t.Fatal("request did not fence old lease")
			}
			ipPolicyTestExec(t, w, `UPDATE metadata_refresh_sweeps SET generation=90,revision=91,phase='people'`)
			ipPolicyTestExec(t, w, `UPDATE screen_metadata_consent SET revision=90,confirmed=0`)
			if name == "conflict" {
				r.nowMS++
			}
			turn = ipPolicyTestTurn(t, w, IPRefreshRequest)
			echo, e := ipRefreshBody(t.Context(), turn, r, ipPolicyTestDecode())
			if name == "conflict" {
				if !errors.Is(e, ErrConflict) || echo != (ipRefreshResult{}) {
					t.Fatal(echo, e)
				}
				_, _ = turn.transaction.rollback(e)
			} else {
				if e != nil || echo != out {
					t.Fatal(echo, e)
				}
				ipPolicyTestCommit(t, turn)
			}
			if ipPolicyTestScalar(t, w, `SELECT generation FROM metadata_refresh_sweeps`) != 90 {
				t.Fatal("replay re-requested")
			}
		})
	}
}

func TestIPRefreshNativeFilteredCursorAndFinish(t *testing.T) {
	w := ipRefreshFixture(t, 20)
	// These exclusions must not redefine the RAW cursor or initiate rematches.
	ipPolicyTestExec(t, w, `UPDATE metadata_acquisition_jobs SET status='unmatched',selected_id='' WHERE entity_id<=16`)
	ipPolicyTestExec(t, w, `UPDATE metadata_acquisition_jobs SET lease='another',lease_until_ms=200000 WHERE entity_id=17`)
	ipPolicyTestExec(t, w, `UPDATE metadata_acquisition_jobs SET next_attempt_ms=200000 WHERE entity_id=18`)
	r := ipRefreshTestRequest(ipRefreshPageKind)
	out := ipRefreshTestApply(t, w, r)
	if out.afterID != 16 || out.scheduled != 0 || out.phase != "entities" {
		t.Fatal(out)
	}
	// A chunk reaching its limit cannot serve as no-next/completion proof.
	finish := ipRefreshNextRequest(t, w, r, out, ipRefreshFinishEntitiesKind)
	turn := ipPolicyTestTurn(t, w, IPRefreshPage)
	got, err := ipRefreshBody(t.Context(), turn, finish, ipPolicyTestDecode())
	if !errors.Is(err, ErrConflict) || got != (ipRefreshResult{}) {
		t.Fatal(got, err)
	}
	_, _ = turn.transaction.rollback(err)
	r = finish
	r.kind = ipRefreshPageKind
	out = ipRefreshTestApply(t, w, r)
	if out.afterID != 20 || out.scheduled != 2 || out.waiting != 1 || out.phase != "entities" {
		t.Fatal(out)
	}
	if ipPolicyTestScalar(t, w, `SELECT spent FROM metadata_refresh_budget WHERE span='hour'`) != 2 || ipPolicyTestScalar(t, w, `SELECT spent FROM metadata_refresh_budget WHERE span='day'`) != 2 {
		t.Fatal("allowance not atomic")
	}
	if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE selected_id='identity-kept'`) != 4 {
		t.Fatal("rematched identities")
	}
	// Exact page replay returns original scheduling count even after fresh
	// policy, consent, lease and sweep generation have all changed.
	ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET enabled=0,revision=90`)
	ipPolicyTestExec(t, w, `UPDATE screen_metadata_consent SET revision=90,confirmed=0`)
	ipPolicyTestExec(t, w, `UPDATE metadata_refresh_sweeps SET generation=90,job_lease_owner=99`)
	echo := ipRefreshTestApply(t, w, r)
	if echo != out {
		t.Fatal(echo, out)
	}
	if ipPolicyTestScalar(t, w, `SELECT spent FROM metadata_refresh_budget WHERE span='hour'`) != 2 {
		t.Fatal("replay spent again")
	}
	ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET enabled=1,revision=5`)
	ipPolicyTestExec(t, w, `UPDATE screen_metadata_consent SET revision=3,confirmed=1`)
	ipPolicyTestExec(t, w, `UPDATE metadata_refresh_sweeps SET generation=7,job_lease_owner=41`)
	// A pending page blocks finish; a provider retry backoff does not.
	r = ipRefreshNextRequest(t, w, r, out, ipRefreshFinishEntitiesKind)
	turn = ipPolicyTestTurn(t, w, IPRefreshPage)
	got, err = ipRefreshBody(t.Context(), turn, r, ipPolicyTestDecode())
	if !errors.Is(err, ErrConflict) {
		t.Fatal(got, err)
	}
	_, _ = turn.transaction.rollback(err)
	ipPolicyTestExec(t, w, `UPDATE metadata_acquisition_jobs SET next_attempt_ms=200000 WHERE entity_id>=19`)
	out = ipRefreshTestApply(t, w, r)
	if out.phase != "people" || out.scheduled != 0 || out.afterID != 20 {
		t.Fatal("false sweep completion", out)
	}
	// People require a separate typed traversal; this entity body refuses it.
	r = ipRefreshNextRequest(t, w, r, out, ipRefreshFinishEntitiesKind)
	turn = ipPolicyTestTurn(t, w, IPRefreshPage)
	got, err = ipRefreshBody(t.Context(), turn, r, ipPolicyTestDecode())
	if !errors.Is(err, ErrConflict) || got != (ipRefreshResult{}) {
		t.Fatal(got, err)
	}
	_, _ = turn.transaction.rollback(err)
}

func TestIPRefreshNativePageBoundsAndBudget(t *testing.T) {
	for _, name := range []string{"bounded_16", "artwork_purpose", "one_unit", "spent", "future_window", "provider_backoff", "provider_disabled", "policy_disabled", "waiting_noop", "job_overflow", "sweep_overflow", "stale_generation", "stale_lease", "expired_lease", "cursor_CAS", "receipt_rollback", "debit_rollback"} {
		t.Run(name, func(t *testing.T) {
			w := ipRefreshFixture(t, 17)
			r := ipRefreshTestRequest(ipRefreshPageKind)
			ipPolicyTestExec(t, w, `INSERT INTO metadata_refresh_budget VALUES('tmdb','hour',50,0),('tmdb','day',50,0)`)
			switch name {
			case "artwork_purpose":
				ipPolicyTestExec(t, w, `UPDATE metadata_refresh_sweeps SET purpose='artwork'`)
				ipPolicyTestExec(t, w, `UPDATE metadata_acquisition_jobs SET purpose='artwork'`)
				r.purpose = "artwork"
			case "one_unit":
				ipPolicyTestExec(t, w, `UPDATE metadata_refresh_budget SET spent=239 WHERE span='hour'`)
			case "spent":
				ipPolicyTestExec(t, w, `UPDATE metadata_refresh_budget SET spent=240 WHERE span='hour'`)
			case "future_window":
				ipPolicyTestExec(t, w, `UPDATE metadata_refresh_budget SET window_start=200,spent=240 WHERE span='hour'`)
			case "provider_backoff":
				ipPolicyTestExec(t, w, `UPDATE metadata_providers SET next_attempt_ms=200000`)
			case "provider_disabled":
				ipPolicyTestExec(t, w, `UPDATE metadata_providers SET enabled=0`)
			case "policy_disabled":
				ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET enabled=0`)
			case "waiting_noop":
				ipPolicyTestExec(t, w, `UPDATE metadata_refresh_sweeps SET page_waiting=1,page_from_id=0,page_until_id=1,after_id=1`)
				ipPolicyTestExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending' WHERE entity_id=1`)
				r.afterID = 1
			case "job_overflow":
				ipPolicyTestExec(t, w, `UPDATE metadata_acquisition_jobs SET generation=? WHERE entity_id=2`, int64(math.MaxInt64))
			case "sweep_overflow":
				ipPolicyTestExec(t, w, `UPDATE metadata_refresh_sweeps SET revision=?`, int64(math.MaxInt64))
				r.revision = math.MaxInt64
			case "stale_generation":
				r.generation--
			case "stale_lease":
				r.leaseGeneration--
			case "expired_lease":
				r.nowMS = 200000
			case "cursor_CAS":
				r.afterID = 1
			case "receipt_rollback":
				// A valid receipt on the other purpose has the same UNIQUE op ID;
				// the page fails only after scheduling, both debits and cursor CAS.
				ipPolicyTestExec(t, w, `INSERT INTO metadata_refresh_sweeps(provider,purpose,generation,revision,epoch_ms,phase,command_incarnation) VALUES('tmdb','artwork',1,1,0,'entities',?)`, ipPolicyTestOwner[:])
				ipPolicyTestExec(t, w, `INSERT INTO metadata_refresh_receipts(provider,purpose,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,generation,epoch_ms,phase,after_id,scheduled_count,page_waiting) VALUES('tmdb','artwork',?,1,?,zeroblob(32),0,zeroblob(32),'request',1,1,0,'entities',0,0,0)`, ipPolicyTestOwner[:], r.identity.OperationID[:])
			}
			defs := ipPolicyTestDefinitions()
			if name == "debit_rollback" {
				for i := range defs {
					if defs[i].ID == ipRefreshBudgetDebit {
						defs[i].SQL = `INSERT INTO metadata_refresh_budget(provider,span,window_start,spent) VALUES(?,?,?,?) ON CONFLICT(provider,span) DO UPDATE SET window_start=excluded.window_start,spent=excluded.spent WHERE window_start=? AND spent=? AND spent=-1`
					}
				}
			}
			turn := ipPolicyTestTurn(t, w, IPRefreshPage, defs)
			out, err := ipRefreshBody(t.Context(), turn, r, ipPolicyTestDecode())
			refuse := name == "job_overflow" || name == "sweep_overflow" || name == "stale_generation" || name == "stale_lease" || name == "expired_lease" || name == "cursor_CAS" || name == "receipt_rollback" || name == "debit_rollback"
			if refuse {
				if err == nil || out != (ipRefreshResult{}) {
					t.Fatal(out, err)
				}
				_, _ = turn.transaction.rollback(err)
				if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending'`) != 0 || ipPolicyTestScalar(t, w, `SELECT spent FROM metadata_refresh_budget WHERE span='hour'`) != 0 || ipPolicyTestScalar(t, w, `SELECT after_id FROM metadata_refresh_sweeps WHERE purpose='metadata'`) != 0 {
					t.Fatal("failed command leaked prefix")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ipPolicyTestCommit(t, turn)
			scheduled, after := int64(16), int64(16)
			switch name {
			case "one_unit":
				scheduled, after = 1, 1
			case "spent":
				scheduled, after = 0, 0
			case "provider_backoff", "provider_disabled", "policy_disabled":
				scheduled = 0
			case "waiting_noop":
				scheduled, after = 0, 1
			}
			if out.scheduled != scheduled || out.afterID != after || out.phase != "entities" {
				t.Fatal(out, scheduled, after)
			}
			if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending' AND entity_id>1`) > 15 {
				t.Fatal("unbounded page")
			}
		})
	}
}
