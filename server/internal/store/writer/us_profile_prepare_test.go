package writer

import (
	"strings"
	"testing"
)

func usProfileFixtureTarget() usProfileTarget {
	return usProfileTarget{member: usMemberTarget{account: "us-profile-account", incarnation: strings.Repeat("e", 32), expectedRevision: 1, expectedEpoch: 1}, profile: "us-profile", lifetime: strings.Repeat("f", 32), revision: 1}
}

func TestUSProfilePreparation(t *testing.T) {
	target := usProfileFixtureTarget()
	p, err := prepareUSProfileCreate(target.member, target.profile, target.lifetime, "  Child  ", "")
	if err != nil || p.name != "Child" || p.art != "blue" || p.kind != 530 {
		t.Fatal(p, err)
	}
	name, art := "  Élodie  ", "rose"
	p, err = prepareUSProfileMetadata(target, &name, &art)
	name, art = "changed", "mint"
	if err != nil || p.name != "Élodie" || p.art != "rose" || p.kind != 531 {
		t.Fatal(p, err)
	}
	for _, v := range []string{"", " \n ", strings.Repeat("é", 61), "bad\x00name", "bad\x7fname", "bad\tname", string([]byte{255})} {
		if _, err := prepareUSProfileMetadata(target, &v, nil); err == nil {
			t.Fatalf("accepted name %q", v)
		}
	}
	for _, v := range []string{strings.Repeat("é", 60), "A\u200dB"} {
		if _, err := prepareUSProfileMetadata(target, &v, nil); err != nil {
			t.Fatal("changed product name rules", err)
		}
	}
	for _, v := range []string{"", "Blue", " blue", "avatar-file"} {
		if _, err := prepareUSProfileMetadata(target, nil, &v); err == nil {
			t.Fatal("accepted art", v)
		}
	}
	if _, err := prepareUSProfileMetadata(target, nil, nil); err == nil {
		t.Fatal("empty edit")
	}
	for _, mutate := range []func(*usProfileTarget){func(v *usProfileTarget) { v.lifetime = strings.Repeat("z", 32) }, func(v *usProfileTarget) { v.member.expectedEpoch = 0 }, func(v *usProfileTarget) { v.revision = usMaxRevision }, func(v *usProfileTarget) { v.profile = "bad/id" }} {
		v := target
		mutate(&v)
		if _, err := prepareUSProfileMetadata(v, &name, nil); err == nil {
			t.Fatal("invalid target")
		}
	}
	for _, id := range []CommandID{530, 531} {
		d, ok := describeUSProfileCommand(id)
		if !ok || d.Durability != Full || isUSSecurityReceiptPurpose(id) {
			t.Fatal("purpose granted receipt authority", d)
		}
	}
}
