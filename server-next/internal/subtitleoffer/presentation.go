// Package subtitleoffer holds the presentation rules shared by direct subtitle
// acceptance and the bounded informational publication worker.
package subtitleoffer

import (
	"golang.org/x/text/language"
	"strings"
	"unicode"
	"unicode/utf8"
)

func textField(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func Language(s string) (string, bool) {
	if s == "" {
		return "und", true
	}
	if !textField(s, 64) {
		return "", false
	}
	tag, err := language.Parse(s)
	if err != nil {
		return "", false
	}
	return tag.String(), true
}
func Title(s string) string {
	if !textField(s, 160) {
		return "Subtitles"
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "Subtitles"
	}
	return s
}
func HearingImpairedTitle(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "sdh") || strings.Contains(s, "hearing") || strings.Contains(s, "[cc]") || strings.Contains(s, "closed caption")
}
func ValidFormat(s string) bool {
	switch s {
	case "srt", "vtt", "ass", "ssa", "pgs", "sup", "vobsub", "idx", "dvb", "smi", "sami", "ttml", "dfxp":
		return true
	}
	return false
}
func TextFormat(s string) bool {
	switch s {
	case "srt", "vtt", "ass", "ssa", "smi", "sami", "ttml", "dfxp":
		return true
	}
	return false
}

type Presentation struct{ Language, Title, Renderer string }

func Present(format, lang, title string, hearing bool) Presentation {
	normalized, ok := Language(lang)
	if !ok {
		normalized = "und"
	}
	title = Title(title)
	if hearing && !HearingImpairedTitle(title) {
		title += " SDH"
	}
	renderer := "external_text"
	if !TextFormat(format) || format == "ass" || format == "ssa" {
		renderer = "burn_in"
	}
	return Presentation{normalized, title, renderer}
}
