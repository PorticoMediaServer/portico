package administration

import "strings"

// Decimal identifiers stay as strings: SemVer places no machine integer limit
// on a version, so a large numeric identifier must not overflow the ordering.
type semanticVersion struct{ core, pre []string }

func decimal(v string) bool {
	if v == "" {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func identifiers(v string, prerelease bool) bool {
	for _, id := range strings.Split(v, ".") {
		if id == "" || prerelease && decimal(id) && len(id) > 1 && id[0] == '0' {
			return false
		}
		for _, c := range id {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
				return false
			}
		}
	}
	return true
}
func parseVersion(v string) (semanticVersion, bool) {
	out := semanticVersion{}
	core, meta, hasMeta := strings.Cut(v, "+")
	if hasMeta && !identifiers(meta, false) {
		return out, false
	}
	core, pre, hasPre := strings.Cut(core, "-")
	out.core = strings.Split(core, ".")
	if len(out.core) != 3 {
		return out, false
	}
	for _, n := range out.core {
		if !decimal(n) || len(n) > 1 && n[0] == '0' {
			return out, false
		}
	}
	if hasPre {
		if !identifiers(pre, true) {
			return out, false
		}
		out.pre = strings.Split(pre, ".")
	}
	return out, true
}
func numberOrder(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}
func compareVersions(a, b string) int {
	l, lok := parseVersion(a)
	r, rok := parseVersion(b)
	if !lok || !rok {
		if lok {
			return 1
		}
		if rok {
			return -1
		}
		return 0
	}
	for i := range l.core {
		if cmp := numberOrder(l.core[i], r.core[i]); cmp != 0 {
			return cmp
		}
	}
	if len(l.pre) == 0 {
		if len(r.pre) == 0 {
			return 0
		}
		return 1
	}
	if len(r.pre) == 0 {
		return -1
	}
	for i := 0; i < len(l.pre) && i < len(r.pre); i++ {
		x, y := l.pre[i], r.pre[i]
		xn, yn := decimal(x), decimal(y)
		cmp := strings.Compare(x, y)
		if xn && yn {
			cmp = numberOrder(x, y)
		} else if xn {
			cmp = -1
		} else if yn {
			cmp = 1
		}
		if cmp != 0 {
			return cmp
		}
	}
	if len(l.pre) < len(r.pre) {
		return -1
	}
	if len(l.pre) > len(r.pre) {
		return 1
	}
	return 0
}
