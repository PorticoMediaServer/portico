package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

type AdminDelivery struct {
	Hardware        *playback.SessionHardware        `json:"hardware,omitempty"`
	Streams         []playback.SessionStreamDecision `json:"streams"`
	VideoHeight     int                              `json:"videoHeight,omitempty"`
	VideoBitrateBps int                              `json:"videoBitrateBps,omitempty"`
	AudioBitrateBps int                              `json:"audioBitrateBps,omitempty"`
	AudioChannels   int                              `json:"audioChannels,omitempty"`
}
type AdminMedia struct {
	Video     *VideoStream   `json:"video,omitempty"`
	Audio     *AudioStream   `json:"audio,omitempty"`
	Subtitles *AdminSubtitle `json:"subtitles,omitempty"`
	// Present only after the converter publishes its actual encode execution.
	VideoHardware *bool `json:"videoHardware,omitempty"`
}

type AdminSubtitle struct {
	ID       string `json:"id"`
	Format   string `json:"format"`
	Language string `json:"language,omitempty"`
}

// Enrich only this bounded page; never read a capability URL into the DTO.
func enrichAdminFacts(ctx context.Context, tx *sql.Tx, s *Service, items []AdminSession) error {
	ids := []string{}
	for _, a := range items {
		ids = append(ids, a.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	raw, _ := json.Marshal(ids)
	rows, e := tx.QueryContext(ctx, `SELECT v.id,v.media_session_id,COALESCE(d.app,''),COALESCE(d.platform,''),COALESCE(d.ip,''),COALESCE(ps.asset_id,''),v.audio_track_id,v.subtitle_track_id
 FROM json_each(?) selected CROSS JOIN playback_v1_sessions v ON v.id=selected.value
 LEFT JOIN playback_sessions ps ON ps.id=v.media_session_id
 LEFT JOIN identity_devices d ON d.id=v.device_id AND d.account_id=v.account_id`, string(raw))
	if e != nil {
		return e
	}
	type fact struct{ id, media, app, platform, address, asset, audio, subtitle string }
	facts := []fact{}
	for rows.Next() {
		var f fact
		if e = rows.Scan(&f.id, &f.media, &f.app, &f.platform, &f.address, &f.asset, &f.audio, &f.subtitle); e != nil {
			rows.Close()
			return e
		}
		facts = append(facts, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, f := range facts {
		for n := range items {
			a := &items[n]
			if a.ID != f.id {
				continue
			}
			a.App, a.Platform = f.app, f.platform
			if net.ParseIP(f.address) != nil {
				a.Address = f.address
			}
			if s.Traffic != nil {
				rate, address := s.Traffic.Rate(f.media, time.UnixMilli(s.now().UnixMilli()))
				if a.State == "paused" {
					rate = 0
				}
				a.CurrentBitrateKbps = &rate
				if address != "" {
					a.Address = address
				}
			}
			if f.asset != "" {
				var version Version
				// Delegate selected-source facts to the canonical streams reader;
				// never infer breadth from titles or detail JSON in this projection.
				if e = s.streams(ctx, tx, f.asset, &version); e != nil {
					return e
				}
				m := &AdminMedia{}
				if len(version.Video) > 0 {
					v := version.Video[0]
					m.Video = &v
				}
				for _, v := range version.Audio {
					if v.ID == f.audio {
						v := v
						m.Audio = &v
					}
				}
				for _, v := range version.Subtitles {
					if v.ID == f.subtitle {
						v := v
						m.Subtitles = &AdminSubtitle{ID: v.ID, Format: v.Format, Language: v.Language}
					}
				}
				if f.subtitle != "" && m.Subtitles == nil {
					var sub AdminSubtitle
					e = tx.QueryRowContext(ctx, `SELECT r.id,s.format,s.language FROM playback_manifest_subtitles pin JOIN subtitle_resources r ON r.id=pin.resource_id JOIN subtitle_revisions s ON s.resource_id=pin.resource_id AND s.revision=pin.resource_revision WHERE r.id=? AND r.source_id=? AND pin.session_id=?`, f.subtitle, f.asset, f.media).Scan(&sub.ID, &sub.Format, &sub.Language)
					if e == nil {
						m.Subtitles = &sub
					} else if e != sql.ErrNoRows {
						return e
					}
				}
				a.Media = m
			}
			if f.media != "" {
				delivery, e := playback.AdminSessionDeliveryTx(ctx, tx, playback.DefaultDeliverySettings(), f.media)
				if e != nil {
					return e
				}
				if delivery != nil {
					a.Delivery = &AdminDelivery{Streams: delivery.Streams, VideoHeight: delivery.TargetDisplayHeight, VideoBitrateBps: delivery.TargetVideoBitrateBPS, AudioBitrateBps: delivery.TargetAudioBitrateBPS, AudioChannels: delivery.AudioChannels}
					if len(delivery.Hardware.Stages) > 0 || delivery.Hardware.Execution != nil {
						a.Delivery.Hardware = &delivery.Hardware
					}
					if a.Media != nil {
						for _, stage := range delivery.Hardware.Stages {
							if stage.Operation == "encode" && (stage.Execution == "hardware" || stage.Execution == "software") {
								hardware := stage.Execution == "hardware"
								a.Media.VideoHardware = &hardware
							}
						}
					}
				}
			}
		}
	}
	return nil
}
