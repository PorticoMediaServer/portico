package storage

import "errors"

// SetBackgroundTaskPriority applies the owner setting to future background
// children and admission. Active work is never interrupted when it changes.
func (s *Supervisor) SetBackgroundTaskPriority(value string) error {
	if s == nil {
		return errors.New("missing background supervisor")
	}
	if e := CheckBackgroundTaskPriority(value); e != nil {
		return e
	}
	s.backgroundNormal.Store(value == "normal")
	return nil
}

// CheckBackgroundTaskPriority reports whether value is a priority
// SetBackgroundTaskPriority accepts, without applying it.
func CheckBackgroundTaskPriority(value string) error {
	if value != "lower" && value != "normal" {
		return errors.New("invalid background task priority")
	}
	return nil
}

func (s *Supervisor) BackgroundTaskPriority() string {
	if s != nil && s.backgroundNormal.Load() {
		return "normal"
	}
	return "lower"
}
