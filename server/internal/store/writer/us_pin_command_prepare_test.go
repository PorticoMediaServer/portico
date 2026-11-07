package writer

import (
	"strings"
	"testing"
)

func TestUSPINCompleteFrozenCommand(t *testing.T) {
	identity, policy := usPolicyFixture()
	pin := usPINTestRequest()
	command, err := prepareUSPINCommand(usPINSet, identity, policy.ownerID, policy.issuer, policy.memberIncarnation, pin)
	if err != nil {
		t.Fatal(err)
	}
	pin.hash[7] = 'A'
	if string(command.request.prepared.request.hash) != usPINPreparedTestHash {
		t.Fatal("caller hash mutation escaped preparation")
	}
	other, err := prepareUSPINCommand(usPINSet, identity, policy.ownerID, policy.issuer, strings.Repeat("f", 32), usPINTestRequest())
	if err != nil || other.digest == command.digest {
		t.Fatal("target membership lifetime omitted", err)
	}
	changed := command.request
	changed.issuer.installation = strings.Repeat("e", 32)
	digest, err := canonicalRequestDigest(usPINSet, 1, identity, changed)
	if err != nil || digest == command.digest {
		t.Fatal("issuer installation omitted", err)
	}
	for _, id := range []CommandID{usPINSet, usPINClear} {
		actual, err := Describe(id)
		expected, ok := describeUSPINCommand(id)
		if !ok || err != nil || actual != expected || actual.Durability != Full {
			t.Fatal("explicit FULL descriptor differs", actual, err)
		}
	}
	clearRequest := usPINTestRequest()
	clearRequest.hash = nil
	clear, err := prepareUSPINCommand(usPINClear, identity, policy.ownerID, policy.issuer, policy.memberIncarnation, clearRequest)
	if err != nil || clear.digest == command.digest {
		t.Fatal("PIN purpose/hash manifest omitted", err)
	}
}
