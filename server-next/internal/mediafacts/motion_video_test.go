package mediafacts

import "testing"

// Audio tags are read only for files without a motion-video stream; an image
// track (cover art is already set aside, chapter images use an image codec)
// keeps an audiobook's tags and chapters.
func TestHasMotionVideoIgnoresImageTracks(t *testing.T) {
	for _, c := range []struct {
		streams []Stream
		want    bool
	}{
		{nil, false},
		{[]Stream{{Type: "audio", Codec: "aac"}}, false},
		{[]Stream{{Type: "audio", Codec: "aac"}, {Type: "video", Codec: "mjpeg"}}, false},
		{[]Stream{{Type: "audio", Codec: "aac"}, {Type: "video", Codec: "png"}}, false},
		{[]Stream{{Type: "video", Codec: "h264"}, {Type: "audio", Codec: "aac"}}, true},
		{[]Stream{{Type: "video", Codec: "mjpeg"}, {Type: "video", Codec: "hevc"}}, true},
	} {
		if got := hasMotionVideo(Facts{Streams: c.streams}); got != c.want {
			t.Fatalf("%+v: got %v, want %v", c.streams, got, c.want)
		}
	}
}
