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

// One enormous element in a guide stops the refresh with a reason, never takes the memory
// it claims.
func TestAHugeGuideElementIsRefusedWithinItsBudget(t *testing.T) {
	desc := strings.Repeat("a", 64<<20)
	guide := `<tv><programme channel="x" start="20261110000000 +0000" stop="20261110010000 +0000"><title>T</title><desc>` + desc + `</desc></programme></tv>`
	desc = ""
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := ParseGuide(strings.NewReader(guide), &Report{}, func(GuideChannel) error { return nil }, func(Programme) error { return nil })
	runtime.ReadMemStats(&after)
	if err == nil || (after.TotalAlloc-before.TotalAlloc) > 32<<20 {
		t.Fatalf("err %v, allocated %d MB", err, (after.TotalAlloc-before.TotalAlloc)>>20)
	}
}
