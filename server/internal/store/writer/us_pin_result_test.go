package writer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usPINResultFixture(t *testing.T) (CommandIdentity, RequestDigest, usPINSavedResult) {
	t.Helper()
	identity, policy := usPolicyFixture()
	pin := usPINTestRequest()
	pin.account, pin.profile, pin.profileLifetime = policy.issuer.account, policy.issuer.profile, policy.issuer.profileLifetime
	command, err := prepareUSPINCommand(usPINSet, identity, policy.ownerID, policy.issuer, policy.memberIncarnation, pin)
	if err != nil {
		t.Fatal(err)
	}
	return identity, command.digest, usPINSavedResult{kind: usPINSet, owner: policy.ownerID, account: pin.account,
		member: policy.memberIncarnation, profile: pin.profile, lifetime: pin.profileLifetime,
		revision: 2, pinRevision: 2, appliedMS: 1000, pinRequired: true}
}

func TestUSPINResultBindsEveryScalar(t *testing.T) {
	i, request, v := usPINResultFixture(t)
	original, err := usPINResultDigest(i, request, v)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*usPINSavedResult){
		"revision":       func(v *usPINSavedResult) { v.revision++ },
		"pin_revision":   func(v *usPINSavedResult) { v.pinRevision++ },
		"applied_ms":     func(v *usPINSavedResult) { v.appliedMS++ },
		"pin_required":   func(v *usPINSavedResult) { v.pinRequired = false },
		"owner":          func(v *usPINSavedResult) { v.owner = "other-owner" },
		"account":        func(v *usPINSavedResult) { v.account = "other-account" },
		"member":         func(v *usPINSavedResult) { v.member = strings.Repeat("f", 32) },
		"profile":        func(v *usPINSavedResult) { v.profile = "other-profile" },
		"lifetime":       func(v *usPINSavedResult) { v.lifetime = strings.Repeat("a", 32) },
		"purpose_set":    func(v *usPINSavedResult) { v.kind = usPINClear },
		"identity":       func(*usPINSavedResult) {},
		"request_digest": func(*usPINSavedResult) {},
	} {
		t.Run(name, func(t *testing.T) {
			switch name {
			case "identity":
				other := i
				other.OperationID[1] = 1
				actual, err := usPINResultDigest(other, request, v)
				if err != nil || actual == original {
					t.Fatal("identity omitted", err)
				}
				return
			case "request_digest":
				other := request
				other[1] = 1
				actual, err := usPINResultDigest(i, other, v)
				if err != nil || actual == original {
					t.Fatal("request digest omitted", err)
				}
				return
			}
			x := v
			change(&x)
			actual, err := usPINResultDigest(i, request, x)
			if err != nil || actual == original {
				t.Fatal("native result field omitted", err)
			}
		})
	}
	if bytes, err := usPINResultVariableBytes(v); err != nil || bytes != 204 {
		t.Fatal("exact receipt charge", bytes, err)
	}
	// A clear result must never claim a PIN, and a set result must never omit it.
	clear := v
	clear.kind, clear.pinRequired = usPINClear, false
	if _, err := usPINResultDigest(i, request, clear); err != nil {
		t.Fatal("valid clear result refused", err)
	}
	clear.pinRequired = true
	if _, err := usPINResultDigest(i, request, clear); err == nil {
		t.Fatal("clear claiming a PIN accepted")
	}
	// Both revisions always advance at least once.
	for _, edit := range []func(*usPINSavedResult){func(v *usPINSavedResult) { v.revision = 1 }, func(v *usPINSavedResult) { v.pinRevision = 1 }} {
		x := v
		edit(&x)
		if _, err := usPINResultDigest(i, request, x); err == nil {
			t.Fatal("unbumped revision accepted")
		}
	}
	if _, err := usPINResultDigest(CommandIdentity{Sequence: 1}, request, v); err == nil {
		t.Fatal("zero incarnation accepted")
	}
	if _, err := usPINResultDigest(i, RequestDigest{}, v); err == nil {
		t.Fatal("zero request digest accepted")
	}
	unregistered := v
	unregistered.kind = 999
	if _, err := usPINResultDigest(i, request, unregistered); err == nil {
		t.Fatal("unregistered purpose accepted")
	}
}

// The image charge is the exact sum of every variable string the DTO carries:
// the profile ID is repeated in Restrictions and Avatar, and the avatar URL
// carries the account. A mutation of any one string changes the charge.
func TestUSPINProfileImageOctetsCountEveryString(t *testing.T) {
	base := usMemberProfileImageFixture()
	original, err := usMemberProfileImageOctets(base)
	if err != nil {
		t.Fatal(err)
	}
	if original <= 0 {
		t.Fatal(original)
	}
	clone := base.Clone()
	clone.AllowedLibraries[0] += "x"
	if n, err := usMemberProfileImageOctets(clone); err != nil || n != original+1 {
		t.Fatal("library octet", n, err)
	}
	clone = base.Clone()
	clone.Restrictions.BlockedLabels[0] += "x"
	if n, err := usMemberProfileImageOctets(clone); err != nil || n != original+1 {
		t.Fatal("label octet", n, err)
	}
	clone = base.Clone()
	clone.Avatar.URL += "x"
	if n, err := usMemberProfileImageOctets(clone); err != nil || n != original+1 {
		t.Fatal("avatar url octet", n, err)
	}
	clone = base.Clone()
	clone.Avatar = nil
	withoutAvatar := original - int64(len(base.Avatar.ProfileID)+len(base.Avatar.UpdatedAt)+len(base.Avatar.SourceMIME)+len(base.Avatar.URL))
	if n, err := usMemberProfileImageOctets(clone); err != nil || n != withoutAvatar {
		t.Fatal("avatar presence", n, err)
	}
	// URLs are the product's exact avatar route, never a caller path.
	if got := usPINProfileURL("account", "profile", 7); got != "/v1/admin/access/members/account/profiles/profile/avatar?v=7" {
		t.Fatal(got)
	}
}

func TestUSPINResultRedaction(t *testing.T) {
	image := usPINPublicImage{Profile: &USMemberProfileImage{Name: "secret-name"}}
	out := usPINDispatchResult{Value: usPINSavedResult{owner: "secret-owner"}}
	for _, value := range []any{image, out, usPINOutcome{}} {
		for _, rendered := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(rendered, "secret") {
				t.Fatal("dispatch value leaked", rendered)
			}
		}
		if !strings.Contains(fmt.Sprintf("%v", value), "redacted") {
			t.Fatal("dispatch value is not redacted", value)
		}
	}
}

// The closed statement inventory stays hot, captured and waiver-free. The
// INTEGER octet points are registered with the plan package, so Capture proves
// their exact SQL/definition and native EQP key before any use.
func TestUSPINStatementCapture(t *testing.T) {
	defs := append([]stmt.Definition(nil), usPINImageDefinitions()...)
	defs = append(defs, usPINReceiptDefinitions()...)
	defs = append(defs, usPINEffectDefinitions()...)
	if len(defs) == 0 {
		t.Fatal("empty PIN statement inventory")
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) == 0 {
			t.Fatal("PIN statement must stay hot, captured and waiver-free", d.ID)
		}
	}
	w := wc008NativeExecutor(t)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "us_pin_result.go", "us_pin_receipt_statements.go", "us_pin_statements.go")
	if len(registry.Manifest()) != len(defs) {
		t.Fatal("registry did not bind the complete PIN inventory")
	}
}
