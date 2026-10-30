package playback

import (
	"strings"
	"testing"
)

func TestUnselectableOriginalRemuxKeepsSelectedDefaultRendition(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		for _, selected := range []int{1} {
			client := appleTVProfile()
			client.EmbeddedAudioSwitching = false
			transport := client.Transports[2]
			container := "mpegts_hls"
			if fragmented {
				transport = client.Transports[3]
				container = "fmp4_hls"
			}
			client.Transports = []ClientTransport{client.Transports[0], transport}
			source := videoSource("mp4", "h264", avcDetail,
				SourceAudio{Codec: "aac", Channels: 2, Language: "eng"},
				SourceAudio{Codec: "aac", Channels: 2, Language: "fra", Default: true})
			plan := decide(t, source, client, func(in *DeliveryInput) {
				in.HasAudioChoice, in.AudioStream = true, selected
				in.FMP4Output, in.Server = fragmented, matrixToolchain()
				in.Config.ConversionMode = "never"
				in.TranscodingEnabled = false
				in.Policy.Transcode = "never"
			})
			if plan.Strategy != DeliveryCopyRemux || plan.AudioStream != selected || plan.OutputContainer != container || len(plan.Renditions) != 2 || !plan.Renditions[0].Default || plan.Renditions[1].Default {
				t.Fatalf("selected %d on %s lost the selected default rendition: %+v", selected, container, plan)
			}
			original := routeEvaluation(plan.Trace, DeliveryOriginal)
			if original == nil || !hasRejection(original.Rejections, RejectAudioTrack) || plan.Trace.Audio == nil || plan.Trace.Audio.StreamIndex != selected {
				t.Fatal("remux lost the selected stream or original-route rejection", plan.Trace)
			}
			if manifest := string(hlsMasterManifest(&plan)); strings.Count(manifest, "TYPE=AUDIO") != 2 || !strings.Contains(manifest, `AUDIO="audio"`) {
				t.Fatal("remux lost selectable alternate audio renditions", manifest)
			}
			if len(source.Audio) != 2 {
				t.Fatal("pinning the mux removed a source audio choice", source.Audio)
			}
		}
	}
}

func TestOriginalSelectionConstraintKeepsOtherHLSRenditions(t *testing.T) {
	for _, originalSelectable := range []bool{false, true} {
		client := chromeProfile()
		client.EmbeddedAudioSwitching = originalSelectable
		container := "mkv"
		if originalSelectable {
			container = "mp4"
		}
		source := videoSource(container, "h264", avcDetail,
			SourceAudio{Codec: "aac", Channels: 2, Language: "eng"},
			SourceAudio{Codec: "aac", Channels: 2, Language: "fra", Default: true})
		plan := decide(t, source, client, func(in *DeliveryInput) {
			in.HasAudioChoice, in.AudioStream = true, 1
			in.Policy.DirectPlay = "never"
		})
		if plan.Mode != "hls" || len(plan.Renditions) != 2 || !plan.Renditions[0].Default {
			t.Fatal("an unrelated HLS route lost selectable alternate tracks", plan)
		}
	}
}
