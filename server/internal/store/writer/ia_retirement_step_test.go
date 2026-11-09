package writer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The group C removal candidate is not embedded. The exact DDL subset is the
// same fixture IA-08 loads; no canonical schema file is edited and no Capture
// golden is recorded from it.
func iaRetirementStepFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRetirementStepScope, [16]byte) {
	t.Helper()
	w, keeper, p := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-retirement-begin-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	registry, err := stmttest.New(t, iaRetirementStepDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	iaRetirementBeginLibrarySeed(t, w, 900, "lib-step", "/media/step", "Library", 5)
	// The step is only admitted for a target the BEGIN fence already retired.
	wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=900`)
	iaRetirementStepKind(t, w)
	incarnation := [16]byte{1}
	iaRetirementStepDriverInsert(t, w, "library", "lib-step", 900, "lib-step", "/media/step", "", 0, "", incarnation)
	expiry := w.now().Add(time.Hour).UnixMilli()
	scope := iaRetirementStepScope{
		kind: "library", target: "lib-step", libraryKey: "lib-step", resolvedRoot: "/media/step",
		libraryID: 900, revision: 2, leaseOwner: 1, leaseGeneration: 1, leaseExpiresMS: expiry,
		phase: iaRetirementStepMembers, entityKind: 0, entityCursor: 0, objectCursor: "0",
	}
	return w, &iaEntryRegistry{registry, keeper}, p, scope, incarnation
}

func iaRetirementStepDriverInsert(t *testing.T, w *executor, kind, target string, libraryID int64, key, root, sourceIncarnation string, sourceGeneration int64, rootIdentity string, incarnation [16]byte) {
	t.Helper()
	var sourceInc, sourceGen, identity any
	if kind == "folder" {
		sourceInc, sourceGen, identity = sourceIncarnation, sourceGeneration, rootIdentity
	}
	operation := make([]byte, 16)
	copy(operation, []byte("ia-step:"+target))
	wc008NativeExec(t, w, `INSERT INTO catalog_removal_progress(kind,target,original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,acknowledged_command_sequence,retirement_state,phase,object_cursor,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,
receipt_retained_count,receipt_retained_bytes)
VALUES(?,?,?,?,?,?,?,?,?,?,?,1,1,'retiring',1,'0',2,1,1,?,1,300)`,
		kind, target, operation, make([]byte, 32), incarnation[:], libraryID, key, root, sourceInc, sourceGen, identity,
		w.now().Add(time.Hour).UnixMilli())
}

func iaRetirementStepID(incarnation [16]byte, sequence int64, operation byte) CommandIdentity {
	return CommandIdentity{Incarnation: incarnation, Sequence: sequence, OperationID: [16]byte{operation}}
}

func iaRetirementStepPrepared(t *testing.T, identity CommandIdentity, request iaRetirementStepRequest) ReceiptReference {
	t.Helper()
	command, err := newIARetirementStepCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: identity, RequestDigest: command.digest}
}

func iaRetirementStepTurn(t *testing.T, w *executor, r *iaEntryRegistry) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IARetireStep})
}

func iaRetirementStepRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, identity CommandIdentity, request iaRetirementStepRequest, commit bool) (iaRetirementStepOutcome, error) {
	t.Helper()
	ref := iaRetirementStepPrepared(t, identity, request)
	return iaRetirementStepRunRef(t, w, r, p, ref, request, commit)
}

func iaRetirementStepRunRef(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, ref ReceiptReference, request iaRetirementStepRequest, commit bool) (iaRetirementStepOutcome, error) {
	t.Helper()
	turn, budget := iaRetirementStepTurn(t, w, r)
	out, err := stepIARetirement(t.Context(), turn, p, t.Context(), budget, ref, request)
	if err != nil {
		turn.transaction.rollback(err)
		return out, err
	}
	if commit {
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
	} else {
		turn.transaction.rollback(nil)
	}
	return out, nil
}

func iaRetirementStepAdvance(scope iaRetirementStepScope, out iaRetirementStepOutcome, revision int64) iaRetirementStepScope {
	scope.phase, scope.entityKind, scope.entityCursor, scope.objectCursor = out.result.phase, out.result.entityKind, out.result.entityCursor, out.result.objectCursor
	scope.walkRemoved, scope.walkKept = out.result.walkRemoved, out.result.walkKept
	scope.revision = revision
	return scope
}

func iaRetirementStepCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func iaRetirementStepAck(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET acknowledged_command_sequence=last_command_sequence`)
}

func iaRetirementStepKind(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT OR IGNORE INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'movie',1,0,1,1,0)`)
}

func iaRetirementStepEntitySeed(t *testing.T, w *executor, id, kind, libraryID int64, title string) {
	t.Helper()
	publicID := []byte(fmt.Sprintf("%016d", id))
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, id, publicID, libraryID, kind)
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation)
VALUES(?,?,?,?,?,?,?,?,?,1,zeroblob(16),zeroblob(16))`,
		id, publicID, libraryID, kind, title, title, title, title, title)
}

func iaRetirementStepAssetSeed(t *testing.T, w *executor, id, libraryID int64, token string) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
VALUES(?,?,?,?,42,11,'','','',0,0,0)`, id, token, libraryID, "/media/step/"+token)
}

func iaRetirementStepLinkSeed(t *testing.T, w *executor, entity, asset, libraryID int64) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(?,?,?)`, entity, asset, libraryID)
}

// One committed link-removal step, used by the replay and saved-proof cells.
func iaRetirementStepCommitted(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRetirementStepScope, [16]byte, ReceiptReference, iaRetirementStepRequest) {
	t.Helper()
	w, r, p, scope, incarnation := iaRetirementStepFixture(t)
	iaRetirementStepKind(t, w)
	iaRetirementStepAssetSeed(t, w, 41, 900, "step-asset")
	iaRetirementStepEntitySeed(t, w, 10, 20, 900, "One")
	iaRetirementStepLinkSeed(t, w, 10, 41, 900)
	identity := iaRetirementStepID(incarnation, 2, 2)
	request := iaRetirementStepRequest{scope: scope}
	ref := iaRetirementStepPrepared(t, identity, request)
	out, err := iaRetirementStepRunRef(t, w, r, p, ref, request, true)
	if err != nil || out.decision != receiptFresh || !out.result.walkRemoved {
		t.Fatal("seed step", out, err)
	}
	return w, r, p, scope, incarnation, ref, request
}

// Child-first: the member family is drained by keyset before any entity is
// selected; an empty member family moves into the entity family in the same
// turn; remaining asset custody holds rather than claiming completion.
func TestIARetirementStepMembersBeforeEntities(t *testing.T) {
	w, r, p, scope, incarnation := iaRetirementStepFixture(t)
	iaRetirementStepKind(t, w)
	iaRetirementStepAssetSeed(t, w, 41, 900, "step-asset")
	iaRetirementStepEntitySeed(t, w, 10, 20, 900, "One")
	iaRetirementStepEntitySeed(t, w, 11, 20, 900, "Two")
	iaRetirementStepLinkSeed(t, w, 10, 41, 900)
	iaRetirementStepLinkSeed(t, w, 11, 41, 900)

	request := iaRetirementStepRequest{scope: scope}
	out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), request, true)
	if err != nil || out.decision != receiptFresh || !out.result.walkRemoved || out.result.walkKept ||
		out.result.phase != iaRetirementStepMembers || out.result.entityCursor != 10 || out.result.objectCursor != "41" ||
		out.result.state != "retiring" || out.digest == (ResultDigest{}) {
		t.Fatal("first member unit", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_asset_links WHERE library_id=900`); got != 1 {
		t.Fatal("member unit removed more than one link", got)
	}
	iaRetirementStepAck(t, w)

	scope = iaRetirementStepAdvance(scope, out, 3)
	request = iaRetirementStepRequest{scope: scope}
	out, err = iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 3, 3), request, true)
	if err != nil || !out.result.walkRemoved || out.result.phase != iaRetirementStepMembers || out.result.entityCursor != 11 {
		t.Fatal("second member unit", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_asset_links WHERE library_id=900`); got != 0 {
		t.Fatal("member family not drained", got)
	}
	iaRetirementStepAck(t, w)

	// No next member: the same turn removes the first parent entity.
	scope = iaRetirementStepAdvance(scope, out, 4)
	request = iaRetirementStepRequest{scope: scope}
	out, err = iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 4, 4), request, true)
	if err != nil || !out.result.walkRemoved || out.result.phase != iaRetirementStepEntities ||
		out.result.entityCursor != 10 || out.result.objectCursor != "" {
		t.Fatal("entity after members", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_entities WHERE library_id=900`); got != 1 {
		t.Fatal("entity unit removed more than one entity", got)
	}
	iaRetirementStepAck(t, w)

	scope = iaRetirementStepAdvance(scope, out, 5)
	request = iaRetirementStepRequest{scope: scope}
	out, err = iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 5, 5), request, true)
	if err != nil || !out.result.walkRemoved || out.result.entityCursor != 11 {
		t.Fatal("second entity", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_entities WHERE library_id=900`); got != 0 {
		t.Fatal("entity family not drained", got)
	}
	iaRetirementStepAck(t, w)

	// Catalogue families are empty but the asset is still physical custody.
	scope = iaRetirementStepAdvance(scope, out, 6)
	request = iaRetirementStepRequest{scope: scope}
	out, err = iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 6, 6), request, false)
	if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaRetirementVictimCustodyUnresolved) || out != (iaRetirementStepOutcome{}) {
		t.Fatal("asset custody claimed complete", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT last_command_sequence FROM catalog_removal_progress WHERE target='lib-step'`); got != 5 {
		t.Fatal("custody hold advanced the driver", got)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 4 {
		t.Fatal("custody hold wrote a receipt", got)
	}
}

// A phase-2 cursor may not be honoured while a member link still exists: the
// child-first order is proven, never assumed from the cursor.
func TestIARetirementStepEarlyEntityPhaseRefused(t *testing.T) {
	w, r, p, scope, incarnation := iaRetirementStepFixture(t)
	iaRetirementStepKind(t, w)
	iaRetirementStepAssetSeed(t, w, 41, 900, "step-asset")
	iaRetirementStepEntitySeed(t, w, 10, 20, 900, "One")
	iaRetirementStepLinkSeed(t, w, 10, 41, 900)
	wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET phase=2,object_cursor=''`)
	scope.phase, scope.entityKind, scope.entityCursor, scope.objectCursor = iaRetirementStepEntities, 0, 0, ""
	out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), iaRetirementStepRequest{scope: scope}, false)
	if !errors.Is(err, ErrConflict) || out != (iaRetirementStepOutcome{}) {
		t.Fatal("early entity phase admitted", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=10`); got != 1 {
		t.Fatal("entity removed before its member", got)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_asset_links WHERE entity_id=10`); got != 1 {
		t.Fatal("member removed by the refused turn", got)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 0 {
		t.Fatal("refused turn wrote a receipt", got)
	}
}

// A bounded raw window may contain only kept (collection-referenced) entities.
// A short exhausted window holds; a full window records a truthful empty unit
// with an advanced cursor and no deletion.
func TestIARetirementStepAllRejectedWindow(t *testing.T) {
	w, r, p, scope, incarnation := iaRetirementStepFixture(t)
	iaRetirementStepKind(t, w)
	iaRetirementBeginLibrarySeed(t, w, 901, "lib-hold", "/media/hold", "Hold", 5)
	iaRetirementStepEntitySeed(t, w, 1000, 20, 901, "Collection")
	for id := int64(1); id <= 3; id++ {
		iaRetirementStepEntitySeed(t, w, id, 20, 900, fmt.Sprintf("Item %d", id))
		wc008NativeExec(t, w, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(1000,?,'k')`, id)
	}
	wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET phase=2,object_cursor=''`)
	scope.phase, scope.entityKind, scope.entityCursor, scope.objectCursor = iaRetirementStepEntities, 0, 0, ""
	request := iaRetirementStepRequest{scope: scope}
	out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), request, false)
	if !errors.Is(err, ErrResolveRequired) || out != (iaRetirementStepOutcome{}) {
		t.Fatal("short kept window invented progress", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_entities WHERE library_id=900`); got != 3 {
		t.Fatal("kept entity was cascaded", got)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_collection_members`); got != 3 {
		t.Fatal("collection membership was cascaded", got)
	}
	// Removing the holds makes the same request able to progress.
	wc008NativeExec(t, w, `DELETE FROM catalog_collection_members WHERE item_id=1`)
	out, err = iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), request, true)
	if err != nil || !out.result.walkRemoved || out.result.entityCursor != 1 {
		t.Fatal("eligible entity after a hold", out, err)
	}

	// A full window of kept rows is a truthful empty unit, not a terminal claim.
	w, r, p, scope, incarnation = iaRetirementStepFixture(t)
	iaRetirementStepKind(t, w)
	iaRetirementBeginLibrarySeed(t, w, 901, "lib-hold", "/media/hold", "Hold", 5)
	iaRetirementStepEntitySeed(t, w, 1000, 20, 901, "Collection")
	for id := int64(1); id <= iaRetirementStepWindow; id++ {
		iaRetirementStepEntitySeed(t, w, id, 20, 900, fmt.Sprintf("Item %d", id))
		wc008NativeExec(t, w, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(1000,?,'k')`, id)
	}
	wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET phase=2,object_cursor=''`)
	scope.phase, scope.entityKind, scope.entityCursor, scope.objectCursor = iaRetirementStepEntities, 0, 0, ""
	out, err = iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), iaRetirementStepRequest{scope: scope}, true)
	if err != nil || out.decision != receiptFresh || out.result.walkRemoved || !out.result.walkKept ||
		out.result.phase != iaRetirementStepEntities || out.result.entityCursor != iaRetirementStepWindow ||
		out.result.state != "retiring" {
		t.Fatal("full kept window", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts WHERE command_kind='retire_step'`); got != 1 {
		t.Fatal("empty window receipt", got)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_entities WHERE library_id=900`); got != int64(iaRetirementStepWindow) {
		t.Fatal("kept window deleted an entity", got)
	}
}

func TestIARetirementStepLeaseCursorConflicts(t *testing.T) {
	for name, mutation := range map[string]string{
		"lease_generation": `UPDATE catalog_removal_progress SET job_lease_generation=2`,
		"lease_owner":      `UPDATE catalog_removal_progress SET job_lease_owner=2`,
		"job_revision":     `UPDATE catalog_removal_progress SET job_revision=3`,
		"object_cursor":    `UPDATE catalog_removal_progress SET object_cursor='9'`,
		"entity_cursor":    `UPDATE catalog_removal_progress SET entity_cursor=4`,
		"walk_removed":     `UPDATE catalog_removal_progress SET walk_removed=1`,
		"phase":            `UPDATE catalog_removal_progress SET phase=2,object_cursor=''`,
		"complete":         `UPDATE catalog_removal_progress SET retirement_state='complete',phase=5`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p, scope, incarnation := iaRetirementStepFixture(t)
			iaRetirementStepKind(t, w)
			iaRetirementStepAssetSeed(t, w, 41, 900, "step-asset")
			iaRetirementStepEntitySeed(t, w, 10, 20, 900, "One")
			iaRetirementStepLinkSeed(t, w, 10, 41, 900)
			wc008NativeExec(t, w, mutation)
			out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), iaRetirementStepRequest{scope: scope}, false)
			if !errors.Is(err, ErrConflict) || out != (iaRetirementStepOutcome{}) {
				t.Fatal("stale driver admitted", out, err)
			}
			if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_asset_links`); got != 1 {
				t.Fatal("stale driver deleted a member", got)
			}
			if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 0 {
				t.Fatal("stale driver wrote a receipt", got)
			}
		})
	}
	t.Run("sequence_gap", func(t *testing.T) {
		w, r, p, scope, incarnation := iaRetirementStepFixture(t)
		out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 3, 2), iaRetirementStepRequest{scope: scope}, false)
		if !errors.Is(err, ErrConflict) || out != (iaRetirementStepOutcome{}) {
			t.Fatal("sequence gap admitted", out, err)
		}
	})
	t.Run("expired_lease", func(t *testing.T) {
		w, r, p, scope, incarnation := iaRetirementStepFixture(t)
		expired := w.now().UnixMilli()
		wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET job_lease_expires_ms=?`, expired)
		scope.leaseExpiresMS = expired
		out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), iaRetirementStepRequest{scope: scope}, false)
		if !errors.Is(err, ErrConflict) || out != (iaRetirementStepOutcome{}) {
			t.Fatal("expired lease admitted", out, err)
		}
	})
}

func TestIARetirementStepPinnedSourceFailsClosed(t *testing.T) {
	w, r, p, _, _ := iaRetirementStepFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 901, "lib-source", "/media/source", "Source", 5)
	wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=0 WHERE id=901`)
	iaConfigureSourceLive(t, w, "lib-source", "source-00", 3, "incarnation-00", "/media/source", "identity-00", false, "removing", true)
	iaRetirementStepDriverInsert(t, w, "folder", "source-00", 901, "lib-source", "/media/source", "incarnation-00", 3, "identity-00", [16]byte{1})
	for name, pins := range map[string]bool{"unresolved": false, "resolved": true} {
		t.Run(name, func(t *testing.T) {
			scope := iaRetirementStepScope{
				kind: "folder", target: "source-00", libraryKey: "lib-source", resolvedRoot: "/media/source",
				libraryID: 901, revision: 2, leaseOwner: 1, leaseGeneration: 1, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(),
				sourceIncarnation: "incarnation-00", sourceGeneration: 3, rootIdentity: "identity-00",
				phase: iaRetirementStepMembers, objectCursor: "0",
			}
			if name == "resolved" {
				wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET last_command_sequence=1,acknowledged_command_sequence=1`)
			}
			out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID([16]byte{1}, 2, 2),
				iaRetirementStepRequest{scope: scope, sourcePinsResolved: pins}, false)
			if !errors.Is(err, ErrResolveRequired) || out != (iaRetirementStepOutcome{}) {
				t.Fatal("source step admitted without release proof", out, err)
			}
			if pins {
				if !errors.Is(err, iaRetirementVictimCustodyUnresolved) {
					t.Fatal("resolved pin seam claimed custody", err)
				}
			} else if !errors.Is(err, iaRetirementSourcePinsUnresolved) {
				t.Fatal("unresolved MP-10 seam lost its name", err)
			}
			if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 0 {
				t.Fatal("source seam wrote a receipt", got)
			}
		})
	}
}

func TestIARetirementStepCurrentDenial(t *testing.T) {
	for name, mutation := range map[string]string{
		"owner_role":     `UPDATE direct_memberships SET role='member'`,
		"token_retired":  `UPDATE authorization_family_tokens SET retired=1`,
		"deny_fence":     `UPDATE identity_security_fences SET denied=1`,
		"stale_lifetime": `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p, _, incarnation, _, request := iaRetirementStepCommitted(t)
			wc008NativeExec(t, w, mutation)
			turn, budget := iaRetirementStepTurn(t, w, r)
			next := request
			next.scope.revision = 3
			nextRef := iaRetirementStepPrepared(t, iaRetirementStepID(incarnation, 3, 3), next)
			out, err := stepIARetirement(t.Context(), turn, p, t.Context(), budget, nextRef, next)
			if !errors.Is(err, ErrDisclosureDenied) || out != (iaRetirementStepOutcome{}) {
				t.Fatal("denied authority reached history", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 1 {
				t.Fatal("denial wrote a receipt", got)
			}
		})
	}
	t.Run("forged_issuer", func(t *testing.T) {
		w, r, p, _, incarnation, _, request := iaRetirementStepCommitted(t)
		fake := *p
		fake.witness.issuer.family = "forged"
		next := request
		next.scope.revision = 3
		turn, budget := iaRetirementStepTurn(t, w, r)
		ref := iaRetirementStepPrepared(t, iaRetirementStepID(incarnation, 3, 3), next)
		out, err := stepIARetirement(t.Context(), turn, &fake, t.Context(), budget, ref, next)
		if !errors.Is(err, ErrDisclosureDenied) || out != (iaRetirementStepOutcome{}) {
			t.Fatal("forged issuer admitted", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, scope, incarnation := iaRetirementStepFixture(t)
		iaRetirementStepKind(t, w)
		iaRetirementStepAssetSeed(t, w, 41, 900, "step-asset")
		iaRetirementStepEntitySeed(t, w, 10, 20, 900, "One")
		iaRetirementStepLinkSeed(t, w, 10, 41, 900)
		identity := iaRetirementStepID(incarnation, 2, 2)
		request := iaRetirementStepRequest{scope: scope}
		ref := iaRetirementStepPrepared(t, identity, request)
		turn, budget := iaRetirementStepTurn(t, w, r)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := stepIARetirement(t.Context(), turn, p, cancelled, budget, ref, request)
		if err == nil || out != (iaRetirementStepOutcome{}) {
			t.Fatal("cancelled lifetime released a result", out, err)
		}
		turn.transaction.rollback(nil)
		if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_asset_links`); got != 1 {
			t.Fatal("cancelled lifetime deleted a member", got)
		}
		if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 0 {
			t.Fatal("cancelled lifetime wrote a receipt", got)
		}
	})
}

func TestIARetirementStepReplayRetiredAndUnknownCommit(t *testing.T) {
	w, r, p, scope, incarnation, ref, request := iaRetirementStepCommitted(t)
	// Replay survives deletion of every live catalogue row.
	iaRetirementStepKind(t, w)
	iaRetirementStepAssetSeed(t, w, 42, 900, "other-asset")
	wc008NativeExec(t, w, `DELETE FROM catalog_asset_links`)
	wc008NativeExec(t, w, `DELETE FROM catalog_assets`)
	wc008NativeExec(t, w, `DELETE FROM catalog_entities`)
	replay, err := iaRetirementStepRunRef(t, w, r, p, ref, request, false)
	if err != nil || replay.decision != receiptReplay ||
		replay.result.phase != iaRetirementStepMembers || replay.result.entityCursor != 10 ||
		replay.result.objectCursor != "41" || !replay.result.walkRemoved || replay.digest == (ResultDigest{}) {
		t.Fatal("replay after live deletion", replay, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 1 {
		t.Fatal("replay wrote a receipt", got)
	}
	// ACK alone retires the saved result; nothing is refunded here.
	iaRetirementStepAck(t, w)
	retired, err := iaRetirementStepRunRef(t, w, r, p, ref, request, false)
	if err != nil || retired.decision != receiptRetired || retired.result != (iaRetirementResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal("ACKed step decoded", retired, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 1 {
		t.Fatal("ACK refunded the step receipt", got)
	}

	// A later step acknowledges the previous one; the older step is then Retired.
	w, r, p, scope, incarnation, ref, request = iaRetirementStepCommitted(t)
	iaRetirementStepAssetSeed(t, w, 43, 900, "third-asset")
	iaRetirementStepEntitySeed(t, w, 11, 20, 900, "Two")
	iaRetirementStepAck(t, w)
	next := request
	next.scope.revision = 3
	next.scope.entityCursor, next.scope.objectCursor = 10, "41"
	next.scope.walkRemoved = true
	if _, err = iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 3, 3), next, true); err != nil {
		t.Fatal(err)
	}
	retired, err = iaRetirementStepRunRef(t, w, r, p, ref, request, false)
	if err != nil || retired.decision != receiptRetired {
		t.Fatal("predecessor after a later step", retired, err)
	}

	// Unknown COMMIT: read back through a fresh read-only connection.
	w, r, p, scope, incarnation = iaRetirementStepFixture(t)
	iaRetirementStepKind(t, w)
	iaRetirementStepAssetSeed(t, w, 41, 900, "step-asset")
	iaRetirementStepEntitySeed(t, w, 10, 20, 900, "One")
	iaRetirementStepLinkSeed(t, w, 10, 41, 900)
	path := iaConfigureLibraryPath(t, w)
	identity := iaRetirementStepID(incarnation, 2, 2)
	unknownRequest := iaRetirementStepRequest{scope: scope}
	unknownRef := iaRetirementStepPrepared(t, identity, unknownRequest)
	if err = w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, budget := iaRetirementStepTurn(t, w, r)
	if out, err := stepIARetirement(t.Context(), turn, p, t.Context(), budget, unknownRef, unknownRequest); err != nil || out.decision != receiptFresh {
		t.Fatal("fresh before unknown commit", out, err)
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
		{`SELECT count(*) FROM catalog_asset_links WHERE library_id=900`, 0},
		{`SELECT count(*) FROM catalog_removal_receipts WHERE command_kind='retire_step' AND command_sequence=2 AND result_target='lib-step'`, 1},
		{`SELECT count(*) FROM catalog_removal_progress WHERE target='lib-step' AND last_command_sequence=2 AND job_revision=3`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IARetireStep, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

func TestIARetirementStepTerminalPhysicalExit(t *testing.T) {
	t.Run("empty_catalogue", func(t *testing.T) {
		w, r, p, scope, incarnation := iaRetirementStepFixture(t)
		wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET phase=2,object_cursor=''`)
		scope.phase, scope.entityKind, scope.entityCursor, scope.objectCursor = iaRetirementStepEntities, 0, 0, ""
		out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), iaRetirementStepRequest{scope: scope}, true)
		if err != nil || out.decision != receiptFresh || out.result.phase != iaRetirementStepComplete ||
			out.result.state != "complete" || out.result.walkRemoved || out.result.walkKept || out.result.objectCursor != "" {
			t.Fatal("empty catalogue did not complete", out, err)
		}
		if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_progress WHERE target='lib-step' AND retirement_state='complete' AND phase=5`); got != 1 {
			t.Fatal("driver not terminal", got)
		}
		if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_removal_receipts WHERE command_kind='retire_step' AND result_state='complete' AND result_phase=5`); got != 1 {
			t.Fatal("terminal receipt", got)
		}
		// No further fresh step is admitted from a completed driver.
		iaRetirementStepAck(t, w)
		out, err = iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 3, 3), iaRetirementStepRequest{scope: scope}, false)
		if !errors.Is(err, ErrConflict) || out != (iaRetirementStepOutcome{}) {
			t.Fatal("completed driver advanced", out, err)
		}
	})
	t.Run("asset_custody", func(t *testing.T) {
		w, r, p, scope, incarnation := iaRetirementStepFixture(t)
		iaRetirementStepAssetSeed(t, w, 41, 900, "step-asset")
		wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET phase=2,object_cursor=''`)
		scope.phase, scope.entityKind, scope.entityCursor, scope.objectCursor = iaRetirementStepEntities, 0, 0, ""
		out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), iaRetirementStepRequest{scope: scope}, false)
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaRetirementVictimCustodyUnresolved) || out != (iaRetirementStepOutcome{}) {
			t.Fatal("asset custody claimed complete", out, err)
		}
		if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_assets WHERE id=41`); got != 1 {
			t.Fatal("custody hold deleted the asset", got)
		}
	})
	t.Run("object_custody", func(t *testing.T) {
		w, r, p, scope, incarnation := iaRetirementStepFixture(t)
		iaRetirementBeginLibrarySeed(t, w, 902, "lib-other", "/media/other", "Other", 5)
		iaRetirementStepAssetSeed(t, w, 41, 902, "other-asset")
		iaConfigureSourceLive(t, w, "lib-step", "source-00", 1, "incarnation-00", "/media/step", "identity-00", false, "removing", true)
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
VALUES('obj-1','source-00','other-asset','incarnation-00','file','revision','{}',1,1)`)
		wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET phase=2,object_cursor=''`)
		scope.phase, scope.entityKind, scope.entityCursor, scope.objectCursor = iaRetirementStepEntities, 0, 0, ""
		out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), iaRetirementStepRequest{scope: scope}, false)
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaRetirementVictimCustodyUnresolved) || out != (iaRetirementStepOutcome{}) {
			t.Fatal("object custody claimed complete", out, err)
		}
		if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM inventory_objects WHERE id='obj-1'`); got != 1 {
			t.Fatal("custody hold deleted the object", got)
		}
	})
}

func TestIARetirementStepBoundedInput(t *testing.T) {
	_, _, _, scope, incarnation := iaRetirementStepFixture(t)
	base := iaRetirementStepRequest{scope: scope}
	for name, tc := range map[string]struct {
		mutate func(*iaRetirementStepRequest)
	}{
		"phase_zero":        {func(q *iaRetirementStepRequest) { q.scope.phase = 0 }},
		"phase_asset":       {func(q *iaRetirementStepRequest) { q.scope.phase = 3 }},
		"phase_six":         {func(q *iaRetirementStepRequest) { q.scope.phase = 6 }},
		"empty_object":      {func(q *iaRetirementStepRequest) { q.scope.objectCursor = "" }},
		"negative_object":   {func(q *iaRetirementStepRequest) { q.scope.objectCursor = "-1" }},
		"text_object":       {func(q *iaRetirementStepRequest) { q.scope.objectCursor = "x" }},
		"padded_object":     {func(q *iaRetirementStepRequest) { q.scope.objectCursor = "007" }},
		"phase_two_object":  {func(q *iaRetirementStepRequest) { q.scope.phase = 2 }},
		"kind_in_phase_one": {func(q *iaRetirementStepRequest) { q.scope.entityKind = 3 }},
		"negative_entity":   {func(q *iaRetirementStepRequest) { q.scope.entityCursor = -1 }},
		"empty_target":      {func(q *iaRetirementStepRequest) { q.scope.target = "" }},
		"relative_root":     {func(q *iaRetirementStepRequest) { q.scope.resolvedRoot = "media/step" }},
		"unclean_root":      {func(q *iaRetirementStepRequest) { q.scope.resolvedRoot = "/media/../step" }},
		"zero_library":      {func(q *iaRetirementStepRequest) { q.scope.libraryID = 0 }},
		"zero_revision":     {func(q *iaRetirementStepRequest) { q.scope.revision = 0 }},
		"max_revision":      {func(q *iaRetirementStepRequest) { q.scope.revision = 9223372036854775807 }},
		"zero_lease":        {func(q *iaRetirementStepRequest) { q.scope.leaseOwner = 0 }},
		"library_with_src":  {func(q *iaRetirementStepRequest) { q.scope.sourceIncarnation = "source" }},
		"folder_no_source":  {func(q *iaRetirementStepRequest) { q.scope.kind = "folder" }},
		"unknown_kind":      {func(q *iaRetirementStepRequest) { q.scope.kind = "volume" }},
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			tc.mutate(&request)
			if _, err := newIARetirementStepCommand(iaRetirementStepID(incarnation, 2, 2), request); !errors.Is(err, ErrInvalid) {
				t.Fatal("bounded input admitted", err)
			}
		})
	}
	if _, err := newIARetirementStepCommand(iaRetirementStepID(incarnation, 1, 2), base); !errors.Is(err, ErrInvalid) {
		t.Fatal("sequence one admitted", err)
	}
	if _, err := newIARetirementStepCommand(CommandIdentity{Incarnation: incarnation, Sequence: 2}, base); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero operation admitted", err)
	}
}

func TestIARetirementStepQuotaRefusesOversizeUnit(t *testing.T) {
	w, r, p, scope, incarnation := iaRetirementStepFixture(t)
	iaRetirementStepKind(t, w)
	iaRetirementStepEntitySeed(t, w, 10, 20, 900, strings.Repeat("x", int(iaRetirementStepMaxBytes)+1))
	out, err := iaRetirementStepRun(t, w, r, p, iaRetirementStepID(incarnation, 2, 2), iaRetirementStepRequest{scope: scope}, false)
	if !errors.Is(err, ErrCap) || out != (iaRetirementStepOutcome{}) {
		t.Fatal("oversize unit admitted", out, err)
	}
	if got := iaRetirementStepCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=10`); got != 1 {
		t.Fatal("oversize unit was deleted", got)
	}
}

func TestIARetirementStepRejectsIncompleteSavedProof(t *testing.T) {
	for name, mutation := range map[string]string{
		"missing_receipt":  `DELETE FROM catalog_removal_receipts`,
		"wrong_digest":     `UPDATE catalog_removal_receipts SET result_digest=zeroblob(32)`,
		"wrong_phase":      `UPDATE catalog_removal_receipts SET result_phase=2`,
		"wrong_cursor":     `UPDATE catalog_removal_receipts SET result_entity_cursor=11`,
		"wrong_walk_flag":  `UPDATE catalog_removal_receipts SET result_walk_removed=0`,
		"wrong_debt_bytes": `UPDATE catalog_removal_progress SET receipt_retained_count=1,receipt_retained_bytes=1`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p, _, _, ref, request := iaRetirementStepCommitted(t)
			wc008NativeExec(t, w, mutation)
			out, err := iaRetirementStepRunRef(t, w, r, p, ref, request, false)
			if !errors.Is(err, ErrResolveRequired) || out != (iaRetirementStepOutcome{}) {
				t.Fatal("incomplete saved proof escaped", out, err)
			}
		})
	}
}
