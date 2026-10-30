//go:build darwin

package storage

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestBackgroundProcessUsesLowestCPUAndBackgroundIOPolicy(t *testing.T) {
	cmd := exec.Command("/usr/bin/true", "arg")
	lowerBackgroundPriority(cmd)
	want := []string{"nice", "-n", "19", "/usr/sbin/taskpolicy", "-b", "/usr/bin/true", "arg"}
	if cmd.Path != "/usr/bin/nice" || !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("background command path=%q args=%q", cmd.Path, cmd.Args)
	}
}
