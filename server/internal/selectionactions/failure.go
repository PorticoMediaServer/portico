package selectionactions

// TargetFailure is an established, nonretryable domain refusal with no
// uncheckpointed external effects. It fails this target, never the worker.
// Transport/storage uncertainty must remain an ordinary error for safe replay.
type TargetFailure struct {
	Code  string
	Cause error
}

func (e *TargetFailure) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Cause.Error()
	}
	return e.Code
}
func (e *TargetFailure) Unwrap() error { return e.Cause }
