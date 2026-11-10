package streaming

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

type TextRenditionPlan struct {
	Resource                          string
	Revision                          int64
	Language, Label, Grant            string
	Selected, Forced, HearingImpaired bool
}

func publishTextManifests(dir string, p *DeliveryPlan) error {
	count := int(math.Ceil(p.Duration / 60))
	if count < 1 || count > 1440 {
		return nil
	}
	for ordinal, r := range manifestTextRenditions(p) {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:60\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n")
		for i := 0; i < count; i++ {
			fmt.Fprintf(&b, "#EXTINF:%.6f,\n%s.vtt?segment=%d\n", math.Min(60, p.Duration-float64(i*60)), subtitles.MediaBaseURL(r.Grant, r.Resource, r.Revision), i)
		}
		b.WriteString("#EXT-X-ENDLIST\n")
		if e := writeNamedManifest(dir, fmt.Sprintf("text-%d.m3u8", ordinal), []byte(b.String())); e != nil {
			return e
		}
	}
	return nil
}

// loadTextRenditions gives a plan the text subtitles this stream offers in its
// playlist, the selected one first as the caller ordered them.
func (h *HLS) loadTextRenditions(ctx context.Context, id string, p *DeliveryPlan) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[id]
	if s == nil {
		return ErrGrantEnded
	}
	for _, r := range s.text {
		if len(p.TextRenditions) >= 32 {
			break
		}
		if p.AirPlay && !r.Selected {
			continue
		}
		r.Grant = s.grant
		r.Label = subtitles.ManifestSubtitleName(r.Label, r.Language, len(p.TextRenditions))
		p.TextRenditions = append(p.TextRenditions, r)
	}
	return nil
}
