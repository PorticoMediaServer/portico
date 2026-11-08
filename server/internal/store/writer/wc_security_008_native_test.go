package writer

import (
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"strings"
	"testing"
)

func wc008NativeSecurity(t *testing.T, w *executor) wc008SecurityCommand {
	t.Helper()
	q := wc008SecurityCommand{family: "target", issuerFamily: "issuer", server: "server", account: "account", profile: "profile",
		memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32),
		identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}},
		epoch:    7, tokenGeneration: 3, resultVersion: 1, appliedMS: 1123, revokedAt: "1970-01-01T00:00:01Z"}
	wc008NativeExec(t, w, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) VALUES(?,?,?,?,'local','member',?,'horizon',?,?,?,?,0)`, q.family, q.server, q.account, q.profile, q.epoch, q.tokenGeneration, q.memberIncarnation, q.profileLifetime, q.identity.Incarnation[:])
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes FROM authorization_session_families WHERE id=?`, q.family).Scan(&q.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestWC008NativeSecurityPermanentResultAndRollback(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			w := wc008NativeExecutor(t)
			q := wc008NativeSecurity(t, w)
			turn := wc008NativeTurn(t, w, SecurityAccept, wcSecurityDefinitions())
			e, err := applyWC008SecurityDeny(t.Context(), turn, q)
			if err != nil {
				t.Fatal(err)
			}
			// Same account, different family is permitted by the domain shape;
			// actual OWNER/ADMIN permission issuance remains W's responsibility.
			issuer := wc008IssuerScope{account: q.account, profile: q.profile, ingressFamily: q.issuerFamily,
				memberIncarnation: q.memberIncarnation, profileLifetime: q.profileLifetime}
			if !commit {
				issuer.profileLifetime = "" // fail after the native deny prefix
			}
			n, err := storeWC008SecurityResult(t.Context(), turn, q, issuer, e, RequestDigest{3}, ResultDigest{4})
			if commit {
				if err != nil || n <= e.controlVariableBytes {
					t.Fatal("permanent native result/control bytes", n, e, err)
				}
				if _, err := turn.transaction.commit(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || n != 0 {
					t.Fatal("invalid issuer exposed stored result", n, err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
			}
			var revoked, last, ack, charge int64
			var kind sql.NullString
			var version, due sql.NullInt64
			if err := w.db.QueryRowContext(t.Context(), `SELECT revoked,command_last_sequence,command_acknowledged_sequence,revoke_command_kind,revoke_result_version,teardown_due_ms,control_variable_bytes FROM authorization_session_families WHERE id=?`, q.family).Scan(&revoked, &last, &ack, &kind, &version, &due, &charge); err != nil {
				t.Fatal(err)
			}
			if commit && (revoked != 1 || last != 1 || ack != 0 || kind.String != "admin_revoke" || version.Int64 != 1 || due.Int64 != q.appliedMS || charge != n) {
				t.Fatal("native security result mismatch", revoked, last, ack, kind, version, due, charge)
			}
			if !commit && (revoked != 0 || last != 0 || ack != 0 || kind.Valid || version.Valid || due.Valid || charge != q.oldControlBytes) {
				t.Fatal("denial prefix survived rollback", revoked, last, ack, kind, version, due, charge)
			}

			if commit {
				// ACK retains the permanent result. Fresh execution remains forbidden;
				// historical/current disclosure and replay remain W's separate classifier.
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET command_acknowledged_sequence=command_last_sequence`)
				var count, sequence int64
				if err := w.db.QueryRowContext(t.Context(), `SELECT revoke_result_count,revoke_command_sequence FROM authorization_session_families`).Scan(&count, &sequence); err != nil || count != 1 || sequence != 1 {
					t.Fatal("ACK lost permanent result", count, sequence, err)
				}
				q.last, q.acknowledged, q.identity.Sequence, q.oldControlBytes = 1, 1, 2, charge
				q.identity.OperationID[0]++
				turn = wc008NativeTurn(t, w, SecurityAccept, wcSecurityDefinitions())
				if again, err := applyWC008SecurityDeny(t.Context(), turn, q); err == nil || again != (wc008SecurityEffect{}) {
					t.Fatal("revoked target re-executed", again, err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestWC008NativeSecurityStatementsPrepare(t *testing.T) {
	w := wc008NativeExecutor(t)
	for _, d := range wcSecurityDefinitions() {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	wc008NativePrepare(t, w, wcSecurityDefinitions())
}

func TestWC008NativeSecurityWrongBirthCannotDeny(t *testing.T) {
	w := wc008NativeExecutor(t)
	q := wc008NativeSecurity(t, w)
	q.memberIncarnation = strings.Repeat("c", 32)
	turn := wc008NativeTurn(t, w, SecurityAccept, wcSecurityDefinitions())
	if e, err := applyWC008SecurityDeny(t.Context(), turn, q); err == nil || e != (wc008SecurityEffect{}) {
		t.Fatal("different native birth admitted deny", e, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var revoked, last int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revoked,command_last_sequence FROM authorization_session_families`).Scan(&revoked, &last); err != nil || revoked != 0 || last != 0 {
		t.Fatal("different-birth prefix persisted", revoked, last, err)
	}
}
