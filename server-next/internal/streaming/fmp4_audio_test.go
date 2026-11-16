package streaming

import (
	"math"
	"strings"
	"testing"
)

// A one-track fMP4 film must publish separate, synchronized picture and sound:
// Roku ignores the second track of a muxed CMAF fragment. AC-3/E-AC-3 also
// cannot write their copy-segment header before a packet has been parsed.
func TestSingleTrackFMP4HasSeparateSoundAtStartSeekAndResume(t *testing.T) {
	for _, row := range []struct{ name, encoder, audio, action string }{
		{"copied-aac", "libx264", "aac", "copy"},
		{"copied-ac3", "libx265", "ac3", "copy"},
		{"copied-eac3", "libx265", "eac3", "copy"},
		{"converted-aac", "mpeg2video", "aac", "convert"},
		{"converted-ac3", "mpeg2video", "ac3", "convert"},
	} {
		t.Run(row.name, func(t *testing.T) {
			r := newMediaRig(t)
			codecs := []string{"-c:v", row.encoder, "-c:a", row.audio}
			if row.encoder == "libx265" {
				codecs = append(codecs, "-preset", "ultrafast", "-x265-params", "pools=1:frame-threads=1:keyint=48:min-keyint=48:scenecut=0:log-level=error")
			}
			film := r.generate("film.mkv", 24, codecs...)
			adjust := func(request *PlanRequest) {
				request.Device = planTestDevice(t, "roku_4k")
				request.Device.Engines.Native.Transports = []ClientTransport{{Transport: TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "ac3", "eac3"}}}
				if row.name != "converted-ac3" {
					request.Settings.OutputFormat = "h264"
				}
			}
			stream, plan := r.start("film", film, 0, adjust)
			if plan.OutputContainer != "fmp4_hls" || plan.VideoAction != row.action || len(plan.Renditions) != 1 || !plan.Renditions[0].Default || plan.Renditions[0].Codec != row.audio {
				t.Fatalf("single-track film has no separate default sound: %s %s %+v", plan.OutputContainer, plan.VideoAction, plan.Renditions)
			}
			r.ready("film")
			master := string(r.fetch(stream.Grant, "master.m3u8"))
			if !strings.Contains(master, `AUDIO="audio"`) || strings.Count(master, "TYPE=AUDIO") != 1 || !strings.Contains(master, "#EXT-X-INDEPENDENT-SEGMENTS") || !strings.Contains(master, audioCodecString(row.audio, "copy", "LC")) {
				t.Fatal(master)
			}
			// Copied video follows its source keyframes; audio uses the six-second
			// grid. Both playlists must describe their actual packet timestamps.
			check := func(grant string, from int) {
				lists := r.playlists(grant)
				tracks := r.packets(grant, from, from+1)
				if len(lists) != 2 || len(tracks) != 2 || tracks[0].kind == tracks[1].kind {
					t.Fatalf("want separate video and audio, got %d playlists and %d tracks", len(lists), len(tracks))
				}
				for i, track := range tracks {
					bounds := manifestBoundaries(t, string(r.fetch(grant, lists[i].name)))
					if len(track.times) == 0 {
						t.Fatalf("%s has no samples", track.kind)
					}
					first, last := track.times[0], track.times[len(track.times)-1]
					if math.Abs(first-bounds[from]) > 0.25 || last < bounds[from+1]-0.25 {
						t.Fatalf("%s packets %.3f–%.3f disagree with playlist %.3f–%.3f", track.kind, first, last, bounds[from], bounds[from+1])
					}
					target := float64(from * 6)
					if first > target+0.25 || last < target {
						t.Fatalf("%s does not reach source seek %.3f: %.3f–%.3f", track.kind, target, first, last)
					}
				}
			}
			check(stream.Grant, 0)
			// Fetching a distant slot starts production there; neither track waits
			// for the unplayed interval. A new session starts the same way on Resume.
			check(stream.Grant, 2)
			r.streams.Stop("film")
			stream, _ = r.start("resume", film, 12000, adjust)
			r.ready("resume")
			check(stream.Grant, 2)
			r.streams.Stop("resume")
		})
	}
}
