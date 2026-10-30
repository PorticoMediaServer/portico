package reading

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxSrcsetBytes = 4096
const maxSrcsetCandidates = 16

// validateSrcset admits inert publication data, not responsive loading. URLs
// follow the existing archive resolver; renderers must strip srcset before DOM
// or loader exposure and independently rewrite the admitted fallback src.
//
// This is the bounded plain-reference subset of candidate parsing. URL tokens
// run to ASCII whitespace (commas inside a token are not separators). Only a
// single trailing comma may end a descriptorless URL; ambiguous/internal commas,
// parentheses, quoted URL tokens and ignored empty candidates are refused.
func validateSrcset(base, raw string, assets map[string]string) error {
	if len(raw) == 0 || len(raw) > maxSrcsetBytes || !utf8.ValidString(raw) {
		return ErrInvalid
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] == 127 || raw[i] < 32 && !cssSpace(raw[i]) {
			return ErrInvalid
		}
	}
	i, count := 0, 0
	mode := byte(0)
	paths, descriptors := map[string]bool{}, map[string]bool{}
	skipSpace := func() {
		for i < len(raw) && cssSpace(raw[i]) {
			i++
		}
	}
	for {
		skipSpace()
		if i == len(raw) || raw[i] == ',' {
			return ErrInvalid
		}
		start := i
		for i < len(raw) && !cssSpace(raw[i]) {
			i++
		}
		reference := raw[start:i]
		separated := strings.HasSuffix(reference, ",")
		if separated {
			reference = reference[:len(reference)-1]
		}
		if reference == "" || len(reference) > 2048 || strings.ContainsAny(reference, ",()\"'<>`\\") {
			return ErrInvalid
		}
		name, err := resolveReference(base, reference)
		if err != nil || paths[name] {
			return ErrInvalid
		}
		switch assets[name] {
		case "image/jpeg", "image/png", "image/webp":
		default:
			return ErrInvalid
		}
		paths[name] = true
		skipSpace()
		descriptor := ""
		if !separated && i < len(raw) && raw[i] != ',' {
			start = i
			for i < len(raw) && !cssSpace(raw[i]) && raw[i] != ',' {
				i++
			}
			descriptor = raw[start:i]
			skipSpace()
			if i < len(raw) && raw[i] != ',' {
				return ErrInvalid
			}
		}
		kind, key, ok := srcsetDescriptor(descriptor)
		if !ok || mode != 0 && mode != kind || descriptors[key] {
			return ErrInvalid
		}
		mode = kind
		descriptors[key] = true
		count++
		if count > maxSrcsetCandidates {
			return ErrLimit
		}
		if separated {
			// A comma attached to the URL already ended that candidate. It
			// must be followed by another complete candidate, not ignored tail.
			if i == len(raw) {
				return ErrInvalid
			}
			continue
		}
		if i == len(raw) {
			return nil
		}
		if raw[i] != ',' {
			return ErrInvalid
		}
		i++
	}
}

func srcsetDescriptor(raw string) (byte, string, bool) {
	if raw == "" {
		return 'x', "1", true
	}
	if len(raw) < 2 || len(raw) > 32 {
		return 0, "", false
	}
	kind, value := raw[len(raw)-1], raw[:len(raw)-1]
	if kind == 'w' {
		for i := range value {
			if value[i] < '0' || value[i] > '9' {
				return 0, "", false
			}
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil || n == 0 {
			return 0, "", false
		}
		return kind, strconv.FormatUint(n, 10), true
	}
	if kind != 'x' {
		return 0, "", false
	}
	// A conservative finite decimal subset. Signs, exponents, functions and
	// extra descriptor tokens are unsupported rather than partially parsed.
	digits, dot := 0, false
	for i := range value {
		if value[i] == '.' && !dot {
			dot = true
			continue
		}
		if value[i] < '0' || value[i] > '9' {
			return 0, "", false
		}
		digits++
	}
	if digits == 0 || value[len(value)-1] == '.' {
		return 0, "", false
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
		return 0, "", false
	}
	return kind, strconv.FormatFloat(n, 'g', -1, 64), true
}
