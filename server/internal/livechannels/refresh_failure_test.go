package livechannels

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCanonicalFutureCorrectionAndEpisodeContradiction(t *testing.T) {
	s := testStore(t)
	a := testAuthority("owner", true, true)
	in := testInput()
	start := time.Now().UTC().Truncate(time.Hour).Add(72 * time.Hour)
	makeGuide := func(at time.Time, episode string) string {
		return `<tv><programme id="provider-occurrence" channel="one" start="` + at.Format("20060102150405 -0700") + `" stop="` + at.Add(time.Hour).Format("20060102150405 -0700") + `"><title>Not an identity</title><episode-num system="dd_progid">` + episode + `</episode-num></programme></tv>`
	}
	in.Guide = makeGuide(start, "EP00000001.0001")
	first, e := s.Save(context.Background(), a, in)
	if e != nil {
		t.Fatal(e)
	}
	lookup := func(gen string) string {
		var id string
		if e := s.db.QueryRow(`SELECT id FROM live_programmes WHERE generation_id=?`, gen).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	original := lookup(first.Generation)
	in.ExpectedRevision = first.Revision
	in.RequestID = strings.Repeat("ee", 24)
	in.Guide = makeGuide(start.Add(5*time.Minute), "EP00000001.0001")
	second, e := s.Save(context.Background(), a, in)
	if e != nil {
		t.Fatal(e)
	}
	if lookup(second.Generation) != original {
		t.Fatal("correction lost canonical occurrence")
	}
	in.ExpectedRevision = second.Revision
	in.RequestID = strings.Repeat("ff", 24)
	in.Guide = makeGuide(start.Add(10*time.Minute), "EP00000001.0002")
	third, e := s.Save(context.Background(), a, in)
	if e != nil {
		t.Fatal(e)
	}
	if lookup(third.Generation) == original {
		t.Fatal("contradictory episode silently reused prior occurrence")
	}
}
func TestUnmappedProviderRowsDoNotLeakIntoGuide(t *testing.T) {
	in := testInput()
	in.Guide = strings.Replace(in.Guide, `channel="one"`, `channel="unmapped-provider-channel"`, 1)
	p, e := PreviewSource(in)
	if e != nil || p.Programmes != 1 {
		t.Fatalf("preview %#v %v", p, e)
	}
}
func TestCancellationCannotPublishGuide(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Save(ctx, testAuthority("owner", true, true), testInput()); e == nil {
		t.Fatal("cancelled operation succeeded")
	}
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM live_sources`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestRemotePlaintextRoundTripAndShapeLimits(t *testing.T) {
	s := testStore(t)
	stored, e := s.seal("synthetic credential", "source-A:preview")
	if e != nil {
		t.Fatal(e)
	}
	// Plaintext ignores purpose bindings by design; folder permissions are
	// the protection.
	if locator, e := s.unseal(stored, "source-B:preview"); e != nil || locator != "synthetic credential" {
		t.Fatal("plaintext not returned as stored", e)
	}
	if _, e = s.unseal(nil, "source-A:preview"); !errors.Is(e, ErrUnavailable) {
		t.Fatal("empty value accepted", e)
	}
	if _, e = s.unseal([]byte{0xff, 0xfe}, "source-A:preview"); !errors.Is(e, ErrUnavailable) {
		t.Fatal("non-text value accepted", e)
	}
}
