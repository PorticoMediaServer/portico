package writer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func iaRetirementBeginFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-retirement-begin-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	registry, err := stmttest.New(t, iaRetirementBeginDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaRetirementBeginLibrarySeed(t *testing.T, w *executor, id int64, key, root, name string, revision int64) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(?,?,?)`, id, key, root)
	wc008NativeExec(t, w, `INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES(?,?,'movie',?,1,?,?)`,
		key, name, root, revision, revision)
}

func iaRetirementBeginSourceSeed(t *testing.T, w *executor, libraryKey, id string, generation int64, incarnation, root, identity string) {
	t.Helper()
	iaConfigureSourceLive(t, w, libraryKey, id, generation, incarnation, root, identity, true, "ready", true)
}

func iaRetirementBeginIdentity(operation, incarnation byte, sequence int64) CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{incarnation}, Sequence: sequence, OperationID: [16]byte{operation}}
}

func iaRetirementBeginLibraryRequest(key string, id, revision int64, root, name, confirmation string) iaRetirementBeginRequest {
	return iaRetirementBeginRequest{
		scope:       iaRetirementBeginScope{libraryID: id, libraryKey: key, libraryRevision: revision, resolvedRoot: root},
		libraryName: name, confirmation: confirmation,
	}
}

func iaRetirementBeginSourceRequest(key, sourceID string, id, revision, generation int64, root, incarnation, identity string, pins bool) iaRetirementBeginRequest {
	return iaRetirementBeginRequest{
		scope: iaRetirementBeginScope{libraryID: id, libraryKey: key, sourceID: sourceID, sourceIncarnation: incarnation,
			sourceGeneration: generation, libraryRevision: revision, resolvedRoot: root, rootIdentity: identity},
		sourcePinsResolved: pins,
	}
}

// A valid reference binds the exact canonical request digest this writer would
// recompute; a caller's mismatch is tested separately as a conflict.
func iaRetirementBeginPrepared(t *testing.T, purpose CommandID, identity CommandIdentity, request iaRetirementBeginRequest) ReceiptReference {
	t.Helper()
	command, err := newIARetirementBeginCommand(purpose, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: identity, RequestDigest: command.digest}
}

func iaRetirementBeginTurn(t *testing.T, w *executor, r *iaEntryRegistry, purpose CommandID, ref ReceiptReference) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: purpose, action: iaEntryApply, reference: ref})
}

func iaRetirementBeginRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, purpose CommandID, ref ReceiptReference, request iaRetirementBeginRequest) (*commandTurn, iaRetireBeginOutcome, error) {
	t.Helper()
	turn, budget := iaRetirementBeginTurn(t, w, r, purpose, ref)
	out, err := beginIARetirement(t.Context(), turn, p, t.Context(), budget, ref, request)
	return turn, out, err
}

func iaRetirementBeginCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// One committed library BEGIN on a private library, used by the read-side cells.
func iaRetirementBeginCommittedLibrary(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, ReceiptReference, iaRetirementBeginRequest, iaRetireBeginOutcome) {
	t.Helper()
	w, r, p := iaRetirementBeginFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 900, "lib-history", "/media/history", "Library", 5)
	iaRetirementBeginSourceSeed(t, w, "lib-history", "source-00", 1, "incarnation-00", "/media/history", "identity-00")
	identity := iaRetirementBeginIdentity(20, 21, 1)
	request := iaRetirementBeginLibraryRequest("lib-history", 900, 5, "/media/history", "Library", "Library")
	ref := iaRetirementBeginPrepared(t, IARetireLibrary, identity, request)
	turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("seed committed library begin", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return w, r, p, ref, request, out
}

func TestIARetirementBeginLibraryFenceReplayRetired(t *testing.T) {
	w, r, p := iaRetirementBeginFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 777, "lib-retire", "/media/retire", "Library", 5)
	iaRetirementBeginSourceSeed(t, w, "lib-retire", "source-00", 1, "incarnation-00", "/media/retire", "identity-00")
	identity := iaRetirementBeginIdentity(2, 1, 1)
	request := iaRetirementBeginLibraryRequest("lib-retire", 777, 5, "/media/retire", "Library", " Library ")
	ref := iaRetirementBeginPrepared(t, IARetireLibrary, identity, request)
	turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) ||
		out.result.original.kind != "library" || out.result.original.target != "lib-retire" ||
		out.result.original.libraryID != 777 || out.result.original.resolvedRoot != "/media/retire" ||
		out.result.acceptedRevision != 1 || out.result.appliedMS != w.now().UnixMilli() || out.result.state != "retiring" {
		t.Fatal("fresh library begin", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_libraries WHERE id=777 AND library_id='lib-retire' AND retired=1`, 1},
		{`SELECT count(*) FROM libraries WHERE id='lib-retire' AND configuration_revision=6`, 1},
		{`SELECT count(*) FROM library_sources WHERE id='source-00' AND enabled=1 AND generation=1`, 1},
		{`SELECT count(*) FROM catalog_removal_progress WHERE kind='library' AND target='lib-retire' AND original_library_id=777 AND last_command_sequence=1 AND acknowledged_command_sequence=0 AND retirement_state='retiring' AND phase=0`, 1},
		{`SELECT count(*) FROM catalog_removal_receipts WHERE command_kind='retire_library' AND result_kind='library' AND result_target='lib-retire' AND result_state='retiring' AND result_count=0`, 1},
	} {
		if got := iaRetirementBeginCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// A saved original survives deletion of the live library and source rows.
	wc008NativeExec(t, w, `DELETE FROM library_sources WHERE library_id='lib-retire'`)
	wc008NativeExec(t, w, `DELETE FROM libraries WHERE id='lib-retire'`)
	turn, replay, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result != out.result {
		t.Fatal("replay after live deletion", replay, err)
	}
	turn.transaction.rollback(nil)
	// ACK alone retires the result; the retained driver and exact receipt bytes
	// are never refunded by this task.
	wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET acknowledged_command_sequence=last_command_sequence`)
	turn, retired, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
	if err != nil || retired.decision != receiptRetired || retired.result != (iaRetirementResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal("ACKed result decoded", retired, err)
	}
	turn.transaction.rollback(nil)
	if got := iaRetirementBeginCount(t, w, `SELECT count(*) FROM catalog_removal_progress WHERE kind='library' AND target='lib-retire'`); got != 1 {
		t.Fatal("retained driver lost", got)
	}
	if got := iaRetirementBeginCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 1 {
		t.Fatal("ACK refunded the exact receipt", got)
	}
	// The ACK floor is honoured even when the body is physically gone.
	wc008NativeExec(t, w, `DELETE FROM catalog_removal_receipts`)
	turn, retired, err = iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
	if err != nil || retired.decision != receiptRetired || retired.result != (iaRetirementResult{}) {
		t.Fatal("ACKed body was queried", retired, err)
	}
	turn.transaction.rollback(nil)
}

func TestIARetirementBeginSourceFenceNoCascade(t *testing.T) {
	w, r, p := iaRetirementBeginFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 778, "lib-source", "/media/source", "Library", 5)
	iaRetirementBeginSourceSeed(t, w, "lib-source", "source-00", 3, "incarnation-00", "/media/source", "identity-00")
	iaRetirementBeginSourceSeed(t, w, "lib-source", "source-01", 1, "incarnation-01", "/media/source/other", "identity-01")
	wc008NativeExec(t, w, `INSERT INTO inventory_strict_intents(source_id,relative_path,library_id,source_generation,root_incarnation,root_identity,root,configured_root,source_kind,follow_symlinks,mode,created_ms)
VALUES('source-00','.','lib-source',3,'incarnation-00','identity-00','/media/source','/media/source','local',0,'incremental',1)`)
	identity := iaRetirementBeginIdentity(4, 3, 1)
	request := iaRetirementBeginSourceRequest("lib-source", "source-00", 778, 5, 3, "/media/source", "incarnation-00", "identity-00", true)
	ref := iaRetirementBeginPrepared(t, IARetireSource, identity, request)
	turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireSource, ref, request)
	if err != nil || out.decision != receiptFresh || out.result.original.kind != "folder" || out.result.original.target != "source-00" ||
		out.result.original.sourceIncarnation != "incarnation-00" || out.result.original.sourceGeneration != 3 ||
		out.result.original.rootIdentity != "identity-00" || out.result.acceptedRevision != 1 {
		t.Fatal("fresh source begin", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM library_sources WHERE id='source-00' AND enabled=0 AND health='removing' AND generation=4 AND next_scan_at=''`, 1},
		{`SELECT count(*) FROM library_sources WHERE id='source-01' AND enabled=1 AND health='ready' AND generation=1`, 1},
		{`SELECT count(*) FROM libraries WHERE id='lib-source' AND configuration_revision=6`, 1},
		{`SELECT count(*) FROM catalog_libraries WHERE id=778 AND retired=0`, 1},
		{`SELECT count(*) FROM inventory_strict_intents WHERE source_id='source-00'`, 1},
		{`SELECT count(*) FROM catalog_removal_progress WHERE kind='folder' AND target='source-00' AND original_source_incarnation='incarnation-00' AND original_source_generation=3 AND original_root_identity='identity-00' AND receipt_retained_count=1`, 1},
		{`SELECT count(*) FROM catalog_removal_receipts WHERE command_kind='retire_source' AND result_kind='folder' AND result_target='source-00'`, 1},
	} {
		if got := iaRetirementBeginCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// A saved original survives deletion of the live library, source and queued
	// intent rows; the retained catalog namespace anchor remains.
	wc008NativeExec(t, w, `DELETE FROM inventory_strict_intents WHERE source_id='source-00'`)
	wc008NativeExec(t, w, `DELETE FROM library_sources WHERE library_id='lib-source'`)
	wc008NativeExec(t, w, `DELETE FROM libraries WHERE id='lib-source'`)
	turn, replay, err := iaRetirementBeginRun(t, w, r, p, IARetireSource, ref, request)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result != out.result {
		t.Fatal("source replay after live deletion", replay, err)
	}
	turn.transaction.rollback(nil)
	// No cascade: the fence either keeps rows or rolls them back, never deletes.
	wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET acknowledged_command_sequence=1`)
	turn, retired, err := iaRetirementBeginRun(t, w, r, p, IARetireSource, ref, request)
	if err != nil || retired.decision != receiptRetired || retired.result != (iaRetirementResult{}) {
		t.Fatal("source ACKed replay", retired, err)
	}
	turn.transaction.rollback(nil)
	for _, query := range []string{
		`SELECT count(*) FROM catalog_removal_progress WHERE kind='folder' AND target='source-00'`,
		`SELECT count(*) FROM catalog_removal_receipts WHERE result_target='source-00'`,
	} {
		if got := iaRetirementBeginCount(t, w, query); got != 1 {
			t.Fatal("retained proof removed", query, got)
		}
	}
}

func TestIARetirementBeginSourcePinSeamFailsClosed(t *testing.T) {
	w, r, p := iaRetirementBeginFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 779, "lib-pins", "/media/pins", "Library", 5)
	iaRetirementBeginSourceSeed(t, w, "lib-pins", "source-00", 1, "incarnation-00", "/media/pins", "identity-00")
	identity := iaRetirementBeginIdentity(6, 5, 1)
	request := iaRetirementBeginSourceRequest("lib-pins", "source-00", 779, 5, 1, "/media/pins", "incarnation-00", "identity-00", false)
	ref := iaRetirementBeginPrepared(t, IARetireSource, identity, request)
	turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireSource, ref, request)
	if !errors.Is(err, iaRetirementSourcePinsUnresolved) || !errors.Is(err, ErrResolveRequired) || out != (iaRetireBeginOutcome{}) {
		t.Fatal("unresolved MP-10 pin admitted a source begin", out, err)
	}
	turn.transaction.rollback(nil)
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_removal_progress`, 0},
		{`SELECT count(*) FROM catalog_removal_receipts`, 0},
		{`SELECT count(*) FROM library_sources WHERE id='source-00' AND enabled=1 AND generation=1`, 1},
		{`SELECT count(*) FROM libraries WHERE id='lib-pins' AND configuration_revision=5`, 1},
	} {
		if got := iaRetirementBeginCount(t, w, check.query); got != check.want {
			t.Fatal("pin seam wrote or changed", check.query, got, check.want)
		}
	}
}

func TestIARetirementBeginConflicts(t *testing.T) {
	w, r, p := iaRetirementBeginFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 780, "lib-conflict", "/media/conflict", "Library", 5)
	iaRetirementBeginLibrarySeed(t, w, 781, "lib-other", "/media/other", "Other", 5)
	iaRetirementBeginLibrarySeed(t, w, 782, "lib-third", "/media/third", "Third", 5)
	iaRetirementBeginSourceSeed(t, w, "lib-conflict", "source-00", 1, "incarnation-00", "/media/conflict", "identity-00")
	identity := iaRetirementBeginIdentity(8, 7, 1)
	request := iaRetirementBeginLibraryRequest("lib-conflict", 780, 5, "/media/conflict", "Library", "Library")
	ref := iaRetirementBeginPrepared(t, IARetireLibrary, identity, request)
	turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// The retired namespace is now fenced; later retired-live cases use lib-third.
	wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=782`)
	for name, tc := range map[string]struct {
		purpose  CommandID
		identity CommandIdentity
		request  iaRetirementBeginRequest
		ref      ReceiptReference
		want     error
	}{
		"same_id_new_incarnation":      {IARetireLibrary, iaRetirementBeginIdentity(8, 9, 1), request, ReceiptReference{}, ErrConflict},
		"same_target_new_operation":    {IARetireLibrary, iaRetirementBeginIdentity(10, 7, 1), request, ReceiptReference{}, ErrConflict},
		"same_operation_other_target":  {IARetireLibrary, identity, iaRetirementBeginLibraryRequest("lib-other", 781, 5, "/media/other", "Other", "Other"), ReceiptReference{}, ErrConflict},
		"stale_configuration_revision": {IARetireLibrary, iaRetirementBeginIdentity(11, 7, 1), iaRetirementBeginLibraryRequest("lib-other", 781, 4, "/media/other", "Other", "Other"), ReceiptReference{}, ErrConflict},
		"live_library_retired":         {IARetireLibrary, iaRetirementBeginIdentity(12, 7, 1), iaRetirementBeginLibraryRequest("lib-third", 782, 5, "/media/third", "Third", "Third"), ReceiptReference{}, ErrConflict},
		"changed_request_digest": {IARetireLibrary, identity, func() iaRetirementBeginRequest {
			other := request
			other.confirmation = "Library "
			return other
		}(), ref, ErrConflict},
	} {
		t.Run(name, func(t *testing.T) {
			prepared := tc.ref
			if prepared == (ReceiptReference{}) {
				prepared = iaRetirementBeginPrepared(t, tc.purpose, tc.identity, tc.request)
			}
			turn, out, err := iaRetirementBeginRun(t, w, r, p, tc.purpose, prepared, tc.request)
			if !errors.Is(err, tc.want) || out != (iaRetireBeginOutcome{}) {
				t.Fatal("conflict admitted", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaRetirementBeginCount(t, w, `SELECT count(*) FROM catalog_removal_progress`); got != 1 {
				t.Fatal("conflict wrote a driver", got)
			}
			if got := iaRetirementBeginCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 1 {
				t.Fatal("conflict minted a receipt", got)
			}
		})
	}
	// A sequence other than one is not an original BEGIN at all.
	seqTurn, seqBudget := iaRetirementBeginTurn(t, w, r, IARetireLibrary, ReceiptReference{
		Identity: iaRetirementBeginIdentity(13, 7, 2), RequestDigest: RequestDigest{1},
	})
	seqOut, seqErr := beginIARetirement(t.Context(), seqTurn, p, t.Context(), seqBudget,
		ReceiptReference{Identity: iaRetirementBeginIdentity(13, 7, 2), RequestDigest: RequestDigest{1}}, request)
	seqTurn.transaction.rollback(nil)
	if !errors.Is(seqErr, ErrInvalid) || seqOut != (iaRetireBeginOutcome{}) {
		t.Fatal("sequence two admitted", seqOut, seqErr)
	}
}

func TestIARetirementBeginCurrentDenial(t *testing.T) {
	for name, mutation := range map[string]string{
		"owner_role":     `UPDATE direct_memberships SET role='member'`,
		"token_retired":  `UPDATE authorization_family_tokens SET retired=1`,
		"deny_fence":     `UPDATE identity_security_fences SET denied=1`,
		"stale_lifetime": `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p, ref, request, _ := iaRetirementBeginCommittedLibrary(t)
			wc008NativeExec(t, w, mutation)
			turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
			if !errors.Is(err, ErrDisclosureDenied) || out != (iaRetireBeginOutcome{}) {
				t.Fatal("denied authority reached history", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaRetirementBeginCount(t, w, `SELECT count(*) FROM catalog_removal_progress`); got != 1 {
				t.Fatal("denial wrote or removed a driver", got)
			}
		})
	}
	t.Run("forged_issuer", func(t *testing.T) {
		w, r, p, ref, request, _ := iaRetirementBeginCommittedLibrary(t)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaRetirementBeginRun(t, w, r, &fake, IARetireLibrary, ref, request)
		if !errors.Is(err, ErrDisclosureDenied) || out != (iaRetireBeginOutcome{}) {
			t.Fatal("forged issuer admitted", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p := iaRetirementBeginFixture(t)
		iaRetirementBeginLibrarySeed(t, w, 783, "lib-cancel", "/media/cancel", "Library", 5)
		identity := iaRetirementBeginIdentity(14, 7, 1)
		request := iaRetirementBeginLibraryRequest("lib-cancel", 783, 5, "/media/cancel", "Library", "Library")
		ref := iaRetirementBeginPrepared(t, IARetireLibrary, identity, request)
		turn, budget := iaRetirementBeginTurn(t, w, r, IARetireLibrary, ref)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := beginIARetirement(t.Context(), turn, p, cancelled, budget, ref, request)
		if err == nil || out != (iaRetireBeginOutcome{}) {
			t.Fatal("cancelled lifetime released a result", out, err)
		}
		turn.transaction.rollback(nil)
		for _, query := range []string{`SELECT count(*) FROM catalog_removal_progress`, `SELECT count(*) FROM catalog_removal_receipts`} {
			if got := iaRetirementBeginCount(t, w, query); got != 0 {
				t.Fatal("cancelled lifetime wrote", query, got)
			}
		}
	})
}

func TestIARetirementBeginStaleLiveWitness(t *testing.T) {
	for name, tc := range map[string]struct {
		purpose CommandID
		mutate  func(*iaRetirementBeginRequest)
		live    string
	}{
		"source_revision":    {IARetireSource, func(r *iaRetirementBeginRequest) { r.scope.libraryRevision = 4 }, ""},
		"source_generation":  {IARetireSource, func(r *iaRetirementBeginRequest) { r.scope.sourceGeneration = 2 }, ""},
		"source_root":        {IARetireSource, func(r *iaRetirementBeginRequest) { r.scope.resolvedRoot = "/media/other" }, ""},
		"source_identity":    {IARetireSource, func(r *iaRetirementBeginRequest) { r.scope.rootIdentity = "other-identity" }, ""},
		"library_stale_name": {IARetireLibrary, func(r *iaRetirementBeginRequest) {}, `UPDATE libraries SET name='Renamed' WHERE id='lib-stale'`},
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaRetirementBeginFixture(t)
			iaRetirementBeginLibrarySeed(t, w, 788, "lib-stale", "/media/stale", "Library", 5)
			iaRetirementBeginSourceSeed(t, w, "lib-stale", "source-00", 3, "incarnation-00", "/media/stale", "identity-00")
			if tc.live != "" {
				wc008NativeExec(t, w, tc.live)
			}
			request := iaRetirementBeginLibraryRequest("lib-stale", 788, 5, "/media/stale", "Library", "Library")
			if tc.purpose == IARetireSource {
				request = iaRetirementBeginSourceRequest("lib-stale", "source-00", 788, 5, 3, "/media/stale", "incarnation-00", "identity-00", true)
			}
			tc.mutate(&request)
			identity := iaRetirementBeginIdentity(30, 31, 1)
			ref := iaRetirementBeginPrepared(t, tc.purpose, identity, request)
			turn, out, err := iaRetirementBeginRun(t, w, r, p, tc.purpose, ref, request)
			if !errors.Is(err, ErrConflict) || out != (iaRetireBeginOutcome{}) {
				t.Fatal("stale live witness admitted", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaRetirementBeginCount(t, w, `SELECT count(*) FROM catalog_removal_progress`); got != 0 {
				t.Fatal("stale witness wrote a driver", got)
			}
			if got := iaRetirementBeginCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 0 {
				t.Fatal("stale witness minted a receipt", got)
			}
		})
	}
}

func TestIARetirementBeginLateRevocationAfterWrites(t *testing.T) {
	w, r, p := iaRetirementBeginFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 784, "lib-late", "/media/late", "Library", 5)
	wc008NativeExec(t, w, `CREATE TRIGGER ia_retirement_late_revocation AFTER INSERT ON catalog_removal_progress BEGIN UPDATE identity_security_fences SET denied=1; END`)
	identity := iaRetirementBeginIdentity(15, 7, 1)
	request := iaRetirementBeginLibraryRequest("lib-late", 784, 5, "/media/late", "Library", "Library")
	ref := iaRetirementBeginPrepared(t, IARetireLibrary, identity, request)
	turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
	if !errors.Is(err, ErrDisclosureDenied) || out != (iaRetireBeginOutcome{}) {
		t.Fatal("late post-write recheck", out, err)
	}
	// The effects were reached before the final recheck; rollback removes them.
	var reached int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_removal_progress`).Scan(&reached); err != nil || reached != 1 {
		t.Fatal("driver insert not reached", reached, err)
	}
	turn.transaction.rollback(nil)
	for _, query := range []string{
		`SELECT count(*) FROM catalog_removal_progress`,
		`SELECT count(*) FROM catalog_removal_receipts`,
		`SELECT count(*) FROM catalog_libraries WHERE id=784 AND retired=1`,
	} {
		if got := iaRetirementBeginCount(t, w, query); got != 0 {
			t.Fatal("rollback retained", query, got)
		}
	}
}

func TestIARetirementBeginBoundedInput(t *testing.T) {
	w, r, p := iaRetirementBeginFixture(t)
	base := iaRetirementBeginLibraryRequest("lib-bounds", 785, 5, "/media/bounds", "Library", "Library")
	source := iaRetirementBeginSourceRequest("lib-bounds", "source-00", 785, 5, 1, "/media/bounds", "incarnation-00", "identity-00", true)
	for name, tc := range map[string]struct {
		purpose CommandID
		mutate  func(*iaRetirementBeginRequest)
	}{
		"empty_key":        {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.scope.libraryKey = "" }},
		"relative_root":    {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.scope.resolvedRoot = "media/bounds" }},
		"unclean_root":     {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.scope.resolvedRoot = "/media/../bounds" }},
		"empty_name":       {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.libraryName = "" }},
		"padded_name":      {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.libraryName = " Library" }},
		"confirmation":     {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.confirmation = "Other" }},
		"long_name":        {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.libraryName = strings.Repeat("n", 101) }},
		"zero_revision":    {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.scope.libraryRevision = 0 }},
		"source_missing":   {IARetireSource, func(r *iaRetirementBeginRequest) { r.scope.sourceID = "" }},
		"source_zero_gen":  {IARetireSource, func(r *iaRetirementBeginRequest) { r.scope.sourceGeneration = 0 }},
		"source_max_gen":   {IARetireSource, func(r *iaRetirementBeginRequest) { r.scope.sourceGeneration = 9223372036854775807 }},
		"source_identity":  {IARetireSource, func(r *iaRetirementBeginRequest) { r.scope.rootIdentity = "" }},
		"library_with_src": {IARetireLibrary, func(r *iaRetirementBeginRequest) { r.scope.sourceID = "source-00" }},
		"source_with_name": {IARetireSource, func(r *iaRetirementBeginRequest) { r.libraryName = "Library" }},
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			if tc.purpose == IARetireSource {
				request = source
			}
			tc.mutate(&request)
			if _, err := newIARetirementBeginCommand(tc.purpose, iaRetirementBeginIdentity(16, 7, 1), request); !errors.Is(err, ErrInvalid) {
				t.Fatal("bounded input admitted", err)
			}
		})
	}
	// Maximum accepted text is still a valid canonical request: a 100-byte name.
	long := strings.Repeat("N", 100)
	iaRetirementBeginLibrarySeed(t, w, 786, "lib-max", "/media/max", long, 5)
	request := iaRetirementBeginLibraryRequest("lib-max", 786, 5, "/media/max", long, long)
	identity := iaRetirementBeginIdentity(17, 7, 1)
	ref := iaRetirementBeginPrepared(t, IARetireLibrary, identity, request)
	turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
	if err != nil || out.decision != receiptFresh || out.result.original.target != "lib-max" {
		t.Fatal("maximum accepted name", out, err)
	}
	turn.transaction.rollback(nil)
}

func TestIARetirementBeginUnknownCommitFreshRead(t *testing.T) {
	w, r, p := iaRetirementBeginFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 787, "lib-unknown", "/media/unknown", "Library", 5)
	path := iaConfigureLibraryPath(t, w)
	identity := iaRetirementBeginIdentity(18, 7, 1)
	request := iaRetirementBeginLibraryRequest("lib-unknown", 787, 5, "/media/unknown", "Library", "Library")
	ref := iaRetirementBeginPrepared(t, IARetireLibrary, identity, request)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, budget := iaRetirementBeginTurn(t, w, r, IARetireLibrary, ref)
	if out, err := beginIARetirement(t.Context(), turn, p, t.Context(), budget, ref, request); err != nil || out.decision != receiptFresh {
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
		{`SELECT count(*) FROM catalog_libraries WHERE id=787 AND retired=1`, 1},
		{`SELECT count(*) FROM catalog_removal_progress WHERE target='lib-unknown' AND last_command_sequence=1`, 1},
		{`SELECT count(*) FROM catalog_removal_receipts WHERE result_target='lib-unknown' AND command_kind='retire_library'`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	// The executor remains fenced: an ambiguous COMMIT is never success.
	if _, err = w.begin(t.Context(), IARetireLibrary, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

func TestIARetirementBeginRejectsIncompleteSavedProof(t *testing.T) {
	for name, mutation := range map[string]string{
		"missing_receipt": `DELETE FROM catalog_removal_receipts`,
		"wrong_walk_flag": `UPDATE catalog_removal_receipts SET result_walk_removed=1`,
		"wrong_digest":    `UPDATE catalog_removal_receipts SET result_digest=zeroblob(32)`,
		"wrong_cursor":    `UPDATE catalog_removal_receipts SET result_object_cursor='c',retained_bytes=retained_bytes+1`,
		"zero_owner_debt": `UPDATE catalog_removal_progress SET receipt_retained_count=0,receipt_retained_bytes=0`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p, ref, request, _ := iaRetirementBeginCommittedLibrary(t)
			wc008NativeExec(t, w, mutation)
			turn, out, err := iaRetirementBeginRun(t, w, r, p, IARetireLibrary, ref, request)
			if !errors.Is(err, ErrResolveRequired) || out != (iaRetireBeginOutcome{}) {
				t.Fatal("incomplete saved proof escaped", out, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}
