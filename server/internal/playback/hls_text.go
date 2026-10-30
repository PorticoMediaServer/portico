package playback

import (
	"context"
	"fmt"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"strings"
)

type TextRenditionPlan struct {
	Resource                          string
	Revision                          int64
	Language, Label, Grant            string
	Selected, Forced, HearingImpaired bool
}

func (h *HLS) loadTextRenditions(ctx context.Context, id string, p *DeliveryPlan) error {
	rows, e := dbwork.Query(ctx, h.db, `SELECT pin.resource_id,pin.resource_revision,v.language,v.title,s.grant_token,COALESCE(st.resource_id=pin.resource_id AND st.resource_revision=pin.resource_revision AND st.renderer='external_text',0),COALESCE(rr.is_forced,0),COALESCE(rr.hearing_impaired,0) FROM playback_manifest_subtitles pin JOIN playback_sessions s ON s.id=pin.session_id JOIN subtitle_revisions v ON v.resource_id=pin.resource_id AND v.revision=pin.resource_revision LEFT JOIN subtitle_render_revisions rr ON rr.resource_id=v.resource_id AND rr.revision=v.revision LEFT JOIN playback_subtitle_state st ON st.session_id=s.id AND st.generation=s.generation WHERE pin.session_id=? ORDER BY CASE WHEN st.resource_id=pin.resource_id THEN 0 ELSE 1 END,pin.resource_id LIMIT 32`, id)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var r TextRenditionPlan
		if e = rows.Scan(&r.Resource, &r.Revision, &r.Language, &r.Label, &r.Grant, &r.Selected, &r.Forced, &r.HearingImpaired); e != nil {
			return e
		}
		if p.AirPlay && !r.Selected {
			continue
		}
		r.Label = subtitles.ManifestSubtitleName(r.Label, r.Language, len(p.TextRenditions))
		p.TextRenditions = append(p.TextRenditions, r)
	}
	return rows.Err()
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
