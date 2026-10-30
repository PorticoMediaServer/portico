package livechannels

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// FEAT-04 (Channels spec §8.1): the XMLTV facts a guide cell and sheet need
// reach the guide read, and a programme always publishes categories and flags.
func TestXMLTVProgrammeFactsReachTheGuide(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer-a", true, true)
	in := testInput()
	in.Playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\",Channel one\nhttps://provider.invalid/one\n"
	in.Guide = `<?xml version="1.0"?><tv><programme id="show-1" channel="one" start="20260905120000 +0000" stop="20260905130000 +0000">
<title>Harbour Lights</title><sub-title>The Arrival</sub-title><desc>Boats.</desc>
<category>Drama</category><category>drama</category><category>Mystery</category>
<episode-num system="xmltv_ns">1.4.0/1</episode-num><episode-num system="onscreen">S2E5</episode-num>
<date>2019</date><rating system="VCHIP"><value>TV-14</value></rating><star-rating><value>7/10</value></star-rating>
<new/><premiere>Season premiere</premiere></programme>
<programme channel="one" start="20260905130000 +0000" stop="20260905140000 +0000"><title>News</title><live/><previously-shown/><episode-num system="xmltv_ns">bad</episode-num><date>n/a</date></programme></tv>`
	if _, err := s.Save(ctx, a, in); err != nil {
		t.Fatal(err)
	}
	q := testQuery()
	q.Limit = 5
	guide, err := s.Guide(ctx, a, q)
	if err != nil || len(guide.Channels) != 1 || len(guide.Channels[0].Programmes) != 2 {
		t.Fatalf("guide %#v %v", guide, err)
	}
	first, second := guide.Channels[0].Programmes[0], guide.Channels[0].Programmes[1]
	if first.Subtitle != "The Arrival" || first.Episode == nil || *first.Episode != (ProgrammeEpisode{Season: 2, Number: 5, Display: "S2E5"}) ||
		!reflect.DeepEqual(first.Categories, []string{"Drama", "Mystery"}) || first.Rating == nil || *first.Rating != (ProgrammeRating{"VCHIP", "TV-14"}) ||
		first.Year != 2019 || first.StarRating != "7/10" || !first.Flags.Premiere || first.Flags.Live {
		t.Fatalf("first %+v", first)
	}
	raw, _ := json.Marshal(guide.Channels[0].Programmes)
	var wire []map[string]any
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if flags := wire[0]["flags"].(map[string]any); flags["new"] != true || flags["repeat"] != false {
		t.Fatalf("flags %v", flags)
	}
	// A bare programme still publishes an empty categories array and its flags;
	// a malformed episode number or date is dropped, not a failed guide.
	if second.Episode != nil || second.Year != 0 || !second.Flags.Live {
		t.Fatalf("second %+v", second)
	}
	if cats, ok := wire[1]["categories"].([]any); !ok || len(cats) != 0 || wire[1]["flags"].(map[string]any)["repeat"] != true {
		t.Fatalf("second wire %s", raw)
	}
	if strings.Contains(string(raw), "provider.invalid") {
		t.Fatal("locator leaked")
	}
}

func TestGuideDaysCountsToTheLatestAvailableEnd(t *testing.T) {
	now := time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC)
	src := func(end string) GuideSource { return GuideSource{AvailableEnd: end} }
	for _, v := range []struct {
		sources []GuideSource
		want    int
	}{
		{nil, 0},
		{[]GuideSource{src("2026-09-23T19:00:00Z")}, 0},
		{[]GuideSource{src("2026-09-23T23:00:00Z")}, 1},
		{[]GuideSource{src("2026-09-24T20:00:00Z"), src("2026-09-30T21:00:00Z")}, 8},
		{[]GuideSource{src("2027-01-01T00:00:00Z")}, 31},
	} {
		if got := GuideDays(v.sources, now); got != v.want {
			t.Fatalf("%v: %d, want %d", v.sources, got, v.want)
		}
	}
}

// Channels spec §8.1: a programme the viewer's restrictions refuse keeps its
// slot but shows the restricted title and nothing else.
func TestGuideRedactsProgrammesTheViewerMayNotSee(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer-a", true, true)
	in := testInput()
	in.Playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\",Channel one\nhttps://provider.invalid/one\n"
	in.Guide = `<?xml version="1.0"?><tv><programme channel="one" start="20260905120000 +0000" stop="20260905130000 +0000"><title>Late Crime Drama</title><sub-title>Blood</sub-title><desc>Violent.</desc><category>Crime</category><rating system="VCHIP"><value>TV-MA</value></rating></programme>
<programme channel="one" start="20260905130000 +0000" stop="20260905140000 +0000"><title>Cartoon Hour</title><rating system="VCHIP"><value>TV-Y</value></rating></programme></tv>`
	if _, err := s.Save(ctx, a, in); err != nil {
		t.Fatal(err)
	}
	q := testQuery()
	q.Limit = 5
	q.ProgrammeAllowed = func(rating string) bool { return rating == "TV-Y" }
	guide, err := s.Guide(ctx, a, q)
	if err != nil || len(guide.Channels) != 1 || len(guide.Channels[0].Programmes) != 2 {
		t.Fatalf("guide %#v %v", guide, err)
	}
	hidden, shown := guide.Channels[0].Programmes[0], guide.Channels[0].Programmes[1]
	raw, _ := json.Marshal(hidden)
	if hidden.Title != RestrictedProgrammeTitle || hidden.Rating != nil || hidden.Subtitle != "" || len(hidden.Categories) != 0 || hidden.Description != "" || strings.Contains(string(raw), "Crime") || strings.Contains(string(raw), "Blood") {
		t.Fatalf("restricted programme leaked: %s", raw)
	}
	if hidden.Start == "" || hidden.End == "" || hidden.ID == "" {
		t.Fatalf("restricted programme lost its slot: %s", raw)
	}
	if shown.Title != "Cartoon Hour" || shown.Rating == nil {
		t.Fatalf("allowed programme: %+v", shown)
	}
}
