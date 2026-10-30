package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"testing"
)

func TestWebAACUsesOnlyQualifiedProfile(t *testing.T) {
	cap := AudioDecodeCap{Codec: "aac", Containers: []string{"mp4"}, Via: "webcodecs"}
	f := audiofacts.Facts{Codec: "aac", Container: "mp4", Profile: "HE-AAC", SampleRate: 48000, Channels: 2}
	if cap.decodes(f) {
		t.Fatal("HE-AAC declared as LC")
	}
	f.Profile = "LC"
	if !cap.decodes(f) {
		t.Fatal("LC refused")
	}
	cap.Via = "ExtAudioFile"
	f.Profile = "HE-AAC"
	if !cap.decodes(f) {
		t.Fatal("native profile was constrained by web qualification")
	}
}
