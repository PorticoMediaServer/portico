package catalog

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const fileInfoOpeningTestCaller = readprep.Caller("server-alpha/session-9/profile-3/authority-grant-1")

func fileInfoOpeningTestScope() DetailScope {
	return DetailScope{
		ServerID:    "server-alpha",
		LibraryID:   "library-movies",
		ItemID:      strings.Repeat("ab", 16),
		ViewerFence: "fence-profile-3-restrictions-7",
	}
}

func fileInfoOpeningTestProof(now time.Time, scope DetailScope) fileInfoOpeningProof {
	var public, incarnation [16]byte
	for i := range public {
		public[i] = byte(i + 1)
	}
	for i := range incarnation {
		incarnation[i] = byte(0xf0 + i)
	}
	return fileInfoOpeningProof{
		Item:                  23,
		Library:               4,
		ItemIncarnation:       incarnation,
		PublicItem:            public,
		InfoEpoch:             11,
		ConfigurationRevision: 3,
		Revision:              ContentRevision{Catalog: 41, Viewer: 7},
		Expires:               now.Add(20 * time.Minute),
		ScopeDigest:           fileInfoScopeDigest(scope),
	}
}

func fileInfoOpeningTestCodecs(t *testing.T, clock func() time.Time) (*fileInfoAnchorCodec, *fileInfoOpeningCodec) {
	t.Helper()
	anchors, err := newFileInfoAnchorCodec()
	if err != nil {
		t.Fatal(err)
	}
	anchors.now = clock
	openings, err := newFileInfoOpeningCodec(anchors)
	if err != nil {
		t.Fatal(err)
	}
	return anchors, openings
}

func TestFileInfoOpeningRoundTripsOriginalDescriptorOnly(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 123, time.UTC)
	_, openings := fileInfoOpeningTestCodecs(t, func() time.Time { return now })
	scope := fileInfoOpeningTestScope()
	want := fileInfoOpeningTestProof(now, scope)

	token, err := openings.encodeOpening(fileInfoOpeningTestCaller, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 183 {
		t.Fatalf("opening token size %d", len(token))
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != fileInfoOpeningSize || raw[0] != 2 {
		t.Fatalf("opening layout: %d bytes version %d %v", len(raw), raw[0], err)
	}
	// The incarnation occupies the full 16 bytes at raw[17:33]; the token keeps
	// its fixed size because it replaced the earlier two int64 lifetime fields.
	if !bytes.Equal(raw[17:33], want.ItemIncarnation[:]) {
		t.Fatalf("incarnation bytes %x", raw[17:33])
	}
	// The token is a fixed descriptor plus MAC: no query handle, label, path or
	// other server data may appear verbatim, only the scope digest.
	for _, secret := range []string{scope.ServerID, scope.LibraryID, scope.ItemID, scope.ViewerFence, "/media/movies/title.mkv", "Season 1", "query-handle"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("token embeds %q", secret)
		}
	}

	got, err := openings.decodeOpening(fileInfoOpeningTestCaller, token)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Expires.Equal(want.Expires) {
		t.Fatalf("opening expiry %v", got.Expires)
	}
	got.Expires = want.Expires
	if got != want {
		t.Fatalf("opening descriptor %+v", got)
	}
	if err = got.CheckScope(scope); err != nil {
		t.Fatalf("exact scope rejected: %v", err)
	}

	for _, caller := range []readprep.Caller{"", "server-alpha/other-session/profile-3/authority-grant-1", "server-alpha/session-9/other-profile/authority-grant-1", "server-alpha/session-9/profile-3/revoked-grant"} {
		if _, err = openings.decodeOpening(caller, token); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("foreign caller %q: %v", caller, err)
		}
	}
	if _, err = openings.encodeOpening("", want); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("empty caller encoded: %v", err)
	}
}

func TestFileInfoOpeningRejectsFreshScopeFence(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	_, openings := fileInfoOpeningTestCodecs(t, func() time.Time { return now })
	scope := fileInfoOpeningTestScope()
	want := fileInfoOpeningTestProof(now, scope)
	token, err := openings.encodeOpening(fileInfoOpeningTestCaller, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := openings.decodeOpening(fileInfoOpeningTestCaller, token)
	if err != nil {
		t.Fatal(err)
	}
	fresh := scope
	fresh.ViewerFence = "fence-profile-3-restrictions-8"
	if err = got.CheckScope(fresh); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("fresh fence accepted: %v", err)
	}
	for _, mutate := range []func(*DetailScope){
		func(s *DetailScope) { s.ServerID = "server-beta" },
		func(s *DetailScope) { s.LibraryID = "library-tv" },
		func(s *DetailScope) { s.ItemID = strings.Repeat("cd", 16) },
		func(s *DetailScope) { s.ViewerFence = "" },
	} {
		changed := scope
		mutate(&changed)
		if err = got.CheckScope(changed); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("scope change accepted: %v", err)
		}
	}
	// Length-prefixing: shifting one character between adjacent fields must not
	// collide even though the concatenated strings are identical.
	left, right := scope, scope
	left.LibraryID, left.ItemID = scope.LibraryID+"a", scope.ItemID[1:]
	right.LibraryID, right.ItemID = scope.LibraryID, "a"+scope.ItemID[1:]
	if fileInfoScopeDigest(left) == fileInfoScopeDigest(right) {
		t.Fatal("scope digest is ambiguous across field boundaries")
	}
}

func TestFileInfoOpeningKeepsOriginalRevisionNotCurrent(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	_, openings := fileInfoOpeningTestCodecs(t, func() time.Time { return now })
	scope := fileInfoOpeningTestScope()
	original := fileInfoOpeningTestProof(now, scope)
	token, err := openings.encodeOpening(fileInfoOpeningTestCaller, original)
	if err != nil {
		t.Fatal(err)
	}

	// A later accepted Detail exists concurrently, with its own revision and
	// epoch. The decoder receives neither of these, so replay cannot adopt them.
	current := ContentRevision{Catalog: 58, Viewer: 9}
	later := original
	later.Revision = current
	later.InfoEpoch = 19
	laterToken, err := openings.encodeOpening(fileInfoOpeningTestCaller, later)
	if err != nil {
		t.Fatal(err)
	}
	if laterToken == token {
		t.Fatal("a later accepted Detail reused the original opening")
	}

	now = now.Add(5 * time.Minute)
	got, err := openings.decodeOpening(fileInfoOpeningTestCaller, token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != original.Revision {
		t.Fatalf("opening adopted current revision %+v", got.Revision)
	}
	if got.InfoEpoch != original.InfoEpoch {
		t.Fatalf("opening adopted current epoch %d", got.InfoEpoch)
	}
	if got.Revision == current {
		t.Fatal("current revision leaked into the original opening")
	}
	// The original capture is still the authority fence for the same content.
	if err = got.CheckScope(scope); err != nil {
		t.Fatalf("original scope rejected after unrelated churn: %v", err)
	}
	// Continuation republishes the same original descriptor and expiry.
	next, err := openings.encodeOpening(fileInfoOpeningTestCaller, got)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := openings.decodeOpening(fileInfoOpeningTestCaller, next)
	if err != nil {
		t.Fatal(err)
	}
	if !continued.Expires.Equal(original.Expires) || continued.Revision != original.Revision || continued.InfoEpoch != original.InfoEpoch {
		t.Fatalf("continuation moved the original opening: %+v", continued)
	}
}

func TestFileInfoOpeningExpiryIsOriginalAndNeverExtended(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	_, openings := fileInfoOpeningTestCodecs(t, func() time.Time { return now })
	scope := fileInfoOpeningTestScope()
	want := fileInfoOpeningTestProof(now, scope)
	token, err := openings.encodeOpening(fileInfoOpeningTestCaller, want)
	if err != nil {
		t.Fatal(err)
	}
	// Editing the encoded expiry cannot extend it: the MAC covers it.
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	tampered := append([]byte(nil), raw...)
	tampered[57+7] ^= 1
	if _, err = openings.decodeOpening(fileInfoOpeningTestCaller, base64.RawURLEncoding.EncodeToString(tampered)); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("edited expiry accepted: %v", err)
	}
	// Re-encoding shortly before expiry preserves the original instant exactly.
	now = want.Expires.Add(-time.Nanosecond)
	got, err := openings.decodeOpening(fileInfoOpeningTestCaller, token)
	if err != nil {
		t.Fatal(err)
	}
	next, err := openings.encodeOpening(fileInfoOpeningTestCaller, got)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := openings.decodeOpening(fileInfoOpeningTestCaller, next)
	if err != nil {
		t.Fatal(err)
	}
	if !continued.Expires.Equal(want.Expires) {
		t.Fatalf("continuation extended expiry to %v", continued.Expires)
	}
	// The original expiry is a hard stop.
	now = want.Expires
	if _, err = openings.decodeOpening(fileInfoOpeningTestCaller, token); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("expired opening accepted")
	}
	if _, err = openings.encodeOpening(fileInfoOpeningTestCaller, got); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("expired opening re-encoded")
	}
}

func TestFileInfoOpeningRejectsRecreatedItemIncarnation(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	_, openings := fileInfoOpeningTestCodecs(t, func() time.Time { return now })
	scope := fileInfoOpeningTestScope()
	original := fileInfoOpeningTestProof(now, scope)
	token, err := openings.encodeOpening(fileInfoOpeningTestCaller, original)
	if err != nil {
		t.Fatal(err)
	}

	// Delete and recreate the item: the row id, the original public id and the
	// initial info epoch can all repeat, but the incarnation nonce cannot.
	recreated := original
	recreated.InfoEpoch = 1
	for i := range recreated.ItemIncarnation {
		recreated.ItemIncarnation[i] = byte(i + 1)
	}
	if recreated.ItemIncarnation == original.ItemIncarnation {
		t.Fatal("incarnation fixture did not change")
	}
	recreatedToken, err := openings.encodeOpening(fileInfoOpeningTestCaller, recreated)
	if err != nil {
		t.Fatal(err)
	}
	if recreatedToken == token {
		t.Fatal("recreated item shared the original opening")
	}
	got, err := openings.decodeOpening(fileInfoOpeningTestCaller, token)
	if err != nil {
		t.Fatal(err)
	}
	if got.ItemIncarnation != original.ItemIncarnation {
		t.Fatalf("opening moved to a different incarnation: %x", got.ItemIncarnation)
	}
	// The adapter compares the token's full nonce with the current row's nonce,
	// so this opening is stale for the recreated item and cannot be replayed.
	if got.ItemIncarnation == recreated.ItemIncarnation {
		t.Fatal("old opening matched the recreated incarnation")
	}
	if err = got.CheckScope(scope); err != nil {
		t.Fatalf("unchanged scope rejected: %v", err)
	}

	// All 16 bytes are authenticated in full, never truncated to a hash prefix:
	// first-byte and last-byte variants separate openings.
	firstEdge, lastEdge := original, original
	firstEdge.ItemIncarnation[0] ^= 1
	lastEdge.ItemIncarnation[15] ^= 1
	firstToken, err := openings.encodeOpening(fileInfoOpeningTestCaller, firstEdge)
	if err != nil {
		t.Fatal(err)
	}
	lastToken, err := openings.encodeOpening(fileInfoOpeningTestCaller, lastEdge)
	if err != nil {
		t.Fatal(err)
	}
	if firstToken == token || lastToken == token || firstToken == lastToken {
		t.Fatal("incarnation bytes do not fully separate openings")
	}
}

func TestFileInfoOpeningRejectsInvalidDescriptor(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	_, openings := fileInfoOpeningTestCodecs(t, func() time.Time { return now })
	base := fileInfoOpeningTestProof(now, fileInfoOpeningTestScope())
	cases := map[string]func(*fileInfoOpeningProof){
		"zero item":              func(p *fileInfoOpeningProof) { p.Item = 0 },
		"negative item":          func(p *fileInfoOpeningProof) { p.Item = -1 },
		"zero library":           func(p *fileInfoOpeningProof) { p.Library = 0 },
		"negative library":       func(p *fileInfoOpeningProof) { p.Library = -2 },
		"zero incarnation":       func(p *fileInfoOpeningProof) { p.ItemIncarnation = [16]byte{} },
		"negative configuration": func(p *fileInfoOpeningProof) { p.ConfigurationRevision = -1 },
		"zero epoch":             func(p *fileInfoOpeningProof) { p.InfoEpoch = 0 },
		"negative epoch":         func(p *fileInfoOpeningProof) { p.InfoEpoch = -7 },
		"negative catalog":       func(p *fileInfoOpeningProof) { p.Revision.Catalog = -1 },
		"negative viewer":        func(p *fileInfoOpeningProof) { p.Revision.Viewer = -1 },
		"zero expiry":            func(p *fileInfoOpeningProof) { p.Expires = time.Time{} },
		"expiry at now":          func(p *fileInfoOpeningProof) { p.Expires = now },
		"expiry in past":         func(p *fileInfoOpeningProof) { p.Expires = now.Add(-time.Second) },
		"zero public item":       func(p *fileInfoOpeningProof) { p.PublicItem = [16]byte{} },
		"zero scope digest":      func(p *fileInfoOpeningProof) { p.ScopeDigest = [32]byte{} },
	}
	for name, mutate := range cases {
		proof := base
		mutate(&proof)
		if _, err := openings.encodeOpening(fileInfoOpeningTestCaller, proof); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("%s encoded: %v", name, err)
		}
	}
	var nilCodec *fileInfoOpeningCodec
	if _, err := nilCodec.encodeOpening(fileInfoOpeningTestCaller, base); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("nil codec encoded")
	}
	if _, err := nilCodec.decodeOpening(fileInfoOpeningTestCaller, "x"); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("nil codec decoded")
	}
	if _, err := newFileInfoOpeningCodec(nil); !errors.Is(err, readprep.ErrInvalid) {
		t.Fatal("opening codec derived from no anchor key")
	}
}

func TestFileInfoOpeningRejectsTamperAndForeignProcessKey(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	anchors, openings := fileInfoOpeningTestCodecs(t, func() time.Time { return now })
	scope := fileInfoOpeningTestScope()
	token, err := openings.encodeOpening(fileInfoOpeningTestCaller, fileInfoOpeningTestProof(now, scope))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	for i := range raw {
		changed := append([]byte(nil), raw...)
		changed[i] ^= 1
		if _, err := openings.decodeOpening(fileInfoOpeningTestCaller, base64.RawURLEncoding.EncodeToString(changed)); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("tamper byte %d accepted", i)
		}
	}
	for _, bad := range []string{token + "=", token[:171], strings.Repeat("a", 4096), token[:171] + "_"} {
		if _, err := openings.decodeOpening(fileInfoOpeningTestCaller, bad); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("malformed token accepted: length %d", len(bad))
		}
	}
	otherAnchors, err := newFileInfoAnchorCodec()
	if err != nil {
		t.Fatal(err)
	}
	otherAnchors.now = anchors.now
	otherOpenings, err := newFileInfoOpeningCodec(otherAnchors)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = otherOpenings.decodeOpening(fileInfoOpeningTestCaller, token); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("opening survived process-key replacement")
	}
}

func TestFileInfoOpeningNeverCrossDecodesAnchors(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	anchors, openings := fileInfoOpeningTestCodecs(t, func() time.Time { return now })
	scope := fileInfoOpeningTestScope()

	openingToken, err := openings.encodeOpening(fileInfoOpeningTestCaller, fileInfoOpeningTestProof(now, scope))
	if err != nil {
		t.Fatal(err)
	}
	anchorToken, err := anchors.encode(fileInfoOpeningTestCaller, fileInfoAnchor{
		Handle:    readprep.Handle(base64.RawURLEncoding.EncodeToString(make([]byte, 32))),
		Opening:   fileInfoScopeDigest(scope),
		Item:      23,
		Asset:     42,
		Expires:   now.Add(20 * time.Minute),
		Lane:      fileInfoLaneFiles,
		Direction: fileInfoForward,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = openings.decodeOpening(fileInfoOpeningTestCaller, anchorToken); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("version 1 anchor decoded as an opening: %v", err)
	}
	if _, err = anchors.decode(fileInfoOpeningTestCaller, openingToken, fileInfoScopeDigest(scope), fileInfoLaneFiles); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("version 2 opening decoded as an anchor: %v", err)
	}
	openingRaw, _ := base64.RawURLEncoding.DecodeString(openingToken)
	anchorRaw, _ := base64.RawURLEncoding.DecodeString(anchorToken)
	if openingRaw[0] != 2 || anchorRaw[0] != 1 {
		t.Fatalf("versions %d/%d", openingRaw[0], anchorRaw[0])
	}
}
