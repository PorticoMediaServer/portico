package streaming

import (
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// listPreferences is a preference reader that also answers the list accessor.
type listPreferences map[string][]string

func (listPreferences) Bool(string) bool           { return true }
func (listPreferences) Int(string) int             { return 0 }
func (listPreferences) Text(string) string         { return "" }
func (l listPreferences) List(key string) []string { return l[key] }

// A remote bitrate cap narrows every video codec's ceiling to it: an unlimited
// codec (0) and one above it become the ceiling, one below it is untouched. No
// ceiling leaves the profile equal, and the input's slice is never modified.
func TestWithVideoBitrateCeiling(t *testing.T) {
	in := BaselineClientProfile()
	in.Video = append(in.Video, ClientVideoCodec{Codec: "hevc", MaxBitrateBPS: 20_000_000}, ClientVideoCodec{Codec: "av1", MaxBitrateBPS: 1_000_000})
	got := WithVideoBitrateCeiling(in, 3_000_000)
	if len(got.Video) != 3 || got.Video[0].MaxBitrateBPS != 3_000_000 || got.Video[1].MaxBitrateBPS != 3_000_000 || got.Video[2].MaxBitrateBPS != 1_000_000 {
		t.Fatalf("narrowed codecs: %+v", got.Video)
	}
	if in.Video[0].MaxBitrateBPS != 0 || in.Video[1].MaxBitrateBPS != 20_000_000 || in.Video[2].MaxBitrateBPS != 1_000_000 {
		t.Fatalf("the input profile was modified: %+v", in.Video)
	}
	if equal := WithVideoBitrateCeiling(in, 0); !reflect.DeepEqual(equal, in) {
		t.Fatalf("ceiling 0 changed the profile: %+v", equal)
	}
}

// Every DTS stream is scanned as the "dts" codec with its variant beside it, so
// a device that declares DTS-HD must fold onto that codec. The fold used to keep
// a "dts_hd" name no source ever carries, which quietly converted DTS-HD audio
// for devices that take it as it is.
func TestDTSHDDeclarationMatchesTheScannedDTSCodec(t *testing.T) {
	device := func(audio string) ClientProfile {
		t.Helper()
		doc, err := ParseCapabilityDocument(
			[]byte(
				`{"version":2,"client":{"family":"shield"},"display":{"dynamicRanges":["sdr"]},"engines":{"native":{"name":"exoplayer","evidence":"declared","video":[{"codec":"h264","bitDepths":[8],"m` +
					`axWidth":1920,"maxHeight":1080}],"audio":[` + audio + `],"transports":[{"transport":"direct","containers":["mkv"],"video":["h264"],"audio":["dts"]}]}}}`,
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		return doc.Profile(EngineNative)
	}
	hdma := videoSource("mkv", "h264", avcDetail, SourceAudio{Codec: "dts", Channels: 8, StreamDetail: mediafacts.StreamDetail{Profile: "DTS-HD MA"}})
	for _, declared := range []string{"dts-hd", "DTS-HD", "dtshd", "dts_hd", "dts-hd ma"} {
		client := device(`{"codec":"` + declared + `","variants":["dts_hd_ma"],"maxChannels":8}`)
		if client.Audio[0].Codec != "dts" {
			t.Fatalf("%s stayed %q instead of the scanned codec", declared, client.Audio[0].Codec)
		}
		if p := decide(t, hdma, client, nil); p.Strategy != DeliveryOriginal {
			t.Fatalf("%s did not play as it is: %+v", declared, p)
		}
	}
	// A family-only DTS declaration still does not promise DTS-HD: the route is
	// refused outright (there is no other track and no encoder in this fixture).
	source := hdma
	client := device(`{"codec":"dts","maxChannels":8}`)
	policy := openPolicy()
	if p, err := planDelivery(DeliveryInput{Source: source, Client: client, Policy: policy, Target: autoTarget(policy, 1080), Config: DefaultDeliveryConfiguration(),
		TranscodingEnabled: true}); err == nil &&
		p.Strategy == DeliveryOriginal {
		t.Fatal("a bare DTS declaration claimed DTS-HD Master Audio")
	}
}
