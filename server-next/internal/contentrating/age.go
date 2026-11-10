// Package contentrating turns a content rating into the youngest age it is meant for, which
// is what a profile's rating limit compares.
package contentrating

import (
	"strconv"
	"strings"
)

var ages = map[string]int{
	"G": 0, "PG": 8, "PG-13": 13, "R": 17, "NC-17": 18,
	"TV-Y": 0, "TV-G": 0, "TV-Y7": 7, "TV-Y7-FV": 7, "TV-PG": 8, "TV-14": 14, "TV-MA": 17,
	// Other countries' ratings that do not name their age in digits.
	"U": 0, "UC": 0, "E": 0, "TP": 0, "T": 0, "L": 0, "AL": 0, "A": 0, "ALL": 0, "M": 15, "MA15+": 15, "MA 15+": 15,
	"R18+": 18, "X18+": 18, "12A": 12, "R18": 18, "FSK 0": 0, "BPJM": 18,
}

// Age is the youngest age a rating admits. ok is false for a rating Portico does not know
// and for no rating: such a title is unrated. A rating that names its age ("16", "FSK 12",
// "15+", "12A", "-12") stands for that age; a country in front ("de/FSK 16", "US:PG-13") is
// passed over.
func Age(rating string) (age int, ok bool) {
	r := strings.ToUpper(strings.TrimSpace(rating))
	if at := strings.LastIndexAny(r, ":/"); at >= 0 && at+1 < len(r) {
		r = strings.TrimSpace(r[at+1:])
	}
	if strings.HasPrefix(r, "RATED ") {
		r = strings.TrimPrefix(r, "RATED ")
	}
	if age, ok := ages[r]; ok {
		return age, true
	}
	digits := strings.Map(func(c rune) rune {
		if c >= '0' && c <= '9' {
			return c
		}
		return -1
	}, r)
	letters := strings.Trim(r, "0123456789+- ")
	if digits == "" || len(digits) > 2 || letters != "" && letters != "FSK" && letters != "A" && letters != "PEGI" && letters != "VM" {
		return 0, false
	}
	n, _ := strconv.Atoi(digits)
	return n, true
}
