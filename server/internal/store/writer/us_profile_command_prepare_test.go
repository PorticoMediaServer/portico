package writer

import "testing"

type usProfileCommandInput struct {
	Scope             usMemberCommandInput
	Profile, Lifetime string
	Revision          int64
	Name, Art         string
}

func usProfileCommandFixture() usProfileCommandInput {
	v := usProfileFixtureTarget()
	return usProfileCommandInput{usMemberCommandFixture(), v.profile, v.lifetime, v.revision, "Child", "blue"}
}
func (v usProfileCommandInput) target() usProfileTarget {
	return usProfileTarget{member: v.Scope.Target.target(), profile: v.Profile, lifetime: v.Lifetime, revision: v.Revision}
}
func TestUSProfileCommandCompleteWitnesses(t *testing.T) {
	usCommandWitnessMutations(t, usProfileCommandFixture(), func(raw any) (RequestDigest, error) {
		v := raw.(usProfileCommandInput)
		s := v.Scope
		c, err := prepareUSProfileMetadataCommand(s.Identity, s.Owner, s.Issuer.issuer(), v.target(), &v.Name, &v.Art)
		if err != nil {
			return RequestDigest{}, err
		}
		return c.digest, nil
	})
	// Create's revision is fixed at one; all other original inputs are variable.
	type createInput struct {
		Scope                        usMemberCommandInput
		Profile, Lifetime, Name, Art string
	}
	v := usProfileCommandFixture()
	usCommandWitnessMutations(t, createInput{v.Scope, v.Profile, v.Lifetime, v.Name, v.Art}, func(raw any) (RequestDigest, error) {
		v := raw.(createInput)
		s := v.Scope
		c, err := prepareUSProfileCreateCommand(s.Identity, s.Owner, s.Issuer.issuer(), s.Target.target(), v.Profile, v.Lifetime, v.Name, v.Art)
		if err != nil {
			return RequestDigest{}, err
		}
		return c.digest, nil
	})
}
func TestUSProfileCommandCanonicalAndOptionalFields(t *testing.T) {
	v := usProfileCommandFixture()
	s := v.Scope
	seen := map[RequestDigest]bool{}
	for _, flags := range [][2]bool{{true, true}, {false, true}, {true, false}} {
		var name, art *string
		if flags[0] {
			name = &v.Name
		}
		if flags[1] {
			art = &v.Art
		}
		c, err := prepareUSProfileMetadataCommand(s.Identity, s.Owner, s.Issuer.issuer(), v.target(), name, art)
		if err != nil {
			t.Fatal(err)
		}
		p := usProfilePrepared{kind: usProfileMetadataEdit, target: usProfileTarget{member: usMemberTarget{account: s.Target.Account, incarnation: s.Target.Incarnation, expectedRevision: s.Target.Revision, expectedEpoch: s.Target.Epoch}, profile: v.Profile, lifetime: v.Lifetime, revision: v.Revision}, setName: flags[0], setArt: flags[1]}
		if flags[0] {
			p.name = v.Name
		}
		if flags[1] {
			p.art = v.Art
		}
		full := usProfileCommandRequest{ownerID: s.Owner, issuer: s.Issuer.issuer(), prepared: p}
		want, err := canonicalRequestDigest(usProfileMetadataEdit, 1, s.Identity, full)
		if err != nil || want != c.digest || seen[c.digest] {
			t.Fatal("optional field or canonical mismatch", err)
		}
		seen[c.digest] = true
		usCommandCheckRedaction(t, *c, c, c.request)
	}
	c, err := prepareUSProfileCreateCommand(s.Identity, s.Owner, s.Issuer.issuer(), s.Target.target(), v.Profile, v.Lifetime, v.Name, "")
	if err != nil {
		t.Fatal(err)
	}
	full := usProfileCommandRequest{ownerID: s.Owner, issuer: s.Issuer.issuer(), prepared: usProfilePrepared{kind: usProfileCreate, target: usProfileTarget{member: usMemberTarget{account: s.Target.Account, incarnation: s.Target.Incarnation, expectedRevision: s.Target.Revision, expectedEpoch: s.Target.Epoch}, profile: v.Profile, lifetime: v.Lifetime, revision: 1}, name: v.Name, art: "blue", setName: true, setArt: true}}
	want, err := canonicalRequestDigest(usProfileCreate, 1, s.Identity, full)
	if err != nil || want != c.digest || seen[c.digest] {
		t.Fatal("create canonical mismatch", err)
	}
	name, art := v.Name, v.Art
	frozen, err := prepareUSProfileMetadataCommand(s.Identity, s.Owner, s.Issuer.issuer(), v.target(), &name, &art)
	if err != nil {
		t.Fatal(err)
	}
	before := frozen.digest
	name, art = "changed", "rose"
	again, err := canonicalRequestDigest(usProfileMetadataEdit, 1, s.Identity, frozen.request)
	if err != nil || again != before || frozen.request.prepared.name != v.Name || frozen.request.prepared.art != v.Art {
		t.Fatal("caller mutation escaped", err)
	}
}
func TestUSProfileCommandRejectsForgedPreparation(t *testing.T) {
	v := usProfileCommandFixture()
	s := v.Scope
	p := usProfilePrepared{kind: usProfileMetadataEdit, target: v.target(), name: v.Name, art: v.Art, setName: true, setArt: true}
	for _, mutate := range []func(*usProfilePrepared){
		func(p *usProfilePrepared) { p.kind = usMemberRoleChange },
		func(p *usProfilePrepared) { p.setName = false },
		func(p *usProfilePrepared) { p.setArt = false },
		func(p *usProfilePrepared) { p.name = " Child " },
		func(p *usProfilePrepared) { p.art = "invalid" },
		func(p *usProfilePrepared) { p.target.member.expectedEpoch = 0 },
		func(p *usProfilePrepared) { p.kind = usProfileCreate; p.target.revision = 2 },
		func(p *usProfilePrepared) { p.kind = usProfileCreate; p.setName = false; p.name = "" },
	} {
		forged := p
		mutate(&forged)
		if c, err := freezeUSProfileCommand(s.Identity, s.Owner, s.Issuer.issuer(), forged); c != nil || err == nil {
			t.Fatal("forged preparation accepted")
		}
	}
	if c, err := prepareUSProfileMetadataCommand(s.Identity, s.Owner, s.Issuer.issuer(), v.target(), nil, nil); c != nil || err == nil {
		t.Fatal("empty edit")
	}
	if c, err := prepareUSProfileCreateCommand(CommandIdentity{}, s.Owner, s.Issuer.issuer(), s.Target.target(), v.Profile, v.Lifetime, v.Name, v.Art); c != nil || err == nil {
		t.Fatal("invalid identity")
	}
	if c, err := prepareUSProfileMetadataCommand(s.Identity, s.Owner, usPolicyIssuer{}, v.target(), &v.Name, nil); c != nil || err == nil {
		t.Fatal("invalid issuer")
	}
}
