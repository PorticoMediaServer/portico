package jobs

import (
	"slices"
	"strconv"
)

// Busy is the jobs running and waiting now, by id and state, for noticing when one starts or ends.
func (r *Runner) Busy() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, l := range r.lanes {
		if l.current != 0 {
			out = append(out, strconv.FormatInt(l.current, 10)+" running")
		}
		for _, j := range l.queue {
			out = append(out, strconv.FormatInt(j.ID, 10)+" waiting")
		}
	}
	slices.Sort(out)
	return out
}
