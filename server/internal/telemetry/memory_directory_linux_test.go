//go:build linux

package telemetry

import "testing"

func TestMemoryTranscodeCandidatePrefersTheWritableDedicatedMount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		usable map[string]bool
		want   string
	}{
		{"dedicated mount", map[string]bool{"/memory-transcode": true, "/dev/shm": true}, "/memory-transcode"},
		{"stock mount", map[string]bool{"/dev/shm": true}, "/dev/shm"},
		{"no writable tmpfs", map[string]bool{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := memoryTranscodeCandidate(func(path string) bool { return tc.usable[path] })
			if got != tc.want {
				t.Fatalf("candidate = %q, want %q", got, tc.want)
			}
		})
	}
}
