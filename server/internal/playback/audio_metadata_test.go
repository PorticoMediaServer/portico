package playback

import (
	"math"
	"testing"
)

func TestManifestLanguageCanonicalAndInjection(t *testing.T) {
	for raw, want := range map[string]string{"eng": "en", "fra": "fr", "fre": "fr", "deu": "de", "ger": "de", "EN-us": "en-US", "zh-Hant-TW": "zh-Hant-TW", "und": "und", "": "und", "zzzz": "und", "en\n\"URI=evil": "und", "en_US": "und", "en,fr": "und", "not a language": "und"} {
		if got := manifestAudioLanguage(raw); got != want {
			t.Errorf("%q =>%q wanted%q", raw, got, want)
		}
	}
}
func TestRenditionRateWindowAndShortFallback(t *testing.T) {
	p := audioPlaylist{target: 6, segments: []audioSegment{{600, 6}, {1800, 6}, {300, 1}}}
	r, e := measureAudioRate(p, 3)
	if e != nil {
		t.Fatal(e)
	}
	// Valid6s and7s windows; the final1s segment alone must not determine peak.
	if math.Abs(r.peak-2400) > 1e-8 || math.Abs(r.average-(2700.0*8/13)) > 1e-8 {
		t.Fatal(r)
	}
	short := audioPlaylist{target: 1, segments: []audioSegment{{100, .1}}}
	r, e = measureAudioRate(short, 1)
	if e != nil || r.peak != 8000 {
		t.Fatal(r, e)
	}
	for _, p := range []audioPlaylist{{target: 6}, {target: 6, segments: []audioSegment{{1, 0}}}, {target: 6, segments: []audioSegment{{1, math.NaN()}}}, {target: 6, segments: []audioSegment{{audioArtifactBytes + 1, 6}}}} {
		if _, e = measureAudioRate(p, len(p.segments)); e == nil {
			t.Fatal("invalid rate admitted")
		}
	}
}
