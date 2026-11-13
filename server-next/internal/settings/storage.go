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

// PlayedRetentionSeconds keeps recently viewed conversion output for seeking
// back without converting it again. Downloads have their own keeping time.
func (s StorageSettings) PlayedRetentionSeconds() int {
	switch s.KeepGeneratedFiles {
	case "short":
		return 60
	case "long":
		return 1800
	default:
		return 300
	}
}
