package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func uoPublicReadingFixture(t *testing.T, root bool) (*executor, *usOwnerAuthorityProof, uoPublicReadingRequest) {
	t.Helper()
	w, _, proof := usOwnerAuthorityFixture(t)
	// A member on a non-primary profile is valid for ordinary reading. Never
	// turn a NORMAL command into an owner/main-profile security authority stamp.
	wc008NativeExec(t, w, `UPDATE direct_memberships SET role='member' WHERE account_id='issuer'`)
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET role='member' WHERE id='issuer-family'`)
	wc008NativeExec(t, w, `UPDATE direct_profiles SET is_primary=0 WHERE id='issuer-profile'`)
	q := uoPublicReadingRequest{kind: 770, key: fmt.Sprintf("p251_%013d_%032x", w.now().UnixMilli(), 1), request: RequestDigest{1}}
	if root {
		q.root = "abcdefghijklmnopqrstuv"
		i := proof.witness.issuer
		wc008NativeExec(t, w, `INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,1,'active')`, q.root, i.installation, i.account, i.profile, i.memberIncarnation, i.profileLifetime, i.family)
	}
	return w, proof, q
}

func uoPublicReadingTurn(t *testing.T, w *executor, p *usOwnerAuthorityProof, kind CommandID) (*commandTurn, *usCurrentSessionAuthorization) {
	t.Helper()
	defs := uoPublicReadingFixtureDefinitions()
	turn := wc008NativeTurn(t, w, kind, defs)
	a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return turn, a
}

func uoPublicReadingValue(kind CommandID) uoPublicReadingResult {
	v := uoPublicReadingResult{publicID: [16]byte{4}, readingRevision: 17, personalRevision: 19, observedMS: 23, accepted: true, cleared: kind == 771, reason: "original"}
	if kind == 770 {
		v.asset = sql.NullInt64{Int64: 7, Valid: true}
		v.sourceToken = sql.NullString{String: "original-source", Valid: true}
		v.sourceRevision = sql.NullString{String: strings.Repeat("a", 64), Valid: true}
		v.format = sql.NullString{String: "pdf", Valid: true}
		v.page = sql.NullInt64{Int64: 12, Valid: true}
		v.progression = sql.NullFloat64{Float64: 0.25, Valid: true}
	}
	return v
}
func uoPublicReadingCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
func uoPublicReadingCount(t *testing.T, w *executor, q string) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUOPublicReadingFreshSaveReplayAndQuota(t *testing.T) {
	for _, root := range []bool{false, true} {
		for _, kind := range []CommandID{770, 771} {
			t.Run(fmt.Sprint(root, kind), func(t *testing.T) {
				w, p, q := uoPublicReadingFixture(t, root)
				q.kind = kind
				turn, a := uoPublicReadingTurn(t, w, p, kind)
				check, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
				if err != nil || check.reservation == nil || check.original != nil {
					t.Fatal(check, err)
				}
				value := uoPublicReadingValue(kind)
				header := usermutation.Result{Status: "applied", TargetID: "original-title", Revision: 19}
				if err = saveUOPublicReadingReceipt(t.Context(), turn, check.reservation, header, value); err != nil {
					t.Fatal(err)
				}
				uoPublicReadingCommit(t, turn)
				bytes := uoPublicReadingCount(t, w, `SELECT ordinary_bytes FROM user_mutation_global`)
				actual := uoPublicReadingCount(t, w, `SELECT variable_bytes FROM user_mutation_reading_results`)
				if bytes != usermutation.BaseChargeBytes+usermutation.AccountChargeBytes+usermutation.ReceiptChargeBytes+actual {
					t.Fatal("wrong retained charge", bytes, actual)
				}
				if n := uoPublicReadingCount(t, w, `SELECT ordinary_count FROM user_mutation_accounts`); n != 1 {
					t.Fatal(n)
				}
				if root {
					wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining'`)
				}
				turn, a = uoPublicReadingTurn(t, w, p, kind)
				var before, after int64
				if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
					t.Fatal(err)
				}
				replay, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
				if err != nil || replay.reservation != nil || replay.original == nil || replay.original.reading != value || replay.original.receipt.Result != header {
					t.Fatal(replay, err)
				}
				if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
					t.Fatal("replay effects", before, after, err)
				}
				uoPublicReadingCommit(t, turn)
				if n := uoPublicReadingCount(t, w, `SELECT ordinary_bytes FROM user_mutation_global`); n != bytes {
					t.Fatal(n)
				}
			})
		}
	}
}

func TestUOPublicReadingUnsavedAndIgnoredFailureCannotCommit(t *testing.T) {
	for _, mode := range []string{"unsaved", "child-prefix", "header-prefix", "malformed", "canceled-save"} {
		t.Run(mode, func(t *testing.T) {
			w, p, q := uoPublicReadingFixture(t, false)
			turn, a := uoPublicReadingTurn(t, w, p, q.kind)
			check, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE search_revision SET revision=2`); err != nil {
				t.Fatal(err)
			}
			if mode != "unsaved" {
				v := uoPublicReadingValue(q.kind)
				ctx := t.Context()
				switch mode {
				case "child-prefix":
					delete(turn.steps, uoPublicReadingGlobalReconcile)
				case "header-prefix":
					delete(turn.steps, uoPublicReadingHeaderSave)
				case "malformed":
					v.reason = strings.Repeat("x", 129)
				case "canceled-save":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				err = saveUOPublicReadingReceipt(ctx, turn, check.reservation, usermutation.Result{Status: "applied", Revision: 19}, v)
				if err == nil {
					t.Fatal("missing refusal")
				}
			}
			if mode == "child-prefix" || mode == "header-prefix" {
				var children int
				if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM user_mutation_reading_results`).Scan(&children); e != nil || children != 1 {
					t.Fatal("typed-child prefix not exercised", children, e)
				}
			}
			if _, err = turn.transaction.commit(); err == nil {
				t.Fatal("partial original committed")
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_receipts`); n != 0 {
				t.Fatal(n)
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_reading_results`); n != 0 {
				t.Fatal(n)
			}
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_accounts`); n != 0 {
				t.Fatal(n)
			}
			if n := uoPublicReadingCount(t, w, `SELECT revision FROM search_revision`); n != 1 {
				t.Fatal("source prefix survived", n)
			}
		})
	}
}

func TestUOPublicReadingRootAndIdentityMismatch(t *testing.T) {
	for _, mode := range []string{"account", "member", "profile", "lifetime", "installation", "family", "draining"} {
		t.Run(mode, func(t *testing.T) {
			w, p, q := uoPublicReadingFixture(t, true)
			column := map[string]string{"account": "account_id", "member": "account_incarnation", "profile": "profile_id", "lifetime": "profile_incarnation", "installation": "installation_incarnation", "family": "family_id", "draining": "state"}[mode]
			value := "foreign"
			if mode == "lifetime" || mode == "installation" {
				value = strings.Repeat("f", 32)
			}
			if mode == "draining" {
				value = "draining"
			}
			wc008NativeExec(t, w, "UPDATE user_mutation_roots SET "+column+"=?", value) // fixed test-only vocabulary
			turn, a := uoPublicReadingTurn(t, w, p, q.kind)
			check, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
			if err == nil || check != (uoPublicReadingCheck{}) {
				t.Fatal(check, err)
			}
			if mode != "draining" && !errors.Is(err, usermutation.ErrRootGone) {
				t.Fatal(err)
			}
			_, _ = turn.transaction.rollback(nil)
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_accounts`); n != 0 {
				t.Fatal(n)
			}
		})
	}
}

func TestUOPublicReadingOriginalMismatchAndMissingChild(t *testing.T) {
	for _, mode := range []string{"digest", "kind", "missing-child", "reserved", "root", "family"} {
		t.Run(mode, func(t *testing.T) {
			w, p, q := uoPublicReadingFixture(t, false)
			turn, a := uoPublicReadingTurn(t, w, p, q.kind)
			c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
			if err != nil {
				t.Fatal(err)
			}
			if err = saveUOPublicReadingReceipt(t.Context(), turn, c.reservation, usermutation.Result{Status: "applied", Revision: 19}, uoPublicReadingValue(q.kind)); err != nil {
				t.Fatal(err)
			}
			uoPublicReadingCommit(t, turn)
			switch mode {
			case "digest":
				q.request[0]++
			case "kind":
				q.kind = 771
			case "missing-child":
				wc008NativeExec(t, w, `DELETE FROM user_mutation_reading_results`)
			case "reserved":
				wc008NativeExec(t, w, `UPDATE user_mutation_receipts SET status='reserved'`)
			case "root":
				wc008NativeExec(t, w, `UPDATE user_mutation_receipts SET root_id='abcdefghijklmnopqrstuv'`)
			case "family":
				wc008NativeExec(t, w, `UPDATE user_mutation_receipts SET family_id='foreign'`)
			}
			turn, a = uoPublicReadingTurn(t, w, p, q.kind)
			out, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
			if err == nil || out != (uoPublicReadingCheck{}) {
				t.Fatal(out, err)
			}
			_, _ = turn.transaction.rollback(nil)
		})
	}
}

func TestUOPublicReadingCurrentFenceDenials(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE direct_profiles SET deleted=1`, `DELETE FROM direct_profiles`, `DELETE FROM identity_installations`,
		`UPDATE identity_installations SET retired=1`, `UPDATE direct_memberships SET disabled=1`, `UPDATE direct_memberships SET incarnation='ffffffffffffffffffffffffffffffff'`,
		`UPDATE accounts SET epoch=2`, `UPDATE authorization_session_families SET revoked=1`, `UPDATE authorization_session_families SET security_kind=1`,
		`UPDATE identity_devices SET retired_ms=1`, `UPDATE identity_security_fences SET denied=1`, `UPDATE identity_state SET authority_generation=2`,
		`UPDATE authorization_family_tokens SET retired=1`, `UPDATE direct_memberships SET role='owner'`,
		`INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`,
		`INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','password')`,
		`INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`,
		`INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','owner_claim_completion')`,
		`UPDATE authorization_session_families SET pin_lineage_id=NULL`,
		`UPDATE identity_state SET transition_id='pending-move'`,
		`UPDATE identity_state SET sign_in_kind='portico'`,
		`UPDATE accounts SET must_change=1`,
		`UPDATE identity_devices SET platform='api'`,
	} {
		t.Run(mutation, func(t *testing.T) {
			w, p, _ := uoPublicReadingFixture(t, false)
			if strings.Contains(mutation, "transition_id") {
				wc008NativeExec(t, w, `INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms) VALUES('pending-move','issuer',1,'local','portico',0,'preview',1,1,1,1,'Fixture',0)`)
			}
			wc008NativeExec(t, w, mutation)
			turn := wc008NativeTurn(t, w, 770, uoPublicReadingFixtureDefinitions())
			a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
			if err == nil || a != nil {
				t.Fatal(a, err)
			}
		})
	}
}

func TestUOPublicReadingFinalCurrentFenceAndFrameReuse(t *testing.T) {
	w, p, q := uoPublicReadingFixture(t, false)
	turn, a := uoPublicReadingTurn(t, w, p, q.kind)
	c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveUOPublicReadingReceipt(t.Context(), turn, c.reservation, usermutation.Result{Status: "applied", Revision: 19}, uoPublicReadingValue(q.kind)); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_devices SET retired_ms=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err == nil {
		t.Fatal("terminal failed to reprove current authority")
	}
	if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_receipts`); n != 0 {
		t.Fatal(n)
	}
	other, _ := uoPublicReadingTurn(t, w, p, q.kind)
	if err = a.recheck(t.Context(), other); err == nil {
		t.Fatal("rolled-back frame proof reused")
	}
}

func TestUOPublicReadingQuotaAndCutoff(t *testing.T) {
	for _, mode := range []string{"account", "global-count", "bytes", "expired", "future"} {
		t.Run(mode, func(t *testing.T) {
			w, p, q := uoPublicReadingFixture(t, false)
			wc008NativeExec(t, w, `INSERT INTO user_mutation_accounts(authority,account_id,owner_account_id,charged_stop) VALUES('local','issuer','issuer',0)`)
			wc008NativeExec(t, w, `INSERT INTO user_mutation_global(singleton) VALUES(1)`)
			switch mode {
			case "account":
				wc008NativeExec(t, w, `UPDATE user_mutation_accounts SET ordinary_count=8192`)
			case "global-count":
				wc008NativeExec(t, w, `UPDATE user_mutation_global SET ordinary_count=65536`)
			case "bytes":
				wc008NativeExec(t, w, `UPDATE user_mutation_global SET ordinary_bytes=31457280`)
			case "expired":
				q.key = fmt.Sprintf("p251_%013d_%032x", w.now().UnixMilli()-usermutation.RetentionMs-1, 1)
			case "future":
				q.key = fmt.Sprintf("p251_%013d_%032x", w.now().Add(6*time.Minute).UnixMilli(), 1)
			}
			turn, a := uoPublicReadingTurn(t, w, p, q.kind)
			c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
			if err == nil || c != (uoPublicReadingCheck{}) {
				t.Fatal(c, err)
			}
			_, _ = turn.transaction.rollback(nil)
			if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_receipts`); n != 0 {
				t.Fatal(n)
			}
		})
	}
}

func TestUOPublicReadingStatementsAndRedaction(t *testing.T) {
	w, _, _ := uoPublicReadingFixture(t, false)
	for _, d := range append(usCurrentSessionDefinitions(), uoPublicReadingDefinitions()...) {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	wc008NativePrepare(t, w, append(usCurrentSessionDefinitions(), uoPublicReadingDefinitions()...))
	if text := fmt.Sprintf("%+v %#v", uoPublicReadingRequest{key: "private-key"}, uoPublicReadingReservation{}); strings.Contains(text, "private-key") {
		t.Fatal(text)
	}
}

func uoPublicReadingFixtureDefinitions() []stmt.Definition {
	defs := append(usCurrentSessionDefinitions(), uoPublicReadingDefinitions()...)
	for n := range defs {
		d := &defs[n]
		if len(d.Cohorts) != 0 {
			continue
		}
		args := make([]any, len(d.Params))
		for j, p := range d.Params {
			if p.Nullable {
				continue
			}
			switch p.Type {
			case stmt.Text:
				args[j] = "fixture"
			case stmt.Blob:
				args[j] = make([]byte, 16)
			case stmt.Real:
				args[j] = 0.25
			case stmt.Boolean:
				args[j] = false
			default:
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "unqualified_public_reading_shape", Bindings: args}}
	}
	return defs
}

func TestUOPublicReadingPINClearAndClosedPurpose(t *testing.T) {
	w, p, _ := uoPublicReadingFixture(t, false)
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET pin_gate=1`)
	turn, a := uoPublicReadingTurn(t, w, p, 770)
	if a == nil {
		t.Fatal("PIN clear retired ordinary sign-in")
	}
	_, _ = turn.transaction.rollback(nil)
	turn = wc008NativeTurn(t, w, SetPersonalFlags, uoPublicReadingFixtureDefinitions())
	a, err := authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
	if err != nil || a == nil {
		t.Fatal("600 current-fence consumer", err)
	}
	_, _ = turn.transaction.rollback(nil)
	turn = wc008NativeTurn(t, w, USMemberLibraries, uoPublicReadingFixtureDefinitions())
	a, err = authorizeUSCurrentSession(t.Context(), turn, p, t.Context())
	if err == nil || a != nil {
		t.Fatal("FULL purpose minted NORMAL proof")
	}
}

func TestUOPublicReadingCutoffChangesBeforeTerminal(t *testing.T) {
	w, p, q := uoPublicReadingFixture(t, false)
	turn, a := uoPublicReadingTurn(t, w, p, q.kind)
	c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveUOPublicReadingReceipt(t.Context(), turn, c.reservation, usermutation.Result{Status: "applied", Revision: 19}, uoPublicReadingValue(q.kind)); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_accounts SET retired_before_ms=?`, w.now().UnixMilli()+1); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err == nil {
		t.Fatal("retired original committed")
	}
	if n := uoPublicReadingCount(t, w, `SELECT count(*) FROM user_mutation_receipts`); n != 0 {
		t.Fatal(n)
	}
}

func TestUOPublicReadingLegalMaximumBodyReservation(t *testing.T) {
	w, p, q := uoPublicReadingFixture(t, false)
	turn, a := uoPublicReadingTurn(t, w, p, q.kind)
	c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	v := uoPublicReadingValue(q.kind)
	v.sourceToken.String = strings.Repeat("t", 512)
	v.format.String = "epub"
	v.page = sql.NullInt64{}
	v.cfi = sql.NullString{String: strings.Repeat("c", 4096), Valid: true}
	v.section = sql.NullString{String: strings.Repeat("s", 1024), Valid: true}
	v.reason = strings.Repeat("r", 128)
	if err = saveUOPublicReadingReceipt(t.Context(), turn, c.reservation, usermutation.Result{Status: "applied", Revision: 19}, v); err != nil {
		t.Fatal(err)
	}
	uoPublicReadingCommit(t, turn)
	if n := uoPublicReadingCount(t, w, `SELECT variable_bytes FROM user_mutation_reading_results`); n != uoReadingChildMaxBytes {
		t.Fatal(n, uoReadingChildMaxBytes)
	}
}

func TestUOPublicReadingReplayAfterTokenRotation(t *testing.T) {
	w, p, q := uoPublicReadingFixture(t, true)
	turn, a := uoPublicReadingTurn(t, w, p, q.kind)
	c, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
	if err != nil {
		t.Fatal(err)
	}
	original := uoPublicReadingValue(q.kind)
	if err = saveUOPublicReadingReceipt(t.Context(), turn, c.reservation, usermutation.Result{Status: "applied", Revision: 19}, original); err != nil {
		t.Fatal(err)
	}
	uoPublicReadingCommit(t, turn)
	witness := p.witness
	witness.issuer.familyGeneration = 2
	witness.issuer.tokenDigest = usCredentialFixtureHash("new-current-reading-token")
	p, err = prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE authorization_family_tokens SET retired=1`)
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET current_generation=2`)
	wc008NativeExec(t, w, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,'issuer-family',2,?)`, witness.issuer.tokenDigest, witness.tokenExpiry)
	turn, a = uoPublicReadingTurn(t, w, p, q.kind)
	out, err := checkUOPublicReadingReceipt(t.Context(), turn, a, q)
	if err != nil || out.original == nil || out.original.reading != original {
		t.Fatal(out, err)
	}
	uoPublicReadingCommit(t, turn)
}
