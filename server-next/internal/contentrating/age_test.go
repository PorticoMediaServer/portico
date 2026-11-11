package contentrating

import "testing"

func TestAge(t *testing.T) {
	known := map[string]int{"G": 0, "pg-13": 13, "R": 17, "TV-MA": 17, "TV-Y7": 7, "FSK 16": 16, "de/FSK 12": 12, "US:PG": 8, "15": 15, "12A": 12, "18+": 18}
	for rating, want := range known {
		if got, ok := Age(rating); !ok || got != want {
			t.Errorf("Age(%q) = %d, %v; want %d", rating, got, ok, want)
		}
	}
	for _, rating := range []string{"", "Not Rated", "Unrated", "XYZ", "NR", "A", "N/A", "U/A"} {
		if age, ok := Age(rating); ok {
			t.Errorf("Age(%q) = %d; want unrated", rating, age)
		}
	}
}
