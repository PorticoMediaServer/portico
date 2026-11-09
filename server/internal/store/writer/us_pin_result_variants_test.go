package writer

import (
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// The closed 532/533 result variant. SOL-USERS: an own-account PIN set or clear
// keeps the CALLER's complete DirectSnapshot, while an administrative clear of
// another account's profile keeps the MemberProfile shape. The arm is derived
// from the validated entry/authority/target and can never be selected by a
// request field.
func TestUSPINResultVariantClosedDerivation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   CommandID
		caller string
		target string
		want   usPINResultVariant
	}{
		{"set_own", usPINSet, "issuer", "issuer", usPINVariantCallerSnapshot},
		{"clear_own", usPINClear, "issuer", "issuer", usPINVariantCallerSnapshot},
		{"clear_admin", usPINClear, "issuer", "target", usPINVariantAdministrativeProfile},
		{"set_admin", usPINSet, "issuer", "target", usPINVariantAdministrativeProfile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveUSPINResultVariant(tc.kind, tc.caller, tc.target)
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
	for _, bad := range []struct {
		kind   CommandID
		caller string
		target string
	}{
		{USMemberLibraries, "issuer", "issuer"},
		{usPINSet, "", "issuer"},
		{usPINSet, "issuer", ""},
		{usPINSet, "issuer", "two words"},
	} {
		if _, err := deriveUSPINResultVariant(bad.kind, bad.caller, bad.target); err == nil {
			t.Fatal("invalid variant inputs accepted", bad)
		}
	}
	// The image itself has no selector field: exactly one arm is legal and the
	// arm is read back from the presence shape.
	snapshot := usPINPublicImage{Snapshot: &USDirectSnapshotImage{}}
	if v, err := snapshot.variantOf(); err != nil || v != usPINVariantCallerSnapshot {
		t.Fatal("snapshot arm", v, err)
	}
	profile := usPINPublicImage{Profile: &USMemberProfileImage{}}
	if v, err := profile.variantOf(); err != nil || v != usPINVariantAdministrativeProfile {
		t.Fatal("profile arm", v, err)
	}
	for _, bad := range []usPINPublicImage{{}, {Snapshot: &USDirectSnapshotImage{}, Profile: &USMemberProfileImage{}}} {
		if _, err := bad.variantOf(); err == nil {
			t.Fatal("ambiguous or empty image accepted")
		}
		if _, err := bad.Encode(); err == nil {
			t.Fatal("ambiguous or empty image encoded")
		}
	}
}

// The variant is bound into the original result digest: identical scalar result
// facts under the two arms can never share a digest, so a saved original proves
// which response shape it captured at birth.
func TestUSPINResultVariantBoundIntoDigest(t *testing.T) {
	identity, request, v := usPINResultFixture(t)
	own, err := usPINResultDigest(identity, request, usPINVariantCallerSnapshot, v)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := usPINResultDigest(identity, request, usPINVariantAdministrativeProfile, v)
	if err != nil {
		t.Fatal(err)
	}
	if own == admin || own == (ResultDigest{}) {
		t.Fatal("variant omitted from the result digest")
	}
	if _, err := usPINResultDigest(identity, request, usPINResultVariant(0), v); err == nil {
		t.Fatal("unregistered variant accepted")
	}
	if _, err := usPINResultDigest(identity, request, usPINResultVariant(9), v); err == nil {
		t.Fatal("unknown variant accepted")
	}
}

func TestUSPINImageCodecRoundTripsBothArms(t *testing.T) {
	snapshot := usPINPublicImage{Snapshot: func() *USDirectSnapshotImage {
		v := directImageFixture()
		return &v
	}()}
	profile := usPINPublicImage{Profile: func() *USMemberProfileImage {
		v := usMemberProfileImageFixture()
		return &v
	}()}
	for name, image := range map[string]usPINPublicImage{"caller_snapshot": snapshot, "administrative_profile": profile} {
		t.Run(name, func(t *testing.T) {
			saved, err := image.Encode()
			if err != nil || len(saved) == 0 {
				t.Fatal("encode", len(saved), err)
			}
			again, err := image.Encode()
			if err != nil || !reflect.DeepEqual(saved, again) {
				t.Fatal("encode is not deterministic")
			}
			decoded, err := usPINPublicImageDecode(saved)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, image) {
				t.Fatalf("decoded image differs:\n got %#v\nwant %#v", decoded, image)
			}
			reencoded, err := decoded.Encode()
			if err != nil || !reflect.DeepEqual(reencoded, saved) {
				t.Fatal("decode/encode did not return the original saved bytes")
			}
		})
	}
}

// The decoder proves a declared length against the bytes it already holds before
// any allocation, refuses tampered variants and refuses trailing bytes.
func TestUSPINImageCodecRefusesTrailingAndOversized(t *testing.T) {
	image := usPINPublicImage{Snapshot: func() *USDirectSnapshotImage {
		v := directImageFixture()
		return &v
	}()}
	saved, err := image.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usPINPublicImageDecode(append(append([]byte(nil), saved...), 0)); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("trailing byte accepted", err)
	}
	if _, err := usPINPublicImageDecode(saved[:len(saved)-1]); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("truncated body accepted")
	}
	// A hostile 1 TiB declared string length must be refused from the remaining
	// byte count, never by attempting the allocation.
	header := &usPINImageBuffer{}
	e := requestEncoder{h: header}
	e.text(usPINImageCodecMagic)
	e.number(usPINImageCodecVersion)
	e.number(uint64(usPINVariantCallerSnapshot))
	e.number(10)  // snapshot field count
	e.number(0)   // TwoStepSetupRequired
	e.flag(false) // value
	e.number(1)   // Authority
	e.number(1 << 40)
	if e.err != nil {
		t.Fatal(e.err)
	}
	var refusal *Refusal
	if _, err := usPINPublicImageDecode(header.buf); !errors.As(err, &refusal) && !errors.Is(err, ErrResolveRequired) {
		t.Fatal("oversized declared length accepted", err)
	}
	// An unknown variant or a variant/arm mismatch is refused, never guessed.
	tampered := append([]byte(nil), saved...)
	// magic(8-byte len + bytes) + version(8) then the variant number.
	offset := 8 + len(usPINImageCodecMagic) + 8
	binary.BigEndian.PutUint64(tampered[offset:], uint64(usPINVariantAdministrativeProfile))
	if _, err := usPINPublicImageDecode(tampered); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("variant/arm mismatch accepted")
	}
	binary.BigEndian.PutUint64(tampered[offset:], 9)
	if _, err := usPINPublicImageDecode(tampered); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unknown variant accepted")
	}
	if _, err := usPINPublicImageDecode(nil); err == nil {
		t.Fatal("empty saved body accepted")
	}
}

// Replay reads ONLY the original saved bytes. Installing a counting connection
// proves the decoder executes zero statements and never touches live source.
func TestUSPINImageCodecReplayRunsNoStatements(t *testing.T) {
	h := usPINDispatchFixture(t)
	snapshotImage := usPINPublicImage{Snapshot: func() *USDirectSnapshotImage {
		v := directImageFixture()
		return &v
	}()}
	profileImage := usPINPublicImage{Profile: func() *USMemberProfileImage {
		v := usMemberProfileImageFixture()
		return &v
	}()}
	snapshotSaved, err := snapshotImage.Encode()
	if err != nil {
		t.Fatal(err)
	}
	profileSaved, err := profileImage.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(h.path, h.w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	var queries int
	h.w.openPool(connector, func(conn driver.Conn) driver.Conn {
		return &usPINOrderConn{writerDriverConn: conn.(writerDriverConn), onQuery: func(string) { queries++ }}
	})
	for name, tc := range map[string]struct {
		saved []byte
		want  usPINPublicImage
	}{
		"caller_snapshot":        {snapshotSaved, snapshotImage},
		"administrative_profile": {profileSaved, profileImage},
	} {
		t.Run(name, func(t *testing.T) {
			queries = 0
			got, err := usPINPublicImageDecode(tc.saved)
			if err != nil {
				t.Fatal(err)
			}
			if queries != 0 {
				t.Fatalf("replay ran %d statements", queries)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatal("replay did not return the original saved bytes")
			}
		})
	}
}

// A saved tag is a canonical uint64 from the closed set. Narrowing it to
// uint8 before validation would map 257 back onto arm 1 and accept a body whose
// tag is not the one the encoder writes; every non-canonical tag is refused.
func TestUSPINImageCodecRefusesNonCanonicalVariantTag(t *testing.T) {
	image := usPINPublicImage{Snapshot: func() *USDirectSnapshotImage {
		v := directImageFixture()
		return &v
	}()}
	saved, err := image.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// magic(8-byte len + bytes) + version(8) then the 8-byte tag.
	offset := 8 + len(usPINImageCodecMagic) + 8
	for name, tag := range map[string]uint64{
		"zero":       0,
		"wrap_one":   257,
		"wrap_two":   258,
		"max_uint64": ^uint64(0),
	} {
		t.Run(name, func(t *testing.T) {
			tampered := append([]byte(nil), saved...)
			binary.BigEndian.PutUint64(tampered[offset:], tag)
			if _, err := usPINPublicImageDecode(tampered); !errors.Is(err, ErrResolveRequired) {
				t.Fatalf("non-canonical tag %d accepted", tag)
			}
		})
	}
	// An over-long tag encoding (an extra tag byte before the arm body) shifts
	// the arm and is refused rather than read as a shorter canonical tag.
	t.Run("overlong_tag_encoding", func(t *testing.T) {
		tampered := append([]byte(nil), saved[:offset+8]...)
		tampered = append(tampered, 0)
		tampered = append(tampered, saved[offset+8:]...)
		if _, err := usPINPublicImageDecode(tampered); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("over-long tag encoding accepted")
		}
	})
}

// The two image loaders are not interchangeable. Each refuses the other arm by
// name before reading source, so a direct loader call can never return a shape
// the arm did not select.
func TestUSPINImageLoadersRefuseWrongArm(t *testing.T) {
	h := usPINDispatchFixture(t)
	v := usPINSavedResult{kind: usPINSet, owner: h.owner, account: h.issuer.account, member: h.member,
		profile: h.issuer.profile, lifetime: h.issuer.profileLifetime, revision: 2, pinRevision: 2,
		pinRequired: true, appliedMS: h.w.now().UnixMilli()}
	scope := usPINFenceScope{account: h.issuer.account, member: h.member, profile: h.issuer.profile, lifetime: h.issuer.profileLifetime}
	t.Run("profile_rejects_caller_snapshot", func(t *testing.T) {
		turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
		if _, _, err := captureUSPINProfileImage(t.Context(), turn, a, usPINVariantCallerSnapshot, scope, v); !errors.Is(err, errUSPINResultArm) {
			t.Fatal("MemberProfile loader accepted the caller-snapshot arm", err)
		}
		_, _ = turn.transaction.rollback(nil)
	})
	t.Run("snapshot_rejects_administrative_profile", func(t *testing.T) {
		turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
		if _, _, err := captureUSPINSelfSnapshot(t.Context(), turn, a, usPINVariantAdministrativeProfile, scope, v, h.w.now().UnixMilli()); !errors.Is(err, errUSPINResultArm) {
			t.Fatal("DirectSnapshot loader accepted the administrative arm", err)
		}
		_, _ = turn.transaction.rollback(nil)
	})
}

// The snapshot primitive is post-effect and own-account only, and it binds the
// edited profile in the image back to the saved result scalars.
func TestUSPINSnapshotCaptureGuards(t *testing.T) {
	h := usPINDispatchFixture(t)
	base := usPINSavedResult{kind: usPINSet, owner: h.owner, account: h.issuer.account, member: h.member,
		profile: h.issuer.profile, lifetime: h.issuer.profileLifetime, revision: 1, pinRevision: 1, pinRequired: true, appliedMS: h.w.now().UnixMilli()}
	scope := usPINFenceScope{account: h.issuer.account, member: h.member, profile: h.issuer.profile, lifetime: h.issuer.profileLifetime}
	t.Run("pre_effect_revision_refused", func(t *testing.T) {
		turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
		v := base
		v.revision, v.pinRevision = 2, 2
		if _, _, err := captureUSPINSelfSnapshot(t.Context(), turn, a, usPINVariantCallerSnapshot, scope, v, h.w.now().UnixMilli()); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("pre-effect image accepted", err)
		}
	})
	t.Run("cross_account_refused", func(t *testing.T) {
		turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
		v := base
		v.account = "target"
		target := scope
		target.account = "target"
		if _, _, err := captureUSPINSelfSnapshot(t.Context(), turn, a, usPINVariantCallerSnapshot, target, v, h.w.now().UnixMilli()); !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("cross-account snapshot accepted", err)
		}
	})
	t.Run("wrong_purpose_refused", func(t *testing.T) {
		turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
		v := base
		v.kind = USMemberLibraries
		if _, _, err := captureUSPINSelfSnapshot(t.Context(), turn, a, usPINVariantCallerSnapshot, scope, v, h.w.now().UnixMilli()); !errors.Is(err, ErrInvalid) {
			t.Fatal("unregistered purpose accepted", err)
		}
	})
}

// Identity Token() values are 32 random bytes in base64.RawURLEncoding: 43
// octets. The snapshot adds Authority/ServerID/Account.ID that no source column
// preflights, so an ordinary PIN edit under a real-sized identity must still
// capture instead of refusing after the effect.
func TestUSPINSnapshotTokenSizedIdentifiers(t *testing.T) {
	serverID, accountID := strings.Repeat("s", 43), strings.Repeat("a", 43)
	h := usPINDispatchFixtureIssuer(t, func(i *usPolicyIssuer) {
		i.server, i.account = serverID, accountID
	})
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	got, err := h.dispatch(t, usPINSet, h.input(t, command))
	if err != nil {
		t.Fatal("volume bound refused a Token-sized identity", err)
	}
	snapshot := got.Result.Image.Snapshot
	if got.Result.Outcome.State != Applied || snapshot == nil || got.Result.ImageBytes <= 0 || len(got.Result.Saved) == 0 {
		t.Fatal("Token-sized 532 did not capture", got.Result.Outcome)
	}
	if snapshot.Authority != "local" || snapshot.ServerID != serverID || snapshot.Account.ID != accountID ||
		snapshot.Account.PrimaryProfileID != h.issuer.profile || !snapshot.CanManage {
		t.Fatalf("Token-sized snapshot: %+v", snapshot.Account)
	}
	if image, e := USDirectSnapshotImageOctets(*snapshot); e != nil || image <= 0 {
		t.Fatal("image octets", image, e)
	}
}
