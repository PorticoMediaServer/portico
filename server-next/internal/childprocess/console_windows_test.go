//go:build windows

package childprocess

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A tool the server starts has no console of its own. One that had would be ended by Windows
// whenever anyone signed out. This runs only on Windows, in the Windows service job.
func TestAStartedToolHasNoConsole(t *testing.T) {
	if os.Getenv("PORTICO_CONSOLE_FIXTURE") == "1" {
		window, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
		fmt.Printf("console=%d\n", window)
		return
	}
	child := func(configured bool) string {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAStartedToolHasNoConsole$")
		cmd.Env = append(os.Environ(), "PORTICO_CONSOLE_FIXTURE=1")
		if configured {
			Configure(cmd)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("the child did not run: %v", err)
		}
		return string(out)
	}
	t.Logf("a child started plainly from this test: %s", strings.TrimSpace(child(false)))
	if out := child(true); !strings.Contains(out, "console=0\n") {
		t.Fatalf("a configured child has a console: %s", out)
	}
}
