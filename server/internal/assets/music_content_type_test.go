package assets

import "testing"

func TestMusicOriginalContentTypeUsesContainerFacts(t *testing.T) {
	for _, row := range []struct{ container, mime string }{
		{"caf", "audio/x-caf"}, {"w64", "application/octet-stream"}, {"unknown", "application/octet-stream"}, {"", "application/octet-stream"},
		{"mov", "audio/mp4"}, {"mp4", "audio/mp4"}, {"m4a", "audio/mp4"}, {"m4b", "audio/mp4"},
		{"mpc7", "audio/x-musepack"}, {"mpc8", "audio/x-musepack"}, {"matroska", "audio/x-matroska"}, {"mkv", "audio/x-matroska"}, {"webm", "audio/webm"},
		{"ape", "audio/x-ape"}, {"wv", "audio/x-wavpack"}, {"dsf", "audio/x-dsf"},
	} {
		if got := OriginalContentType(row.container, ""); got != row.mime {
			t.Fatalf("%q: %s want %s", row.container, got, row.mime)
		}
	}
	if got := OriginalContentType("mkv", "h264"); got != "video/x-matroska" {
		t.Fatal("video type changed", got)
	}
}
