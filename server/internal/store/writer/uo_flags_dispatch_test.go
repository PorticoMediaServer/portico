package writer

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoFlagsDispatchDefinitions() []stmt.Definition {
	return append(append(uoRootDefinitions(), uoRootAckDefinitions()...), uoPersonalFlagsDefinitions()...)
}

// uoFlagsDispatchFixture seeds one native root/owner beside the sparse personal
// fixture. The identity is the exact root scope the private proof must bind; the
// catalog rows are the same native fixture the merged flags component uses.
func uoFlagsDispatchFixture(t *testing.T) (w *executor, path, root string, identity uoRootIdentity, incarnation [16]byte, q uoPersonalFlagsInput) {
	t.Helper()
	w, path, _ = testExecutor(t)
	q = uoPersonalFlagsInput{profileKey: "profile-one", libraryKey: "library-one", subjectID: 2111, libraryID: 7, publicID: [16]byte{3, 128}, entityRevision: 1, admissionRevision: 1,
		patch: uoPersonalFlagsPatch{watchlisted: uoFlagValue{set: true, value: true}}}
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'library-one','/fixture')`)
	wc008NativeExec(t, w, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2111,?,7,1)`, q.publicID[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2111,?,7,1,'Title','title','t','title','t',0,?,?)`, q.publicID[:], q.publicID[:], q.publicID[:])
	identity = uoRootIdentity{account: "account", memberIncarnation: strings.Repeat("m", 32), profile: "profile", profileLifetime: strings.Repeat("p", 32), installation: strings.Repeat("i", 32), family: "family"}
	incarnation = [16]byte{1}
	root = "abcdefghijklmnopqrstuv"
	wc008NativeExec(t, w, `INSERT INTO user_mutation_global(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING`)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,'active')`,
		root, identity.installation, identity.account, identity.profile, identity.memberIncarnation, identity.profileLifetime, identity.family, int64(1))
	wc008NativeExec(t, w, `INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, root, incarnation[:])
	return w, path, root, identity, incarnation, q
}

func uoFlagsDispatchTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	return wc008NativeTurn(t, w, SetPersonalFlags, uoFlagsDispatchDefinitions())
}

func uoFlagsProof(t *testing.T, turn *commandTurn, identity uoRootIdentity) *uoRootCurrentProof {
	t.Helper()
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok {
		t.Fatal("turn has no deadline")
	}
	return &uoRootCurrentProof{turn: turn, purpose: SetPersonalFlags, lifetime: t.Context(), deadline: deadline, identity: identity}
}

func uoFlagsRef(root string, sequence int64, operation, request byte) uoRootReference {
	var inc [16]byte
	inc[0] = 1
	var op [16]byte
	op[0] = operation
	return uoRootReference{root: root, kind: SetPersonalFlags,
		identity: CommandIdentity{Incarnation: inc, Sequence: sequence, OperationID: op},
		request:  RequestDigest{request}}
}

func uoFlagsScalar(t *testing.T, w *executor, q string, args ...any) (n int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

// uoFlagsOwnerOracle reads the exact native owner debt with plain SQL.
func uoFlagsOwnerOracle(t *testing.T, w *executor, root string) (last, ack, revision, rows, bytes int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT last_command_sequence,acknowledged_command_sequence,revision,receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&last, &ack, &revision, &rows, &bytes); err != nil {
		t.Fatal(err)
	}
	return last, ack, revision, rows, bytes
}

// TestUOFlagsDispatch is the 600 behavioural control: two-profile isolation,
// unchanged defaults, preserved resume/watched/history, exact original replay
// after source drift, prefix rollback, ACK floor/no-refund, an unknown COMMIT
// and a post-COMMIT caller cancel resolved by a retry with no second effect.
func TestUOFlagsDispatch(t *testing.T) {
	t.Run("two-profile control", func(t *testing.T) {
		w, _, root, identity, _, q := uoFlagsDispatchFixture(t)
		wc008NativeExec(t, w, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id) VALUES('profile-one',?,2111,7),('profile-other',?,2111,7)`, q.publicID[:], q.publicID[:])
		wc008NativeExec(t, w, `UPDATE personal_items SET favorite=1 WHERE profile_id='profile-other'`)
		q.expectedRevision = 1
		q.patch = uoPersonalFlagsPatch{favorite: uoFlagValue{set: true, value: true}}
		turn := uoFlagsDispatchTurn(t, w)
		out, floor, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: uoFlagsRef(root, 1, 9, 19), public: q.publicID, input: q, nowMS: 300})
		if err != nil || floor != 0 || out.root != root || out.public != q.publicID || out.appliedMS != 300 || !out.effect.changed || !out.effect.state.favorite || out.effect.state.watchlisted || out.effect.state.revision != 2 || out.effect.viewerRevision != 1 || out.effect.selectionRevision != 1 {
			t.Fatal("acting-profile original", out, floor, err)
		}
		var ownRevision, otherRevision, ownFavorite, otherFavorite, heads, headProfile, headRevision int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT revision FROM personal_items WHERE profile_id='profile-one'),(SELECT revision FROM personal_items WHERE profile_id='profile-other'),(SELECT favorite FROM personal_items WHERE profile_id='profile-one'),(SELECT favorite FROM personal_items WHERE profile_id='profile-other'),(SELECT count(*) FROM viewer_revisions),(SELECT count(*) FROM viewer_revisions WHERE profile_id='profile-one'),(SELECT revision FROM viewer_revisions WHERE profile_id='profile-one')`).Scan(&ownRevision, &otherRevision, &ownFavorite, &otherFavorite, &heads, &headProfile, &headRevision); err != nil || ownRevision != 2 || otherRevision != 1 || ownFavorite != 1 || otherFavorite != 1 || heads != 1 || headProfile != 1 || headRevision != 1 {
			t.Fatal("two-profile control", ownRevision, otherRevision, ownFavorite, otherFavorite, heads, headProfile, headRevision, err)
		}
		last, ack, revision, rows, bytes := uoFlagsOwnerOracle(t, w, root)
		if last != 1 || ack != 0 || revision != 2 || rows != 1 || bytes <= uoRootFixedBytes {
			t.Fatal("root owner debt", last, ack, revision, rows, bytes)
		}
		var payload, variable, kind, storedBytes int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT length(result_payload),variable_bytes,command_kind,receipt_variable_bytes FROM user_mutation_command_receipts JOIN user_mutation_command_owners USING(root_id) WHERE root_id=?`, root).Scan(&payload, &variable, &kind, &storedBytes); err != nil || payload < 1 || kind != int64(SetPersonalFlags) || variable != payload+uoRootFixedBytes || storedBytes != variable {
			t.Fatal("stored original bytes", payload, variable, kind, storedBytes, err)
		}
	})

	t.Run("unchanged defaults", func(t *testing.T) {
		w, _, root, identity, _, q := uoFlagsDispatchFixture(t)
		q.patch = uoPersonalFlagsPatch{watchlisted: uoFlagValue{set: true, value: false}}
		turn := uoFlagsDispatchTurn(t, w)
		out, floor, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: uoFlagsRef(root, 1, 9, 19), public: q.publicID, input: q, nowMS: 300})
		if err != nil || floor != 0 || out.effect.changed || out.effect.state != (uoPersonalFlagsState{}) || out.effect.viewerRevision != 0 || out.appliedMS != 300 {
			t.Fatal("unchanged default result", out, floor, err)
		}
		var personal, heads int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM personal_items),(SELECT count(*) FROM viewer_revisions)`).Scan(&personal, &heads); err != nil || personal != 0 || heads != 0 {
			t.Fatal("default patch materialized state", personal, heads, err)
		}
		_, _, _, rows, bytes := uoFlagsOwnerOracle(t, w, root)
		if rows != 1 || bytes <= uoRootFixedBytes {
			t.Fatal("no-op original not retained", rows, bytes)
		}
	})

	t.Run("preserve resume watched history", func(t *testing.T) {
		w, _, root, identity, _, q := uoFlagsDispatchFixture(t)
		wc008NativeExec(t, w, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,revision,watchlisted,resume_position,watched,play_count,played_ms,last_played_at) VALUES('profile-one',?,2111,7,1,1,42,1,3,1234,'2026-01-01T00:00:00Z')`, q.publicID[:])
		wc008NativeExec(t, w, `INSERT INTO play_history(id,occurred_at,event_kind,account_key,profile_id,playback_id,occurrence_generation,accepted_sequence,item_public_id,item_title,played_ms) VALUES(1,'2026-01-01T00:00:00Z','started','account','profile-one','playback-one',1,9,?,'Title',1234)`, q.publicID[:])
		q.expectedRevision = 1
		q.patch = uoPersonalFlagsPatch{rating: uoRatingValue{set: true, value: sql.NullInt64{Int64: 0, Valid: true}}}
		turn := uoFlagsDispatchTurn(t, w)
		out, _, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: uoFlagsRef(root, 1, 9, 19), public: q.publicID, input: q, nowMS: 300})
		if err != nil || !out.effect.changed || !out.effect.state.rating.Valid || out.effect.state.rating.Int64 != 0 || out.effect.state.revision != 2 {
			t.Fatal("nullable zero rating", out, err)
		}
		var resume, watched, plays, played, history, events int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT resume_position,watched,play_count,played_ms,(SELECT count(*) FROM play_history),(SELECT count(*) FROM api_events) FROM personal_items WHERE profile_id='profile-one'`).Scan(&resume, &watched, &plays, &played, &history, &events); err != nil || resume != 42 || watched != 1 || plays != 3 || played != 1234 || history != 1 || events != 0 {
			t.Fatal("flags rewrote activity or history", resume, watched, plays, played, history, events, err)
		}
		if got := uoFlagsScalar(t, w, `SELECT count(*) FROM personal_items WHERE profile_id='profile-one' AND rating=0`); got != 1 {
			t.Fatal("present zero rating not stored", got)
		}
	})

	t.Run("exact source drift replay", func(t *testing.T) {
		w, _, root, identity, _, q := uoFlagsDispatchFixture(t)
		ref := uoFlagsRef(root, 1, 9, 19)
		turn := uoFlagsDispatchTurn(t, w)
		original, _, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: ref, public: q.publicID, input: q, nowMS: 300})
		if err != nil {
			t.Fatal(err)
		}
		// The mutable catalog drifts after the original: a replay must not
		// consult it, re-run the patch or fail because the source is gone.
		wc008NativeExec(t, w, `UPDATE catalog_entities SET revision=2,retired=1 WHERE id=2111`)
		wc008NativeExec(t, w, `UPDATE catalog_entities SET admission_revision=2 WHERE id=2111`)
		wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		beforeRevision := uoFlagsScalar(t, w, `SELECT revision FROM personal_items WHERE profile_id='profile-one'`)
		beforeReceipts := uoFlagsScalar(t, w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, root)
		turn = uoFlagsDispatchTurn(t, w)
		replay, floor, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: ref, public: q.publicID, input: q, nowMS: 300})
		if err != nil || floor != 0 || replay != original {
			t.Fatal("source drift replay", replay, original, floor, err)
		}
		if after := uoFlagsScalar(t, w, `SELECT revision FROM personal_items WHERE profile_id='profile-one'`); after != beforeRevision {
			t.Fatal("replay rewrote the personal row", after, beforeRevision)
		}
		if after := uoFlagsScalar(t, w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, root); after != beforeReceipts {
			t.Fatal("replay wrote a second receipt", after, beforeReceipts)
		}
		// A different request identity can never read the same original.
		wrong := ref
		wrong.request[0]++
		turn = uoFlagsDispatchTurn(t, w)
		if _, _, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: wrong, public: q.publicID, input: q, nowMS: 300}); !errors.Is(err, ErrConflict) {
			t.Fatal("different request not a conflict", err)
		}
	})

	t.Run("receipt rollback after nonempty prefix", func(t *testing.T) {
		w, _, root, identity, _, q := uoFlagsDispatchFixture(t)
		// A foreign row reuses the operation identity at another sequence, so the
		// fresh receipt INSERT fails only AFTER the personal prefix is written.
		inc := make([]byte, 16)
		inc[0] = 1
		op := make([]byte, 16)
		op[0] = 9
		wc008NativeExec(t, w, `INSERT INTO user_mutation_command_receipts(root_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,9,?,600,?,1,0,?,?,100)`,
			root, inc, op, make([]byte, 32), make([]byte, 32), []byte{1, 2, 3})
		turn := uoFlagsDispatchTurn(t, w)
		out, floor, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: uoFlagsRef(root, 1, 9, 19), public: q.publicID, input: q, nowMS: 300})
		if !errors.Is(err, ErrConflict) || out != (uoRootFlagsResult{}) || floor != 0 {
			t.Fatal("duplicate operation did not conflict", out, floor, err)
		}
		var personal, heads, receipts, events int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM personal_items),(SELECT count(*) FROM viewer_revisions),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT count(*) FROM api_events)`).Scan(&personal, &heads, &receipts, &events); err != nil || personal != 0 || heads != 0 || receipts != 1 || events != 0 {
			t.Fatal("nonempty prefix survived rollback", personal, heads, receipts, events, err)
		}
		if _, _, _, rows, bytes := uoFlagsOwnerOracle(t, w, root); rows != 0 || bytes != 0 {
			t.Fatal("failed receipt retained owner debt", rows, bytes)
		}
	})

	t.Run("ack floor without refund", func(t *testing.T) {
		w, _, root, identity, _, q := uoFlagsDispatchFixture(t)
		ref := uoFlagsRef(root, 1, 9, 19)
		turn := uoFlagsDispatchTurn(t, w)
		if _, _, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: ref, public: q.publicID, input: q, nowMS: 300}); err != nil {
			t.Fatal(err)
		}
		_, _, _, rows, bytes := uoFlagsOwnerOracle(t, w, root)
		turn = uoFlagsDispatchTurn(t, w)
		out, floor, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: ref, nowMS: 400, ack: true})
		if err != nil || out != (uoRootFlagsResult{}) || floor != 1 {
			t.Fatal("ACK floor", out, floor, err)
		}
		last, ack, _, afterRows, afterBytes := uoFlagsOwnerOracle(t, w, root)
		if last != 1 || ack != 1 || afterRows != rows || afterBytes != bytes {
			t.Fatal("ACK refunded or moved retained bytes", last, ack, afterRows, afterBytes)
		}
		if got := uoFlagsScalar(t, w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, root); got != 1 {
			t.Fatal("ACK erased the retained original", got)
		}
		if got := uoFlagsScalar(t, w, `SELECT watchlisted FROM personal_items WHERE profile_id='profile-one'`); got != 1 {
			t.Fatal("ACK changed the personal effect", got)
		}
		// A repeat ACK of the now-retired sequence is a zero-write floor: this is
		// the first real caller of UO-01's shared acknowledgeUORootOperation.
		before := uoFlagsScalar(t, w, `SELECT total_changes()`)
		turn = uoFlagsDispatchTurn(t, w)
		lookup, err := lookupUORootFlags(t.Context(), turn, uoFlagsProof(t, turn, identity), ref)
		if err != nil || lookup.decision != receiptRetired {
			t.Fatal("retired lookup", lookup.decision, err)
		}
		out, floor, err = runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: ref, nowMS: 500, ack: true})
		if err != nil || out != (uoRootFlagsResult{}) || floor != 1 {
			t.Fatal("repeat ACK", out, floor, err)
		}
		if after := uoFlagsScalar(t, w, `SELECT total_changes()`); after != before {
			t.Fatal("repeat ACK wrote facts", before, after)
		}
	})

	t.Run("unknown commit", func(t *testing.T) {
		w, path, root, identity, _, q := uoFlagsDispatchFixture(t)
		if err := w.db.Close(); err != nil {
			t.Fatal(err)
		}
		connector, err := sqliteconn.Connector(path, w.config)
		if err != nil {
			t.Fatal(err)
		}
		installFaultPool(w, &faultConnector{Connector: connector, replyError: true, afterCommit: true})
		turn := uoFlagsDispatchTurn(t, w)
		out, floor, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: uoFlagsRef(root, 1, 9, 19), public: q.publicID, input: q, nowMS: 300})
		if !errors.Is(err, ErrResolveRequired) || out != (uoRootFlagsResult{}) || floor != 0 {
			t.Fatal("unknown commit invented a result", out, floor, err)
		}
	})

	t.Run("post-commit caller cancel", func(t *testing.T) {
		w, path, root, identity, _, q := uoFlagsDispatchFixture(t)
		ref := uoFlagsRef(root, 1, 9, 19)
		if err := w.db.Close(); err != nil {
			t.Fatal(err)
		}
		connector, err := sqliteconn.Connector(path, w.config)
		if err != nil {
			t.Fatal(err)
		}
		caller, cancel := context.WithCancel(t.Context())
		defer cancel()
		installFaultPool(w, &faultConnector{Connector: connector, afterNativeCommit: cancel})
		turn := uoFlagsDispatchTurn(t, w)
		out, floor, err := runUOFlagsDispatch(caller, turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: ref, public: q.publicID, input: q, nowMS: 300})
		if !errors.Is(err, context.Canceled) || out != (uoRootFlagsResult{}) || floor != 0 {
			t.Fatal("post-commit cancel published a result", out, floor, err)
		}
		// The COMMIT is durable: the personal effect and the original are saved.
		var watchlisted, receipts int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT watchlisted FROM personal_items WHERE profile_id='profile-one'),(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?)`, root).Scan(&watchlisted, &receipts); err != nil || watchlisted != 1 || receipts != 1 {
			t.Fatal("committed effect missing", watchlisted, receipts, err)
		}
		_, _, ownerRevision, _, _ := uoFlagsOwnerOracle(t, w, root)
		retryTurn := uoFlagsDispatchTurn(t, w)
		retry, retryFloor, err := runUOFlagsDispatch(t.Context(), retryTurn, uoFlagsProof(t, retryTurn, identity), uoRootFlagsInvocation{reference: ref, public: q.publicID, input: q, nowMS: 300})
		if err != nil || retryFloor != 0 || !retry.effect.changed || retry.effect.state.revision != 1 || retry.effect.viewerRevision != 1 {
			t.Fatal("retry did not resolve the original", retry, retryFloor, err)
		}
		if _, _, afterRevision, afterRows, _ := uoFlagsOwnerOracle(t, w, root); afterRevision != ownerRevision || afterRows != 1 {
			t.Fatal("retry re-applied the effect", ownerRevision, afterRevision, afterRows)
		}
		if got := uoFlagsScalar(t, w, `SELECT revision FROM personal_items WHERE profile_id='profile-one'`); got != 1 {
			t.Fatal("retry rewrote the personal row", got)
		}
	})

	t.Run("current proof same turn", func(t *testing.T) {
		w, _, root, identity, _, _ := uoFlagsDispatchFixture(t)
		ref := uoFlagsRef(root, 1, 9, 19)
		turn := uoFlagsDispatchTurn(t, w)
		if _, err := lookupUORootFlags(t.Context(), turn, nil, ref); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
			t.Fatal("nil proof reached custody", err)
		}
		proof := uoFlagsProof(t, turn, identity)
		wrongPurpose := *proof
		wrongPurpose.purpose = RootReceiptReclaim
		if _, err := lookupUORootFlags(t.Context(), turn, &wrongPurpose, ref); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
			t.Fatal("other purpose proof accepted", err)
		}
		foreign := *proof
		foreign.identity.family = "other-family"
		if _, err := lookupUORootFlags(t.Context(), turn, &foreign, ref); !errors.Is(err, ErrUORootGone) {
			t.Fatal("foreign root identity passed", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		// A proof from a rolled-back frame cannot be reused on the next frame.
		prior := uoFlagsDispatchTurn(t, w)
		priorProof := uoFlagsProof(t, prior, identity)
		if _, err := prior.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		current := uoFlagsDispatchTurn(t, w)
		if _, err := lookupUORootFlags(t.Context(), current, priorProof, ref); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
			t.Fatal("prior frame proof accepted", err)
		}
		if _, err := current.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		// A finished frame cannot redeem its own proof either.
		finished := uoFlagsDispatchTurn(t, w)
		finishedProof := uoFlagsProof(t, finished, identity)
		if _, err := finished.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		if _, err := lookupUORootFlags(t.Context(), finished, finishedProof, ref); !errors.Is(err, ErrUORootCurrentFenceUnavailable) && !errors.Is(err, ErrInvalid) {
			t.Fatal("finished frame proof accepted", err)
		}
	})

	t.Run("finish guards", func(t *testing.T) {
		for file, names := range map[string][]string{
			"uo_flags_dispatch.go":   {"lookupUORootFlags", "runUOFlagsDispatch"},
			"uo_root_flags_codec.go": {"persistUORootFlags", "restoreUORootFlags", "acknowledgeUORootFlags", "readUORootFlagsPayload"},
		} {
			f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				for _, name := range names {
					if fn.Name.Name != name {
						continue
					}
					if fn.Body == nil || len(fn.Body.List) == 0 {
						t.Fatal(name, "has no finish")
					}
					d, ok := fn.Body.List[0].(*ast.DeferStmt)
					if !ok || !guardedFinishDefer(fn, d) {
						t.Fatal(name, "does not use shared first-action finish")
					}
					found[name] = true
				}
			}
			for _, name := range names {
				if !found[name] {
					t.Fatal("missing release entry", name)
				}
			}
		}
		if _, _, err := runUOFlagsDispatch(t.Context(), nil, nil, uoRootFlagsInvocation{}); !errors.Is(err, ErrUORootCurrentFenceUnavailable) && !errors.Is(err, ErrInvalid) {
			t.Fatal("nil frame dispatch", err)
		}
	})
}
