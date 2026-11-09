package writer

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// usInvitationReviewCreateCommand builds a create command with an explicit
// sign-in-kind witness. The local default helper is used elsewhere; Portico
// sign-in needs its own frozen kind/revision.
func usInvitationReviewCreateCommand(t *testing.T, id string, signIn usInvitationSignInKind, libraries []string) (*usInvitationCommand, string) {
	t.Helper()
	key := bytes.Repeat([]byte{7}, 32)
	code, hash, err := usInvitationLegacyLink(key, id)
	if err != nil {
		t.Fatal(err)
	}
	i, scope := usInvitationPreparationFixture()
	p, err := prepareUSInvitationCreate(i, scope, id, hash, usInvitationCreateInput{role: "member", libraries: libraries})
	if err != nil {
		t.Fatal(err)
	}
	protected, err := prepareUSInvitationCode(p, "invitation-key-id", key, bytes.Repeat([]byte{9}, 12), code)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &usInvitationCommand{kind: usInvitationCreate, ownerID: scope.ownerID, actor: scope.issuer.account,
		identity: i, prepared: p, signIn: signIn, protected: protected}
	cmd.digest, err = usInvitationCommandDigest(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return cmd, code
}

// B1: the "all libraries" default [] must create, edit and revoke. The receipt
// charge equals header+typed exactly when the cohort is empty, so the old >=
// guard refused every [] invitation before any write.
func TestUSInvitationAllLibrariesCohort(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		cmd, _ := usInvitationTestCreateCommand(t, "us-all", "member", []string{})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if err != nil || out.Outcome.State != Applied || out.DTO.State != "pending" ||
			out.DTO.AllowedLibraries == nil || len(out.DTO.AllowedLibraries) != 0 {
			t.Fatal("all-libraries create", out, err)
		}
		var stored string
		if err := w.db.QueryRowContext(t.Context(), `SELECT allowed_libraries FROM access_invitations WHERE id='us-all'`).Scan(&stored); err != nil || stored != "[]" {
			t.Fatal("stored all-libraries create", stored, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM security_result_libraries WHERE cohort='invitation'`) != 0 {
			t.Fatal("empty cohort retained children")
		}
	})
	t.Run("edit", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		edit := usInvitationTestEditCommand(t, 1, "member", []string{}, usInvitationTestMS()+2000)
		turn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
		editOut, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, edit))
		if err != nil || editOut.Outcome.State != Applied || editOut.DTO.AllowedLibraries == nil || len(editOut.DTO.AllowedLibraries) != 0 {
			t.Fatal("all-libraries edit", editOut, err)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		revoke := usInvitationTestRevokeCommand(t, 1)
		turn := wc008NativeTurn(t, w, usInvitationRevoke, usInvitationTestDefinitions())
		revokeOut, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, revoke))
		if err != nil || revokeOut.Outcome.State != Applied || revokeOut.DTO.State != "revoked" {
			t.Fatal("all-libraries revoke", revokeOut, err)
		}
	})
}

// B2: edit now checks the requested libraries exactly like create. [] is the
// all-libraries default; a missing id refuses before any source write.
func TestUSInvitationEditLibraryExistence(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)

	cmd := usInvitationTestEditCommand(t, 1, "member", []string{"missing"}, usInvitationTestMS()+2000)
	turn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
	out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
	if !errors.Is(err, ErrInvitationLibrary) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("edit accepted a missing library", out, floor, err)
	}
	var libraries, state string
	var revision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT allowed_libraries,state,revision FROM access_invitations WHERE id='us-invite'`).Scan(&libraries, &state, &revision); err != nil || libraries != `["movies"]` || state != "pending" || revision != 1 {
		t.Fatal("edit library refusal wrote", libraries, state, revision, err)
	}
	if usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("edit library refusal retained a receipt")
	}
}

// B6: legacy order is not-found, tier, state/expiry, then revision. A manager
// that cannot manage the row's tier must see Forbidden even on an expired or
// stale row, never a revision/expired detail.
func TestUSInvitationErrorOrder(t *testing.T) {
	w, _ := usInvitationTestExecutor(t)
	lower := usTestInvitationManager("us-owner", "member")
	usInvitationTestSeedInvitation(t, w, "admin", "pending", 2, usInvitationTestMS()-500)
	stale := usInvitationTestEditCommand(t, 1, "member", []string{"movies"}, usInvitationTestMS()+2000)

	turn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
	out, floor, err := runUSInvitationDispatch(t.Context(), turn, lower, usInvitationTestInvocation(t, stale))
	if !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrInvitationRevision) || errors.Is(err, ErrInvitationExpired) ||
		!reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("lower tier learned source detail", out, floor, err)
	}

	// Control: a manager that can manage the tier still learns expired before
	// revision for an expired, stale edit.
	w, m := usInvitationTestExecutor(t)
	usInvitationTestSeedInvitation(t, w, "admin", "pending", 2, usInvitationTestMS()-500)
	cmd := usInvitationTestEditCommand(t, 1, "admin", []string{"movies"}, usInvitationTestMS()+2000)
	turn = wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
	if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd)); !errors.Is(err, ErrInvitationExpired) {
		t.Fatal("control order", err)
	}

	// Pending, unexpired, stale revision reaches revision.
	w, m = usInvitationTestExecutor(t)
	usInvitationTestSeedInvitation(t, w, "admin", "pending", 2, usInvitationTestMS()+1000)
	cmd = usInvitationTestEditCommand(t, 1, "admin", []string{"movies"}, usInvitationTestMS()+2000)
	turn = wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
	if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd)); !errors.Is(err, ErrInvitationRevision) {
		t.Fatal("revision order", err)
	}
}

// The CURRENT trusted entry scope must equal the frozen preparation in all 11
// issuer fields on a fresh submission. One changed field is a conflict with no
// source row and no receipt.
func TestUSInvitationFreshIssuerBinding(t *testing.T) {
	changes := []struct {
		name   string
		change func(*usPolicyIssuer)
	}{
		{"server", func(i *usPolicyIssuer) { i.server += "-other" }},
		{"account", func(i *usPolicyIssuer) { i.account += "-other" }},
		{"profile", func(i *usPolicyIssuer) { i.profile += "-other" }},
		{"family", func(i *usPolicyIssuer) { i.family += "-other" }},
		{"member_incarnation", func(i *usPolicyIssuer) { i.memberIncarnation = strings.Repeat("d", 32) }},
		{"profile_lifetime", func(i *usPolicyIssuer) { i.profileLifetime = strings.Repeat("e", 32) }},
		{"installation", func(i *usPolicyIssuer) { i.installation = strings.Repeat("f", 32) }},
		{"token_digest", func(i *usPolicyIssuer) { i.tokenDigest = usCredentialFixtureHash("us-other-token") }},
		{"epoch", func(i *usPolicyIssuer) { i.epoch++ }},
		{"family_generation", func(i *usPolicyIssuer) { i.familyGeneration++ }},
		{"authority_generation", func(i *usPolicyIssuer) { i.authorityGeneration++ }},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			w, m := usInvitationTestExecutor(t)
			cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
			inv := usInvitationTestInvocation(t, cmd)
			test.change(&inv.scope.issuer)
			turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
			out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("fresh submission accepted a foreign current scope", out, floor, err)
			}
			if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 ||
				usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
				t.Fatal("foreign current scope wrote")
			}
		})
	}
}

// A retired (already acknowledged) original reports Retired with no body: the
// product does not receive a 1970-dated DTO for a retained-but-retired receipt.
func TestUSInvitationRetiredNoBody(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
	inv := usInvitationTestInvocation(t, cmd)
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv); err != nil {
		t.Fatal("original", err)
	}
	ackTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	if _, _, err := runUSInvitationDispatch(t.Context(), ackTurn, m, &usInvitationInvocation{request: inv.request, ack: true}); err != nil {
		t.Fatal("ack", err)
	}
	replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	out, _, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: inv.request})
	if err != nil || out.Outcome.State != ResultRetired || !reflect.DeepEqual(out.DTO, usInvitationPublicResult{}) ||
		!reflect.DeepEqual(out.Value, usInvitationSavedResult{}) {
		t.Fatal("retired projected a body", out, err)
	}
}

// B3: the retained owner is addressed by every stable stored column. One
// mutation per column must refuse replay and ACK.
func TestUSInvitationOwnerScopeBinding(t *testing.T) {
	controls := []struct {
		name   string
		update string
	}{
		{"server", `UPDATE security_command_owners SET server_id='other-server'`},
		{"member_incarnation", `UPDATE security_command_owners SET issuer_member_incarnation=? WHERE id='security-owner'`},
		{"profile", `UPDATE security_command_owners SET issuer_profile_id='other-profile'`},
		{"profile_lifetime", `UPDATE security_command_owners SET issuer_profile_lifetime=? WHERE id='security-owner'`},
		{"installation", `UPDATE security_command_owners SET issuer_installation_incarnation=? WHERE id='security-owner'`},
		{"family", `UPDATE security_command_owners SET original_family_id='other-family'`},
		{"ingress", `UPDATE security_command_owners SET ingress='system',issuer_account_id=NULL,issuer_member_incarnation=NULL,issuer_profile_id=NULL,issuer_profile_lifetime=NULL,issuer_installation_incarnation=NULL,original_family_id=NULL`},
	}
	for _, control := range controls {
		t.Run(control.name, func(t *testing.T) {
			w, m := usInvitationTestExecutor(t)
			cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
			inv := usInvitationTestInvocation(t, cmd)
			turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
			if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv); err != nil {
				t.Fatal("original", err)
			}
			switch control.name {
			case "member_incarnation":
				wc008NativeExec(t, w, control.update, strings.Repeat("d", 32))
			case "profile_lifetime":
				wc008NativeExec(t, w, control.update, strings.Repeat("e", 32))
			case "installation":
				wc008NativeExec(t, w, control.update, strings.Repeat("f", 32))
			default:
				wc008NativeExec(t, w, control.update)
			}
			before := usInvitationTestCount(t, w, `SELECT total_changes()`)
			replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
			out, floor, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: inv.request})
			if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("replay accepted a foreign owner column", out, floor, err)
			}
			ackTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
			if ack, ackFloor, err := runUSInvitationDispatch(t.Context(), ackTurn, m, &usInvitationInvocation{request: inv.request, ack: true}); err == nil || ackFloor.AcknowledgedThrough != 0 || !reflect.DeepEqual(ack, usInvitationDispatchResult{}) {
				t.Fatal("ack accepted a foreign owner column", ack, ackFloor, err)
			}
			if usInvitationTestCount(t, w, `SELECT total_changes()`) != before {
				t.Fatal("foreign owner column refusal wrote")
			}
		})
	}
}

// The six stable owner columns still resolve when the CURRENT session rotates
// token/epoch/generations (same family). The saved original is returned with no
// new effect.
func TestUSInvitationReplayTokenRotation(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
	inv := usInvitationTestInvocation(t, cmd)
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv); err != nil {
		t.Fatal("original", err)
	}
	rotated := inv.request
	rotated.issuer.tokenDigest = usCredentialFixtureHash("us-rotated-token")
	rotated.issuer.epoch++
	rotated.issuer.familyGeneration++
	rotated.issuer.authorityGeneration++
	before := usInvitationTestCount(t, w, `SELECT total_changes()`)
	replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	out, _, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: rotated,
		scope: usInvitationInvocationScope{issuer: rotated.issuer}})
	if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed || out.DTO.ID != "us-created" {
		t.Fatal("rotation replay", out, err)
	}
	if usInvitationTestCount(t, w, `SELECT total_changes()`) != before ||
		usInvitationTestCount(t, w, `SELECT retained_count FROM security_command_owners WHERE id='security-owner'`) != 1 {
		t.Fatal("rotation replay wrote")
	}
}

// B4: the sealed Code is carried as the protected type and its manifest is
// proved before the effect. A same-length body sealed for another invitation is
// refused with no source row and no receipt.
func TestUSInvitationProtectedCodeBinding(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	key := bytes.Repeat([]byte{7}, 32)
	otherCode, otherHash, err := usInvitationLegacyLink(key, "us-other")
	if err != nil {
		t.Fatal(err)
	}
	i, scope := usInvitationPreparationFixture()
	otherPrepared, err := prepareUSInvitationCreate(i, scope, "us-other", otherHash, usInvitationCreateInput{role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	otherProtected, err := prepareUSInvitationCode(otherPrepared, "invitation-key-id", key, bytes.Repeat([]byte{9}, 12), otherCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherProtected.body) != usInvitationLegacyCodeLen+16 {
		t.Fatal("fixture body width")
	}

	_, hash, err := usInvitationLegacyLink(key, "us-created")
	if err != nil {
		t.Fatal(err)
	}
	target, err := prepareUSInvitationCreate(i, scope, "us-created", hash, usInvitationCreateInput{role: "member", libraries: []string{"movies"}})
	if err != nil {
		t.Fatal(err)
	}
	signIn, err := prepareUSInvitationSignInKind("local", 1)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &usInvitationCommand{kind: usInvitationCreate, ownerID: scope.ownerID, actor: scope.issuer.account,
		identity: i, prepared: target, signIn: signIn, protected: otherProtected}
	cmd.digest, err = usInvitationCommandDigest(cmd)
	if err != nil {
		t.Fatal(err)
	}
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("foreign sealed body accepted", out, floor, err)
	}
	if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 ||
		usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("foreign sealed body wrote")
	}
}

// B5: serverId is projected only on a FRESH LOCAL create; edit, revoke and
// every replay carry none.
func TestUSInvitationServerIDProjection(t *testing.T) {
	t.Run("local_create", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		inv := usInvitationTestInvocation(t, cmd)
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
		if err != nil || out.DTO.ServerID != inv.request.issuer.server {
			t.Fatal("local create serverId", out.DTO.ServerID, err)
		}
		replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		replay, _, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: inv.request})
		if err != nil || replay.DTO.ServerID != "" {
			t.Fatal("replay serverId", replay.DTO.ServerID, err)
		}
	})
	t.Run("portico_create", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		wc008NativeExec(t, w, `UPDATE identity_state SET sign_in_kind='portico'`)
		signIn, err := prepareUSInvitationSignInKind("portico", 1)
		if err != nil {
			t.Fatal(err)
		}
		cmd, _ := usInvitationReviewCreateCommand(t, "us-portico", signIn, []string{"movies"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if err != nil || out.Outcome.State != Applied || out.DTO.ServerID != "" {
			t.Fatal("portico create serverId", out.DTO.ServerID, err)
		}
	})
	t.Run("edit", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		edit := usInvitationTestEditCommand(t, 1, "member", []string{"movies"}, usInvitationTestMS()+2000)
		turn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, edit))
		if err != nil || out.DTO.ServerID != "" {
			t.Fatal("edit serverId", out.DTO.ServerID, err)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		revoke := usInvitationTestRevokeCommand(t, 1)
		turn := wc008NativeTurn(t, w, usInvitationRevoke, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, revoke))
		if err != nil || out.DTO.ServerID != "" {
			t.Fatal("revoke serverId", out.DTO.ServerID, err)
		}
	})
}

// The 19 source/receipt statements stay hot, captured, waiver-free and
// grammar-checked. This is a keyed SEARCH capture only; it records no golden.
func TestUSInvitationStatementCapture(t *testing.T) {
	defs := append([]stmt.Definition(nil), usInvitationSourceDefinitions()...)
	defs = append(defs, usInvitationReceiptDefinitions()...)
	if len(defs) != 19 {
		t.Fatal("invitation statement inventory changed", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) == 0 {
			t.Fatal("invitation statement must stay hot, captured and waiver-free", d.ID)
		}
		if strings.Contains(d.SQL, "octet_length(") || strings.Contains(d.SQL, "count(") || strings.Contains(d.SQL, "EXISTS(") {
			continue
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w := wc008NativeExecutor(t)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "us_invitation_snapshot.go", "us_invitation_receipt_statements.go")
	if len(registry.Manifest()) != len(defs) {
		t.Fatal("registry did not bind the complete invitation inventory")
	}
}
