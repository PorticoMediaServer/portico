package writer

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestUSSessionCommandWitnesses(t *testing.T) {
	type edit struct {
		name  string
		only  CommandID
		apply func(*CommandIdentity, *usSessionRotationRequest, *usSessionClocks)
	}
	changes := []edit{
		{"ownerID", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.ownerID += "-other" }},
		{"requestID", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.requestID += "-other" }},
		{"pins.server", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.server += "-other" }},
		{"pins.account", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.account += "-other" }},
		{"pins.profile", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.profile += "-other" }},
		{"pins.family", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.family += "-other" }},
		{"pins.device", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.device += "-other" }},
		{"pins.installation", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.installation += "-other"
		}},
		{"controllerID", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.controllerID += "-other" }},
		{"controllerEpoch", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.controllerEpoch += "-other"
		}},
		{"pins.memberIncarnation", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.memberIncarnation = strings.Repeat("9", 32)
		}},
		{"pins.profileLifetime", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.profileLifetime = strings.Repeat("9", 32)
		}},
		{"pins.accessDigest", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.accessDigest = usCredentialFixtureHash("session-fixture-9")
		}},
		{"refreshDigest", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.refreshDigest = usCredentialFixtureHash("session-fixture-9")
		}},
		{"priorReceiptDigest", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.priorReceiptDigest = usCredentialFixtureHash("session-fixture-9")
		}},
		{"output.accessDigest", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.output.accessDigest = usCredentialFixtureHash("session-fixture-9")
		}},
		{"output.refreshDigest", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.output.refreshDigest = usCredentialFixtureHash("session-fixture-9")
		}},
		{"pins.epoch", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.epoch++ }},
		{"pins.authorityGeneration", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.authorityGeneration++
		}},
		{"pins.generation", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.generation++ }},
		{"refreshGeneration", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.refreshGeneration++ }},
		{"identity.Incarnation", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { i.Incarnation[1]++ }},
		{"identity.OperationID", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { i.OperationID[1]++ }},

		{"identity.Sequence", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { i.Sequence++ }},
		{"pins.incarnation", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.incarnation[1]++ }},
		{"pins.accessExpiresAt", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.accessExpiresAt = "2026-11-07T12:06:00Z"
		}},
		{"pins.horizon", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.horizon = "2026-11-08T13:00:00Z"
		}},
		{"refreshExpiresAt", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.refreshExpiresAt = "2026-11-08T13:00:00Z"
		}},
		{"output.nonce", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.output.nonce[0]++ }},
		{"output.ciphertext", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.output.ciphertext[0]++ }},
		{"clock.now_and_exact_expiry", 0, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			c.now = "2026-11-07T12:01:00Z"
			q.output.accessExpiresAt = "2026-11-07T12:16:00Z"
		}},
		{"clock.resultHorizon_and_output.horizon", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			c.resultHorizon = "2026-11-10T12:00:00Z"
			q.output.horizon = c.resultHorizon
		}},
	}
	for _, kind := range []CommandID{usSessionRenewal, usSessionRefresh} {
		i, q, c := usSessionFixture(kind)
		base, err := prepareUSSessionCommand(kind, i, q, c)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range changes {
			if tc.only != 0 && tc.only != kind {
				continue
			}
			t.Run(fmt.Sprintf("%d/%s", kind, tc.name), func(t *testing.T) {
				i, q, c := usSessionFixture(kind)
				tc.apply(&i, &q, &c)
				got, err := prepareUSSessionCommand(kind, i, q, c)
				if err != nil {
					t.Fatal(err)
				}
				if got.digest == base.digest {
					t.Fatal("constructor omitted witness")
				}
			})
		}
	}
}

// Build the complete expected payload from ORIGINAL inputs, independently of
// the wrapper and its returned request (including fixed/empty variant fields).
func TestUSSessionCommandWholeOriginalPayload(t *testing.T) {
	var digests []RequestDigest
	for _, kind := range []CommandID{usSessionRenewal, usSessionRefresh} {
		i, q, c := usSessionFixture(kind)
		if kind == usSessionRefresh {
			q.priorReceiptDigest = usCredentialFixtureHash("session-fixture-7")
		}
		want, err := canonicalRequestDigest(kind, 1, i, usSessionCommandRequest{prepared: usSessionRotation{kind: kind, identity: i, request: q, clocks: c}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := prepareUSSessionCommand(kind, i, q, c)
		if err != nil {
			t.Fatal(err)
		}
		if got.digest != want {
			t.Fatal("complete original payload differs")
		}
		digests = append(digests, got.digest)
		frozen := got.request.prepared
		q.output.ciphertext[0]++
		if len(q.output.nonce) > 0 {
			q.output.nonce[0]++
		}
		q.pins.account = "changed"
		c.now = "changed"
		i.Sequence++
		if got.request.prepared.request.output.ciphertext[0] != 1 || len(got.request.prepared.request.output.nonce) > 0 && got.request.prepared.request.output.nonce[0] != 4 || !reflect.DeepEqual(got.request.prepared.clocks, frozen.clocks) {
			t.Fatal("source aliases frozen input")
		}
		again, err := canonicalRequestDigest(kind, 1, got.identity, got.request)
		if err != nil || again != want {
			t.Fatal("frozen payload changed")
		}
		for _, v := range []any{got, *got, got.request} {
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
				if !strings.Contains(fmt.Sprintf(verb, v), "redacted") {
					t.Fatal("unredacted wrapper")
				}
			}
		}
	}
	if digests[0] == digests[1] {
		t.Fatal("rotation kinds collide")
	}
}

func TestUSSessionCommandRevalidates(t *testing.T) {
	i, q, c := usSessionFixture(usSessionRenewal)
	forged := usSessionRotation{kind: usSessionRenewal, identity: i, request: q, clocks: c}
	forged.request.output.accessExpiresAt = "2026-11-07T12:16:00Z"
	if p, e := prepareUSSessionCommand(forged.kind, forged.identity, forged.request, forged.clocks); p != nil || !errors.Is(e, ErrInvalid) {
		t.Fatal("forged preparation accepted")
	}
	if p, e := prepareUSSessionCommand(usLocalPasswordReplace, i, q, c); p != nil || !errors.Is(e, ErrInvalid) {
		t.Fatal("unsupported kind accepted")
	}
}

// A clipped recipe permits now to vary without changing the protected output.
func TestUSSessionCommandOriginalClockWithClippedExpiry(t *testing.T) {
	i, q, c := usSessionFixture(usSessionRefresh)
	c.resultHorizon = "2026-11-07T12:10:00Z"
	q.output.horizon = c.resultHorizon
	q.output.accessExpiresAt = c.resultHorizon
	a, e := prepareUSSessionCommand(usSessionRefresh, i, q, c)
	if e != nil {
		t.Fatal(e)
	}
	c.now = "2026-11-07T12:01:00Z"
	b, e := prepareUSSessionCommand(usSessionRefresh, i, q, c)
	if e != nil {
		t.Fatal(e)
	}
	if a.digest == b.digest {
		t.Fatal("original clock omitted behind clipped expiry")
	}
	q.output.accessExpiresAt = "2026-11-07T12:09:00Z"
	if p, e := prepareUSSessionCommand(usSessionRefresh, i, q, c); p != nil || !errors.Is(e, ErrInvalid) {
		t.Fatal("inexact clipped expiry accepted")
	}
}
