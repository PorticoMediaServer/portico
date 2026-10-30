package assets

import (
	"math"
	"sort"
)

// CleanChapters rejects bad entries individually and retains a bounded timeline.
func CleanChapters(input []Chapter, duration float64) ([]Chapter, string) {
	out := make([]Chapter, 0, min(len(input), 4096))
	status := "known"
	if len(input) > 4096 {
		status = "partial"
		input = input[:4096]
	}
	sorted := append([]Chapter(nil), input...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if math.IsNaN(sorted[i].Start) {
			return false
		}
		if math.IsNaN(sorted[j].Start) {
			return true
		}
		return sorted[i].Start < sorted[j].Start
	})
	for i, c := range sorted {
		if math.IsNaN(c.Start) || math.IsNaN(c.End) || math.IsInf(c.Start, 0) || math.IsInf(c.End, 0) || c.Start < 0 || c.Start >= duration {
			status = "partial"
			continue
		}
		if c.End == 0 {
			c.End = duration
			if i+1 < len(sorted) && sorted[i+1].Start > c.Start {
				c.End = sorted[i+1].Start
			}
		}
		if c.End > duration {
			c.End = duration
			status = "partial"
		}
		if len(out) > 0 && c.Start < out[len(out)-1].End {
			if out[len(out)-1].End-c.Start <= .1 {
				c.Start = out[len(out)-1].End
				status = "partial"
			} else {
				status = "partial"
				continue
			}
		}
		if c.End <= c.Start {
			status = "partial"
			continue
		}
		out = append(out, c)
	}
	return out, status
}
