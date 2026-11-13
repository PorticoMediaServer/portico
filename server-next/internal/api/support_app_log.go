package api

import (
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

var supportLogID = regexp.MustCompile(`^(?:(?:itm|med|prf|usr|ses|pbs|ply|req)_[0-9a-hjkmnp-tv-z]{16}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
var supportLogVersion = regexp.MustCompile(`^\d+(?:\.\d+){0,2}(?:-(?:dev|alpha|beta|rc)(?:\.\d+)?)?(?:\+\d+)?$`)

// App uploads are untrusted. Keep only the events/facts app-log.ts permits: no free text,
// names, addresses, paths, headers or credentials, even when server paths are opted in.
func supportAppLog(raw []byte, from, to time.Time) string {
	choices := map[string]string{
		"screen": "home library item reader search settings player accounts downloads live-tv",
		"code": "network_error http_error timeout cancelled not_found not_permitted unsupported_format decode_error source_not_supported " +
			"audio_decode_error manifest_incompatible engine_error stalled_without_data source_unreachable invalid_response server_unreachable connection_failed",
		"route":  "original repackaged converted direct stream remux transcode",
		"codec":  "aac mp3 flac opus alac vorbis h264 hevc av1 vp9 ac3 eac3 dts dts_hd truehd pcm mp2 ac4",
		"engine": "native mpv avplayer media3 browser web-audio",
		"state":  "playing paused buffering stopped ended",
	}
	var out strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		var event struct {
			Time    int64          `json:"time"`
			Level   string         `json:"level"`
			Area    string         `json:"area"`
			Message string         `json:"message"`
			Facts   map[string]any `json:"facts"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Time < from.UnixMilli() || event.Time > to.UnixMilli() ||
			!slices.Contains([]string{"info", "warn", "error"}, event.Level) || !slices.Contains([]string{"app", "screen", "playback", "network"}, event.Area) ||
			!slices.Contains([]string{"started", "screen", "playback", "stall", "network-failure", "warning", "error", "crash"}, event.Message) {
			continue
		}
		facts := map[string]any{}
		for key, value := range event.Facts {
			if text, ok := value.(string); ok {
				switch key {
				case "accountId", "profileId", "itemId", "sessionId", "versionId", "requestId":
					if supportLogID.MatchString(text) {
						facts[key] = text
					}
				case "appVersion", "osVersion":
					if len(text) <= 60 && supportLogVersion.MatchString(text) {
						facts[key] = text
					}
				default:
					if slices.Contains(strings.Fields(choices[key]), text) {
						facts[key] = text
					}
				}
			} else if n, ok := value.(float64); ok && math.Trunc(n) == n {
				if key == "status" && n >= 0 && n <= 599 || key == "playerErrorCode" && math.Abs(n) <= 9007199254740991 {
					facts[key] = n
				}
			}
		}
		event.Facts = facts
		encoded, _ := json.Marshal(event)
		out.Write(encoded)
		out.WriteByte('\n')
	}
	return out.String()
}
