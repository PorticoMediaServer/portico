package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// TestUSInvitationSourceClassification closes G2: the point source read exists
// in the same frame and each CAS miss becomes one named outcome.
func TestUSInvitationSourceClassification(t *testing.T) {
	w, _ := usInvitationTestExecutor(t)
	usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
	edit := usInvitationTestEditCommand(t, 1, "member", []string{"music"}, usInvitationTestMS()+2000)
	revoke := usInvitationTestRevokeCommand(t, 1)
	expireLow := fmt.Sprintf("UPDATE access_invitations SET expires_ms=%d WHERE id='us-invite'", usInvitationTestMS()-500)
	expireOK := fmt.Sprintf("UPDATE access_invitations SET expires_ms=%d WHERE id='us-invite'", usInvitationTestMS()+1000)
	cases := []struct {
		name       string
		update     string
		prepared   *usInvitationPrepared
		want       error
		afterCheck func()
	}{
		{"ok", "", edit.prepared, nil, nil},
		{"revision", `UPDATE access_invitations SET revision=2 WHERE id='us-invite'`, edit.prepared, ErrInvitationRevision,
			func() { wc008NativeExec(t, w, `UPDATE access_invitations SET revision=1 WHERE id='us-invite'`) }},
		{"expired", expireLow, edit.prepared, ErrInvitationExpired,
			func() { wc008NativeExec(t, w, expireOK) }},
		{"accepted", `UPDATE access_invitations SET state='accepted' WHERE id='us-invite'`, edit.prepared, ErrInvitationAccepted,
			func() { wc008NativeExec(t, w, `UPDATE access_invitations SET state='pending' WHERE id='us-invite'`) }},
		{"revoked", `UPDATE access_invitations SET state='revoked' WHERE id='us-invite'`, edit.prepared, ErrInvitationRevoked,
			func() { wc008NativeExec(t, w, `UPDATE access_invitations SET state='pending' WHERE id='us-invite'`) }},
		{"revoke_accepted", `UPDATE access_invitations SET state='accepted' WHERE id='us-invite'`, revoke.prepared, ErrInvitationAccepted,
			func() { wc008NativeExec(t, w, `UPDATE access_invitations SET state='pending' WHERE id='us-invite'`) }},
		{"revoke_revoked_allowed", `UPDATE access_invitations SET state='revoked' WHERE id='us-invite'`, revoke.prepared, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.update != "" {
				wc008NativeExec(t, w, tc.update)
			}
			turn := wc008NativeTurn(t, w, tc.prepared.kind, usInvitationTestDefinitions())
			row, err := readUSInvitationSource(t.Context(), turn, "us-invite")
			if err == nil {
				err = classifyUSInvitationSource(tc.prepared, row, usInvitationTestMS())
			}
			_, _ = turn.transaction.rollback(nil)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatal("classification", err)
			}
			if tc.afterCheck != nil {
				tc.afterCheck()
			}
		})
	}
	// Not-found is a distinct named outcome.
	wc008NativeExec(t, w, `DELETE FROM access_invitations WHERE id='us-invite'`)
	turn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
	row, err := readUSInvitationSource(t.Context(), turn, "us-invite")
	if err == nil {
		err = classifyUSInvitationSource(edit.prepared, row, usInvitationTestMS())
	}
	_, _ = turn.transaction.rollback(nil)
	if !errors.Is(err, ErrInvitationNotFound) {
		t.Fatal("not-found classification", err)
	}
}

// TestUSInvitationSignInDrift closes G3: the kind/revision witness is inside the
// frozen request digest and a drift refuses before any source write.
func TestUSInvitationSignInDrift(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
	other := *cmd
	other.signIn = usInvitationSignInKind{kind: "portico", revision: 2}
	otherDigest, err := usInvitationCommandDigest(&other)
	if err != nil || otherDigest == cmd.digest {
		t.Fatal("sign-in witness not bound by the frozen digest", err)
	}
	wc008NativeExec(t, w, `UPDATE identity_state SET sign_in_kind='portico',revision=2`)
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("drifted kind accepted", out, floor, err)
	}
	if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 ||
		usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("drifted kind wrote a prefix")
	}
	// Revision-only drift is refused too.
	wc008NativeExec(t, w, `UPDATE identity_state SET sign_in_kind='local',revision=3`)
	turn = wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	if out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd)); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
		t.Fatal("drifted revision accepted", out, err)
	}
}

// TestUSInvitationCreateAdmission closes G4: the exact legacy HMAC derivation,
// named PK/code_hash collisions, the 200-live cap and library existence.
func TestUSInvitationCreateAdmission(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	code, hash, err := usInvitationLegacyLink(key, "us-created")
	if err != nil || len(code) != 43 {
		t.Fatal("legacy code shape", len(code), err)
	}
	sum := sha256.Sum256([]byte(code))
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatal("legacy hash is not digest(code)")
	}
	other, _, err := usInvitationLegacyLink(key, "us-other")
	if err != nil || other == code {
		t.Fatal("code is not bound to the id", err)
	}
	if _, err := usInvitationCodeHash("short"); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted malformed code")
	}

	t.Run("id_collision", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		wc008NativeExec(t, w, `UPDATE access_invitations SET id='us-created' WHERE id='us-invite'`)
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrInvitationIDCollision) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("id collision", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 1 ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
			t.Fatal("id collision wrote")
		}
	})

	t.Run("code_collision", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		wc008NativeExec(t, w, `UPDATE access_invitations SET code_hash=? WHERE id='us-invite'`, hash)
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrInvitationCodeCollision) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("code collision", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 {
			t.Fatal("code collision wrote")
		}
	})

	t.Run("live_capacity", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		for n := 0; n < 200; n++ {
			wc008NativeExec(t, w, `INSERT INTO access_invitations(id,code_hash,role,allowed_libraries,state,created_ms,expires_ms,created_by,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating)
VALUES(?,?,'member','[]','pending',?,?,'us-owner',1,'','inherit',0,1,1,'')`, fmt.Sprintf("us-live-%03d", n), fmt.Sprintf("%064d", n), usInvitationTestMS()-500, usInvitationTestMS()+1000)
		}
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrInvitationCapacity) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("live capacity", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 {
			t.Fatal("capacity refusal wrote")
		}
	})

	t.Run("library_missing", func(t *testing.T) {
		w, _ := usInvitationTestExecutor(t)
		m := usTestInvitationManager("us-owner", "admin", "member")
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"missing"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrInvitationLibrary) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("missing library", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 {
			t.Fatal("missing library wrote")
		}
	})
}

// TestUSInvitationManagerSeam proves the narrow authority seam: tier denial,
// issuer mismatch and Recheck denial at entry, before payload and at release.
func TestUSInvitationManagerSeam(t *testing.T) {
	t.Run("tier_create_admin_denied", func(t *testing.T) {
		w, _ := usInvitationTestExecutor(t)
		m := usTestInvitationManager("us-owner", "member")
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "admin", []string{"movies"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("tier create", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 {
			t.Fatal("tier denial wrote")
		}
	})

	t.Run("tier_edit_current_admin_denied", func(t *testing.T) {
		w, _ := usInvitationTestExecutor(t)
		m := usTestInvitationManager("us-owner", "member")
		usInvitationTestSeedInvitation(t, w, "admin", "pending", 1, usInvitationTestMS()+1000)
		cmd := usInvitationTestEditCommand(t, 1, "member", []string{"music"}, usInvitationTestMS()+2000)
		turn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
		if out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd)); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("tier edit current", out, err)
		}
	})

	t.Run("tier_edit_requested_admin_denied", func(t *testing.T) {
		w, _ := usInvitationTestExecutor(t)
		m := usTestInvitationManager("us-owner", "member")
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		cmd := usInvitationTestEditCommand(t, 1, "admin", []string{"music"}, usInvitationTestMS()+2000)
		turn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("tier edit requested", out, err)
		}
	})

	t.Run("tier_revoke_current_admin_denied", func(t *testing.T) {
		w, _ := usInvitationTestExecutor(t)
		m := usTestInvitationManager("us-owner", "member")
		usInvitationTestSeedInvitation(t, w, "admin", "pending", 1, usInvitationTestMS()+1000)
		cmd := usInvitationTestRevokeCommand(t, 1)
		turn := wc008NativeTurn(t, w, usInvitationRevoke, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("tier revoke", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-invite' AND state='pending'`) != 1 {
			t.Fatal("tier denial revoked")
		}
	})

	t.Run("owner_cannot_manage_owner", func(t *testing.T) {
		m := usTestInvitationManager("us-owner", "admin", "member")
		if m.ManagesTier("owner") {
			t.Fatal("owner manager may manage owner tier")
		}
		i, scope := usInvitationPreparationFixture()
		if _, err := prepareUSInvitationCreate(i, scope, "us-created", usInvitationFixtureHash, usInvitationCreateInput{role: "owner"}); !errors.Is(err, ErrInvalid) {
			t.Fatal("owner-tier invitation was prepared", err)
		}
	})

	t.Run("issuer_mismatch", func(t *testing.T) {
		w, _ := usInvitationTestExecutor(t)
		m := usTestInvitationManager("us-other", "admin", "member")
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
			t.Fatal("foreign issuer", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
			t.Fatal("foreign issuer wrote")
		}
	})

	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("recheck_entry_%d", failAt), func(t *testing.T) {
			w, _ := usInvitationTestExecutor(t)
			m := usTestInvitationManagerFailing("us-owner", failAt, "admin", "member")
			cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
			turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
			out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
			if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("recheck denial", out, floor, err)
			}
			if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 ||
				usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
				t.Fatal("recheck denial wrote")
			}
		})
	}

	t.Run("recheck_release", func(t *testing.T) {
		// Probe the exact number of Recheck calls on the happy path, then fail
		// the last one so the composed prefix must be rolled back.
		w, probe := usInvitationTestExecutor(t)
		probeCmd, _ := usInvitationTestCreateCommand(t, "us-probe", "member", []string{"movies"})
		probeTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		if _, _, err := runUSInvitationDispatch(t.Context(), probeTurn, probe, usInvitationTestInvocation(t, probeCmd)); err != nil {
			t.Fatal("probe run", err)
		}
		if probe.calls < 3 {
			t.Fatal("probe rechecks", probe.calls)
		}
		w, _ = usInvitationTestExecutor(t)
		m := usTestInvitationManagerFailing("us-owner", probe.calls, "admin", "member")
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
			t.Fatal("release denial", out, floor, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
			usInvitationTestCount(t, w, `SELECT retained_count FROM security_command_owners WHERE id='security-owner'`) != 0 {
			t.Fatal("release denial left a prefix")
		}
	})
	// A replay denial lands before any retained payload is decoded: a corrupted
	// child would otherwise be an ErrResolveRequired, but Recheck refuses first.
	t.Run("recheck_before_payload_replay", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		inv := usInvitationTestInvocation(t, cmd)
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv); err != nil {
			t.Fatal("original", err)
		}
		wc008NativeExec(t, w, `UPDATE security_result_libraries SET library_id='drifted' WHERE cohort='invitation' AND ordinal=1`)
		denied := usTestInvitationManagerFailing("us-owner", 2, "admin", "member")
		replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, floor, err := runUSInvitationDispatch(t.Context(), replayTurn, denied, &usInvitationInvocation{request: inv.request})
		if !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
			t.Fatal("replay denial read the payload", out, floor, err)
		}
	})
}

// TestUSInvitationReceiptQuota proves both retained bounds refuse a fresh result
// before any source effect: 64 receipts and 1 MiB of variable bytes.
func TestUSInvitationReceiptQuota(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner string
	}{
		{"count", `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=64,retained_variable_bytes=1 WHERE id='security-owner'`},
		{"bytes", `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=1,retained_variable_bytes=1048576 WHERE id='security-owner'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, m := usInvitationTestExecutor(t)
			usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
			wc008NativeExec(t, w, tc.owner)
			cmd := usInvitationTestRevokeCommand(t, 1)
			cmd.identity.Sequence = 2
			var err error
			cmd.digest, err = usInvitationCommandDigest(cmd)
			if err != nil {
				t.Fatal(err)
			}
			turn := wc008NativeTurn(t, w, usInvitationRevoke, usInvitationTestDefinitions())
			out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("quota refusal", out, floor, err)
			}
			if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-invite' AND state='pending'`) != 1 ||
				usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
				t.Fatal("quota refusal wrote")
			}
		})
	}
}
