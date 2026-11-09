package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// A quarantined writer pool is closed. Proof about the durable outcome must use
// a fresh read-only connection, never the closed pool or a borrowed writer.
func ipPolicyDatabasePath(t *testing.T, w *executor) string {
	t.Helper()
	var seq int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func ipPolicyFreshInt(t *testing.T, path, query string) int64 {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := conn.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// Both commands check current owner authority BEFORE the saved receipt is
// resolved. A stale caller is denied outright; it must not learn from a
// conflicting-request error that the sequence already holds a receipt.
func TestIPPolicyCurrentAuthorityBeforeReceiptRefresh(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipPolicyFixture(t)
			cmd := ipRefreshTestCommand(t, 1, 2, nil)
			turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
			if out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			ipPolicyCommitTurn(t, turn, tx)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshBegin)
			out, err := applyIPRefreshBegin(t.Context(), turn, proof, t.Context(), b, cmd)
			if out != (ipRefreshCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority served the saved sweep request", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			// Same retained sequence, different request: still a denial, never
			// the receipt conflict that disclosure would expose.
			conflicting := ipRefreshTestCommand(t, 1, 2, func(q *ipRefreshRequest) {
				q.expectedGeneration, q.expectedRevision = 1, 1
			})
			turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshBegin)
			out, err = applyIPRefreshBegin(t.Context(), turn, proof, t.Context(), b, conflicting)
			if out != (ipRefreshCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrConflict) {
				t.Fatal("stale current authority disclosed a retained receipt conflict", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

// Completes a revoked/forged authority state and returns the proof to use.
func ipPolicyRevoke(t *testing.T, w *executor, p *usOwnerAuthorityProof, mode string) *usOwnerAuthorityProof {
	t.Helper()
	proof := p
	switch mode {
	case "revoked_family":
		ipPolicyExec(t, w, `UPDATE authorization_session_families SET revoked=1 WHERE id='issuer-family'`)
	case "revoked_membership":
		ipPolicyExec(t, w, `UPDATE direct_memberships SET disabled=1 WHERE account_id='issuer'`)
	case "forged_proof":
		forged := *p
		forged.witness.issuer.family = "forged-family"
		proof = &forged
	}
	return proof
}

// A sweep request creates exactly ONE metadata_refresh_sweeps row for its own
// provider/purpose and the second request advances that same row. No provider
// or purpose fanout, so the due keyset has one subject.
func TestIPRefreshBeginOneSweepNoFanout(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_sweeps`); got != 0 {
		t.Fatal("fixture already held sweeps", got)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
	if _, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, ipRefreshTestCommand(t, 1, 2, nil)); err != nil {
		t.Fatal(err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_refresh_sweeps`:                                                                           1,
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:                              1,
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE provider<>'tmdb'`:                                                    0,
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE purpose<>'metadata'`:                                                 0,
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE job_due_ms IS NOT NULL`:                                              1,
		`SELECT count(*) FROM metadata_refresh_receipts WHERE provider='tmdb' AND purpose='metadata' AND command_kind='request'`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// The update path resets the same row and still adds no second one.
	ipPolicyExec(t, w, `UPDATE metadata_refresh_sweeps SET acknowledged_command_sequence=1,last_command_sequence=1 WHERE provider='tmdb' AND purpose='metadata'`)
	second := ipRefreshTestCommand(t, 2, 3, func(q *ipRefreshRequest) { q.expectedGeneration, q.expectedRevision = 1, 1 })
	turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshBegin)
	out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, second)
	if err != nil || out.Value.AcceptedRevision != 2 || out.Value.Generation != 1 {
		t.Fatal(out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_refresh_sweeps`:                                                             1,
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata' AND revision=2`: 1,
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE job_due_ms IS NOT NULL`:                                1,
		`SELECT count(*) FROM metadata_refresh_receipts`:                                                           2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

// A live lease owner refuses the request as a conflict even when the lease is
// already expired; IP-04 clears the lease, begin never overwrites it.
func TestIPRefreshBeginActiveLeaseRefused(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	ipPolicyExec(t, w, `INSERT INTO metadata_refresh_sweeps(provider,purpose,generation,revision,epoch_ms,phase,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation) VALUES('tmdb','metadata',1,1,1,'entities',7,3,1,X'01000000000000000000000000000000')`)
	cmd := ipRefreshTestCommand(t, 1, 2, func(q *ipRefreshRequest) { q.expectedGeneration, q.expectedRevision = 1, 1 })
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
	out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if out != (ipRefreshCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("active lease admitted a sweep request", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT revision FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:        1,
		`SELECT job_lease_owner FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`: 7,
		`SELECT count(*) FROM metadata_refresh_receipts`:                                                   0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

// The CAS refuses a caller that lags the current revision, not only one that
// runs ahead: a stale expected revision never overwrites the accepted one.
func TestIPRefreshBeginRevisionBehindCurrentRefused(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	ipPolicyExec(t, w, `INSERT INTO metadata_refresh_sweeps(provider,purpose,generation,revision,epoch_ms,phase,command_incarnation) VALUES('tmdb','metadata',1,3,1,'entities',X'01000000000000000000000000000000')`)
	cmd := ipRefreshTestCommand(t, 1, 2, func(q *ipRefreshRequest) { q.expectedGeneration, q.expectedRevision = 1, 2 })
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
	out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if out != (ipRefreshCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("behind-current revision admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT revision FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`: 3,
		`SELECT count(*) FROM metadata_refresh_receipts`:                                            0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

// An unknown COMMIT is never success, and its durable outcome is read through a
// FRESH read-only connection after the writer pool is quarantined and closed.
func TestIPPolicyUnknownCommit(t *testing.T) {
	for _, command := range []string{"policy_set", "refresh_begin"} {
		t.Run(command, func(t *testing.T) {
			w, r, p := ipPolicyFixture(t)
			path := ipPolicyDatabasePath(t, w)
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
			var commitErr error
			switch command {
			case "policy_set":
				cmd := ipPolicyTestCommand(t, 1, 2, nil)
				turn, tx, b := ipPolicyTurn(t, w, r, IPPolicySet)
				out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
				if err != nil || out.Outcome.State != Applied {
					t.Fatal(out, err)
				}
				result, err := tx.commit()
				if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
					t.Fatal(result, err)
				}
				commitErr = err
				if out, e := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd); out != (ipPolicyCommandResult{}) || e == nil {
					t.Fatal("uncertain frame reserved the saved policy result", out, e)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_policy_receipts`); got != 1 {
					t.Fatal("durable policy receipt", got)
				}
			case "refresh_begin":
				cmd := ipRefreshTestCommand(t, 1, 2, nil)
				turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
				out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd)
				if err != nil || out.Outcome.State != Applied {
					t.Fatal(out, err)
				}
				result, err := tx.commit()
				if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
					t.Fatal(result, err)
				}
				commitErr = err
				if out, e := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd); out != (ipRefreshCommandResult{}) || e == nil {
					t.Fatal("uncertain frame reserved the saved sweep request", out, e)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_refresh_receipts`); got != 1 {
					t.Fatal("durable refresh receipt", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT job_due_ms FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`); got != w.now().UnixMilli() {
					t.Fatal("durable sweep is not due at the applied time", got)
				}
			}
			if commitErr == nil {
				t.Fatal("unknown COMMIT reported success")
			}
		})
	}
}
