package capabilityreport

import (
	"errors"
	"fmt"
	"testing"
)

func TestReportLogsOncePerChange(t *testing.T) {
	var lines []string
	saved := logf
	logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	defer func() { logf = saved }()
	cause := errors.New("bwrap (bubblewrap) is not on the server's PATH")
	Report(LiveTV, false, "decoder_confinement_unavailable", cause)
	Report(LiveTV, false, "decoder_confinement_unavailable", cause)
	Report(Recording, true, "", nil)
	Report(LiveTV, true, "", nil)
	if len(lines) != 3 {
		t.Fatalf("%d lines: %q", len(lines), lines)
	}
	if lines[0] != "Live TV unavailable (decoder_confinement_unavailable): bwrap (bubblewrap) is not on the server's PATH" || lines[1] != "Recording available" {
		t.Fatalf("%q", lines)
	}
	all := All()
	if len(all) < 2 || all[0].Capability != LiveTV || !all[0].Available || all[0].Code != "" {
		t.Fatalf("%+v", all)
	}
}
