package httpapi

import "testing"

// An original file is labelled as the container it is. Responses carry
// nosniff, so a player that trusts the label must be told the truth.
func TestOriginalContentTypeFollowsTheContainer(t *testing.T) {
	for _, test := range []struct{ container, video, want string }{
		{"mp4", "h264", "video/mp4"},
		{"m4v", "h264", "video/mp4"},
		{"mov", "h264", "video/mp4"},
		{"mkv", "hevc", "video/x-matroska"},
		{"webm", "vp9", "video/webm"},
		{"avi", "mpeg4", "video/x-msvideo"},
		{"ts", "h264", "video/mp2t"},
		{"m2ts", "h264", "video/mp2t"},
		{"mts", "h264", "video/mp2t"},
		{"mpg", "mpeg2video", "video/mpeg"},
		{"mpeg", "mpeg2video", "video/mpeg"},
		{"vob", "mpeg2video", "video/mpeg"},
		{"unheard-of", "h264", "application/octet-stream"},
		{"flac", "", "audio/flac"},
		{"mp3", "", "audio/mpeg"},
		{"m4a", "", "audio/mp4"},
	} {
		if got := originalContentType(test.container, test.video); got != test.want {
			t.Errorf("%s (video %q) is labelled %s, want %s", test.container, test.video, got, test.want)
		}
	}
}
