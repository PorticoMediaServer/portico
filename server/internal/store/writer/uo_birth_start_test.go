package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func uoBirthStartFixture(t *testing.T) (*executor, *usOwnerAuthorityProof, uoRootReference, uoBirthStartResult) {
	t.Helper()
	w, p, reading := uoPublicReadingFixture(t, true)
	q := uoRootReference{root: reading.root, kind: 700, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	wc008NativeExec(t, w, `INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, q.root, q.identity.Incarnation[:])
	now := w.now().UnixMilli()
	v := uoBirthStartResult{root: q.root, preparation: "preparation-original", device: p.witness.device, startKey: strings.Repeat("a", 64), startBody: strings.Repeat("b", 64), item: "original-item", asset: "original-asset", worker: [16]byte{9}, createdMS: now, deadlineMS: now + 60000, tokenExpiresMS: p.tokenExpiry.UnixMilli(), revision: 1, retryMS: 500, appliedMS: now}
	return w, p, q, v
}
func uoBirthStartTurn(t *testing.T, w *executor, p *usOwnerAuthorityProof) (*commandTurn, *usCurrentSessionAuthorization) {
	t.Helper()
	defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
	defs = append(defs, uoBirthStartDefinitions()...)
	turn := wc008NativeTurn(t, w, 700, defs)
	a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return turn, a
}
func uoBirthStartRow(t *testing.T, turn *commandTurn, p *usOwnerAuthorityProof, v uoBirthStartResult) {
	t.Helper()
	i := p.witness.issuer
	_, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO playback_start_preparations(id,device_id,start_key_digest,start_body_digest,item_id,asset_id,part_index,replaces_session_id,replaces_generation,token_expires_ms,created_ms,deadline_ms,mp_command_incarnation,mp_user_root_id,mp_owner_member_incarnation,mp_owner_profile_lifetime) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.preparation, v.device, v.startKey, v.startBody, v.item, v.asset, v.part, v.replaces, v.replacesGeneration, v.tokenExpiresMS, v.createdMS, v.deadlineMS, v.worker[:], v.root, i.memberIncarnation, i.profileLifetime)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUOBirthStartOriginalAndIndependentWorker(t *testing.T) {
	w, p, q, v := uoBirthStartFixture(t)
	turn, a := uoBirthStartTurn(t, w, p)
	lookup, err := lookupUOBirthStart(t.Context(), turn, a, q)
	if err != nil || lookup.root.decision != receiptFresh {
		t.Fatal(lookup, err)
	}
	uoBirthStartRow(t, turn, p, v)
	digest, err := persistUOBirthStart(t.Context(), turn, lookup, v)
	if err != nil || digest == [32]byte{} {
		t.Fatal(digest, err)
	}
	uoPublicReadingCommit(t, turn)
	if n := uoPublicReadingCount(t, w, `SELECT mp_command_last_sequence+mp_command_acknowledged_sequence FROM playback_start_preparations`); n != 0 {
		t.Fatal("borrowed user sequence", n)
	}
	if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM mp_start_command_receipts`); n != 0 {
		t.Fatal("worker receipt on user birth", n)
	}
	if n := uoPublicReadingCount(t, w, `SELECT receipt_variable_bytes-(SELECT sum(variable_bytes) FROM user_mutation_command_receipts) FROM user_mutation_command_owners`); n != 0 {
		t.Fatal("root byte ledger", n)
	}
	wc008NativeExec(t, w, `DELETE FROM playback_start_preparations`)
	wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining'`)
	turn, a = uoBirthStartTurn(t, w, p)
	original, err := lookupUOBirthStart(t.Context(), turn, a, q)
	if err != nil || original.root.decision != receiptReplay || original.original != v {
		t.Fatal(original, err)
	}
	uoPublicReadingCommit(t, turn)
	if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM playback_start_preparations`); n != 0 {
		t.Fatal("retry recreated worker", n)
	}
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=last_command_sequence`)
	turn, a = uoBirthStartTurn(t, w, p)
	retired, err := lookupUOBirthStart(t.Context(), turn, a, q)
	if err != nil || retired.root.decision != receiptRetired || retired.original != (uoBirthStartResult{}) {
		t.Fatal(retired, err)
	}
	uoPublicReadingCommit(t, turn)
}

func TestUOBirthStartNoOwnerSynthesisAndRootBinding(t *testing.T) {
	for _, mode := range []string{"owner-missing", "root-missing", "foreign-root", "draining", "changed-incarnation"} {
		t.Run(mode, func(t *testing.T) {
			w, p, q, _ := uoBirthStartFixture(t)
			switch mode {
			case "owner-missing":
				wc008NativeExec(t, w, `DELETE FROM user_mutation_command_owners`)
			case "root-missing":
				wc008NativeExec(t, w, `DELETE FROM user_mutation_command_owners`)
				wc008NativeExec(t, w, `DELETE FROM user_mutation_roots`)
			case "foreign-root":
				wc008NativeExec(t, w, `UPDATE user_mutation_roots SET account_id='foreign'`)
			case "draining":
				wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining'`)
			case "changed-incarnation":
				q.identity.Incarnation[0]++
			}
			turn, a := uoBirthStartTurn(t, w, p)
			out, err := lookupUOBirthStart(t.Context(), turn, a, q)
			if err == nil || out != nil {
				t.Fatal(out, err)
			}
			if mode == "root-missing" || mode == "foreign-root" || mode == "draining" {
				if !errors.Is(err, ErrUORootGone) || errors.Is(err, sql.ErrNoRows) {
					t.Fatal("root failure class", err)
				}
			}
			if mode == "owner-missing" && (!errors.Is(err, ErrResolveRequired) || !errors.Is(err, ErrOwnerUnavailable) || errors.Is(err, sql.ErrNoRows)) {
				t.Fatal("missing owner escaped typed mapping", err)
			}
			_, _ = turn.transaction.rollback(nil)
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM playback_start_preparations`); n != 0 {
				t.Fatal(n)
			}
		})
	}
}

func TestUOBirthStartWrongNativeOrUnsavedRollsBack(t *testing.T) {
	for _, mode := range []string{"worker", "borrowed-worker", "member", "profile", "device", "native-root", "result-root", "unsaved", "header-prefix", "final-source", "final-authority", "final-header", "final-ledger"} {
		t.Run(mode, func(t *testing.T) {
			w, p, q, v := uoBirthStartFixture(t)
			turn, a := uoBirthStartTurn(t, w, p)
			lookup, err := lookupUOBirthStart(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			uoBirthStartRow(t, turn, p, v)
			switch mode {
			case "worker":
				v.worker[0]++
			case "borrowed-worker":
				v.worker = q.identity.Incarnation
			case "member":
				_, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE playback_start_preparations SET mp_owner_member_incarnation=?`, strings.Repeat("f", 32))
			case "profile":
				_, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE playback_start_preparations SET mp_owner_profile_lifetime=?`, strings.Repeat("f", 32))
			case "native-root":
				_, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE playback_start_preparations SET mp_user_root_id='zyxwvutsrqponmlkjihgfe'`)
			case "result-root":
				v.root = "zyxwvutsrqponmlkjihgfe"
			case "device":
				v.device = "foreign"
			case "header-prefix":
				delete(turn.steps, uoRootOwnerAdvanceNull)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode != "unsaved" {
				_, err = persistUOBirthStart(t.Context(), turn, lookup, v)
				if mode == "final-source" || mode == "final-authority" || mode == "final-header" || mode == "final-ledger" {
					if err != nil {
						t.Fatal(err)
					}
					sql := `UPDATE playback_start_preparations SET start_body_digest='ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'`
					switch mode {
					case "final-authority":
						sql = `UPDATE identity_devices SET retired_ms=1`
					case "final-header":
						sql = `DELETE FROM user_mutation_command_receipts`
					case "final-ledger":
						sql = `UPDATE user_mutation_command_owners SET receipt_rows=0,receipt_variable_bytes=0`
					}
					if _, err = turn.transaction.conn.ExecContext(t.Context(), sql); err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("bad native birth accepted")
				}
			}
			if _, err = turn.transaction.commit(); err == nil {
				t.Fatal("partial/stale birth committed")
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM playback_start_preparations`); n != 0 {
				t.Fatal(n)
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
				t.Fatal(n)
			}
		})
	}
}

func TestUOBirthStartClosedRootPathAndProof(t *testing.T) {
	w, p, q, _ := uoBirthStartFixture(t)
	turn, a := uoBirthStartTurn(t, w, p)
	forged := uoRootBinding{identity: uoRootSessionIdentity(a)}
	if err := forged.check(t.Context(), turn); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("NORMAL binding fell back to cancellation only", err)
	}
	proof, err := bindUSCurrentUORoot(t.Context(), turn, a)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := bindUORoot(turn, nil, proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lookupUORootCommand(t.Context(), turn, &binding, q); !errors.Is(err, ErrInvalid) {
		t.Fatal("generic birth lookup bypass", err)
	}
	_, _ = turn.transaction.rollback(nil)
	turn, a = uoBirthStartTurn(t, w, p)
	proof, err = bindUSCurrentUORoot(t.Context(), turn, a)
	if err != nil {
		t.Fatal(err)
	}
	proof.identity.account = "foreign"
	if _, err = bindUORoot(turn, nil, proof); err == nil {
		t.Fatal("copied/forged proof identity")
	}
	_, _ = turn.transaction.rollback(nil)
	turn, a = uoBirthStartTurn(t, w, p)
	lookup, err := lookupUOBirthStart(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistUORootOperation(t.Context(), turn, lookup.root, uoOperationResult{status: 1, target: "fake birth", appliedMS: 1}); err == nil {
		t.Fatal("generic scalar relabeled as birth")
	}
	_, _ = turn.transaction.rollback(nil)
	other, _ := uoBirthStartTurn(t, w, p)
	if err = a.recheck(t.Context(), other); err == nil {
		t.Fatal("old-frame current proof reused")
	}
}

func TestUOBirthStartRootChangesBeforePayloadRestore(t *testing.T) {
	w, p, q, v := uoBirthStartFixture(t)
	turn, a := uoBirthStartTurn(t, w, p)
	lookup, err := lookupUOBirthStart(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	uoBirthStartRow(t, turn, p, v)
	if _, err = persistUOBirthStart(t.Context(), turn, lookup, v); err != nil {
		t.Fatal(err)
	}
	uoPublicReadingCommit(t, turn)
	turn, a = uoBirthStartTurn(t, w, p)
	replay, err := lookupUOBirthStart(t.Context(), turn, a, q)
	if err != nil || replay.root.decision != receiptReplay {
		t.Fatal(replay, err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET account_id='foreign'`); err != nil {
		t.Fatal(err)
	}
	payload, digest, err := readUORootPayload(t.Context(), turn, replay.root)
	if !errors.Is(err, ErrUORootGone) || len(payload) != 0 || digest != (ResultDigest{}) {
		t.Fatal("stale root released original", len(payload), digest, err)
	}
	if _, err = turn.transaction.commit(); err == nil {
		t.Fatal("stale root committed")
	}
}

func TestUOBirthStartCodecAndPlans(t *testing.T) {
	w, p, q, v := uoBirthStartFixture(t)
	encoded, digest, err := encodeUORootCanonical(q.identity, q.request, v)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeUOBirthStart(q.identity, q.request, q.root, encoded, digest)
	if err != nil || decoded != v {
		t.Fatal(decoded, err)
	}
	for _, payload := range [][]byte{append(append([]byte{}, encoded...), 0), encoded[:len(encoded)-1], encoded[:len(encoded)-8*8-1]} {
		if _, err = decodeUOBirthStart(q.identity, q.request, q.root, payload, digest); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("malformed original class", err)
		}
	}
	q.request[0]++
	if _, err = decodeUOBirthStart(q.identity, q.request, q.root, encoded, digest); err == nil {
		t.Fatal("changed request accepted")
	}
	for _, d := range uoBirthStartDefinitions() {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(issues)
		}
	}
	wc008NativePrepare(t, w, uoBirthStartDefinitions())
	turn, a := uoBirthStartTurn(t, w, p)
	cancelCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := lookupUOBirthStart(cancelCtx, turn, a, q); err == nil || out != nil {
		t.Fatal(out, err)
	}
}

func TestUOBirthStartPreparedComparisonsAreNotAuthority(t *testing.T) {
	for _, mode := range []string{"expiry", "installation-hash"} {
		t.Run(mode, func(t *testing.T) {
			w, p, _, _ := uoBirthStartFixture(t)
			switch mode {
			case "expiry":
				p.witness.tokenExpiry = w.now().Add(-time.Minute).UTC().Format(time.RFC3339)
				wc008NativeExec(t, w, `UPDATE authorization_family_tokens SET expires_at=?`, p.witness.tokenExpiry) // parsed p.tokenExpiry intentionally remains future
			case "installation-hash":
				p.witness.installationID = "different-installation-synthetic-00001"
				wc008NativeExec(t, w, `UPDATE identity_devices SET installation_id=?`, p.witness.installationID) // prepared hash intentionally remains the old registered hash
			}
			defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
			defs = append(defs, uoBirthStartDefinitions()...)
			turn := wc008NativeTurn(t, w, 700, defs)
			a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
			if err == nil || a != nil {
				t.Fatal("frozen comparison minted authority", a, err)
			}
		})
	}
}

func TestUOBirthStartNormalProofBindings(t *testing.T) {
	for _, mode := range []string{"purpose", "deadline", "lifetime", "live-lifetime", "prior-frame", "finished", "rolled-back"} {
		t.Run(mode, func(t *testing.T) {
			w, p, _, _ := uoBirthStartFixture(t)
			turn, a := uoBirthStartTurn(t, w, p)
			proof, err := bindUSCurrentUORoot(t.Context(), turn, a)
			if err != nil {
				t.Fatal(err)
			}
			if err = proof.current(turn); err != nil {
				t.Fatal("live control", err)
			}
			switch mode {
			case "purpose":
				proof.purpose = 600
			case "deadline":
				proof.deadline = proof.deadline.Add(time.Millisecond)
			case "lifetime":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				proof.lifetime = ctx
			case "live-lifetime":
				live, cancel := context.WithCancel(t.Context())
				defer cancel()
				proof.lifetime = live
			case "prior-frame":
				_, _ = turn.transaction.rollback(nil)
				turn, _ = uoBirthStartTurn(t, w, p)
			case "finished":
				uoPublicReadingCommit(t, turn)
			case "rolled-back":
				_, _ = turn.transaction.rollback(nil)
			}
			if err = proof.current(turn); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
				t.Fatal("reused NORMAL proof", err)
			}
		})
	}
}

func TestUOBirthStartReplayDigestAndCurrentFence(t *testing.T) {
	for _, mode := range []string{"digest", "current"} {
		t.Run(mode, func(t *testing.T) {
			w, p, q, v := uoBirthStartFixture(t)
			turn, a := uoBirthStartTurn(t, w, p)
			lookup, err := lookupUOBirthStart(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			uoBirthStartRow(t, turn, p, v)
			if _, err = persistUOBirthStart(t.Context(), turn, lookup, v); err != nil {
				t.Fatal(err)
			}
			uoPublicReadingCommit(t, turn)
			turn, a = uoBirthStartTurn(t, w, p)
			want := ErrConflict
			if mode == "digest" {
				q.request[0]++
			} else {
				if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET denied=1`); err != nil {
					t.Fatal(err)
				}
				// An attempted payload read would now fail with ErrInvalid. Only the
				// independent pre-lookup current fence may produce the expected denial.
				delete(turn.steps, uoRootReceiptPayload)
				want = ErrDisclosureDenied
			}
			out, err := lookupUOBirthStart(t.Context(), turn, a, q)
			if !errors.Is(err, want) || out != nil {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err == nil {
				t.Fatal("denied replay committed")
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 1 {
				t.Fatal(n)
			}
		})
	}
}

func TestUOBirthStartDrainingBeforeCommit(t *testing.T) {
	w, p, q, v := uoBirthStartFixture(t)
	turn, a := uoBirthStartTurn(t, w, p)
	lookup, err := lookupUOBirthStart(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	uoBirthStartRow(t, turn, p, v)
	if _, err = persistUOBirthStart(t.Context(), turn, lookup, v); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET state='draining'`); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); !errors.Is(err, ErrUORootGone) {
		t.Fatal("Fresh survived drain", err)
	}
	if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
		t.Fatal(n)
	}
	if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM playback_start_preparations`); n != 0 {
		t.Fatal(n)
	}
}

func TestUOBirthStartUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			w, p, q, v := uoBirthStartFixture(t)
			var seq int
			var name, path string
			if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
				t.Fatal(err)
			}
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			base, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			installFaultPool(w, &faultConnector{Connector: base, afterCommit: after, replyError: true})
			turn, a := uoBirthStartTurn(t, w, p)
			lookup, err := lookupUOBirthStart(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			uoBirthStartRow(t, turn, p, v)
			if _, err = persistUOBirthStart(t.Context(), turn, lookup, v); err != nil {
				t.Fatal(err)
			}
			result, err := turn.transaction.commit()
			if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) || !w.ambiguous {
				t.Fatal("unknown treated as applied", result, err)
			}
			reader, _, err := sqliteconn.Open(t.Context(), path, sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			want := 0
			if after {
				want = 1
			}
			for _, table := range []string{"playback_start_preparations", "user_mutation_command_receipts"} {
				var n int
				if err = reader.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != want {
					t.Fatal(table, n, want, err)
				}
			}
			if _, err = w.begin(t.Context(), 700, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("ambiguous executor reused", err)
			}
		})
	}
}

func TestUOBirthStartRootBridgePurposeIsClosed(t *testing.T) {
	for _, kind := range []CommandID{600, 770, 771} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			w, p, _ := uoPublicReadingFixture(t, false)
			turn, a := uoPublicReadingTurn(t, w, p, kind)
			proof, err := bindUSCurrentUORoot(t.Context(), turn, a)
			if kind == 600 {
				if err != nil || proof == nil {
					t.Fatal(proof, err)
				}
			} else if !errors.Is(err, ErrUORootCurrentFenceUnavailable) || proof != nil {
				t.Fatal("public reading borrowed root scalar seam", proof, err)
			}
		})
	}
}
