package catalog

import "testing"

func TestPlaylistRanksRemainOrderedAndCompact(t *testing.T) {
	for _, bounds := range [][2]string{{"", ""}, {"0V", "0W"}, {"00000000000000000001V", "00000000000000000002V"}, {"z", ""}} {
		last := bounds[0]
		for i := int64(0); i < 5000; i++ {
			key := playlistRank(bounds[0], bounds[1], i, 5000)
			if key <= last || bounds[1] != "" && key >= bounds[1] || len(key) > len(bounds[0])+20 || key[len(key)-1] == '0' {
				t.Fatal(bounds, i, key, last)
			}
			last = key
		}
		if end := playlistEndKey(last); end <= last {
			t.Fatal(last, end)
		}
	}
}
