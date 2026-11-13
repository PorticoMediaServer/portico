package settings

// StorageSettings is the owner's free-space warnings.
type StorageSettings struct {
	WarnPercent  int `json:"warnPercent"`
	MajorPercent int `json:"majorPercent"`
}

var Storage = Define("storage", StorageSettings{WarnPercent: 10, MajorPercent: 5}, func(s *StorageSettings) error {
	if s.WarnPercent < 1 || s.WarnPercent > 50 {
		return &Invalid{Field: "warnPercent", Message: "Choose 1 to 50 percent."}
	}
	if s.MajorPercent < 1 || s.MajorPercent > s.WarnPercent {
		return &Invalid{Field: "majorPercent", Message: "Choose 1 percent up to the warning's."}
	}
	return nil
})
