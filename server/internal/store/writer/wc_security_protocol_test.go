package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func wcSecurityWireFixture() (CommandIdentity, RequestDigest, SecurityValue) {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, RequestDigest{3}, SecurityValue{
		FamilyID: "target-音", CommandKind: "admin_revoke", ResultVersion: 1, AppliedMS: 1123,
		IssuerAccountID: "account", IssuerProfileID: "profile", IssuerFamilyID: "issuer",
		IssuerMemberIncarnation: strings.Repeat("a", 32), IssuerProfileLifetime: strings.Repeat("b", 32),
		Reason: "admin_revoke", RevokedAt: "1970-01-01T00:00:01Z", ResultCount: 1,
	}
}

func TestWCSecurityProtocolCompleteWire(t *testing.T) {
	id, request, v := wcSecurityWireFixture()
	if err := preflightSecurityValue(v); err != nil {
		t.Fatal(err)
	}
	// Independently form the frozen wire. This covers every saved scalar and
	// the ORIGINAL request/identity, without adding a private digest path.
	wire := []byte(canonicalResultCodec)
	wire = append(wire, 0, 1)
	text := func(s string) { wire = binary.BigEndian.AppendUint64(wire, uint64(len(s))); wire = append(wire, s...) }
	number := func(n int64) { wire = binary.BigEndian.AppendUint64(wire, uint64(n)) }
	text("security_accept")
	text("admin_revoke")
	wire = append(wire, id.Incarnation[:]...)
	number(id.Sequence)
	wire = append(wire, id.OperationID[:]...)
	wire = append(wire, request[:]...)
	text(v.FamilyID)
	text(v.CommandKind)
	number(v.ResultVersion)
	number(v.AppliedMS)
	text(v.IssuerAccountID)
	text(v.IssuerProfileID)
	text(v.IssuerFamilyID)
	text(v.IssuerMemberIncarnation)
	text(v.IssuerProfileLifetime)
	text(v.Reason)
	text(v.RevokedAt)
	number(v.ResultCount)
	e := requestEncoder{h: sha256.New()}
	if err := writeSecurityValue(&e, id, request, v); err != nil || !bytes.Equal(e.h.Sum(nil), sha256Bytes(wire)) {
		t.Fatal("incomplete security scalar wire", err)
	}
	texts := len(v.FamilyID) + len(v.CommandKind) + len(v.IssuerAccountID) + len(v.IssuerProfileID) + len(v.IssuerFamilyID) + len(v.IssuerMemberIncarnation) + len(v.IssuerProfileLifetime) + len(v.Reason) + len(v.RevokedAt)
	if len(wire) != wcSecurityResultFixedBytes+texts || uint64(len(wire)) != e.written {
		t.Fatal("preflight overhead differs from actual wire", len(wire), e.written)
	}
	// The two encodings of the same retained scalar are stable. Different
	// original operation/request bindings cannot acquire the same receipt.
	id.OperationID[0]++
	changed := requestEncoder{h: sha256.New()}
	if err := writeSecurityValue(&changed, id, request, v); err != nil || bytes.Equal(e.h.Sum(nil), changed.h.Sum(nil)) {
		t.Fatal("operation omitted", err)
	}
	id.OperationID[0]--
	request[0]++
	changed = requestEncoder{h: sha256.New()}
	if err := writeSecurityValue(&changed, id, request, v); err != nil || bytes.Equal(e.h.Sum(nil), changed.h.Sum(nil)) {
		t.Fatal("request binding omitted", err)
	}
}

func sha256Bytes(b []byte) []byte { sum := sha256.Sum256(b); return sum[:] }

func TestWCSecurityProtocolScalarShape(t *testing.T) {
	_, _, base := wcSecurityWireFixture()
	for _, test := range []struct {
		name string
		edit func(*SecurityValue)
	}{
		{"self_family", func(v *SecurityValue) { v.IssuerFamilyID = v.FamilyID }},
		{"self_signout_variant", func(v *SecurityValue) { v.CommandKind = "explicit_signout" }},
		{"count", func(v *SecurityValue) { v.ResultCount = 0 }},
		{"version", func(v *SecurityValue) { v.ResultVersion = 2 }},
		{"issuer_birth", func(v *SecurityValue) { v.IssuerMemberIncarnation = "" }},
		{"issuer_lifetime", func(v *SecurityValue) { v.IssuerProfileLifetime = "" }},
		{"time_projection", func(v *SecurityValue) { v.RevokedAt = time.UnixMilli(v.AppliedMS).UTC().Format(time.RFC3339Nano) }},
		{"reason", func(v *SecurityValue) { v.Reason = "explicit_signout" }},
		{"negative_clock", func(v *SecurityValue) { v.AppliedMS = -1 }},
		{"invalid_text", func(v *SecurityValue) { v.IssuerAccountID = "\xff" }},
		{"oversize", func(v *SecurityValue) { v.IssuerAccountID = strings.Repeat("x", maxCanonicalResultBytes) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := base
			test.edit(&v)
			if err := preflightSecurityValue(v); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed scalar admitted", err)
			}
		})
	}
}

func TestWCSecurityProtocolCurrentTierAndDevicesHint(t *testing.T) {
	for _, test := range []struct {
		actor, target string
		want          bool
	}{{"owner", "owner", true}, {"owner", "admin", true}, {"owner", "member", true}, {"admin", "member", true}, {"admin", "admin", false}, {"admin", "owner", false}, {"member", "member", false}, {"owner", "", false}} {
		if got := wcSecurityTargetTierAllowed(test.actor, test.target); got != test.want {
			t.Fatal(test, got)
		}
	}
	e := wcSecurityDevicesEvent(1123)
	if e != (wcAPIEvent{audience: "admin", eventType: "list.changed", resourceKind: "list", resourceID: "devices", revision: "1123", atMS: 1123}) {
		t.Fatal("legacy device-list hint changed", e)
	}
	if n, err := wcAPIEventVariableBytes(e); err != nil || n != 32 {
		t.Fatal("one bounded hint shape", n, err)
	}
}

func TestWCSecurityProtocolNativeOwnerAndTarget(t *testing.T) {
	// Reuse the parent's owned opening, real clocks and actual native births.
	// No raw SQL population, testCustody, fake floor or new opener is used.
	w, d, _ := wcProgressCompositionFixture(t)
	e := w.effective.executor
	defs := wcSecurityProtocolDefinitions()
	for _, def := range defs {
		if issues := plan.LintSQL(def.SQL); len(issues) != 0 {
			t.Fatal(def.ID, issues)
		}
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, defs, "wc_security_protocol.go", "wc_security_protocol_test.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	// Use the parent's existing real governor/binder path with the actual
	// SecurityAccept Recovery class. Figures are component forecasts only.
	recipe := effectiveFixturePlan().recipe
	g, authority, wal, request := governedFixture(t, e.now, governedFixtureProfile{recipe, 8 << 20})
	request.Key.Domain, request.Key.ID, request.Class = uint16(SecurityAccept), d.scope.familyID, governor.Recovery
	request.Footprint = []governor.Scope{{Domain: uint16(SecurityAccept)}}
	if err = refreshWriterWAL(e.fileBinding.path, wal, e.now); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(SecurityAccept, recipe.limit, g, authority, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.begin(t.Context(), SecurityAccept, recipe, custody)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, def := range defs {
		steps = append(steps, statementStep{def.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	s, err := readWCSecurityOwner(t.Context(), turn, d.scope.familyID, d.scope.serverID)
	if err != nil || s.account != d.scope.accountID || s.memberIncarnation != d.scope.memberBirth || s.profileLifetime != d.scope.profileBirth || s.last != 0 || s.acknowledged != 0 {
		t.Fatal("native retained owner", s, err)
	}
	tier, err := readWCSecurityTargetTier(t.Context(), turn, s)
	if err != nil || tier != "member" {
		t.Fatal("CURRENT target membership", tier, err)
	}
	q := SecurityReceiptRequest{FamilyID: s.family, Receipt: ReceiptReference{Identity: CommandIdentity{Incarnation: s.incarnation, Sequence: 1, OperationID: [16]byte{7}}, RequestDigest: RequestDigest{8}}}
	lookup, err := lookupWCSecurity(t.Context(), turn, q, s.server)
	if err != nil || lookup.decision != receiptFresh || lookup.snapshot != s || lookup.value != (SecurityValue{}) {
		t.Fatal("fresh original classification", lookup, err)
	}
	if floor, err := acknowledgeWCSecurity(t.Context(), turn, q, lookup); !errors.Is(err, ErrResolveRequired) || floor != 0 {
		t.Fatal("fresh request acquired a fake floor", floor, err)
	}
	wrong := s
	wrong.memberIncarnation = strings.Repeat("f", 32)
	if tier, err := readWCSecurityTargetTier(t.Context(), turn, wrong); !errors.Is(err, ErrDisclosureDenied) || tier != "" {
		t.Fatal("replaced target membership disclosed", tier, err)
	}
	wrong = s
	wrong.profileLifetime = strings.Repeat("f", 32)
	if tier, err := readWCSecurityTargetTier(t.Context(), turn, wrong); !errors.Is(err, ErrDisclosureDenied) || tier != "" {
		t.Fatal("replaced target profile disclosed", tier, err)
	}
	q.Receipt.Identity.Incarnation[0] ^= 0xff
	if got, err := lookupWCSecurity(t.Context(), turn, q, s.server); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(got, wcSecurityReceiptLookup{}) {
		t.Fatal("wrong owner incarnation returned data", got, err)
	}
	if _, err = tx.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if authority.Usage()[governor.Recovery] != (governor.Amounts{}) {
		t.Fatal("real Recovery grant was not retired")
	}
}
