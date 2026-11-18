package db

// sortSpec is the columns a sort orders by, before the id that makes every position unique.
// When nullable, rows without a value come last in either direction, as a second run. state,
// for a sort by the viewer's own state (their rating, last play, play count), is the condition
// on their state row that the sort has a value.
type sortSpec struct {
	columns  []string
	nullable bool
	state    string
}

// stateFilter limits a run of a sort by the viewer's state to the titles they have such a
// value for, read from their own state rows. Without it the run looked up and sorted every
// title of the library (130 to 260 ms for a 60,000-film library), though a profile has a value
// for few of them, often none.
func (s sortSpec) stateFilter() string {
	if s.state == "" {
		return ""
	}
	return ` AND i.id IN (SELECT item_id FROM user_item_state WHERE profile_id = @viewer_profile AND ` + s.state + `)`
}
