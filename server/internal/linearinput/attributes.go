package linearinput

import (
	"errors"
	"strings"
)

var ErrFormat = errors.New("The live source uses an unsupported or invalid stream format.")

type attr struct {
	key, value string
	quoted     bool
}

func parseAttributes(text string) ([]attr, error) {
	out := []attr{}
	if text == "" || strings.HasSuffix(text, ",") {
		return nil, ErrFormat
	}
	seen := map[string]bool{}
	for text != "" {
		key, rest, ok := strings.Cut(text, "=")
		if !ok || key == "" || seen[key] {
			return nil, ErrFormat
		}
		for _, r := range key {
			if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return nil, ErrFormat
			}
		}
		seen[key] = true
		p := attr{key: key}
		if strings.HasPrefix(rest, "\"") {
			p.quoted = true
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, ErrFormat
			}
			p.value = rest[1 : 1+end]
			rest = rest[2+end:]
			if rest != "" && !strings.HasPrefix(rest, ",") {
				return nil, ErrFormat
			}
			text = strings.TrimPrefix(rest, ",")
		} else {
			p.value, text, _ = strings.Cut(rest, ",")
			if p.value == "" || strings.ContainsAny(p.value, " \t\"\\") {
				return nil, ErrFormat
			}
		}
		if strings.ContainsAny(p.value, "\r\n\x00") {
			return nil, ErrFormat
		}
		out = append(out, p)
		if len(out) > 64 {
			return nil, ErrFormat
		}
	}
	return out, nil
}
func attribute(a []attr, key string) string {
	for _, p := range a {
		if p.key == key {
			return p.value
		}
	}
	return ""
}
func formatAttributes(a []attr) string {
	out := make([]string, len(a))
	for i, p := range a {
		v := p.value
		if p.quoted {
			v = "\"" + v + "\""
		}
		out[i] = p.key + "=" + v
	}
	return strings.Join(out, ",")
}
