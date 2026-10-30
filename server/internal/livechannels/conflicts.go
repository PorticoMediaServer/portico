package livechannels

import (
	"sort"
	"time"
)

type Capacity struct {
	Known            bool   `json:"known"`
	Effective        int    `json:"effective"`
	PlanningEstimate int    `json:"planningEstimate"`
	Mode             string `json:"mode"`
}

type Reservation struct {
	ChannelID  string
	ID         string
	Start, End time.Time
	Priority   int
}
type LosingInterval struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	Demand   int    `json:"demand"`
	Capacity int    `json:"capacity"`
}

func reservationBefore(a, b Reservation) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if !a.Start.Equal(b.Start) {
		return a.Start.Before(b.Start)
	}
	return a.ID < b.ID
}

// Conflicts is a deterministic interval sweep. Unknown planning estimates never
// deny admission. Returned explanations contain no other owner's identity/title.
func Conflicts(all []Reservation, target string, capacity Capacity) []LosingInterval {
	out := []LosingInterval{}
	if !capacity.Known {
		return out
	}
	var selected *Reservation
	for i := range all {
		if all[i].ID == target {
			selected = &all[i]
			break
		}
	}
	if selected == nil || !selected.End.After(selected.Start) {
		return out
	}
	type edge struct {
		at             time.Time
		demand, higher int
		channel        string
	}
	edges := []edge{}
	for _, r := range all {
		if r.ID == target || selected.ChannelID != "" && r.ChannelID == selected.ChannelID || !r.Start.Before(selected.End) || !r.End.After(selected.Start) {
			continue
		}
		a, b := r.Start, r.End
		if a.Before(selected.Start) {
			a = selected.Start
		}
		if b.After(selected.End) {
			b = selected.End
		}
		higher := 0
		if reservationBefore(r, *selected) {
			higher = 1
		}
		key := r.ChannelID
		if key == "" {
			key = r.ID
		}
		edges = append(edges, edge{a, 1, higher, key}, edge{b, -1, -higher, key})
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].at.Before(edges[j].at) })
	demand, higher := 1, 0
	counts := map[string][2]int{}
	last := selected.Start
	for i := 0; i < len(edges); {
		at := edges[i].at
		if at.After(last) && higher >= capacity.Effective {
			a, b := last.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano)
			if len(out) > 0 && out[len(out)-1].End == a && out[len(out)-1].Demand == demand {
				out[len(out)-1].End = b
			} else {
				out = append(out, LosingInterval{a, b, demand, capacity.Effective})
			}
		}
		for i < len(edges) && edges[i].at.Equal(at) {
			edge := edges[i]
			old := counts[edge.channel]
			next := [2]int{old[0] + edge.demand, old[1] + edge.higher}
			counts[edge.channel] = next
			if old[0] == 0 && next[0] > 0 {
				demand++
			}
			if old[0] > 0 && next[0] == 0 {
				demand--
			}
			if old[1] == 0 && next[1] > 0 {
				higher++
			}
			if old[1] > 0 && next[1] == 0 {
				higher--
			}
			i++
		}
		last = at
	}
	return out
}
