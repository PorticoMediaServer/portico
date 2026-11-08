package writer

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func iaConfigureSourceAuthorityRequest(t *testing.T, w *executor) iaConfigureSourceRequest {
	t.Helper()
	root := iaConfigureSourceRoot(t, "media")
	iaConfigureSourceLibrary(t, w, 790, "lib-source-authority", root)
	iaConfigureSourceLive(t, w, "lib-source-authority", "source-authority", 1, "incarnation-1", root, "identity-1", true, "ready", true)
	return iaConfigureSourceRequestFixture(t, 790, "lib-source-authority", "source-authority")
}

// ACK at or above the saved sequence is Retired: the original is never decoded
// again and the caller gets no result, no digest and no new effect.
func TestIAConfigureSourceRetiredReplayHasNoResult(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	q := iaConfigureSourceAuthorityRequest(t, w)
	seedIAEntryHistory(t, w, iaEntryRequest{
		purpose: IAConfigureSource, action: iaEntryApply, reference: q.reference,
		libraryID: q.libraryID, libraryKey: q.libraryKey, sourceID: q.sourceID,
	}, true)
	before := iaConfigureSourceCount(t, w, `SELECT count(*) FROM library_sources WHERE id='source-authority' AND enabled=1 AND generation=1`)
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.sources) != 0 {
		t.Fatal("retired replay", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM library_sources WHERE id='source-authority' AND enabled=1 AND generation=1`); got != before {
		t.Fatal("retired replay changed the live source", got)
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM catalog_configuration_receipts`); got != 1 {
		t.Fatal("retired replay minted a receipt", got)
	}
}

func TestIAConfigureSourceRefusesMissingOrForgedAuthority(t *testing.T) {
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
			w, r, p := iaConfigureSourceFixture(t)
			q := iaConfigureSourceAuthorityRequest(t, w)
			turn, budget := iaConfigureSourceTurn(t, w, r, q)
			out, err := configureIASource(t.Context(), turn, proof(p), t.Context(), budget, q)
			if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
				t.Fatal("forged authority", out, err)
			}
			turn.transaction.rollback(nil)
			iaConfigureSourceNoWrites(t, w, "source-authority", 1, 1)
		})
	}
}

// The entry's current-authority check runs before any retained or live-source
// lookup, so a revoked/stale owner is denied with zero public output.
func TestIAConfigureSourceRefusesStaleCurrentAuthority(t *testing.T) {
	for name, mutation := range map[string]string{
		"demoted_owner":  `UPDATE direct_memberships SET role='member'`,
		"retired_token":  `UPDATE authorization_family_tokens SET retired=1`,
		"profile_reset":  `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
		"denied_device":  `UPDATE identity_security_fences SET denied=1`,
		"reset_session":  `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`,
		"narrowed_grant": `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaConfigureSourceFixture(t)
			q := iaConfigureSourceAuthorityRequest(t, w)
			wc008NativeExec(t, w, mutation)
			turn, budget := iaConfigureSourceTurn(t, w, r, q)
			out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
			if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
				t.Fatal("stale authority", out, err)
			}
			turn.transaction.rollback(nil)
			iaConfigureSourceNoWrites(t, w, "source-authority", 1, 1)
		})
	}
}

func TestIAConfigureSourceRefusesCancelledLifetime(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	q := iaConfigureSourceAuthorityRequest(t, w)
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := configureIASource(cancelled, turn, p, cancelled, budget, q)
	if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
		t.Fatal("cancelled lifetime", out, err)
	}
	turn.transaction.rollback(nil)
	iaConfigureSourceNoWrites(t, w, "source-authority", 1, 1)
}

// The first late recheck runs before any live-source write. A revocation
// observed there returns no output and leaves no row behind.
func TestIAConfigureSourceLateRecheckBeforeWrites(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	q := iaConfigureSourceAuthorityRequest(t, w)
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	// IA-05 samples the caller lifetime at every authorize entry and release,
	// and at each recheck's own entry/release pair. Samples 1-6 belong to entry;
	// the first post-read recheck's entry sample is 7.
	lifetime := &iaLifetimeAfter{cancel: 7}
	out, err := configureIASource(t.Context(), turn, p, lifetime, budget, q)
	if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
		t.Fatal("late pre-write recheck", out, err)
	}
	var owners int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_configuration_operations`).Scan(&owners); err != nil || owners != 0 {
		t.Fatal("pre-write recheck ran after a write", owners, err)
	}
	turn.transaction.rollback(nil)
	iaConfigureSourceNoWrites(t, w, "source-authority", 1, 1)
}

// A same-turn revocation injected by the live-source update is caught by the
// final recheck; without that call the command would release a stale result.
func TestIAConfigureSourceLateRevocationAfterWrites(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	q := iaConfigureSourceAuthorityRequest(t, w)
	wc008NativeExec(t, w, `CREATE TRIGGER ia_configure_source_late_revocation AFTER UPDATE ON library_sources BEGIN UPDATE identity_security_fences SET denied=1; END`)
	q.enabled = false
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
		t.Fatal("late post-write recheck", out, err)
	}
	var disabled int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM library_sources WHERE id='source-authority' AND enabled=0`).Scan(&disabled); err != nil || disabled != 1 {
		t.Fatal("source update not reached", disabled, err)
	}
	turn.transaction.rollback(nil)
	iaConfigureSourceNoWrites(t, w, "source-authority", 1, 1)
}

// A lost COMMIT reply is never success. The test reads the committed file
// through a FRESH read-only connection, never the closed/faulted writer pool.
func TestIAConfigureSourceUnknownCommitIsNotSuccess(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	path := iaConfigureLibraryPath(t, w)
	q := iaConfigureSourceAuthorityRequest(t, w)
	q.enabled = false
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	if out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
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
		{`SELECT count(*) FROM library_sources WHERE id='source-authority' AND enabled=0 AND generation=2`, 1},
		{`SELECT count(*) FROM catalog_configuration_operations WHERE command_kind='configure_source'`, 1},
		{`SELECT count(*) FROM catalog_configuration_receipts WHERE result_count=1 AND result_library_name IS NULL`, 1},
		{`SELECT count(*) FROM catalog_configuration_sources WHERE source_id='source-authority' AND enabled=0`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	// The executor is fenced after an unknown commit; no new original may run on
	// the strength of the ambiguous reply.
	if _, err = w.begin(t.Context(), IAConfigureSourceDisable, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

func TestIAConfigureSourceRejectsChangedOriginal(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	q := iaConfigureSourceAuthorityRequest(t, w)
	q.enabled = false
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	if _, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	changed := q
	changed.reference.RequestDigest[0]++
	changed.expectGeneration = 2
	turn, budget = iaConfigureSourceTurn(t, w, r, changed)
	out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, changed)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed original admitted", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM catalog_configuration_receipts`); got != 1 {
		t.Fatal("conflict minted a receipt", got)
	}
}
