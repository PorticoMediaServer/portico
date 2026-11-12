package datafolder

// Look says nothing on Windows: a folder's mode bits there say nothing of who may open it, so
// no alert is raised on them.
func Look(string) (State, error) { return State{}, ErrNotChecked }

// MakePrivate is refused on Windows, for the same reason.
func MakePrivate(string) error { return ErrNotChecked }
