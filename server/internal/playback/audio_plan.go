package playback

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
)

var ErrConversionCapacity = errors.New("Media conversion capacity is in use. Wait for cleanup or stop another stream, then retry.")

const audioArtifactBytes int64 = 4 << 30
const audioArtifactFiles = 4096

func publicAudioText(s string, n int) string {
	r := []rune(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// reserveHLS admits one new HLS session against the owner's conversion ceilings.
//
// It used to admit two HLS sessions server-wide, whatever they were: the third
// viewer of a Matroska file was refused even though repackaging costs almost
// nothing, and nothing an owner could set changed it. Disk is now guarded by the
// generated-media budget and the free-space floor, and the processor by the
// host-capacity bound on running converters, so what is left for this function
// is the owner's own policy: how many conversions, and how many of each kind.
// A session that only repackages is not counted and is never refused here, and
// the session being replaced (a quality or track change re-plays the title) does
// not count against its own successor.
func reserveHLS(tx *sql.Tx, id, replacing string, plan *DeliveryPlan, cfg DeliveryConfiguration) error {
	converting := plan != nil && plan.VideoAction == "convert"
	if converting && (cfg.MaxConversions > 0 || cfg.MaxHardwareConversions > 0 || cfg.MaxSoftwareConversions > 0) {
		var total, hardware, software int
		now := time.Now().UTC().Format(time.RFC3339)
		err := tx.QueryRow(`SELECT count(*),COALESCE(sum(CASE WHEN d.video_action='convert' AND d.hardware_backend NOT IN('','software') THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN d.video_action='convert' AND d.hardware_backend IN('','software') THEN 1 ELSE 0 END),0) FROM playback_sessions ps JOIN playback_delivery_plans d ON d.session_id=ps.id WHERE ps.mode='hls' AND ps.state NOT IN('stopped','ended','failed') AND ps.expires_at>? AND ps.id<>? AND ps.id<>COALESCE((SELECT owner_session_id FROM playback_private_presentations WHERE session_id=?),'') AND d.video_action='convert'`, now, replacing, id).Scan(&total, &hardware, &software)
		if err != nil {
			return err
		}
		usesHardware := plan.VideoAction == "convert" && plan.HardwareBackend != "" && plan.HardwareBackend != "software"
		usesSoftware := plan.VideoAction == "convert" && !usesHardware
		if (cfg.MaxConversions > 0 && total >= cfg.MaxConversions) || (usesHardware && cfg.MaxHardwareConversions > 0 && hardware >= cfg.MaxHardwareConversions) || (usesSoftware && cfg.MaxSoftwareConversions > 0 && software >= cfg.MaxSoftwareConversions) {
			return ErrConversionCapacity
		}
	}
	_, err := tx.Exec(`INSERT INTO playback_hls_reservations VALUES(?,?) ON CONFLICT(session_id) DO NOTHING`, id, audioArtifactBytes)
	return err
}

// Policy v1 reserves worst-case rate plus25% mux/overshoot allowance,30s timing slack and1MiB playlists.
