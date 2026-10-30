package httpapi

import "testing"

func TestReadingRange(t *testing.T) {
	for _, tc := range []struct {
		raw        string
		size       int64
		ok         bool
		start, end int64
	}{{"bytes=0-65535", 100000, true, 0, 65535}, {"bytes=99999-99999", 100000, true, 99999, 99999}, {"", 100000, false, 0, 0}, {"bytes=0-", 100000, false, 0, 0}, {"bytes=-1", 100000, false, 0, 0}, {"bytes=0-100000", 100000, false, 0, 0}, {"bytes=1-0", 100000, false, 0, 0}, {"bytes=0-1,2-3", 100000, false, 0, 0}, {"bytes=+0-1", 100000, false, 0, 0}, {"bytes=0-2097152", 3000000, false, 0, 0}, {"bytes=0-999999999999999999999", 100000, false, 0, 0}} {
		a, b, ok := readingRange(tc.raw, tc.size)
		if ok != tc.ok || (ok && (a != tc.start || b != tc.end)) {
			t.Fatalf("%q => %d,%d,%v", tc.raw, a, b, ok)
		}
	}
}
