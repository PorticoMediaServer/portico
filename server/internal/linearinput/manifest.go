package linearinput

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

// rewrite parses URI-bearing HLS constructs, including alternate audio and
// encryption keys. Unsupported optional URI constructs are dropped so FFmpeg
// cannot resolve an unreviewed source path itself.
func (g *Gateway) rewrite(data []byte, base string, depth int) ([]byte, error) {
	if len(data) > maxManifest || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, ErrFormat
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.Contains(text, "\r") {
		return nil, ErrFormat
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 20000 || len(lines) == 0 || strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return nil, ErrFormat
	}
	lines = selectVariant(lines)
	variant := false
	for i, line := range lines {
		if len(line) > 16384 {
			return nil, ErrFormat
		}
		line = strings.TrimSpace(line)
		lines[i] = line
		if line == "" {
			continue
		}
		if line[0] != '#' {
			kind := "media"
			if variant {
				kind = "manifest"
			}
			uri, e := g.child(line, base, kind, depth+1)
			if e != nil {
				return nil, e
			}
			lines[i] = uri
			variant = false
			continue
		}
		tag, attrs, has := strings.Cut(line, ":")
		switch tag {
		case "#EXT-X-STREAM-INF":
			if variant {
				return nil, ErrFormat
			}
			variant = true
		case "#EXT-X-PART", "#EXT-X-PART-INF", "#EXT-X-PRELOAD-HINT", "#EXT-X-RENDITION-REPORT", "#EXT-X-DEFINE", "#EXT-X-CONTENT-STEERING", "#EXT-X-SESSION-DATA":
			lines[i] = ""
			continue
		case "#EXT-X-MEDIA", "#EXT-X-I-FRAME-STREAM-INF", "#EXT-X-KEY", "#EXT-X-SESSION-KEY", "#EXT-X-MAP":
			if !has {
				return nil, ErrFormat
			}
			a, e := parseAttributes(attrs)
			if e != nil {
				return nil, e
			}
			kind := "manifest"
			if tag == "#EXT-X-MAP" {
				kind = "media"
			}
			if tag == "#EXT-X-KEY" || tag == "#EXT-X-SESSION-KEY" {
				method := attribute(a, "METHOD")
				if method == "NONE" {
					if attribute(a, "URI") != "" {
						return nil, ErrFormat
					}
					continue
				}
				if method != "AES-128" || (attribute(a, "KEYFORMAT") != "" && attribute(a, "KEYFORMAT") != "identity") {
					return nil, ErrFormat
				}
				kind = "key"
			}
			changed := false
			for n, p := range a {
				if p.key == "URI" {
					uri, e := g.child(p.value, base, kind, depth+1)
					if e != nil {
						return nil, e
					}
					a[n].value = uri
					a[n].quoted = true
					changed = true
				}
			}
			if !changed && tag != "#EXT-X-MEDIA" {
				return nil, ErrFormat
			}
			lines[i] = tag + ":" + formatAttributes(a)
		default:
			// Reject extension URI/path fields, even when the current FFmpeg build might
			// ignore them. Future builds must not silently widen the network boundary.
			if strings.Contains(strings.ToUpper(line), "URI=") || strings.Contains(strings.ToUpper(line), "URL=") {
				lines[i] = ""
			}
		}
	}
	if variant {
		return nil, ErrFormat
	}
	// Only the selected input master declares this source's caption group.
	// Nested audio/subtitle masters must never replace the video declaration.
	if depth == 0 {
		declarations, _ := selectedCaptions(lines)
		g.mu.Lock()
		g.captions = declarations
		g.mu.Unlock()
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// Select one original-quality variant before any decoder sees the master.
// Alternate tracks in that variant's groups remain available to capture.
func selectVariant(lines []string) []string {
	best, bits := -1, int64(-1)
	for i, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			a, e := parseAttributes(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
			if e != nil {
				continue
			}
			n, _ := strconv.ParseInt(attribute(a, "BANDWIDTH"), 10, 64)
			if n > bits {
				best, bits = i, n
			}
		}
	}
	if best < 0 {
		return lines
	}
	selected, _ := parseAttributes(strings.TrimPrefix(lines[best], "#EXT-X-STREAM-INF:"))
	groups := map[string]bool{}
	for _, key := range []string{"AUDIO", "VIDEO", "SUBTITLES", "CLOSED-CAPTIONS"} {
		if group := attribute(selected, key); group != "" {
			groups[group] = true
		}
	}
	kept := make([]string, 0, len(lines))
	skip := false
	for i, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF:") {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MEDIA:") {
			attrs, e := parseAttributes(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
			if e != nil || !groups[attribute(attrs, "GROUP-ID")] {
				continue
			}
		}
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			skip = i != best
			if skip {
				continue
			}
		}
		if skip && line != "" && line[0] != '#' {
			skip = false
			continue
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	return kept
}
