package playback

import "testing"

// A live channel's guide generation moves with every refresh; what a session's
// producer depends on is the source fence. A new generation with the same
// fence keeps the producer, and a new fence replaces it. Library channels
// still follow their schedule entry.
func TestLinearRetuneFollowsTheSourceFenceNotTheGuideGeneration(t *testing.T) {
	live := func(generation, fence string) LinearSelection {
		return LinearSelection{Reference: LinearReference{Kind: "live-source", SourceID: "source", ChannelID: "channel", Generation: generation}, SourceFence: fence, EntryID: "programme-" + generation}
	}
	if linearSelectionRetunes(live("one", "stream"), live("two", "stream")) {
		t.Fatal("a guide refresh that left the stream alone would retune the session")
	}
	if linearProducerOutdated(live("one", "stream"), live("two", "stream")) {
		t.Fatal("a guide refresh that left the stream alone would stop the running producer")
	}
	if !linearSelectionRetunes(live("one", "stream"), live("two", "moved")) || !linearProducerOutdated(live("one", "stream"), live("two", "moved")) {
		t.Fatal("a changed stream did not retune")
	}
	if !linearSelectionRetunes(live("one", "stream"), live("one", "moved")) || !linearProducerOutdated(live("one", "stream"), live("one", "moved")) {
		t.Fatal("a changed stream within one generation did not retune")
	}
	scheduled := func(entry, asset, fence string) LinearSelection {
		return LinearSelection{Reference: LinearReference{Kind: "library-channel", ChannelID: "channel", Generation: "schedule"}, EntryID: entry, AssetID: asset, SourceFence: fence}
	}
	held := scheduled("entry", "asset", "fence")
	if linearSelectionRetunes(held, held) || linearProducerOutdated(held, held) {
		t.Fatal("an unchanged schedule entry would retune")
	}
	for name, next := range map[string]LinearSelection{"entry": scheduled("next", "asset", "fence"), "fence": scheduled("entry", "asset", "other")} {
		if !linearSelectionRetunes(held, next) || !linearProducerOutdated(held, next) {
			t.Fatalf("a library channel's new %s did not retune", name)
		}
	}
	if !linearSelectionRetunes(held, scheduled("entry", "replaced", "fence")) {
		t.Fatal("a library channel's replaced asset did not retune the session")
	}
}
