package hostlimits

import "testing"

func TestEffectiveMemoryBudget(t *testing.T) {
	for _, tc := range []struct {
		name             string
		physical, cgroup uint64
		want             int64
	}{
		{"neither", 0, 0, 0},
		{"physical only", 512 << 20, 0, 512 << 20},
		{"cgroup only", 0, 256 << 20, 256 << 20},
		{"container", 32 << 30, 1 << 30, 1 << 30},
		{"larger cgroup", 512 << 20, 8 << 30, 512 << 20},
		{"overflow", 1 << 63, 0, 0},
		{"overflow with safe cgroup", 1 << 63, 1 << 30, 1 << 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveMemoryBytes(tc.physical, tc.cgroup); got != tc.want {
				t.Fatalf("budget=%d want %d", got, tc.want)
			}
		})
	}
}
