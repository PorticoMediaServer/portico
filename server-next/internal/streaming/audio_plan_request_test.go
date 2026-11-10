package streaming

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/audiofacts"
)

// A track the device decodes plays as it is with its gapless trim; one it
// cannot decode is converted; an unmeasured track gets no gapless plan.
func TestPlanAudioDirectConvertedAndUnmeasured(t *testing.T) {
	flac := audiofacts.Facts{
		Container: "flac", Codec: "flac", SampleRate: 96000, Channels: 2, BitDepth: 24,
		RawFrames: 96000 * 200, DurationFrames: 96000 * 200, TrimSource: "flac",
	}
	device := []AudioDecodeCap{
		{Codec: "flac", Containers: []string{"flac"}, MaxSampleRate: 192000, MaxBitDepth: 24},
		{Codec: "opus", Containers: []string{"ogg"}},
	}
	request := AudioPlanRequest{
		StreamID: "s1", Generation: 1, Grant: "g", Facts: flac, Measured: true, Size: 50 << 20,
		Choice: &Choice{AudioDecode: device}, Settings: DefaultDeliveryConfiguration(), TranscodingEnabled: true,
	}
	plan, err := PlanAudio(request)
	if err != nil || plan.Mode != "direct" || plan.URL != "/v1/media/g/audio" || plan.Trim == nil || plan.DurationFrames != flac.DurationFrames {
		t.Fatalf("a decodable track was not planned as it is: %+v %v", plan, err)
	}

	request.Choice = &Choice{AudioDecode: device[1:]}
	plan, err = PlanAudio(request)
	if err != nil || plan.Mode != "converted" || plan.Codec != "opus" || plan.Convert == nil || plan.Trim == nil || plan.Trim.Source != "opus-preskip" {
		t.Fatalf("an undecodable track was not converted to Opus: %+v %v", plan, err)
	}

	request.Measured = false
	if plan, err = PlanAudio(request); err != nil || plan.Mode != "unavailable" || plan.Reason == "" {
		t.Fatalf("an unmeasured track got a guessed plan: %+v %v", plan, err)
	}
}

// Tags win over the server's own measurement, and an album gain needs every
// track of the album measured.
func TestAudioGainFromTagsThenAnalysis(t *testing.T) {
	gain := AudioGainFrom(map[string]string{"replaygain_track_gain": "-6.5 dB"}, `{"integratedLUFS":-10}`, nil)
	if gain == nil || gain.Source != "tags" || gain.TrackDB == nil || *gain.TrackDB != -6.5 {
		t.Fatalf("tag gain was not used: %+v", gain)
	}
	album := []AlbumTrackLoudness{{Duration: 100, Summary: `{"integratedLUFS":-14}`}, {Duration: 100, Summary: `{"integratedLUFS":-14}`}}
	gain = AudioGainFrom(nil, `{"integratedLUFS":-14,"truePeakLinear":0.9}`, album)
	if gain == nil || gain.Source != "analysis" || *gain.TrackDB != -4 || gain.AlbumDB == nil || *gain.AlbumDB != -4 || *gain.TrackPeak != 0.9 {
		t.Fatalf("measured gain was not used: %+v", gain)
	}
	album[1].Summary = ""
	if gain = AudioGainFrom(nil, `{"integratedLUFS":-14}`, album); gain == nil || gain.AlbumDB != nil {
		t.Fatalf("an album with an unmeasured track got an album gain: %+v", gain)
	}
	if gain = AudioGainFrom(nil, "", nil); gain != nil {
		t.Fatalf("no tags and no measurement gave a gain: %+v", gain)
	}
}
