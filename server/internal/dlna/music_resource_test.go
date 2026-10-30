package dlna

import (
	"strings"
	"testing"
)

func TestMusicResourceAliasesDescribeTheServedOriginal(t *testing.T) {
	for _, row := range []struct{ container, mime, suffix string }{
		{"caf", "audio/x-caf", ".caf"}, {"w64", "application/octet-stream", ".w64"},
		{"wavpack", "audio/x-wavpack", ".wv"}, {"dsdiff", "audio/x-dff", ".dff"},
		{"mpc7", "audio/x-musepack", ".mpc"}, {"mpc8", "audio/x-musepack", ".mpc"},
		{"matroska", "audio/x-matroska", ".mka"}, {"mkv", "audio/x-matroska", ".mka"},
		{"mov", "audio/mp4", ".m4a"}, {"asf", "audio/x-ms-wma", ".wma"},
	} {
		t.Run(row.container, func(t *testing.T) {
			xml := didl("http://home:8096", []object{{id: "item", res: &resource{token: "opaque", container: row.container}}})
			if !strings.Contains(xml, "http-get:*:"+row.mime+":") || !strings.Contains(xml, mediaPath+"opaque"+row.suffix+"</res>") {
				t.Fatal(xml)
			}
			if !strings.Contains(sourceProtocols(), "http-get:*:"+row.mime+":") {
				t.Fatal("ConnectionManager omits served original type", row.mime)
			}
		})
	}
	if got := resourceExtension("mkv", "h264"); got != ".mkv" {
		t.Fatal("video suffix changed", got)
	}
}
