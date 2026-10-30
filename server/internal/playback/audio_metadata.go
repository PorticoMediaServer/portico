package playback

import (
	"errors"
	"golang.org/x/text/language"
	"math"
	"regexp"
)

var manifestLanguageChars = regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)

func manifestAudioLanguage(raw string) string {
	if len(raw) == 0 || len(raw) > 63 || !manifestLanguageChars.MatchString(raw) {
		return "und"
	}
	tag, e := language.Parse(raw)
	if e != nil {
		return "und"
	}
	out := tag.String()
	if len(out) > 63 || !manifestLanguageChars.MatchString(out) {
		return "und"
	}
	return out
}

type audioSegment struct {
	bytes    int64
	duration float64
}
type audioPlaylist struct {
	segments []audioSegment
	target   float64
}
type audioRate struct {
	count                   int
	bytes                   int64
	duration, peak, average float64
}

func measureAudioRate(p audioPlaylist, count int) (audioRate, error) {
	out := audioRate{}
	if count < 1 || count > len(p.segments) || count > 1201 || p.target <= 0 || math.IsNaN(p.target) || math.IsInf(p.target, 0) {
		return out, errors.New("invalid rate sample")
	}
	for _, s := range p.segments[:count] {
		if s.bytes <= 0 || s.bytes > audioArtifactBytes || s.duration <= 0 || math.IsNaN(s.duration) || math.IsInf(s.duration, 0) {
			return out, errors.New("invalid rate segment")
		}
		out.bytes += s.bytes
		out.duration += s.duration
	}
	if out.bytes > audioArtifactBytes {
		return out, errors.New("rate sample exceeds artifact budget")
	}
	out.count = count
	out.average = float64(out.bytes) * 8 / out.duration
	// Bounded by1201segments; only contiguous windows within0.5–1.5 target duration qualify.
	for i := 0; i < count; i++ {
		var bytes int64
		duration := 0.0
		for j := i; j < count; j++ {
			duration += p.segments[j].duration
			bytes += p.segments[j].bytes
			if duration > 1.5*p.target+1e-6 {
				break
			}
			if duration+1e-6 >= 0.5*p.target {
				out.peak = math.Max(out.peak, float64(bytes)*8/duration)
			}
		}
	}
	if out.peak == 0 {
		out.peak = out.average
	} // Entire short presentation belowhalf-target: whole-sample fallback is recorded asestimate.
	if out.peak > 1e12 || out.average > 1e12 {
		return out, errors.New("measured rate exceeds supported bounds")
	}
	if math.IsNaN(out.peak) || math.IsInf(out.peak, 0) || math.IsNaN(out.average) || math.IsInf(out.average, 0) {
		return out, errors.New("invalid measured rates")
	}
	return out, nil
}
