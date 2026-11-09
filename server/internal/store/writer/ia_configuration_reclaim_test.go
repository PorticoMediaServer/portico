package writer

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func iaConfigurationReclaimFixture(t *testing.T) (*executor, *checkpointComponent, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	for _, name := range []string{"testdata/ia-configure-library-draft.sql", "testdata/ia-configuration-reclaim-indexes-draft.sql"} {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
			t.Fatal(name, err)
		}
	}
	registry, err := stmttest.New(t, iaConfigurationReclaimDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, keeper, &iaEntryRegistry{registry, keeper}, proof
}

func iaConfigurationReclaimRegistry(t *testing.T, keeper *checkpointComponent, defs []stmt.Definition) *iaEntryRegistry {
	t.Helper()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return &iaEntryRegistry{registry, keeper}
}

// One committed configure_library receipt leaves the permanent owner with one
// fully charged header and two charged source children.
func iaConfigurationReclaimSeed(t *testing.T, w *executor, keeper *checkpointComponent, p *usOwnerAuthorityProof) (ReceiptReference, iaConfigurationReclaimRequest) {
	t.Helper()
	r := iaConfigurationReclaimRegistry(t, keeper, iaConfigureLibraryDefinitions())
	q := iaConfigureLibraryRequest{
		reference: iaConfigureLibraryReference(2, 1, 3), libraryID: 777, libraryKey: "lib-reclaim", name: "Reclaim", kind: "movie",
		sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/media/reclaim"), iaConfigureLibrarySourceFixture(1, "/media/reclaim/4k")},
	}
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	if out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
		t.Fatal("seed configuration", out, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var operation []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT original_operation_id FROM catalog_configuration_operations`).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	request := iaConfigurationReclaimRequest{libraryID: 777, libraryKey: "lib-reclaim", latestSequence: 1}
	copy(request.originalOperation[:], operation)
	return q.reference, request
}

func iaConfigurationReclaimReference(t *testing.T, identity CommandIdentity, request iaConfigurationReclaimRequest) ReceiptReference {
	t.Helper()
	command, err := newIAConfigurationReclaimCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: identity, RequestDigest: command.digest}
}

// One governed turn. A fresh commit is the caller's; a refusal rolls back.
func iaConfigurationReclaimRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, identity CommandIdentity, request iaConfigurationReclaimRequest) (iaConfigurationReclaimOutcome, error) {
	t.Helper()
	ref := iaConfigurationReclaimReference(t, identity, request)
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAConfigureReclaim, action: iaEntryApply, reference: ref})
	out, err := reclaimIAConfiguration(t.Context(), turn, p, t.Context(), budget, ref, request)
	if err != nil {
		turn.transaction.rollback(nil)
		return out, err
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

type iaConfigurationReclaimFacts struct {
	last, acknowledged, reclaimed int64
	rows, bytes                   int64
	ordinal                       int64
	due, slot                     sql.NullInt64
}

func iaConfigurationReclaimRead(t *testing.T, w *executor) iaConfigurationReclaimFacts {
	t.Helper()
	var f iaConfigurationReclaimFacts
	if err := w.db.QueryRowContext(t.Context(), `SELECT last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,
receipt_retained_count,receipt_retained_bytes,receipt_reclaim_ordinal,receipt_reclaim_due_ms,reclaim_command_sequence
FROM catalog_configuration_operations`).Scan(&f.last, &f.acknowledged, &f.reclaimed, &f.rows, &f.bytes, &f.ordinal, &f.due, &f.slot); err != nil {
		t.Fatal(err)
	}
	return f
}

func iaConfigurationReclaimCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func TestIAConfigurationReclaimChildBeforeHeader(t *testing.T) {
	w, keeper, r, p := iaConfigurationReclaimFixture(t)
	configured, request := iaConfigurationReclaimSeed(t, w, keeper, p)
	before := iaConfigurationReclaimRead(t, w)
	headerBytes := iaConfigurationReclaimCount(t, w, `SELECT retained_bytes FROM catalog_configuration_receipts WHERE command_kind='configure_library'`)
	if before.rows != 3 || before.bytes <= headerBytes || before.acknowledged != 0 || before.reclaimed != 0 || before.ordinal != -1 || before.slot.Valid {
		t.Fatal("seeded owner ledger", before, headerBytes)
	}
	remaining := before.rows
	for ordinal := int64(0); ordinal < 2; ordinal++ {
		charge := iaConfigurationReclaimCount(t, w, `SELECT retained_bytes FROM catalog_configuration_sources WHERE ordinal=?`, ordinal)
		identity := iaRetirementBeginIdentity(byte(40+ordinal), 1, 2+ordinal)
		request.latestSequence = before.last
		out, err := iaConfigurationReclaimRun(t, w, r, p, identity, request)
		if err != nil || out.decision != receiptFresh || out.result.removedKind != "source" ||
			out.result.removedCount != 1 || out.result.removedSequence.Int64 != 1 ||
			!out.result.removedOrdinal.Valid || out.result.removedOrdinal.Int64 != ordinal ||
			out.result.resultSequence != 0 || out.result.noNext || out.digest == (ResultDigest{}) {
			t.Fatal("fresh source child turn", ordinal, out, err)
		}
		after := iaConfigurationReclaimRead(t, w)
		if after.acknowledged != identity.Sequence || after.last != identity.Sequence || after.reclaimed != 0 ||
			after.ordinal != ordinal || after.rows != before.rows-1 || after.bytes >= before.bytes ||
			!after.due.Valid || after.due.Int64 != out.result.appliedMS || after.slot.Int64 != identity.Sequence {
			t.Fatal("source child ledger", after)
		}
		if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_sources`); got != int64(1-ordinal) {
			t.Fatal("source child not deleted", got)
		}
		if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_receipts`); got != 1 {
			t.Fatal("header deleted before its children", got)
		}
		// The refund is exactly the measured child charge, never a guess.
		if out.result.removedBytes != charge || after.bytes != before.bytes-charge {
			t.Fatal("unmeasured refund", out.result.removedBytes, charge, before.bytes, after.bytes)
		}
		remaining = after.rows
		before = after
	}
	if remaining != 1 || before.bytes != headerBytes {
		t.Fatal("remaining debt is not the header exactly", remaining, before.bytes, headerBytes)
	}
	// The header goes last, and the final unit must match the whole debt.
	identity := iaRetirementBeginIdentity(44, 1, before.last+1)
	request.latestSequence = before.last
	out, err := iaConfigurationReclaimRun(t, w, r, p, identity, request)
	if err != nil || out.decision != receiptFresh || out.result.removedKind != "header" ||
		out.result.removedBytes != headerBytes || out.result.resultSequence != 1 || !out.result.noNext ||
		out.result.removedOrdinal.Valid || out.result.resultOrdinal.Int64 != -1 {
		t.Fatal("header turn", out, err)
	}
	after := iaConfigurationReclaimRead(t, w)
	if after.reclaimed != 1 || after.ordinal != -1 || after.rows != 0 || after.bytes != 0 || after.due.Valid ||
		after.acknowledged != identity.Sequence || after.last != identity.Sequence {
		t.Fatal("header ledger", after)
	}
	for _, query := range []string{
		`SELECT count(*) FROM catalog_configuration_receipts`,
		`SELECT count(*) FROM catalog_configuration_sources`,
	} {
		if got := iaConfigurationReclaimCount(t, w, query); got != 0 {
			t.Fatal("retired debt retained", query, got)
		}
	}
	// The permanent owner and its original request digest remain.
	if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_operations WHERE length(original_request_digest)=32`); got != 1 {
		t.Fatal("permanent owner lost", got)
	}
	// ACK/reclaim after the debt is exhausted removes nothing and refunds zero.
	identity = iaRetirementBeginIdentity(45, 1, after.last+1)
	request.latestSequence = after.last
	none, err := iaConfigurationReclaimRun(t, w, r, p, identity, request)
	if err != nil || none.decision != receiptFresh || none.result.removedKind != "none" ||
		none.result.removedCount != 0 || none.result.removedSequence.Valid || none.result.removedBytes != 0 ||
		none.result.rows != 0 || none.result.bytes != 0 || !none.result.noNext || !none.result.resultOrdinal.Valid {
		t.Fatal("empty ACK turn refunded or removed", none, err)
	}
	last := iaConfigurationReclaimRead(t, w)
	if last.rows != 0 || last.bytes != 0 || last.acknowledged != identity.Sequence || last.last != identity.Sequence {
		t.Fatal("empty ACK changed debt", last)
	}
	// Post-retirement resolve: the original configure result is now Retired and
	// is never decoded from the reclaimed cohort.
	configure := iaConfigurationReclaimRegistry(t, keeper, iaConfigureLibraryDefinitions())
	q := iaConfigureLibraryRequest{
		reference: configured, libraryID: 777, libraryKey: "lib-reclaim", name: "Reclaim", kind: "movie",
		sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/media/reclaim"), iaConfigureLibrarySourceFixture(1, "/media/reclaim/4k")},
	}
	turn, budget := iaConfigureLibraryTurn(t, w, configure, q)
	resolve, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
	turn.transaction.rollback(nil)
	if err != nil || resolve.decision != receiptRetired || resolve.digest != (ResultDigest{}) || resolve.result.facts.resultCount != 0 {
		t.Fatal("post-reclaim configure resolve", resolve, err)
	}
}

func TestIAConfigurationReclaimReplayOldSlotAndRetired(t *testing.T) {
	w, keeper, r, p := iaConfigurationReclaimFixture(t)
	_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
	firstRequest := request
	firstIdentity := iaRetirementBeginIdentity(50, 1, 2)
	first, err := iaConfigurationReclaimRun(t, w, r, p, firstIdentity, firstRequest)
	if err != nil || first.decision != receiptFresh {
		t.Fatal(first, err)
	}
	// The same original command replays its exact slot result, with no effects.
	replay, err := iaConfigurationReclaimRun(t, w, r, p, firstIdentity, firstRequest)
	if err != nil || replay.decision != receiptReplay || replay.digest != first.digest || replay.result != first.result {
		t.Fatal("current slot replay", replay, err)
	}
	if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_sources`); got != 1 {
		t.Fatal("replay deleted a second child", got)
	}
	// A second turn overwrites the one fixed slot. The first turn is then an
	// ACKed original: Retired, no decoded result and no new effects.
	request.latestSequence = 2
	second, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(52, 1, 3), request)
	if err != nil || second.decision != receiptFresh {
		t.Fatal(second, err)
	}
	retired, err := iaConfigurationReclaimRun(t, w, r, p, firstIdentity, firstRequest)
	if err != nil || retired.decision != receiptRetired || retired.digest != (ResultDigest{}) || retired.result.removedKind != "" {
		t.Fatal("superseded slot was decoded", retired, err)
	}
	if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_receipts`); got != 1 {
		t.Fatal("retired replay deleted the header", got)
	}
	// A caller-supplied conflicting digest on the live slot identity is refused;
	// it is never a fresh turn.
	bad := iaConfigurationReclaimReference(t, iaRetirementBeginIdentity(52, 1, 3), request)
	bad.RequestDigest[0]++
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAConfigureReclaim, action: iaEntryApply, reference: bad})
	out, err := reclaimIAConfiguration(t.Context(), turn, p, t.Context(), budget, bad, request)
	turn.transaction.rollback(nil)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("conflicting digest admitted", out, err)
	}
}

func TestIAConfigurationReclaimRefusals(t *testing.T) {
	for name, mutate := range map[string]string{
		"ledger_rows":      `UPDATE catalog_configuration_operations SET receipt_retained_count=receipt_retained_count+1`,
		"ledger_bytes":     `UPDATE catalog_configuration_operations SET receipt_retained_bytes=receipt_retained_bytes+1`,
		"children_removed": `DELETE FROM catalog_configuration_sources`,
		"source_gap":       `DELETE FROM catalog_configuration_sources WHERE ordinal=0`,
		"orphan_source":    `DELETE FROM catalog_configuration_sources WHERE ordinal=1`,
		"header_count":     `UPDATE catalog_configuration_receipts SET result_count=3`,
		"child_bytes":      `UPDATE catalog_configuration_sources SET source_name='X',retained_bytes=retained_bytes-6 WHERE ordinal=0`,
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, r, p := iaConfigurationReclaimFixture(t)
			_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
			wc008NativeExec(t, w, mutate)
			rows := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_sources`)
			receipts := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_receipts`)
			out, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(60, 1, 2), request)
			if !errors.Is(err, ErrResolveRequired) || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("incomplete owner proof admitted", out, err)
			}
			if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_sources`); got != rows {
				t.Fatal("refusal deleted a child", got, rows)
			}
			if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_receipts`); got != receipts {
				t.Fatal("refusal deleted the header", got, receipts)
			}
			if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_operations WHERE reclaim_command_sequence IS NULL`); got != 1 {
				t.Fatal("refusal wrote the slot", got)
			}
		})
	}
	t.Run("future_sequence", func(t *testing.T) {
		w, keeper, r, p := iaConfigurationReclaimFixture(t)
		_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
		request.latestSequence = 2
		out, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(62, 1, 3), request)
		if !errors.Is(err, ErrConflict) || out.decision != 0 {
			t.Fatal("future sequence admitted", out, err)
		}
	})
	t.Run("old_sequence", func(t *testing.T) {
		w, keeper, r, p := iaConfigurationReclaimFixture(t)
		_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
		firstRequest := request
		first, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(63, 1, 2), firstRequest)
		if err != nil || first.decision != receiptFresh {
			t.Fatal(first, err)
		}
		// The first turn is now ACKed; a second turn overwrites the one fixed
		// slot, so replaying the first original is Retired, never re-executed.
		request.latestSequence = 2
		if second, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(64, 1, 3), request); err != nil || second.decision != receiptFresh {
			t.Fatal(second, err)
		}
		retired, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(63, 1, 2), firstRequest)
		if err != nil || retired.decision != receiptRetired || retired.digest != (ResultDigest{}) {
			t.Fatal("old ACKed sequence re-executed", retired, err)
		}
	})
	for name, bad := range map[string]iaConfigurationReclaimRequest{
		"wrong_library": func() iaConfigurationReclaimRequest {
			q := iaConfigurationReclaimRequest{libraryID: 778, libraryKey: "lib-reclaim", latestSequence: 1}
			return q
		}(),
		"wrong_source": func() iaConfigurationReclaimRequest {
			q := iaConfigurationReclaimRequest{libraryID: 777, libraryKey: "lib-reclaim", sourceID: "source-x", latestSequence: 1}
			return q
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, r, p := iaConfigurationReclaimFixture(t)
			_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
			bad.originalOperation = request.originalOperation
			out, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(64, 1, 2), bad)
			if !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
				t.Fatal("wrong owner target admitted", out, err)
			}
		})
	}
}

func TestIAConfigurationReclaimSourceOwner(t *testing.T) {
	w, keeper, r, p := iaConfigurationReclaimFixture(t)
	sourceRegister := iaConfigurationReclaimRegistry(t, keeper, iaConfigureSourceDefinitions())
	root := iaConfigureSourceRoot(t, "media")
	iaConfigureSourceLibrary(t, w, 888, "lib-source-reclaim", root)
	iaConfigureSourceLive(t, w, "lib-source-reclaim", "source-1", 1, "incarnation-1", root, "identity-1", true, "ready", true)
	q := iaConfigureSourceRequestFixture(t, 888, "lib-source-reclaim", "source-1")
	turn, budget := iaConfigureSourceTurn(t, w, sourceRegister, q)
	if out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
		t.Fatal("seed source configuration", out, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var operation []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT original_operation_id FROM catalog_configuration_operations`).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	request := iaConfigurationReclaimRequest{libraryID: 888, libraryKey: "lib-source-reclaim", sourceID: "source-1", latestSequence: 1}
	copy(request.originalOperation[:], operation)
	// A library-shaped request cannot reclaim a source owner.
	bad := request
	bad.sourceID = ""
	if out, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(70, 1, 2), bad); !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
		t.Fatal("source owner admitted a library target", out, err)
	}
	child, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(70, 1, 2), request)
	if err != nil || child.decision != receiptFresh || child.result.removedKind != "source" ||
		child.result.removedOrdinal.Int64 != 0 || child.result.resultSequence != 0 {
		t.Fatal("source child turn", child, err)
	}
	request.latestSequence = 2
	header, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(72, 1, 3), request)
	if err != nil || header.decision != receiptFresh || header.result.removedKind != "header" || !header.result.noNext {
		t.Fatal("source header turn", header, err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM catalog_configuration_receipts`,
		`SELECT count(*) FROM catalog_configuration_sources`,
	} {
		if got := iaConfigurationReclaimCount(t, w, query); got != 0 {
			t.Fatal("source debt retained", query, got)
		}
	}
}

func TestIAConfigurationReclaimCurrentDenial(t *testing.T) {
	for name, mutation := range map[string]string{
		"owner_role":     `UPDATE direct_memberships SET role='member'`,
		"token_retired":  `UPDATE authorization_family_tokens SET retired=1`,
		"deny_fence":     `UPDATE identity_security_fences SET denied=1`,
		"stale_lifetime": `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, r, p := iaConfigurationReclaimFixture(t)
			_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
			wc008NativeExec(t, w, mutation)
			out, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(80, 1, 2), request)
			if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("denied authority reached history", out, err)
			}
			if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_sources`); got != 2 {
				t.Fatal("denial deleted a child", got)
			}
		})
	}
	t.Run("forged_issuer", func(t *testing.T) {
		w, keeper, r, p := iaConfigurationReclaimFixture(t)
		_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
		fake := *p
		fake.witness.issuer.family = "forged"
		out, err := iaConfigurationReclaimRun(t, w, r, &fake, iaRetirementBeginIdentity(82, 1, 2), request)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 {
			t.Fatal("forged issuer admitted", out, err)
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, keeper, r, p := iaConfigurationReclaimFixture(t)
		_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
		ref := iaConfigurationReclaimReference(t, iaRetirementBeginIdentity(83, 1, 2), request)
		turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAConfigureReclaim, action: iaEntryApply, reference: ref})
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := reclaimIAConfiguration(context.Background(), turn, p, cancelled, budget, ref, request)
		turn.transaction.rollback(nil)
		if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("cancelled lifetime released a result", out, err)
		}
		if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_sources`); got != 2 {
			t.Fatal("cancelled lifetime deleted a child", got)
		}
	})
}

func TestIAConfigurationReclaimLateRevocationAfterWrites(t *testing.T) {
	w, keeper, r, p := iaConfigurationReclaimFixture(t)
	_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
	wc008NativeExec(t, w, `CREATE TRIGGER ia_configuration_reclaim_late_revocation AFTER UPDATE ON catalog_configuration_operations BEGIN UPDATE identity_security_fences SET denied=1; END`)
	ref := iaConfigurationReclaimReference(t, iaRetirementBeginIdentity(84, 1, 2), request)
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAConfigureReclaim, action: iaEntryApply, reference: ref})
	out, err := reclaimIAConfiguration(t.Context(), turn, p, t.Context(), budget, ref, request)
	if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 {
		t.Fatal("late post-write recheck", out, err)
	}
	// The child delete and slot write were both reached before the recheck.
	var deleted, slotted int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_configuration_sources),(SELECT count(*) FROM catalog_configuration_operations WHERE reclaim_command_sequence IS NOT NULL)`).Scan(&deleted, &slotted); err != nil || deleted != 1 || slotted != 1 {
		t.Fatal("effects not reached", deleted, slotted, err)
	}
	turn.transaction.rollback(nil)
	if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_sources`); got != 2 {
		t.Fatal("rollback retained the child delete", got)
	}
	if got := iaConfigurationReclaimCount(t, w, `SELECT count(*) FROM catalog_configuration_operations WHERE reclaim_command_sequence IS NULL`); got != 1 {
		t.Fatal("rollback retained the slot", got)
	}
}

func TestIAConfigurationReclaimUnknownCommitFreshRead(t *testing.T) {
	w, keeper, r, p := iaConfigurationReclaimFixture(t)
	_, request := iaConfigurationReclaimSeed(t, w, keeper, p)
	path := iaConfigureLibraryPath(t, w)
	ref := iaConfigurationReclaimReference(t, iaRetirementBeginIdentity(85, 1, 2), request)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAConfigureReclaim, action: iaEntryApply, reference: ref})
	if out, err := reclaimIAConfiguration(t.Context(), turn, p, t.Context(), budget, ref, request); err != nil || out.decision != receiptFresh {
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
		{`SELECT count(*) FROM catalog_configuration_sources`, 1},
		{`SELECT count(*) FROM catalog_configuration_operations WHERE last_command_sequence=2 AND acknowledged_command_sequence=2 AND reclaim_command_sequence=2`, 1},
		{`SELECT count(*) FROM catalog_configuration_receipts`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	// The executor remains fenced: an ambiguous COMMIT is never success.
	if _, err = w.begin(t.Context(), IAConfigureReclaim, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

func TestIAConfigurationReclaimBoundedRequest(t *testing.T) {
	base := iaConfigurationReclaimRequest{libraryID: 777, libraryKey: "lib-bounds", latestSequence: 1}
	base.originalOperation = [16]byte{1}
	for name, mutate := range map[string]func(*iaConfigurationReclaimRequest){
		"empty_operation": func(q *iaConfigurationReclaimRequest) { q.originalOperation = [16]byte{} },
		"zero_library":    func(q *iaConfigurationReclaimRequest) { q.libraryID = 0 },
		"empty_key":       func(q *iaConfigurationReclaimRequest) { q.libraryKey = "" },
		"long_key":        func(q *iaConfigurationReclaimRequest) { q.libraryKey = strings.Repeat("k", 129) },
		"zero_latest":     func(q *iaConfigurationReclaimRequest) { q.latestSequence = 0 },
		"max_latest":      func(q *iaConfigurationReclaimRequest) { q.latestSequence = 1<<63 - 1 },
		"nul_source":      func(q *iaConfigurationReclaimRequest) { q.sourceID = "source\x00" },
	} {
		t.Run(name, func(t *testing.T) {
			q := base
			mutate(&q)
			if _, err := newIAConfigurationReclaimCommand(iaRetirementBeginIdentity(90, 91, 2), q); !errors.Is(err, ErrInvalid) {
				t.Fatal("bounded request admitted", err)
			}
		})
	}
	// A sequence other than latest+1 is not this turn at all.
	if _, err := newIAConfigurationReclaimCommand(iaRetirementBeginIdentity(90, 91, 3), base); !errors.Is(err, ErrInvalid) {
		t.Fatal("sequence gap admitted", err)
	}
	t.Run("missing_owner", func(t *testing.T) {
		w, _, r, p := iaConfigurationReclaimFixture(t)
		q := base
		q.originalOperation = [16]byte{0xee}
		out, err := iaConfigurationReclaimRun(t, w, r, p, iaRetirementBeginIdentity(90, 91, 2), q)
		if !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
			t.Fatal("absent owner admitted", out, err)
		}
	})
}
