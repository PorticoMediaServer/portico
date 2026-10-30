package decoder

import (
	"strings"
	"testing"
)

func TestHDR10MetadataReachesSoftwareEncoders(t *testing.T) {
	target := ConversionTarget{VideoCodec: "av1", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "hdr10", MasterDisplay: "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)", MaxCLL: "1000,400"}
	params := svtHDRParams(target)
	if !strings.Contains(params, ":mastering-display=G(0.2650,0.6900)B(0.1500,0.0600)R(0.6800,0.3200)WP(0.3127,0.3290)L(1000.0000,0.0050)") || !strings.Contains(params, ":content-light=1000,400") {
		t.Fatal(params)
	}
	target.DynamicRange = "hlg"
	if svtHDRParams(target) != "" {
		t.Fatal("HLG carries no static metadata")
	}
	if _, err := (ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "hdr10", MasterDisplay: "G(1,2)", Packaging: "fmp4_hls"}).Normalized(); err == nil {
		t.Fatal("a malformed mastering description was accepted")
	}
}
