package writer

import (
	"fmt"
	"strings"
	"testing"
)

func hostedFixture() usHostedPreparation {
	return usHostedPreparation{kind: usHostedChallengeSpend, identity: CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, server: "server", account: "account", installation: strings.Repeat("i", 32), challengeHash: usCredentialFixtureHash("us-hosted-challenge"), nonce: strings.Repeat("n", 22), nowMS: 1800000000000, issuedMS: 1800000000000, expiresMS: 1800000300000, challengeExpiresMS: 1800001800000}
}
func TestUSHostedPreparation(t *testing.T) {
	p := hostedFixture()
	if _, e := prepareUSHosted(p); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*usHostedPreparation){func(p *usHostedPreparation) { p.kind = 572 }, func(p *usHostedPreparation) { p.installation = "short" }, func(p *usHostedPreparation) { p.challengeExpiresMS = p.nowMS }, func(p *usHostedPreparation) { p.issuedMS = p.nowMS + 300001 }, func(p *usHostedPreparation) { p.expiresMS = p.nowMS - 300000 }, func(p *usHostedPreparation) { p.expiresMS = p.issuedMS + 600001 }} {
		q := p
		mutate(&q)
		if _, e := prepareUSHosted(q); e == nil {
			t.Fatal("accepted invalid proof")
		}
	}
	for _, v := range []any{p, &p, p.source, p.nativeResult(p.nowMS)} {
		for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			got := fmt.Sprintf(f, v)
			if !strings.Contains(got, "redacted") || strings.Contains(got, p.challengeHash) {
				t.Fatalf("unredacted %s", f)
			}
		}
	}
}

func TestUSHostedChallengeDigestMatchesProducer(t *testing.T) {
	q := hostedFixture()
	if q.challengeHash != usCredentialFixtureHash("us-hosted-challenge") {
		t.Fatal("fixture differs from producer")
	}
	if _, err := prepareUSHosted(q); err != nil {
		t.Fatal("current challenge hash refused", err)
	}
	for _, bad := range []string{q.challengeHash + "=", strings.Repeat("a", 64), strings.Repeat("A", 42) + "B"} {
		q.challengeHash = bad
		if p, err := prepareUSHosted(q); p != nil || err == nil {
			t.Fatal("noncanonical challenge hash accepted")
		}
	}
}
