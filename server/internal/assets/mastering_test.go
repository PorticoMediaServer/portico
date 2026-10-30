package assets

import (
	"encoding/json"
	"testing"
)

// HDR10 static metadata is read from the stream's side data (Matroska colour
// elements, MP4 mdcv/clli) into the encoder's own notation.
func TestMasteringMetadataIsCapturedFromSideData(t *testing.T) {
	var s probeStreamDetail
	raw := `{"pix_fmt":"yuv420p10le","color_transfer":"smpte2084","side_data_list":[
		{"side_data_type":"Mastering display metadata","red_x":"34000/50000","red_y":"16000/50000","green_x":"13250/50000","green_y":"34500/50000","blue_x":"7500/50000","blue_y":"3000/50000","white_point_x":"15635/50000","white_point_y":"16450/50000","min_luminance":"50/10000","max_luminance":"10000000/10000"},
		{"side_data_type":"Content light level metadata","max_content":1000,"max_average":400}]}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	d := streamDetail("video", "hevc", 3840, 2160, s)
	if d.MasterDisplay != "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)" || d.MaxCLL != "1000,400" {
		t.Fatalf("got %q %q", d.MasterDisplay, d.MaxCLL)
	}
	// A partial or nonsensical description is dropped, never written as the truth.
	partial := probeSideData{Type: "Mastering display metadata", RedX: "34000/50000", MaxLuminance: "1000/1", MinLuminance: "0/1"}
	if partial.masteringDisplay() != "" {
		t.Fatal("a partial mastering description was kept")
	}
	inverted := probeSideData{RedX: "1/2", RedY: "1/2", GreenX: "1/2", GreenY: "1/2", BlueX: "1/2", BlueY: "1/2", WhiteX: "1/2", WhiteY: "1/2", MaxLuminance: "1/1", MinLuminance: "2/1"}
	if inverted.masteringDisplay() != "" {
		t.Fatal("a minimum above the maximum was kept")
	}
	if (probeSideData{MaxContent: "0", MaxAverage: "0"}).contentLightLevel() != "" {
		t.Fatal("an empty light level was kept")
	}
}
