package live

import (
	"runtime"
	"strings"
	"testing"
)

func TestReviewTwoFeedsSharingAGuideIDBothComeIn(t *testing.T) {
	playlist := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"cnn.us\" group-title=\"News\",CNN HD\nhttp://streams.invalid/cnn-hd.ts\n" +
		"#EXTINF:-1 tvg-id=\"cnn.us\" group-title=\"News\",CNN SD\nhttp://streams.invalid/cnn-sd.ts\n"
	var report Report
	var got []Channel
	if err := ParsePlaylist(strings.NewReader(playlist), &report, func(c Channel) error { got = append(got, c); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("two feeds sharing a guide id: %d came in, report %+v", len(got), report)
	}
}

func TestReviewOneHugeProgrammeElement(t *testing.T) {
	desc := strings.Repeat("a", 64<<20)
	guide := `<tv><programme channel="x" start="20261110000000 +0000" stop="20261110010000 +0000"><title>T</title><desc>` + desc + `</desc></programme></tv>`
	desc = ""
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var report Report
	err := ParseGuide(strings.NewReader(guide), &report, func(GuideChannel) error { return nil }, func(Programme) error { return nil })
	runtime.ReadMemStats(&after)
	t.Logf("err=%v allocated %d MB while reading one 64 MB element", err, (after.TotalAlloc-before.TotalAlloc)>>20)
}
