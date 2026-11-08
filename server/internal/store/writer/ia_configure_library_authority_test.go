package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func iaConfigureLibraryPath(t *testing.T, w *executor) string {
	t.Helper()
	var seq int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func iaConfigureLibraryAuthorityRequest(operation, incarnation, digest byte) iaConfigureLibraryRequest {
	return iaConfigureLibraryRequest{
		reference: iaConfigureLibraryReference(operation, incarnation, digest), libraryID: 960, libraryKey: "lib-authority", name: "Authority", kind: "movie",
		sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/authority")},
	}
}

func iaConfigureLibraryNoWrites(t *testing.T, w *executor) {
	t.Helper()
	for _, table := range []string{"libraries", "catalog_libraries", "library_sources", "library_scan_policies", "inventory_strict_intents", "api_events",
		"catalog_configuration_operations", "catalog_configuration_receipts", "catalog_configuration_sources"} {
		if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
}

// ACK at or above the saved sequence is Retired: the original is never decoded
// again and the caller gets no result, no digest and no new effect.
func TestIAConfigureLibraryRetiredReplayHasNoResult(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	q := iaConfigureLibraryAuthorityRequest(70, 71, 72)
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(960,'lib-authority','/authority')`)
	seedIAEntryHistory(t, w, iaEntryRequest{
		purpose: IAConfigureLibrary, action: iaEntryApply, reference: q.reference,
		libraryID: q.libraryID, libraryKey: q.libraryKey,
	}, true)
	before := map[string]int64{}
	for _, table := range []string{"libraries", "library_sources", "library_scan_policies", "inventory_strict_intents", "api_events"} {
		before[table] = iaConfigureLibraryCount(t, w, `SELECT count(*) FROM `+table)
	}
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.sources) != 0 {
		t.Fatal("retired replay", out, err)
	}
	turn.transaction.rollback(nil)
	for table, want := range before {
		if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM `+table); got != want {
			t.Fatal("retired replay wrote", table, got, want)
		}
	}
	if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM catalog_configuration_receipts`); got != 1 {
		t.Fatal("retired replay minted a receipt", got)
	}
}

func TestIAConfigureLibraryRefusesMissingOrForgedAuthority(t *testing.T) {
	for name, proof := range map[string]func(*usOwnerAuthorityProof) *usOwnerAuthorityProof{
		"nil": func(*usOwnerAuthorityProof) *usOwnerAuthorityProof { return nil },
		"forged_family": func(p *usOwnerAuthorityProof) *usOwnerAuthorityProof {
			fake := *p
			fake.witness.issuer.family = "forged"
			return &fake
		},
		"forged_digest": func(p *usOwnerAuthorityProof) *usOwnerAuthorityProof {
			fake := *p
			fake.witness.issuer.tokenDigest = usCredentialFixtureHash("forged-token")
			return &fake
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaConfigureLibraryFixture(t)
			q := iaConfigureLibraryAuthorityRequest(73, 74, 75)
			turn, budget := iaConfigureLibraryTurn(t, w, r, q)
			out, err := configureIALibrary(t.Context(), turn, proof(p), t.Context(), budget, q)
			if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
				t.Fatal("forged authority", out, err)
			}
			turn.transaction.rollback(nil)
			iaConfigureLibraryNoWrites(t, w)
		})
	}
}

// The entry's current-authority check runs before any retained lookup, so a
// revoked/stale owner is denied with zero public output.
func TestIAConfigureLibraryRefusesStaleCurrentAuthority(t *testing.T) {
	for name, mutation := range map[string]string{
		"demoted_owner":  `UPDATE direct_memberships SET role='member'`,
		"retired_token":  `UPDATE authorization_family_tokens SET retired=1`,
		"profile_reset":  `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
		"denied_device":  `UPDATE identity_security_fences SET denied=1`,
		"reset_session":  `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`,
		"narrowed_grant": `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaConfigureLibraryFixture(t)
			wc008NativeExec(t, w, mutation)
			q := iaConfigureLibraryAuthorityRequest(76, 77, 78)
			turn, budget := iaConfigureLibraryTurn(t, w, r, q)
			out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
			if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
				t.Fatal("stale authority", out, err)
			}
			turn.transaction.rollback(nil)
			iaConfigureLibraryNoWrites(t, w)
		})
	}
}

func TestIAConfigureLibraryRefusesCancelledLifetime(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	q := iaConfigureLibraryAuthorityRequest(79, 80, 81)
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := configureIALibrary(cancelled, turn, p, cancelled, budget, q)
	if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
		t.Fatal("cancelled lifetime", out, err)
	}
	turn.transaction.rollback(nil)
	iaConfigureLibraryNoWrites(t, w)
}

// iaLifetimeAfter is a deterministic caller-lifetime revocation: the Nth Err
// observation reports cancellation. IA-05 re-observes the caller lifetime at
// every recheck, so no real clock, goroutine or production hook is involved.
type iaLifetimeAfter struct {
	n      int
	cancel int
}

func (c *iaLifetimeAfter) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *iaLifetimeAfter) Done() <-chan struct{}       { return nil }
func (c *iaLifetimeAfter) Value(any) any               { return nil }
func (c *iaLifetimeAfter) Err() error {
	c.n++
	if c.n >= c.cancel {
		return context.Canceled
	}
	return nil
}

// The first late recheck runs after all reads and before the first write. A
// revocation observed there returns no output and leaves no row behind.
func TestIAConfigureLibraryLateRecheckBeforeWrites(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	q := iaConfigureLibraryAuthorityRequest(82, 83, 84)
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	// IA-05 samples the caller lifetime at every authorize entry and release,
	// and at each recheck's own entry/release pair. Samples 1-6 belong to entry;
	// the first post-read recheck's entry sample is 7.
	lifetime := &iaLifetimeAfter{cancel: 7}
	out, err := configureIALibrary(t.Context(), turn, p, lifetime, budget, q)
	if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
		t.Fatal("late pre-write recheck", out, err)
	}
	// No write may precede this check. A later recheck would deny only after the
	// namespace/owner rows exist, so the empty transaction distinguishes them.
	var owners int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_configuration_operations`).Scan(&owners); err != nil || owners != 0 {
		t.Fatal("pre-write recheck ran after a write", owners, err)
	}
	turn.transaction.rollback(nil)
	iaConfigureLibraryNoWrites(t, w)
}

// A same-turn revocation injected by the policy write is caught by the final
// recheck; without that call the command would release a stale fresh result.
func TestIAConfigureLibraryLateRevocationAfterWrites(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	wc008NativeExec(t, w, `CREATE TRIGGER ia_configure_late_revocation AFTER INSERT ON library_scan_policies BEGIN UPDATE identity_security_fences SET denied=1; END`)
	q := iaConfigureLibraryAuthorityRequest(85, 86, 87)
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
	if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
		t.Fatal("late post-write recheck", out, err)
	}
	// The trigger fired inside the still-open transaction, proving the command
	// reached its writes before the final recheck denied release.
	var policies int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM library_scan_policies`).Scan(&policies); err != nil || policies != 1 {
		t.Fatal("policy write not reached", policies, err)
	}
	turn.transaction.rollback(nil)
	iaConfigureLibraryNoWrites(t, w)
}

// A lost COMMIT reply is never success. The test reads the committed file
// through a FRESH read-only connection, never the closed/faulted writer pool.
func TestIAConfigureLibraryUnknownCommitIsNotSuccess(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	path := iaConfigureLibraryPath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaConfigureLibraryAuthorityRequest(88, 89, 90)
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	if out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
		t.Fatal("fresh before commit", out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	readCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, readCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM libraries`, 1},
		{`SELECT count(*) FROM catalog_libraries WHERE library_id='lib-authority'`, 1},
		{`SELECT count(*) FROM catalog_configuration_receipts WHERE result_count=1`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	// The executor is fenced after an unknown commit; no new original may run on
	// the strength of the ambiguous reply.
	if _, err = w.begin(t.Context(), IAConfigureLibrary, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}
