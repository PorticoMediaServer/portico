package catalog

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func TestFileInfoAnchorPrivateOpeningAndOriginalLease(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 123, time.UTC)
	c, err := newFileInfoAnchorCodec()
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return now }
	opening := sha256.Sum256([]byte("original-detail:17;audience:9;item-life:23;source:37"))
	handle := readprep.Handle(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	want := fileInfoAnchor{Handle: handle, Opening: opening, Item: 23, Asset: 42, Row: 0, Expires: now.Add(17 * time.Minute), Lane: fileInfoLaneAudio, Direction: fileInfoForward}
	token, err := c.encode("server/session/profile/authority", want)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 154 {
		t.Fatalf("anchor size %d", len(token))
	}
	got, err := c.decode("server/session/profile/authority", token, opening, fileInfoLaneAudio)
	if err != nil || !got.Expires.Equal(want.Expires) {
		t.Fatalf("round trip expiry: %+v %v", got, err)
	}
	got.Expires = want.Expires
	if got != want {
		t.Fatalf("round trip coordinates: %+v", got)
	}
	for _, caller := range []readprep.Caller{"", "server/other-session/profile/authority", "server/session/other-profile/authority", "server/session/profile/revoked"} {
		if _, err = c.decode(caller, token, opening, fileInfoLaneAudio); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("foreign caller %q: %v", caller, err)
		}
	}
	changed := sha256.Sum256([]byte("later accepted Detail"))
	if _, err = c.decode("server/session/profile/authority", token, changed, fileInfoLaneAudio); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("opening was adopted")
	}
	if _, err = c.decode("server/session/profile/authority", token, opening, fileInfoLaneSubtitles); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("lane crossing accepted")
	}
	now = now.Add(5 * time.Minute)
	got.Direction = fileInfoBackward
	next, err := c.encode("server/session/profile/authority", got)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := c.decode("server/session/profile/authority", next, opening, fileInfoLaneAudio)
	if err != nil || !continued.Expires.Equal(want.Expires) {
		t.Fatalf("lease extended: %+v %v", continued, err)
	}
	now = want.Expires
	if _, err = c.decode("server/session/profile/authority", token, opening, fileInfoLaneAudio); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("expired anchor accepted")
	}
}

func TestFileInfoAnchorRejectsTamperAndNoncanonicalText(t *testing.T) {
	c, err := newFileInfoAnchorCodec()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	a := fileInfoAnchor{Handle: readprep.Handle(base64.RawURLEncoding.EncodeToString(make([]byte, 32))), Item: 1, Asset: 2, Row: 3, Expires: now.Add(time.Minute), Lane: fileInfoLaneFiles, Direction: fileInfoReload}
	token, err := c.encode("caller", a)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	for i := range raw {
		copyRaw := append([]byte(nil), raw...)
		copyRaw[i] ^= 1
		if _, err := c.decode("caller", base64.RawURLEncoding.EncodeToString(copyRaw), a.Opening, a.Lane); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("tamper byte %d accepted", i)
		}
	}
	for _, bad := range []string{token + "=", token[:153], strings.Repeat("a", 4096), token[:153] + "_"} {
		if _, err := c.decode("caller", bad, a.Opening, a.Lane); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("malformed token accepted: length %d", len(bad))
		}
	}
	other, err := newFileInfoAnchorCodec()
	if err != nil {
		t.Fatal(err)
	}
	other.now = c.now
	if _, err = other.decode("caller", token, a.Opening, a.Lane); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("anchor survived process-key replacement")
	}
}

func TestFileInfoTrackAnchorRequiresOccurrence(t *testing.T) {
	c, err := newFileInfoAnchorCodec()
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range []byte{fileInfoLaneAudio, fileInfoLaneSubtitles, fileInfoLaneDisc, fileInfoLaneMembers} {
		a := fileInfoAnchor{Handle: readprep.Handle(base64.RawURLEncoding.EncodeToString(make([]byte, 32))), Item: 1, Expires: time.Now().Add(time.Minute), Lane: lane, Direction: fileInfoForward}
		if _, err := c.encode("caller", a); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("lane %d accepted no occurrence", lane)
		}
	}
}
