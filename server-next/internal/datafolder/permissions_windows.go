package datafolder

// ReadableByOthers is never true on Windows: a folder's mode bits there say nothing of who
// may open it, so no alert is raised on them.
func ReadableByOthers(string) (bool, error) { return false, ErrNotChecked }

// MakePrivate is refused on Windows, for the same reason.
func MakePrivate(string) error { return ErrNotChecked }
