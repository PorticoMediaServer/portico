package segmentdetect

import (
	"math/rand/v2"
	"testing"
)

// picture is ordinary footage: a lit scene with moving shapes and grain.
func picture(r *rand.Rand, t int) []byte {
	out := make([]byte, FrameWidth*FrameHeight)
	base := r.IntN(120) + 50
	for y := range FrameHeight {
		for x := range FrameWidth {
			out[y*FrameWidth+x] = byte(max(0, min(255, base+(x+t*3)%40-20+y/3+r.IntN(9)-4)))
		}
	}
	return out
}

// crawl is white lines of "type" on black, moved up by speed rows per frame.
func crawl(t, speed int) []byte {
	out := make([]byte, FrameWidth*FrameHeight)
	for y := range FrameHeight {
		line := (y + t*speed) % 12
		if line >= 3 {
			continue
		}
		for x := 60; x < 196; x++ {
			if (x*7+(y+t*speed)/12*13)%5 < 3 {
				out[y*FrameWidth+x] = 230
			}
		}
	}
	return out
}

func classify(t *testing.T, frames [][]byte) []FrameFeature {
	t.Helper()
	r := NewFrameReader()
	out := make([]FrameFeature, len(frames))
	for i, f := range frames {
		var err error
		if out[i], err = r.Read(f); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestFrameClasses(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	features := classify(t, [][]byte{picture(r, 0), make([]byte, FrameWidth*FrameHeight), crawl(0, 4), crawl(1, 4)})
	if features[0].Class != FramePicture || features[1].Class != FrameBlack || features[2].Class != FrameText || features[3].Class != FrameText {
		t.Fatalf("classes %+v", features)
	}
	if features[3].Scroll != 4 {
		t.Fatalf("scroll %d, want 4", features[3].Scroll)
	}
}

func TestMovieCreditsKeepTheMidAndPostCreditsScenes(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	var frames [][]byte
	add := func(n int, make func(i int) []byte) {
		for i := range n {
			frames = append(frames, make(i))
		}
	}
	// 10 min of film, then 90 s of credits, a 40 s mid-credits scene, 120 s of
	// crawl, 3 s black, a 30 s post-credits scene and 4 s of black.
	add(600, func(i int) []byte { return picture(r, i) })
	add(90, func(i int) []byte { return crawl(i, 3) })
	add(40, func(i int) []byte { return picture(r, i) })
	add(120, func(i int) []byte { return crawl(i, 3) })
	add(3, func(int) []byte { return make([]byte, FrameWidth*FrameHeight) })
	add(30, func(i int) []byte { return picture(r, i) })
	add(4, func(int) []byte { return make([]byte, FrameWidth*FrameHeight) })
	duration := int64(len(frames)) * 1e6
	got := VisualCredits(classify(t, frames), 0, 1e6, duration)
	if len(got) != 2 {
		t.Fatalf("detections %+v", got)
	}
	within(t, "credits start", got[0].StartUS, 600e6, 2e6)
	within(t, "credits end at the mid-credits scene", got[0].EndUS, 690e6, 2e6)
	within(t, "crawl start", got[1].StartUS, 730e6, 2e6)
	within(t, "crawl end at the post-credits scene", got[1].EndUS, 853e6, 2e6)
	if !got[0].FollowedByScene || !got[1].FollowedByScene || got[1].Confidence >= 0.85 {
		t.Fatalf("detections %+v", got)
	}
}

func TestDarkSceneIsNotCredits(t *testing.T) {
	// A night scene: dark, with a few bright, unbanded lights.
	var frames [][]byte
	for i := range 300 {
		f := make([]byte, FrameWidth*FrameHeight)
		for p := range f {
			f[p] = byte(18 + (p*7+i)%9)
		}
		for y := 40; y < 60; y++ {
			for x := 100 + i%5; x < 120+i%5; x++ {
				f[y*FrameWidth+x] = 235
			}
		}
		frames = append(frames, f)
	}
	if got := VisualCredits(classify(t, frames), 0, 1e6, 300e6); len(got) != 0 {
		t.Fatalf("a night scene became credits: %+v", got)
	}
}
