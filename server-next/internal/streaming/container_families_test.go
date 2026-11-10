package streaming

import (
	"testing"
)

func TestContainerFamiliesUseMeasuredNamesOnBothSides(t *testing.T) {
	for _, family := range [][]string{
		{"matroska", "mkv", "mka"},
		{"mpegts", "ts", "m2ts", "mts"},
		{"mov", "mp4", "m4v", "m4a", "m4b"},
		{"aac", "adts"},
	} {
		for _, declared := range family {
			for _, measured := range family {
				if !containersAllow([]string{declared}, measured) {
					t.Fatalf("%s rejected measured %s", declared, measured)
				}
			}
		}
	}
	for _, pair := range [][2]string{{"webm", "matroska"}, {"matroska", "webm"}, {"webm", "mkv"}, {"mp4", "mpegts"}, {"", ""}} {
		if containersAllow([]string{pair[0]}, pair[1]) {
			t.Fatal("unrelated or unobserved format admitted", pair)
		}
	}
}
