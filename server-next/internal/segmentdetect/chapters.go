package segmentdetect

import (
	"regexp"
	"strings"
)

// ChapterProvenance names segments taken from chapter titles the file carries:
// someone else's labelling, offered to viewers but never skipped unattended.
const ChapterProvenance = "embedded_chapter_label:v1"

// Chapter titles, matched as whole words after lower-casing. "Prologue" and
// "cold open" are story, not segments. A next-episode preview is published as
// an outro: it is closing material after the story, and every client already
// offers the next episode during one.
var chapterKinds = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"recap", regexp.MustCompile(`\b(recap|previously( on)?|last time( on)?|story so far)\b`)},
	{"outro", regexp.MustCompile(`\b(preview|next (episode|time|week)|coming up next|next on)\b`)},
	{"intro", regexp.MustCompile(`\b(intro(duction)?|opening( titles?| credits| theme)?|main titles?|title sequence|theme song|op)\b`)},
	{"credits", regexp.MustCompile(`\b(credits?|end titles?|closing titles?|ending|outro|ed)\b`)},
	{"commercial", regexp.MustCompile(`\b(commercials?|advert(isement)?s?|ad break|sponsor)\b`)},
}

// ChapterKind returns the segment a chapter title names, or "".
func ChapterKind(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" || strings.Contains(t, "prologue") || strings.Contains(t, "cold open") {
		return ""
	}
	for _, k := range chapterKinds {
		if k.pattern.MatchString(t) {
			return k.kind
		}
	}
	return ""
}
