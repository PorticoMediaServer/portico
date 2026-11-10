package mediaanalysis

import "sort"

// segmentNeighbourCount is how many other episodes a file is compared with.
const segmentNeighbourCount = 8

// showFile is one measured episode file of a show.
type showFile struct {
	Episode        int64
	Season, Number int // Season -1: numbered across the show
}

// segmentNeighbours picks the episodes a file is compared with: the nearest
// episodes of its own season and, when its season is too small to confirm
// anything, the nearest of the seasons around it (an intro often outlives a
// season). Another file of the same episode (a second version) is never a
// neighbour: it would agree with everything.
func segmentNeighbours(files []showFile, i, limit int) []int {
	self := files[i]
	seasonDistance := func(season int) int {
		switch {
		case season == self.Season:
			return 0
		case season == 0 || self.Season == 0:
			return 50 // specials and regular seasons rarely share an intro
		case season > self.Season:
			return season - self.Season
		}
		return self.Season - season
	}
	type candidate struct{ index, season, number int }
	var same, other []candidate
	seen := map[int64]bool{self.Episode: true}
	for j, f := range files {
		if seen[f.Episode] {
			continue
		}
		seen[f.Episode] = true
		d := f.Number - self.Number
		if d < 0 {
			d = -d
		}
		c := candidate{j, seasonDistance(f.Season), d}
		if c.season == 0 {
			same = append(same, c)
		} else {
			other = append(other, c)
		}
	}
	byDistance := func(list []candidate) {
		sort.SliceStable(list, func(a, b int) bool {
			if list[a].season != list[b].season {
				return list[a].season < list[b].season
			}
			return list[a].number < list[b].number
		})
	}
	byDistance(same)
	byDistance(other)
	var out []int
	for _, c := range same {
		if len(out) == limit {
			break
		}
		out = append(out, c.index)
	}
	const enough = 4
	for _, c := range other {
		if len(out) >= enough || len(out) == limit {
			break
		}
		out = append(out, c.index)
	}
	return out
}
