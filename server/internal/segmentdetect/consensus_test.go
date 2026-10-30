package segmentdetect

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect/segmentsynth"
)

type episode struct {
	coldOpen, body, post float64
	introLength          float64
}

// render builds one synthetic episode and fingerprints its whole audio.
func (e episode) fingerprint(t *testing.T, seed uint64, intro, credits segmentsynth.Signal) (Fingerprint, int64) {
	t.Helper()
	parts := segmentsynth.Programme{}
	if e.coldOpen > 0 {
		parts = append(parts, segmentsynth.Part{Signal: segmentsynth.Speech(seed), Length: e.coldOpen})
	}
	introLength := e.introLength
	if introLength == 0 {
		introLength = 50
	}
	parts = append(parts,
		segmentsynth.Part{Signal: intro, Length: introLength},
		segmentsynth.Part{Signal: segmentsynth.Silence(), Length: 1},
		segmentsynth.Part{Signal: segmentsynth.Speech(seed + 1000), Length: e.body},
		segmentsynth.Part{Signal: credits, Length: 40})
	if e.post > 0 {
		parts = append(parts, segmentsynth.Part{Signal: segmentsynth.Speech(seed + 2000), Length: e.post})
	} else {
		parts = append(parts, segmentsynth.Part{Signal: segmentsynth.Silence(), Length: 2})
	}
	f := NewFingerprinter(0, 0)
	if err := f.WritePCM(parts.Render(SampleRate)); err != nil {
		t.Fatal(err)
	}
	return f.Finish(), int64(parts.Duration() * 1e6)
}

func within(t *testing.T, label string, got, want int64, toleranceUS int64) {
	t.Helper()
	if d := got - want; d > toleranceUS || d < -toleranceUS {
		t.Errorf("%s = %.2fs, want %.2fs (±%.2fs)", label, float64(got)/1e6, float64(want)/1e6, float64(toleranceUS)/1e6)
	}
}

func TestSeasonIntroAndCreditsFromSharedThemes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	episodes := []episode{{coldOpen: 0, body: 150}, {coldOpen: 75, body: 140, post: 20}, {coldOpen: 140, body: 200}, {coldOpen: 32, body: 160, post: 15}, {coldOpen: 5, body: 145}}
	prints := make([]Fingerprint, len(episodes))
	durations := make([]int64, len(episodes))
	for i, e := range episodes {
		prints[i], durations[i] = e.fingerprint(t, uint64(100+i), segmentsynth.Theme(1), segmentsynth.Theme(2))
	}
	for i, e := range episodes {
		var others []Fingerprint
		for j := range prints {
			if j != i {
				others = append(others, prints[j])
			}
		}
		pairs := MatchRegion(prints[i], others)
		intro, ok := Intro(prints[i], pairs, durations[i])
		if !ok {
			t.Fatalf("episode %d: no intro", i)
		}
		wantStart := int64(e.coldOpen * 1e6)
		if wantStart <= introSnapUS {
			wantStart = 0
		}
		within(t, "intro start", intro.StartUS, wantStart, 400_000)
		// The intro's end runs on through the second of silence after it.
		within(t, "intro end", intro.EndUS, int64((e.coldOpen+51)*1e6), 600_000)
		if intro.Support < 3 || intro.Confidence < 0.85 {
			t.Errorf("episode %d intro support %d confidence %.2f", i, intro.Support, intro.Confidence)
		}
		credits, ok := Credits(prints[i], pairs, durations[i])
		if !ok {
			t.Fatalf("episode %d: no credits", i)
		}
		creditsAt := int64((e.coldOpen + 51 + e.body) * 1e6)
		within(t, "credits start", credits.StartUS, creditsAt, 400_000)
		if e.post > 0 {
			if !credits.FollowedByScene {
				t.Errorf("episode %d: the post-credits scene was swallowed", i)
			}
			within(t, "credits end", credits.EndUS, creditsAt+40e6, 600_000)
		} else if credits.EndUS != durations[i] || credits.FollowedByScene {
			t.Errorf("episode %d: credits end %d, want the end of the file %d", i, credits.EndUS, durations[i])
		}
	}
}

func TestShowWithoutIntroFindsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	var prints []Fingerprint
	var durations []int64
	for i := range 4 {
		// Every episode has its own music where an intro would be.
		e := episode{coldOpen: 30, body: 120}
		f, d := e.fingerprint(t, uint64(200+i), segmentsynth.Theme(uint64(50+i)), segmentsynth.Theme(uint64(70+i)))
		prints, durations = append(prints, f), append(durations, d)
	}
	pairs := MatchRegion(prints[0], prints[1:])
	if d, ok := Intro(prints[0], pairs, durations[0]); ok {
		t.Fatalf("found an intro in a show without one: %+v", d)
	}
	if d, ok := Credits(prints[0], pairs, durations[0]); ok {
		t.Fatalf("found credits where every episode differs: %+v", d)
	}
}

func TestPairOnlyEvidenceIsASuggestion(t *testing.T) {
	a, da := episode{coldOpen: 20, body: 120}.fingerprint(t, 300, segmentsynth.Theme(9), segmentsynth.Theme(10))
	b, _ := episode{coldOpen: 60, body: 130}.fingerprint(t, 301, segmentsynth.Theme(9), segmentsynth.Theme(10))
	intro, ok := Intro(a, MatchRegion(a, []Fingerprint{b}), da)
	if !ok {
		t.Fatal("no intro from a pair")
	}
	if intro.Confidence >= 0.85 {
		t.Fatalf("a single pair reached automatic confidence %.2f", intro.Confidence)
	}
}
