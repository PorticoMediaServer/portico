package writer

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func signInPolicyFixture() usSignInPolicyRequest {
	s := usProfileCommandFixture().Scope
	return usSignInPolicyRequest{identity: s.Identity, ownerID: s.Owner, issuer: s.Issuer.issuer(), original: usSignInPolicySnapshot{3, defaultUSSignInPolicy()}, expectedRevision: 3, values: defaultUSSignInPolicy()}
}
func TestUSSignInPolicyDigest(t *testing.T) {
	r := signInPolicyFixture()
	c, e := prepareUSSignInPolicy(&r)
	if e != nil {
		t.Fatal(e)
	}
	d, e := canonicalRequestDigest(usSignInPolicyReplace, 1, r.identity, r)
	if e != nil || d != c.digest {
		t.Fatal("complete typed digest", e)
	}
	mutations := []func(*usSignInPolicyRequest){
		func(r *usSignInPolicyRequest) { r.identity.Incarnation[0]++ },
		func(r *usSignInPolicyRequest) { r.identity.OperationID[0]++ },
		func(r *usSignInPolicyRequest) { r.identity.Sequence++ },
		func(r *usSignInPolicyRequest) { r.ownerID += "x" },
		func(r *usSignInPolicyRequest) { r.issuer.server += "x" },
		func(r *usSignInPolicyRequest) { r.issuer.account += "x" },
		func(r *usSignInPolicyRequest) { r.issuer.profile += "x" },
		func(r *usSignInPolicyRequest) { r.issuer.family += "x" },
		func(r *usSignInPolicyRequest) { r.issuer.memberIncarnation = strings.Repeat("9", 32) },
		func(r *usSignInPolicyRequest) { r.issuer.profileLifetime = strings.Repeat("9", 32) },
		func(r *usSignInPolicyRequest) { r.issuer.installation = strings.Repeat("9", 32) },
		func(r *usSignInPolicyRequest) {
			r.issuer.tokenDigest = usCredentialFixtureHash("different-sign-in-issuer")
		},
		func(r *usSignInPolicyRequest) { r.issuer.epoch++ },
		func(r *usSignInPolicyRequest) { r.issuer.familyGeneration++ },
		func(r *usSignInPolicyRequest) { r.issuer.authorityGeneration++ },
		func(r *usSignInPolicyRequest) { r.expectedRevision++; r.original.Revision++ },
		func(r *usSignInPolicyRequest) { r.original.Values.StaySignedInDays = 7 },
		func(r *usSignInPolicyRequest) { r.original.Values.PINRememberDays = 0 },
		func(r *usSignInPolicyRequest) { r.original.Values.PasswordAttempts = "strict" },
		func(r *usSignInPolicyRequest) { r.original.Values.PINAttempts = "relaxed" },
		func(r *usSignInPolicyRequest) { r.original.Values.PasswordMinLength = 16 },
		func(r *usSignInPolicyRequest) { r.original.Values.MemberResetCodeMinutes = 15 },
		func(r *usSignInPolicyRequest) { r.original.Values.RequireTwoStep = "everyone" },
		func(r *usSignInPolicyRequest) { r.original.Values.RememberAccounts = false },
		func(r *usSignInPolicyRequest) { r.original.Values.EndedSessionsDays = 365 },
		func(r *usSignInPolicyRequest) { r.values.StaySignedInDays = 7 },
		func(r *usSignInPolicyRequest) { r.values.PINRememberDays = 0 },
		func(r *usSignInPolicyRequest) { r.values.PasswordAttempts = "strict" },
		func(r *usSignInPolicyRequest) { r.values.PINAttempts = "relaxed" },
		func(r *usSignInPolicyRequest) { r.values.PasswordMinLength = 16 },
		func(r *usSignInPolicyRequest) { r.values.MemberResetCodeMinutes = 15 },
		func(r *usSignInPolicyRequest) { r.values.RequireTwoStep = "everyone" },
		func(r *usSignInPolicyRequest) { r.values.RememberAccounts = false },
		func(r *usSignInPolicyRequest) { r.values.EndedSessionsDays = 365 },
	}
	for i, mutate := range mutations {
		v := r
		mutate(&v)
		got, err := prepareUSSignInPolicy(&v)
		if err != nil || got.digest == c.digest {
			t.Fatalf("witness %d: %v", i, err)
		}
	}
	r.values.RememberAccounts = false
	r.original.Values.PINRememberDays = 0
	r.ownerID = "changed"
	if c.request.ownerID == r.ownerID || !c.request.values.RememberAccounts || c.request.original.Values.PINRememberDays == 0 {
		t.Fatal("input alias")
	}
	if _, err := c.checkedDigest(); err != nil {
		t.Fatal(err)
	}
	c.request.values.RememberAccounts = false
	if _, err := c.checkedDigest(); err == nil {
		t.Fatal("mutation accepted")
	}
	var nilCommand *usSignInPolicyCommand
	if _, err := nilCommand.checkedDigest(); err == nil {
		t.Fatal("nil accepted")
	}
}
func TestUSSignInPolicyValidation(t *testing.T) {
	if _, e := prepareUSSignInPolicy(nil); e == nil {
		t.Fatal("nil input")
	}
	mutations := []func(*usSignInPolicyRequest){
		func(r *usSignInPolicyRequest) { r.identity = CommandIdentity{} },
		func(r *usSignInPolicyRequest) { r.ownerID = "" },
		func(r *usSignInPolicyRequest) { r.issuer = usPolicyIssuer{} },
		func(r *usSignInPolicyRequest) { r.expectedRevision = 0 },
		func(r *usSignInPolicyRequest) { r.original.Revision++ },
		func(r *usSignInPolicyRequest) {
			r.original.Revision = math.MaxInt64
			r.expectedRevision = math.MaxInt64
		},
		func(r *usSignInPolicyRequest) { r.original.Values.StaySignedInDays = -1 },
		func(r *usSignInPolicyRequest) { r.original.Values.PINRememberDays = -1 },
		func(r *usSignInPolicyRequest) { r.original.Values.PasswordAttempts = "bad" },
		func(r *usSignInPolicyRequest) { r.original.Values.PINAttempts = "bad" },
		func(r *usSignInPolicyRequest) { r.original.Values.PasswordMinLength = -1 },
		func(r *usSignInPolicyRequest) { r.original.Values.MemberResetCodeMinutes = -1 },
		func(r *usSignInPolicyRequest) { r.original.Values.RequireTwoStep = "bad" },
		func(r *usSignInPolicyRequest) { r.original.Values.EndedSessionsDays = -1 },
		func(r *usSignInPolicyRequest) { r.values.StaySignedInDays = -1 },
		func(r *usSignInPolicyRequest) { r.values.PINRememberDays = -1 },
		func(r *usSignInPolicyRequest) { r.values.PasswordAttempts = "bad" },
		func(r *usSignInPolicyRequest) { r.values.PINAttempts = "bad" },
		func(r *usSignInPolicyRequest) { r.values.PasswordMinLength = -1 },
		func(r *usSignInPolicyRequest) { r.values.MemberResetCodeMinutes = -1 },
		func(r *usSignInPolicyRequest) { r.values.RequireTwoStep = "bad" },
		func(r *usSignInPolicyRequest) { r.values.EndedSessionsDays = -1 },
	}
	for i, m := range mutations {
		r := signInPolicyFixture()
		m(&r)
		if _, err := prepareUSSignInPolicy(&r); err == nil {
			t.Fatalf("bad input %d", i)
		}
	}
	r := signInPolicyFixture()
	r.expectedRevision = math.MaxInt64 - 1
	r.original.Revision = r.expectedRevision
	if _, err := prepareUSSignInPolicy(&r); err != nil {
		t.Fatal("representable next revision rejected", err)
	}
	d, err := Describe(usSignInPolicyReplace)
	if err != nil || d.ID != 544 || d.Durability != Full {
		t.Fatal("descriptor", err)
	}
}
func TestUSSignInPolicyRedaction(t *testing.T) {
	r := signInPolicyFixture()
	c, err := prepareUSSignInPolicy(&r)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{r, &r, r.original, &r.original, r.values, &r.values, *c, c} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%f", "%20.3s"} {
			if got := fmt.Sprintf(verb, v); !strings.Contains(got, "{redacted}") {
				t.Fatalf("%s leaked: %s", verb, got)
			}
		}
		if !strings.Contains(v.(fmt.Stringer).String(), "{redacted}") || !strings.Contains(v.(fmt.GoStringer).GoString(), "{redacted}") {
			t.Fatal("direct format")
		}
	}
}

func TestUSSignInPolicyExactEnums(t *testing.T) {
	options := map[string][]any{
		"StaySignedInDays": {7, 30, 90, 365}, "PINRememberDays": {0, 7, 30, 90},
		"PasswordAttempts": {"relaxed", "standard", "strict"}, "PINAttempts": {"relaxed", "standard", "strict"},
		"PasswordMinLength": {8, 10, 12, 16}, "MemberResetCodeMinutes": {15, 60, 1440},
		"RequireTwoStep": {"nobody", "administrators", "everyone"}, "RememberAccounts": {false, true}, "EndedSessionsDays": {30, 90, 365},
	}
	if reflect.TypeOf(defaultUSSignInPolicy()).NumField() != len(options) {
		t.Fatal("incomplete policy test")
	}
	for field, values := range options {
		for _, value := range values {
			r := signInPolicyFixture()
			for _, p := range []*usSignInPolicyValues{&r.values, &r.original.Values} {
				f := reflect.ValueOf(p).Elem().FieldByName(field)
				f.Set(reflect.ValueOf(value).Convert(f.Type()))
			}
			if _, err := prepareUSSignInPolicy(&r); err != nil {
				t.Fatalf("%s=%v: %v", field, value, err)
			}
		}
	}
}
