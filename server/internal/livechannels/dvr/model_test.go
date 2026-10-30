package dvr

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"strings"
	"testing"
)

func TestRecordingIdentitySeparatesAuthorityAccountAndProfile(t *testing.T) {
	o := livechannels.Owner{Authority: "local", AccountID: "same", ProfileID: "profile"}
	base := recordID(o, "source", "programme")
	o.Authority = "hosted"
	if recordID(o, "source", "programme") == base {
		t.Fatal("cross-realm recording collision")
	}
	o.Authority = "local"
	o.AccountID = "other"
	if recordID(o, "source", "programme") == base {
		t.Fatal("cross-account recording collision")
	}
	o.AccountID = "same"
	o.ProfileID = "other"
	if recordID(o, "source", "programme") == base {
		t.Fatal("cross-profile recording collision")
	}
}
func TestUnknownEpisodeNeverMatchesNewOnly(t *testing.T) {
	c := RuleConfig{Enabled: true, SeriesID: "series", Episodes: "new", Keywords: []string{"exact"}}
	p := livechannels.Programme{SeriesID: "series", Title: "An exact example", NewEvidence: "unknown"}
	if c.Matches(p) {
		t.Fatal("unknown inferred as new")
	}
	p.NewEvidence = "repeat"
	if c.Matches(p) {
		t.Fatal("repeat inferred as new")
	}
	p.NewEvidence = "new"
	if !c.Matches(p) {
		t.Fatal("known new incorrectly excluded")
	}
	p.SeriesID = "different"
	if c.Matches(p) {
		t.Fatal("title-based series retarget")
	}
}
func TestDeleteWithoutTransactionFailsClosed(t *testing.T) {
	s := &Store{}
	e := s.pendingDeleteTx(context.Background(), nil, livechannels.Owner{}, Recording{ItemID: "published-recording"}, "owner-delete")
	if !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
}
func TestUnconfiguredStorageStoreFailsClosed(t *testing.T) {
	s := &Store{}
	_, e := s.SetStoragePolicy(context.Background(), nil, livechannels.Owner{}, StoragePolicyInput{Mutation: Mutation{RequestID: strings.Repeat("a", 48), ExpectedRevision: 1}, Policy: StoragePolicy{FloorBytes: 1}})
	if !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}
func TestRetentionMoreProtectiveAlwaysWins(t *testing.T) {
	for _, c := range []struct{ a, b, want int }{{0, 7, 0}, {7, 0, 0}, {3, 7, 7}, {7, 3, 7}, {3, 3, 3}} {
		if protective(c.a, c.b) != c.want {
			t.Fatal(c)
		}
	}
}

func TestBlockedKeywordsAndChannelsHaveVeto(t *testing.T) {
	c := RuleConfig{Enabled: true, SeriesID: "s", Episodes: "all", Keywords: []string{"episode"}, BlockedKeywords: []string{"spoiler"}, AllowedChannels: []string{"ch"}}
	p := livechannels.Programme{ChannelID: "ch", SeriesID: "s", Title: "Episode", Description: "SPOILER"}
	if c.Matches(p) {
		t.Fatal("blocked keyword ignored")
	}
	p.Description = ""
	if !c.Matches(p) {
		t.Fatal("matching evidence excluded")
	}
	c.BlockedChannels = []string{"ch"}
	if c.Matches(p) {
		t.Fatal("blocked channel ignored")
	}
}

func TestDVRExplicitTitleRulesRequireRealChannelAnchor(t *testing.T) {
	c := RuleConfig{Name: "News", SourceID: strings.Repeat("a", 48), MatchKind: "title", Title: "News", ChannelID: strings.Repeat("b", 64), Enabled: true, Episodes: "all", AllowedChannels: []string{}, BlockedChannels: []string{}, Keywords: []string{}}
	if !c.Valid() {
		t.Fatal("title rule invalid")
	}
	p := livechannels.Programme{ChannelID: c.ChannelID, Title: "NEWS"}
	if !c.Matches(p) {
		t.Fatal("explicit title match failed")
	}
	p.ChannelID = strings.Repeat("c", 64)
	if c.Matches(p) {
		t.Fatal("title rule crossed channels")
	}
	p.ChannelID = c.ChannelID
	p.Title = "Other news"
	if c.Matches(p) {
		t.Fatal("title rule used substring")
	}
	c.SeriesID = "fabricated"
	if c.Valid() {
		t.Fatal("title rule invented provider identity")
	}
}
