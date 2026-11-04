package playbackv1

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func TestOptionsFrameObservationSchemaIsRequiredAndNullable(t *testing.T) {
	schema, err := apikit.Schema(reflect.TypeFor[VideoStream]())
	if err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	required := map[string]bool{}
	for _, field := range schema["required"].([]string) {
		required[field] = true
	}
	for _, field := range []string{"hdr10Plus", "dolbyVisionRpu", "dolbyVisionBaseLayer"} {
		shape := props[field].(map[string]any)
		if !required[field] || shape["nullable"] != true || shape["type"] != "boolean" {
			t.Fatal("frame observation must be an always-sent boolean or null", field, shape)
		}
	}
	for _, field := range []string{"hdr", "dolbyVisionProfile", "dolbyVisionCompatibility", "masterDisplay", "maxCll"} {
		if props[field].(map[string]any)["nullable"] == true {
			t.Fatal("header-derived answer became nullable", field)
		}
	}
}

func TestOptionsFrameObservationsRemainUnknownWithHeaderOnlyFacts(t *testing.T) {
	for _, test := range []struct {
		name, transfer, wantHDR string
		profile, compatibility  int
	}{
		{"hdr10", "smpte2084", "hdr10", 0, 0},
		{"hlg", "arib-std-b67", "hlg", 0, 0},
		{"dolby-vision", "smpte2084", "dolbyvision", 8, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var version Version
			detail := assets.StreamDetail{DynamicRange: assets.DynamicRangeOf(test.transfer, test.profile), DolbyVisionProfile: test.profile, DolbyVisionCompatibility: test.compatibility, MasterDisplay: "header-mastering", MaxCLL: "1000,400", StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: assets.InterlaceFieldOrderProgressive}}
			(&Service{}).appendStream(&version, assets.Stream{Index: 0, Type: "video", Codec: "hevc", ColorTransfer: test.transfer, Detail: detail})
			raw, err := json.Marshal(version.Video[0])
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err = json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"hdr10Plus", "dolbyVisionRpu", "dolbyVisionBaseLayer"} {
				value, exists := wire[field]
				if !exists || value != nil {
					t.Fatalf("unobserved %s must be null, got %s", field, raw)
				}
			}
			if wire["hdr"] != test.wantHDR || wire["interlaced"] != false || wire["masterDisplay"] != "header-mastering" || wire["maxCll"] != "1000,400" {
				t.Fatalf("header facts changed: %s", raw)
			}
			if test.profile != 0 && (wire["dolbyVisionProfile"] != float64(test.profile) || wire["dolbyVisionCompatibility"] != float64(test.compatibility)) {
				t.Fatalf("DV configuration lost: %s", raw)
			}
		})
	}
}

func TestOptionsFrameObservationsPublishOnlyMeasuredValues(t *testing.T) {
	for _, test := range []struct {
		name            string
		detail          assets.StreamDetail
		plus, rpu, base any
	}{
		{"hdr10plus", assets.StreamDetail{HDR10Plus: true}, true, nil, nil},
		{"dv-positive", assets.StreamDetail{StreamBreadth: assets.StreamBreadth{DolbyVisionMetadataKnown: true, DolbyVisionRPU: true, DolbyVisionBaseLayer: true}}, nil, true, true},
		{"dv-observed-negative", assets.StreamDetail{StreamBreadth: assets.StreamBreadth{DolbyVisionMetadataKnown: true}}, nil, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var version Version
			(&Service{}).appendStream(&version, assets.Stream{Index: 0, Type: "video", Codec: "hevc", Detail: test.detail})
			raw, err := json.Marshal(version.Video[0])
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err = json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if wire["hdr10Plus"] != test.plus || wire["dolbyVisionRpu"] != test.rpu || wire["dolbyVisionBaseLayer"] != test.base {
				t.Fatalf("measured values changed: %s", raw)
			}
		})
	}
}
