package segmentdetect

import "testing"

func TestChapterKinds(t *testing.T) {
	for title, want := range map[string]string{
		"Intro": "intro", "Opening": "intro", "Opening Credits": "intro", "OP": "intro", "Main Titles": "intro",
		"Previously on...": "recap", "Recap": "recap",
		"End Credits": "credits", "ED": "credits", "Ending": "credits", "Credits": "credits",
		"Preview": "outro", "Next Episode": "outro",
		"Commercial Break": "commercial",
		"Prologue":         "", "Cold Open": "", "Chapter 3": "", "The Opera": "", "Edward": "", "": "",
	} {
		if got := ChapterKind(title); got != want {
			t.Errorf("ChapterKind(%q) = %q, want %q", title, got, want)
		}
	}
}
