//go:build !linux

package worker

// Non-Linux hosts retain cooperative pacing; storage helpers use their native
// child-process policy. There is no portable goroutine scheduling priority.
func LowerPriority() {}
