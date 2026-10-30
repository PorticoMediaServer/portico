package compactcatalog

import (
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var readingISOIssueDate = regexp.MustCompile(`[0-9]{4}-[0-9]{2}(?:-[0-9]{2})?`)

// ReadingIssueDate accepts one delimited ISO month/day in the actual inventory
// filename only. It supplies date precision, never type, cadence or grouping.
// Multiple dates, invalid calendar dates, date fragments inside identifiers and
// year-only/localized/ambiguous numeric dates remain absent.
func ReadingIssueDate(relative string) string {
	stem := strings.TrimSuffix(path.Base(relative), path.Ext(relative))
	if len(stem) > 4096 || !utf8.ValidString(stem) {
		return ""
	}
	found := readingISOIssueDate.FindAllStringIndex(stem, 2)
	if len(found) != 1 {
		return ""
	}
	start, end := found[0][0], found[0][1]
	boundary := func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) && r != '-'
	}
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(stem[:start])
		if !boundary(r) {
			return ""
		}
	}
	if end < len(stem) {
		r, _ := utf8.DecodeRuneInString(stem[end:])
		if !boundary(r) {
			return ""
		}
	}
	value := stem[start:end]
	if !ValidReadingIssueDate(value) {
		return ""
	}
	return value
}
func ValidReadingIssueDate(value string) bool {
	layout := "2006-01"
	if len(value) == 10 {
		layout = "2006-01-02"
	} else if len(value) != 7 {
		return false
	}
	parsed, err := time.Parse(layout, value)
	return err == nil && parsed.Year() > 0 && parsed.Format(layout) == value
}
