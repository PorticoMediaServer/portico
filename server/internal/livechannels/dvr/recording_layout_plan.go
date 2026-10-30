package dvr

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// templateValues uses saved programme facts and current authorized source/rule
// names. It never makes a missing season/year/group into a filename format.
func recordingTemplateValues(r Recording, source, group string) map[string]string {
	p := r.Programme
	title := p.Title
	if p.Subtitle != "" {
		title = p.Subtitle
	}
	v := map[string]string{"series": p.Title, "title": title, "channel": r.Channel.Name, "channelNumber": r.Channel.Number, "source": source, "group": group}
	if p.Year > 0 {
		v["year"] = strconv.Itoa(p.Year)
	}
	if p.Episode != nil {
		if p.Episode.Season > 0 {
			v["season"] = strconv.Itoa(p.Episode.Season)
		}
		if p.Episode.Number > 0 {
			v["episode"] = strconv.Itoa(p.Episode.Number)
		}
	}
	if start, err := time.Parse(time.RFC3339Nano, r.Start); err == nil {
		local := start.In(time.Local)
		v["date"], v["time"] = local.Format("2006-01-02"), local.Format("15-04")
	}
	return v
}
func trimRecordingSegment(v string, limit int) string {
	v = strings.TrimFunc(v, func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
	if len(v) > limit {
		v = v[:limit]
		for !utf8.ValidString(v) {
			v = v[:len(v)-1]
		}
		v = strings.TrimFunc(v, func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
	}
	return v
}
func recordingTokenValue(v string) string {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, v)
	return trimRecordingSegment(v, 200)
}

// renderRecordingTemplate returns fallback for only this recording when any
// used token is absent. It does not change the selected defaults or fail capture.
func renderRecordingTemplate(template string, values map[string]string, extension string) (relative string, fallback bool, err error) {
	if template == "" {
		return "", true, nil
	}
	parts, ok := parseRecordingTemplate(template)
	if !ok {
		return "", false, ErrInvalid
	}
	// This engine currently seals actual MPEG-TS. Other extensions may be supplied
	// only by a real container producer, never inferred from a guide/title path.
	if extension == "" || extension[0] != '.' || strings.ContainsAny(extension, `/\:*?"<>|`) || len(extension) > 16 {
		return "", false, ErrInvalid
	}
	var out strings.Builder
	for _, part := range parts {
		if part.token == "" {
			out.WriteString(part.literal)
			continue
		}
		value := values[part.token]
		if value == "" {
			return "", true, nil
		}
		if part.padded {
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || n < 0 {
				return "", true, nil
			}
			value = fmt.Sprintf("%02d", n)
		}
		value = recordingTokenValue(value)
		if value == "" {
			return "", true, nil
		}
		out.WriteString(value)
	}
	segments := strings.Split(out.String(), "/")
	for i, segment := range segments {
		limit := 200
		if i == len(segments)-1 {
			limit -= len(extension)
		}
		segments[i] = trimRecordingSegment(segment, limit)
		if segments[i] == "" {
			return "", true, nil
		}
	}
	relative = strings.Join(segments, "/") + extension
	if !layoutRelativePath(relative) {
		return "", false, ErrInvalid
	}
	return relative, false, nil
}

// layoutCollisionCandidate preserves the drawn base and numbers only an actual
// collision. The final component, including extension and suffix, stays bounded.
func layoutCollisionCandidate(relative string, number int) (string, error) {
	if !layoutRelativePath(relative) || number < 1 {
		return "", ErrInvalid
	}
	if number == 1 {
		return relative, nil
	}
	directory, base := path.Split(relative)
	extension := path.Ext(base)
	suffix := fmt.Sprintf(" (%d)", number)
	if extension == "" || len(extension)+len(suffix) >= 200 {
		return "", ErrInvalid
	}
	stem := trimRecordingSegment(strings.TrimSuffix(base, extension), 200-len(extension)-len(suffix))
	if stem == "" {
		return "", ErrInvalid
	}
	return directory + stem + suffix + extension, nil
}
