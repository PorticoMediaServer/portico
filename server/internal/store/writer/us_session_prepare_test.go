package writer

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func usSessionFixture(kind CommandID) (CommandIdentity, usSessionRotationRequest, usSessionClocks) {
	identity := CommandIdentity{Incarnation: [16]byte{9}, Sequence: 1, OperationID: [16]byte{8}}
	p := usSessionPins{server: "us-server", account: "us-account", profile: "us-profile", family: "us-family", device: "us-device", installation: strings.Repeat("i", 32), memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32), incarnation: [16]byte{1}, epoch: 7, authorityGeneration: 2, generation: 3, accessDigest: strings.Repeat("c", 64), accessExpiresAt: "2026-11-07T12:05:00Z", horizon: "2026-11-08T12:00:00Z"}
	q := usSessionRotationRequest{ownerID: "us-owner", requestID: "us-request", pins: p,
		output: usSessionPreparedOutput{accessDigest: strings.Repeat("d", 64), accessExpiresAt: "2026-11-07T12:15:00Z", horizon: p.horizon, ciphertext: []byte{1, 2, 3}}}
	clocks := usSessionClocks{now: "2026-11-07T12:00:00Z", resultHorizon: p.horizon}
	if kind == usSessionRenewal {
		q.controllerID, q.controllerEpoch, q.output.nonce = "us-controller", "us-controller-epoch", []byte{4, 5, 6}
	} else {
		q.refreshDigest, q.refreshGeneration, q.refreshExpiresAt = strings.Repeat("e", 64), 1, p.horizon
		q.output.refreshDigest, q.output.horizon = strings.Repeat("f", 64), "2026-11-09T12:00:00Z"
		clocks.resultHorizon = q.output.horizon
	}
	return identity, q, clocks
}

func TestUSSessionFrozenAndPurposes(t *testing.T) {
	for _, descriptor := range usSessionDescriptors() {
		if descriptor.Durability != Full || descriptor.ID != usSessionRenewal && descriptor.ID != usSessionRefresh {
			t.Fatal(descriptor)
		}
		if actual, err := Describe(descriptor.ID); err != nil || actual != descriptor {
			t.Fatal("explicit FULL metadata differs", actual, err)
		}
		if isUSSecurityReceiptPurpose(descriptor.ID) {
			t.Fatal("descriptor granted a result schema", descriptor)
		}
		i, q, clocks := usSessionFixture(descriptor.ID)
		c, err := prepareUSSessionRotation(descriptor.ID, i, q, clocks)
		if err != nil {
			t.Fatal(err)
		}
		q.output.ciphertext[0]++
		if len(q.output.nonce) != 0 {
			q.output.nonce[0]++
		}
		q.pins.account = "changed"
		if c.request.output.ciphertext[0] != 1 || c.request.pins.account != "us-account" || len(c.request.output.nonce) > 0 && c.request.output.nonce[0] != 4 {
			t.Fatal("frozen input aliases caller")
		}
		result := c.nativeResult()
		if result.generation != 4 || result.accessPredecessorGeneration != 3 || result.familyIncarnation != c.request.pins.incarnation || result.accessExpiresAt != q.output.accessExpiresAt {
			t.Fatal(result)
		}
		if descriptor.ID == usSessionRefresh && result.refreshPredecessorGeneration != 1 {
			t.Fatal("lagging refresh generation lost", result)
		}
	}
}

func TestUSSessionPreparationRefusals(t *testing.T) {
	cases := []struct {
		name string
		kind CommandID
		edit func(*CommandIdentity, *usSessionRotationRequest, *usSessionClocks)
	}{
		{"identity", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { i.OperationID = [16]byte{} }},
		{"generation_overflow", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.generation = math.MaxInt64
		}},
		{"epoch", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.epoch = 0 }},
		{"authority_generation", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.authorityGeneration = 0
		}},
		{"member_lifetime", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.memberIncarnation = strings.Repeat("z", 32)
		}},
		{"family_incarnation", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.incarnation = [16]byte{}
		}},
		{"installation", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.installation = strings.Repeat("i", 31)
		}},
		{"clock_fraction", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			c.now = "2026-11-07T12:00:00.1Z"
		}},
		{"clock_zone", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			c.now = "2026-11-07T12:00:00+00:00"
		}},
		{"expired_token", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.pins.accessExpiresAt = c.now
		}},
		{"expired_family", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.pins.horizon = c.now }},
		{"renewal_extends_horizon", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			c.resultHorizon = "2026-11-09T12:00:00Z"
			q.output.horizon = c.resultHorizon
		}},
		{"guessed_expiry", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.output.accessExpiresAt = "2026-11-07T12:16:00Z"
		}},
		{"missing_controller", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.controllerEpoch = "" }},
		{"missing_protected_wire", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.output.ciphertext = nil }},
		{"oversize_wire", usSessionRenewal, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.output.ciphertext = make([]byte, usSessionMaxProtectedBytes+1)
		}},
		{"cross_purpose", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.controllerID = "controller"
		}},
		{"refresh_reuses_secret", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.output.refreshDigest = q.refreshDigest
		}},
		{"refresh_uses_bearer", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.refreshDigest = q.pins.accessDigest
		}},
		{"refresh_generation_ahead", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) {
			q.refreshGeneration = q.pins.generation + 1
		}},
		{"refresh_expired", usSessionRefresh, func(i *CommandIdentity, q *usSessionRotationRequest, c *usSessionClocks) { q.refreshExpiresAt = c.now }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i, q, clocks := usSessionFixture(tc.kind)
			tc.edit(&i, &q, &clocks)
			c, err := prepareUSSessionRotation(tc.kind, i, q, clocks)
			if c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
}

func TestUSSessionExactExpiryAndExpiredAccessRefresh(t *testing.T) {
	i, q, clocks := usSessionFixture(usSessionRenewal)
	q.pins.horizon = "2026-11-07T12:10:00Z"
	q.output.horizon = q.pins.horizon
	clocks.resultHorizon = q.pins.horizon
	q.output.accessExpiresAt = q.pins.horizon
	if _, err := prepareUSSessionRenewal(i, q, clocks); err != nil {
		t.Fatal("expiry must clip to exact horizon", err)
	}
	i, q, clocks = usSessionFixture(usSessionRefresh)
	q.pins.accessExpiresAt = "2026-11-07T11:55:00Z"
	if _, err := prepareUSSessionRefresh(i, q, clocks); err != nil {
		t.Fatal("refresh does not require unexpired access", err)
	}
}
