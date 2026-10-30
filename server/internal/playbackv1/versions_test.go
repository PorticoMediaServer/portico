package playbackv1

import "testing"

// Review P5: two copies of a two-part title are two versions of two parts,
// grouped by shape (container, video codec, height), each in part order;
// files that are all distinct parts are one version; the rest stand alone.
func TestVersionsGroupPartsByShape(t *testing.T) {
	uhd := func(id string, part int) assetRow {
		return assetRow{id: id, container: "mkv", videoCodec: "hevc", height: 2160, part: part}
	}
	hd := func(id string, part int) assetRow {
		return assetRow{id: id, container: "mp4", videoCodec: "h264", height: 1080, part: part}
	}
	got := versionsOf([]assetRow{uhd("u1", 1), hd("h1", 1), uhd("u2", 2), hd("h2", 2)})
	if len(got) != 2 || len(got[0]) != 2 || got[0][0].id != "u1" || got[0][1].id != "u2" || got[1][0].id != "h1" || got[1][1].id != "h2" {
		t.Fatalf("two two-part copies: %+v", got)
	}
	if got := versionsOf([]assetRow{uhd("a", 1), hd("b", 2)}); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("distinct parts are one version: %+v", got)
	}
	if got := versionsOf([]assetRow{uhd("a", 0), hd("b", 0), hd("c", 0)}); len(got) != 3 {
		t.Fatalf("single files stand alone: %+v", got)
	}
}
