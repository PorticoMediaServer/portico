package authoritygate

import (
	"log"
	"runtime"
	"strings"
	"time"
)

// Local diagnostics only: this threshold changes no authority decision,
// admission limit or request budget. Callers invoke it after releasing mu.
func warnAuthorityLockWait(wait time.Duration) {
	if wait <= 250*time.Millisecond {
		return
	}
	var pcs [12]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	function, file, line := "unknown", "", 0
	for {
		frame, more := frames.Next()
		function, file, line = frame.Function, frame.File, frame.Line
		if !strings.Contains(frame.Function, "/internal/authoritygate.") || !more {
			break
		}
	}
	log.Printf("Authority lease lock waited for %s at %s:%d (%s)", wait.Round(time.Millisecond), file, line, function)
}
