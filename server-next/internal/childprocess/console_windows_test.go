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

// A tool the server starts has no console. One that had would be ended by Windows whenever
// anyone signed out. This runs only on Windows, in the Windows service job.
func TestAStartedToolHasNoConsole(t *testing.T) {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	// The code page of the console this process has, or 0 when it has none.
	console := func() uintptr {
		page, _, _ := kernel.NewProc("GetConsoleCP").Call()
		return page
	}
	if os.Getenv("PORTICO_CONSOLE_FIXTURE") == "1" {
		fmt.Printf("console=%d\n", console())
		return
	}
	// The test needs a console of its own to hand down, or a child without one proves nothing.
	if console() == 0 {
		if ok, _, err := kernel.NewProc("AllocConsole").Call(); ok == 0 {
			t.Fatalf("this test could not be given a console: %v", err)
		}
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
	if out := child(false); !strings.Contains(out, "console=") || strings.Contains(out, "console=0\n") {
		t.Fatalf("a child started plainly should share this test's console, or the check below proves nothing: %s", out)
	}
	if out := child(true); !strings.Contains(out, "console=0\n") {
		t.Fatalf("a tool started as the server starts one has a console: %s", out)
	}
}
