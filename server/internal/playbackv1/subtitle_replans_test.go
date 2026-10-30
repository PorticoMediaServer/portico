package playbackv1

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// Leaving a subtitle re-plans the stream only when the subtitle had a part in
// the plan: one burned into the picture, or one the engine was chosen to
// draw. A track the client asked to draw itself from the original beside the
// stream had none, so turning it off or switching to text keeps the bytes.
func TestLeavingASubtitleReplansOnlyWhenItShapedThePlan(t *testing.T) {
	request := func(delivery string) string {
		raw, _ := json.Marshal(storedRequest{Subtitles: &SubtitleRequest{TrackID: "s1", Delivery: delivery}})
		return string(raw)
	}
	original := []SubtitleFile{{TrackID: "s1", Format: "pgs", URL: "/v1/media/subtitles/g/s1/1/original.sup"}}
	off := &SubtitleRequest{TrackID: TrackOff}
	cases := []struct {
		name    string
		stored  string
		current Presentation
		want    bool
	}{
		{"drawn by the client from the original beside the stream", request("embeddedClient"), Presentation{Subtitle: &PresentationSubtitle{TrackID: "s1", Kind: playback.SubtitlePGS, Delivery: "device"}, Subtitles: original}, false},
		{"drawn by an engine the plan chose for it", request(""), Presentation{Subtitle: &PresentationSubtitle{TrackID: "s1", Kind: playback.SubtitlePGS, Delivery: "device"}, Subtitles: original}, true},
		{"read by the engine from the file it plays", request("embeddedClient"), Presentation{Subtitle: &PresentationSubtitle{TrackID: "s1", Kind: playback.SubtitlePGS, Delivery: "device"}, Subtitles: []SubtitleFile{}}, true},
		{"burned into the picture", request(""), Presentation{Subtitle: &PresentationSubtitle{TrackID: "s1", Kind: playback.SubtitlePGS, Delivery: "burn"}}, true},
		{"a text track beside the stream", request(""), Presentation{Subtitle: &PresentationSubtitle{TrackID: "s1", Kind: playback.SubtitleText, Delivery: "sidecar"}}, false},
		{"no subtitle at all", "{}", Presentation{}, false},
	}
	for _, c := range cases {
		if got := (&Service{}).subtitleReplans(context.Background(), Caller{}, row{request: c.stored}, c.current, off); got != c.want {
			t.Errorf("%s: re-plan %v, want %v", c.name, got, c.want)
		}
	}
}
