package settings

// StorageSettings is the owner's disk warnings and keeping policy for reusable generated files.
type StorageSettings struct {
	WarnPercent        int    `json:"warnPercent"`
	MajorPercent       int    `json:"majorPercent"`
	KeepGeneratedFiles string `json:"keepGeneratedFiles"`
}

var Storage = Define("storage", StorageSettings{WarnPercent: 10, MajorPercent: 5, KeepGeneratedFiles: "balanced"}, func(s *StorageSettings) error {
	if s.WarnPercent < 1 || s.WarnPercent > 50 {
		return &Invalid{Field: "warnPercent", Message: "Choose 1 to 50 percent."}
	}
	if s.MajorPercent < 1 || s.MajorPercent > s.WarnPercent {
		return &Invalid{Field: "majorPercent", Message: "Choose 1 percent up to the warning's."}
	}
	switch s.KeepGeneratedFiles {
	case "balanced", "short", "long":
	default:
		return &Invalid{Field: "keepGeneratedFiles", Message: "Choose Balanced, Shorter or Longer."}
	}
	return nil
})

// PreparedKeepDays is how long a reusable download copy goes unused before it is removed.
// Stream working files are retired with their stream, not kept for future plays.
func (s StorageSettings) PreparedKeepDays() int {
	switch s.KeepGeneratedFiles {
	case "short":
		return 14
	case "long":
		return 90
	default:
		return 30
	}
}
