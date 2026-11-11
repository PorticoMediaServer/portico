package mediaengine

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// What the engine passed before it read settings was DefaultDeliveryConfiguration() as it
// is, not normalised, with converting allowed.
func TestReviewNothingSavedEqualsWhatWasPassedBefore(t *testing.T) {
	before, now := streaming.DefaultDeliveryConfiguration(), delivery(settings.Streaming.Get(nil))
	fields := changed(before, now)
	t.Logf("fields that differ from the un-normalised default: %v", fields)
	if len(fields) != 1 || fields[0] != "ConversionMode" || now.AudioConversionAllowed(false) != before.AudioConversionAllowed(true) ||
		now.VideoConversionAllowed(false) != before.VideoConversionAllowed(true) {
		t.Fatalf("differs in %v", fields)
	}
}

// A document a client could store that is broken or hostile never panics the reader.
func TestReviewHostileCapabilityDocumentsDoNotPanic(t *testing.T) {
	for _, raw := range []string{``, `null`, `[]`, `"x"`, `{}`, `{"version":99}`, `{"version":"2"}`, `{"version":2}`, `{"version":2,"client":null,"engines":null}`,
		`{"version":2,"client":{"family":7},"engines":[]}`, `{"version":2,"engines":{"native":null}}`, `{"version":2,"engines":{"native":{"video":null,"audio":[null]}}}`,
		`{"version":2,"display":{"dynamicRanges":[null,1,{}]}}`, `{"version":2,"audioDecode":[null,{"codec":null}]}`,
		strings.Repeat(`{"a":`, 5000) + `1` + strings.Repeat(`}`, 5000), `{"version":2,"client":{"family":"` + strings.Repeat("x", 1<<20) + `"}}`,
		`{"version":-1}`, `{"version":2e308}`, "{\"version\":2,\"client\":{\"family\":\"\x00\"}}"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%.60s panicked: %v", raw, r)
				}
			}()
			d, err := streaming.ParseCapabilityDocument([]byte(raw))
			if err == nil {
				_ = d.Profile(streaming.EngineNative)
				_ = d.DecodeCaps()
			}
		}()
	}
}
