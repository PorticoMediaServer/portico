package dvr

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Recording templates are paths below an occurrence owner's managed namespace,
// never filesystem locators. The settings producer uses the same grammar as the
// engine. An empty template selects the original occurrence-private layout.
var recordingTemplateTokens = map[string]bool{
	"series": false, "title": false, "season": true, "episode": true, "year": false,
	"channel": false, "channelNumber": true, "date": false, "time": false, "source": false, "group": false,
}

type recordingTemplatePart struct {
	literal, token string
	padded         bool
}

func parseRecordingTemplate(value string) ([]recordingTemplatePart, bool) {
	if value == "" {
		return nil, true
	}
	if len(value) > 400 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return nil, false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return nil, false
		}
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return nil, false
		}
	}
	parts := []recordingTemplatePart{}
	for len(value) > 0 {
		before, tail, found := strings.Cut(value, "{")
		if strings.ContainsAny(before, "}\\:*?\"<>|") {
			return nil, false
		}
		if before != "" {
			parts = append(parts, recordingTemplatePart{literal: before})
		}
		if !found {
			break
		}
		name, after, closed := strings.Cut(tail, "}")
		if !closed {
			return nil, false
		}
		token, pad, padded := strings.Cut(name, ":")
		supports, known := recordingTemplateTokens[token]
		if !known || padded && (pad != "02" || !supports) {
			return nil, false
		}
		parts = append(parts, recordingTemplatePart{token: token, padded: padded})
		value = after
	}
	return parts, true
}

// ValidRecordingTemplate accepts the drawn presets and documented vocabulary.
// Validation does not advertise the layout engine or enable a settings writer.
func ValidRecordingTemplate(value string) bool { _, ok := parseRecordingTemplate(value); return ok }
