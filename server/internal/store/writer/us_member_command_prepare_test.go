package writer

import (
	"fmt"
	identitydigest "github.com/PorticoMediaServer/portico/server/internal/identity"
	"reflect"
	"strings"
	"testing"
)

// Enumerate every leaf of ORIGINAL inputs, including all issuer and identity
// fields. Each mutation must still pass the public preparation boundary.
func usCommandWitnessMutations(t *testing.T, original any, digest func(any) (RequestDigest, error)) {
	t.Helper()
	base, err := digest(original)
	if err != nil {
		t.Fatal(err)
	}
	var walk func(reflect.Value, []int, string)
	walk = func(v reflect.Value, path []int, label string) {
		if v.Kind() == reflect.Struct {
			for n := 0; n < v.NumField(); n++ {
				walk(v.Field(n), append(append([]int{}, path...), n), label+"/"+v.Type().Field(n).Name)
			}
			return
		}
		t.Run(label, func(t *testing.T) {
			changed := reflect.New(reflect.TypeOf(original)).Elem()
			changed.Set(reflect.ValueOf(original))
			leaf := changed.FieldByIndex(path)
			switch leaf.Kind() {
			case reflect.String:
				s := leaf.String()
				if s == "admin" {
					s = "member"
				} else if s == "blue" {
					s = "rose"
				} else if usIdentityDigest(s) {
					s = identitydigest.Digest(s + "different")
				} else if len(s) == 32 || len(s) == 64 {
					s = strings.Repeat("9", len(s))
				} else {
					s += "x"
				}
				leaf.SetString(s)
			case reflect.Int64:
				leaf.SetInt(leaf.Int() + 1)
			case reflect.Array:
				leaf.Index(0).SetUint(leaf.Index(0).Uint() + 1)
			default:
				t.Fatalf("uncovered witness %v", leaf.Type())
			}
			other, err := digest(changed.Interface())
			if err != nil {
				t.Fatal("mutation must remain valid", err)
			}
			if other == base {
				t.Fatal("witness omitted from digest")
			}
		})
	}
	walk(reflect.ValueOf(original), nil, "witness")
}

// Exported test-only fields permit reflection without unsafe access.
type usCommandIssuerInput struct {
	Server, Account, Profile, Family                              string
	MemberIncarnation, ProfileLifetime, Installation, TokenDigest string
	Epoch, FamilyGeneration, AuthorityGeneration                  int64
}

func usCommandIssuerFixture(i usPolicyIssuer) usCommandIssuerInput {
	return usCommandIssuerInput{i.server, i.account, i.profile, i.family, i.memberIncarnation, i.profileLifetime, i.installation, i.tokenDigest, i.epoch, i.familyGeneration, i.authorityGeneration}
}
func (i usCommandIssuerInput) issuer() usPolicyIssuer {
	return usPolicyIssuer{server: i.Server, account: i.Account, profile: i.Profile, family: i.Family, memberIncarnation: i.MemberIncarnation, profileLifetime: i.ProfileLifetime, installation: i.Installation, tokenDigest: i.TokenDigest, epoch: i.Epoch, familyGeneration: i.FamilyGeneration, authorityGeneration: i.AuthorityGeneration}
}

type usCommandTargetInput struct {
	Account, Incarnation string
	Revision, Epoch      int64
}

func (v usCommandTargetInput) target() usMemberTarget {
	return usMemberTarget{account: v.Account, incarnation: v.Incarnation, expectedRevision: v.Revision, expectedEpoch: v.Epoch}
}

type usMemberCommandInput struct {
	Identity CommandIdentity
	Owner    string
	Issuer   usCommandIssuerInput
	Target   usCommandTargetInput
}

func usMemberCommandFixture() usMemberCommandInput {
	i, q := usPolicyFixture()
	return usMemberCommandInput{i, q.ownerID, usCommandIssuerFixture(q.issuer), usCommandTargetInput{"target", strings.Repeat("e", 32), 1, 1}}
}

func TestUSMemberCommandCompleteWitnesses(t *testing.T) {
	for _, kind := range []CommandID{usMemberRoleChange, usMembershipDisable, usAccountEpochAdvance} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			usCommandWitnessMutations(t, usMemberCommandFixture(), func(raw any) (RequestDigest, error) {
				v := raw.(usMemberCommandInput)
				if kind == usAccountEpochAdvance {
					c, err := prepareUSAccountEpochCommand(v.Identity, v.Owner, v.Issuer.issuer(), v.Target.target())
					if err != nil {
						return RequestDigest{}, err
					}
					return c.digest, nil
				}
				role := ""
				if kind == usMemberRoleChange {
					role = "admin"
				}
				c, err := prepareUSMemberCommand(kind, v.Identity, v.Owner, v.Issuer.issuer(), usMemberRequest{target: v.Target.target(), role: role})
				if err != nil {
					return RequestDigest{}, err
				}
				return c.digest, nil
			})
		})
	}
	v := usMemberCommandFixture()
	a, _ := prepareUSMemberCommand(usMemberRoleChange, v.Identity, v.Owner, v.Issuer.issuer(), usMemberRequest{v.Target.target(), "admin"})
	b, _ := prepareUSMemberCommand(usMemberRoleChange, v.Identity, v.Owner, v.Issuer.issuer(), usMemberRequest{v.Target.target(), "member"})
	if a.digest == b.digest {
		t.Fatal("role omitted")
	}
}

func TestUSMemberCommandCanonicalAndValidation(t *testing.T) {
	v := usMemberCommandFixture()
	for _, kind := range []CommandID{usMemberRoleChange, usMembershipDisable} {
		role := ""
		if kind == usMemberRoleChange {
			role = "admin"
		}
		c, err := prepareUSMemberCommand(kind, v.Identity, v.Owner, v.Issuer.issuer(), usMemberRequest{v.Target.target(), role})
		if err != nil {
			t.Fatal(err)
		}
		// Independent assembly from original input, not c.request or a preparation.
		full := usMemberCommandRequest{ownerID: v.Owner, issuer: v.Issuer.issuer(), prepared: usMemberPrepared{kind: kind, request: usMemberRequest{target: usMemberTarget{account: v.Target.Account, incarnation: v.Target.Incarnation, expectedRevision: v.Target.Revision, expectedEpoch: v.Target.Epoch}, role: role}}}
		want, err := canonicalRequestDigest(kind, 1, v.Identity, full)
		if err != nil || want != c.digest {
			t.Fatal("incomplete canonical member request", err)
		}
		usCommandCheckRedaction(t, *c, c, c.request)
	}
	c, err := prepareUSAccountEpochCommand(v.Identity, v.Owner, v.Issuer.issuer(), v.Target.target())
	if err != nil {
		t.Fatal(err)
	}
	full := usAccountEpochCommandRequest{ownerID: v.Owner, issuer: v.Issuer.issuer(), prepared: usAccountEpochPrepared{target: usMemberTarget{account: v.Target.Account, incarnation: v.Target.Incarnation, expectedRevision: v.Target.Revision, expectedEpoch: v.Target.Epoch}}}
	want, err := canonicalRequestDigest(usAccountEpochAdvance, 1, v.Identity, full)
	if err != nil || want != c.digest {
		t.Fatal("incomplete canonical epoch request", err)
	}
	usCommandCheckRedaction(t, *c, c, c.request)
	for _, bad := range []usMemberCommandInput{{}, {Identity: v.Identity, Owner: v.Owner, Issuer: v.Issuer}} {
		if c, err := prepareUSMemberCommand(usMemberRoleChange, bad.Identity, bad.Owner, bad.Issuer.issuer(), usMemberRequest{bad.Target.target(), "admin"}); c != nil || err == nil {
			t.Fatal("invalid member accepted")
		}
		if c, err := prepareUSAccountEpochCommand(bad.Identity, bad.Owner, bad.Issuer.issuer(), bad.Target.target()); c != nil || err == nil {
			t.Fatal("invalid epoch accepted")
		}
	}
	if c, err := prepareUSMemberCommand(usMembershipDisable, v.Identity, v.Owner, v.Issuer.issuer(), usMemberRequest{v.Target.target(), "admin"}); c != nil || err == nil {
		t.Fatal("forged role accepted")
	}
	if c, err := prepareUSMemberCommand(usProfileCreate, v.Identity, v.Owner, v.Issuer.issuer(), usMemberRequest{v.Target.target(), "admin"}); c != nil || err == nil {
		t.Fatal("wrong kind accepted")
	}
}
func usCommandCheckRedaction(t *testing.T, values ...any) {
	t.Helper()
	for _, v := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%.2v"} {
			if out := fmt.Sprintf(verb, v); !strings.HasSuffix(out, "{redacted}") || strings.Count(out, "redacted") != 1 {
				t.Fatalf("unredacted %T %s", v, verb)
			}
		}
		if s := v.(fmt.Stringer).String(); !strings.HasSuffix(s, "{redacted}") {
			t.Fatal("String")
		}
		if s := v.(fmt.GoStringer).GoString(); !strings.HasSuffix(s, "{redacted}") {
			t.Fatal("GoString")
		}
	}
}
