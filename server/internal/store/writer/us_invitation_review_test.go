package writer

import (
	"bytes"
	"errors"
	"fmt"
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

	// The loop above mutates only the ENTRY scope, so the separate
	// `m.Issuer() != inv.scope.issuer` clause would survive its deletion. These
	// cases leave the entry scope and the frozen preparation untouched and move
	// ONLY the manager's current issuer: a family-only change (family is not an
	// owner predicate) and one stable-column change whose owner row is moved with
	// it. Without the manager clause both submissions create and retain a receipt.
	for _, test := range []struct {
		name   string
		change func(*usPolicyIssuer)
		owner  func(*testing.T, *executor)
	}{
		{"manager_family", func(i *usPolicyIssuer) { i.family += "-other" }, nil},
		{"manager_installation", func(i *usPolicyIssuer) { i.installation = strings.Repeat("f", 32) },
			func(t *testing.T, w *executor) {
				wc008NativeExec(t, w, `UPDATE security_command_owners SET issuer_installation_incarnation=? WHERE id='security-owner'`, strings.Repeat("f", 32))
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, m := usInvitationTestExecutor(t)
			cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
			inv := usInvitationTestInvocation(t, cmd)
			test.change(&m.issuer)
			if test.owner != nil {
				test.owner(t, w)
			}
			turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
			out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("manager current issuer accepted a fresh submission", out, floor, err)
			}
			if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 ||
				usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
				t.Fatal("manager current issuer mismatch wrote")
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

// B3: the retained owner is addressed by the CURRENT manager's six stable
// columns. A different stored installation/incarnation/member refuses replay
// and ACK, while the retained family is provenance only: a renewed family of
// the same installation and member incarnation still serves its original.
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

	// The retained family column is provenance, not a predicate: changing it
	// (or signing in under a NEW family id at the same installation/member
	// incarnation) still serves the retained digest.
	t.Run("new_family_serves", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		inv := usInvitationTestInvocation(t, cmd)
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		original, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
		if err != nil || original.Digest == (ResultDigest{}) {
			t.Fatal("original", err)
		}
		// New sign-in: a fresh family id and generation, same installation and
		// member incarnation. The owner row keeps the ORIGINAL family.
		m.issuer.family = "issuer-family-renewed"
		m.issuer.familyGeneration++
		m.issuer.tokenDigest = usCredentialFixtureHash("us-renewed-token")
		before := usInvitationTestCount(t, w, `SELECT total_changes()`)
		replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		replay, _, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: inv.request})
		if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed ||
			replay.Digest != original.Digest || replay.DTO.ID != "us-created" {
			t.Fatal("new family replay", replay, err)
		}
		if usInvitationTestCount(t, w, `SELECT total_changes()`) != before {
			t.Fatal("new family replay wrote")
		}
		// The retained row's own family string is equally irrelevant.
		wc008NativeExec(t, w, `UPDATE security_command_owners SET original_family_id='other-family' WHERE id='security-owner'`)
		replayTurn = wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		again, _, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: inv.request})
		if err != nil || again.Digest != original.Digest {
			t.Fatal("provenance family replay", again, err)
		}
	})

	// The bug: with the retained issuer as the predicate, another installation
	// or member incarnation of the same account could read the cohort. The
	// CURRENT manager issuer now owns the predicate.
	t.Run("foreign_manager_scope_refuses", func(t *testing.T) {
		for name, mutate := range map[string]func(*usPolicyIssuer){
			"server":             func(i *usPolicyIssuer) { i.server = "other-server" },
			"account":            func(i *usPolicyIssuer) { i.account = "us-other" },
			"member_incarnation": func(i *usPolicyIssuer) { i.memberIncarnation = strings.Repeat("d", 32) },
			"profile":            func(i *usPolicyIssuer) { i.profile = "other-profile" },
			"profile_lifetime":   func(i *usPolicyIssuer) { i.profileLifetime = strings.Repeat("e", 32) },
			"installation":       func(i *usPolicyIssuer) { i.installation = strings.Repeat("f", 32) },
		} {
			t.Run(name, func(t *testing.T) {
				w, m := usInvitationTestExecutor(t)
				cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
				inv := usInvitationTestInvocation(t, cmd)
				turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
				if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv); err != nil {
					t.Fatal("original", err)
				}
				mutate(&m.issuer)
				before := usInvitationTestCount(t, w, `SELECT total_changes()`)
				replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
				out, floor, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: inv.request})
				if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
					t.Fatal("foreign current issuer read the retained cohort", out, floor, err)
				}
				if usInvitationTestCount(t, w, `SELECT total_changes()`) != before {
					t.Fatal("foreign current issuer refusal wrote")
				}
			})
		}
	})
}

// A replay carrying a RE-PREPARED different issuer (same command identity, new
// installation) must classify as a conflicting request digest, not as an
// absent owner. This fails on the old code, which addressed the owner with the
// entry-supplied retained issuer and returned resolve-required instead.
func TestUSInvitationReplayRepreparedIssuerConflicts(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
	inv := usInvitationTestInvocation(t, cmd)
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv); err != nil {
		t.Fatal("original", err)
	}

	key := bytes.Repeat([]byte{7}, 32)
	code, hash, err := usInvitationLegacyLink(key, "us-created")
	if err != nil {
		t.Fatal(err)
	}
	i, scope := usInvitationPreparationFixture()
	scope.issuer.installation = strings.Repeat("f", 32)
	otherPrepared, err := prepareUSInvitationCreate(i, scope, "us-created", hash, usInvitationCreateInput{role: "member", libraries: []string{"movies"}})
	if err != nil {
		t.Fatal(err)
	}
	protected, err := prepareUSInvitationCode(otherPrepared, "invitation-key-id", key, bytes.Repeat([]byte{9}, 12), code)
	if err != nil {
		t.Fatal(err)
	}
	signIn, err := prepareUSInvitationSignInKind("local", 1)
	if err != nil {
		t.Fatal(err)
	}
	other := &usInvitationCommand{kind: usInvitationCreate, ownerID: scope.ownerID, actor: scope.issuer.account,
		identity: i, prepared: otherPrepared, signIn: signIn, protected: protected}
	other.digest, err = usInvitationCommandDigest(other)
	if err != nil {
		t.Fatal(err)
	}
	otherRequest, err := usInvitationReceiptForCommand(other)
	if err != nil {
		t.Fatal(err)
	}
	before := usInvitationTestCount(t, w, `SELECT total_changes()`)
	replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	out, floor, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: otherRequest})
	if !errors.Is(err, ErrConflict) || errors.Is(err, ErrResolveRequired) ||
		!reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("re-prepared issuer was not a request-digest conflict", out, floor, err)
	}
	if usInvitationTestCount(t, w, `SELECT total_changes()`) != before {
		t.Fatal("re-prepared issuer conflict wrote")
	}
}

// A replay after the CURRENT manager rotates its token digest, family,
// generations and epoch (the six stable owner columns are unchanged) still
// serves the retained original. Replay reads no issuer from the request, so
// rotating a request-side issuer would prove nothing: the manager's current
// issuer is the only current witness.
func TestUSInvitationReplayTokenRotation(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
	inv := usInvitationTestInvocation(t, cmd)
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	original, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
	if err != nil || original.Digest == (ResultDigest{}) {
		t.Fatal("original", err)
	}
	// Renewed sign-in for the same member/installation/profile: new family id
	// and generation, new token digest, epoch and authority generation. The owner
	// row keeps the ORIGINAL family.
	m.issuer.family = "issuer-family-renewed"
	m.issuer.tokenDigest = usCredentialFixtureHash("us-rotated-token")
	m.issuer.epoch++
	m.issuer.familyGeneration++
	m.issuer.authorityGeneration++
	before := usInvitationTestCount(t, w, `SELECT total_changes()`)
	replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	out, _, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: inv.request})
	if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed ||
		out.Digest != original.Digest || out.DTO.ID != "us-created" {
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
		if err != nil || out.DTO.ServerID != inv.scope.issuer.server {
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

// B-2: the DISCLOSED tier of a retained original is checked before any replay
// body is released. Replay and a no-input Resolve are the SAME dispatcher entry,
// so this proves it once instead of running the identical call twice. A manager
// that cannot manage the disclosed tier receives the disclosure-denied class
// with no Value, DTO, Code or write. The ACK entry is deliberately absent: it
// releases no body and has its own no-disclosure contract.
func TestUSInvitationReplayTierDisclosure(t *testing.T) {
	// Real authority tier maps: an owner manages admin and member invitations;
	// an admin manages member invitations only. The seam map can express what
	// the real authority cannot (an admin managing the admin tier); such a case
	// says so.
	ownerTiers := map[string]bool{"owner": true, "admin": true, "member": true}
	seamAdminTiers := map[string]bool{"admin": true, "member": true} // NOT real authority
	adminTiers := map[string]bool{"member": true}                    // real admin authority

	original := func(t *testing.T, role string, tiers map[string]bool) (*executor, *testInvitationManager, usInvitationReceiptRequest) {
		t.Helper()
		w, m := usInvitationTestExecutor(t)
		m.tiers = tiers
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", role, []string{"movies"})
		inv := usInvitationTestInvocation(t, cmd)
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
		if err != nil || out.Outcome.State != Applied || out.DTO.Role != role {
			t.Fatal("original invitation", role, out, err)
		}
		return w, m, inv.request
	}

	// Real owner authority: an owner manages the admin tier.
	t.Run("owner_replays_admin_served", func(t *testing.T) {
		w, m, request := original(t, "admin", ownerTiers)
		before := usInvitationTestCount(t, w, `SELECT total_changes()`)
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, &usInvitationInvocation{request: request})
		if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed || out.DTO.Role != "admin" || out.DTO.ID != "us-created" {
			t.Fatal("owner replay", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT total_changes()`) != before {
			t.Fatal("owner replay wrote")
		}
	})

	// Real admin authority: an admin manages the member tier.
	t.Run("admin_replays_member_served", func(t *testing.T) {
		w, m, request := original(t, "member", adminTiers)
		before := usInvitationTestCount(t, w, `SELECT total_changes()`)
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, &usInvitationInvocation{request: request})
		if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed || out.DTO.Role != "member" || out.DTO.ID != "us-created" {
			t.Fatal("admin replay", out, err)
		}
		if usInvitationTestCount(t, w, `SELECT total_changes()`) != before {
			t.Fatal("admin replay wrote")
		}
	})

	// SEAM MAP ONLY: the real authority cannot produce an admin manager that
	// manages the admin tier, but the seam map can. This keeps the served branch
	// covered for a manager that is not the owner.
	t.Run("demoted_admin_replays_admin_served", func(t *testing.T) {
		w, m, request := original(t, "admin", ownerTiers)
		m.tiers = seamAdminTiers
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, &usInvitationInvocation{request: request})
		if err != nil || out.Outcome.State != Applied || out.DTO.Role != "admin" {
			t.Fatal("demoted manager replay", out, err)
		}
	})

	// Real admin authority: an admin cannot manage the admin tier, so the
	// retained admin invitation is refused with no body and no write.
	t.Run("admin_refused_admin_tier", func(t *testing.T) {
		w, m, request := original(t, "admin", ownerTiers)
		m.tiers = adminTiers
		before := usInvitationTestCount(t, w, `SELECT total_changes()`)
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, &usInvitationInvocation{request: request})
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
			t.Fatal("admin replay of admin tier", out, floor, err)
		}
		if usInvitationTestCount(t, w, `SELECT total_changes()`) != before ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 {
			t.Fatal("disclosure refusal wrote or dropped the receipt")
		}
	})
}

// The dispatcher cross-checks the entry request owner and identity against the
// frozen preparation, not only against each other and the digest.
func TestUSInvitationPreparedCrossCheck(t *testing.T) {
	t.Run("owner_id", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		inv := usInvitationTestInvocation(t, cmd)
		cmd.prepared.scope.ownerID = "other-owner"
		var err error
		cmd.digest, err = usInvitationCommandDigest(cmd)
		if err != nil {
			t.Fatal(err)
		}
		inv.request.reference.RequestDigest = cmd.digest
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
		if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
			t.Fatal("owner cross-check", out, floor, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations`) != 0 ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
			t.Fatal("owner cross-check wrote")
		}
	})

	t.Run("identity", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
		inv := usInvitationTestInvocation(t, cmd)
		cmd.identity.OperationID = [16]byte{3}
		var err error
		cmd.digest, err = usInvitationCommandDigest(cmd)
		if err != nil {
			t.Fatal(err)
		}
		inv.request.reference.Identity = cmd.identity
		inv.request.reference.RequestDigest = cmd.digest
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
		if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
			t.Fatal("identity cross-check", out, floor, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations`) != 0 ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
			t.Fatal("identity cross-check wrote")
		}
	})
}

// Legacy order: libraries are proved before live capacity, so a missing library
// is a library refusal even when the server already holds 200 live offers.
func TestUSInvitationAdmissionLibraryBeforeCapacity(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	for n := 0; n < 200; n++ {
		wc008NativeExec(t, w, `INSERT INTO access_invitations(id,code_hash,role,allowed_libraries,state,created_ms,expires_ms,created_by,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating)
VALUES(?,?,'member','[]','pending',?,?,'us-owner',1,'','inherit',0,1,1,'')`, fmt.Sprintf("us-live-%03d", n), fmt.Sprintf("%064d", n), usInvitationTestMS()-500, usInvitationTestMS()+1000)
	}
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"missing"})
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
	if !errors.Is(err, ErrInvitationLibrary) || errors.Is(err, ErrInvitationCapacity) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) {
		t.Fatal("admission order", out, err)
	}
	if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 0 {
		t.Fatal("library refusal wrote")
	}
}

// The declared Recheck budget matches the observed fresh-dispatch call count.
func TestUSInvitationRecheckBudget(t *testing.T) {
	if usInvitationDispatchRechecks != 15 {
		t.Fatal("declared recheck count changed without a funding review", usInvitationDispatchRechecks)
	}
	w, m := usInvitationTestExecutor(t)
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	if _, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd)); err != nil {
		t.Fatal("fresh dispatch", err)
	}
	if int64(m.checks) != usInvitationDispatchRechecks {
		t.Fatal("fresh dispatch recheck count", m.checks, usInvitationDispatchRechecks)
	}
}
