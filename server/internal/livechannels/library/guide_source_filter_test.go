package librarychannels

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// The web and native Live TV source switcher sends sourceId=library for the
// collapsed Library Channels summary (internal/livechannels/directory.go's
// summary CASE returns 'library'); programmes are then read from /v1/guide with
// that same sourceId. The library guide must accept the same source identity the
// directory publishes.
func TestLibraryGuideAcceptsTheLibrarySourceFilterClientsSend(t *testing.T) {
	f := openFixture(t)
	seedDirectoryLibrary(t, f, 2)
	s, _ := New(f.db)
	q := livechannels.GuideQuery{
		Kind: livechannels.LibraryChannel, SourceID: "library",
		Start: f.now.Add(-time.Minute), End: f.now.Add(time.Minute), Timezone: "UTC", Limit: 50,
	}
	guide, err := s.Guide(context.Background(), ownerAuthority(nil), q, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if len(guide.Channels) != 2 {
		t.Fatalf("sourceId=library returned %d channels (state %q); want 2", len(guide.Channels), guide.State)
	}
}

// A library channel's own config id still selects that one channel, and any
// other source id selects none.
func TestLibraryGuideSourceFilterKeepsExactChannelIDs(t *testing.T) {
	f := openFixture(t)
	seedDirectoryLibrary(t, f, 2)
	s, _ := New(f.db)
	read := func(source string) int {
		t.Helper()
		guide, err := s.Guide(context.Background(), ownerAuthority(nil), livechannels.GuideQuery{
			Kind: livechannels.LibraryChannel, SourceID: source,
			Start: f.now.Add(-time.Minute), End: f.now.Add(time.Minute), Timezone: "UTC", Limit: 50,
		}, make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		return len(guide.Channels)
	}
	if n := read("channel-01"); n != 1 {
		t.Fatalf("sourceId=channel-01 returned %d channels; want 1", n)
	}
	if n := read("some-tuner"); n != 0 {
		t.Fatalf("sourceId=some-tuner returned %d library channels; want 0", n)
	}
}
