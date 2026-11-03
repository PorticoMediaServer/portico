package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"testing/synctest"
)

// useLowPriorityThreads runs background in-process work on the low-priority
// threads for one test, as production does.
func useLowPriorityThreads(t *testing.T) {
	t.Helper()
	previous := lowPriorityInline
	lowPriorityInline = false
	t.Cleanup(func() { lowPriorityInline = previous })
}

// A background in-process operation inside a testing/synctest bubble completes
// and leaves nothing behind: the bubble's pipe and context never reach a
// thread outside it, and no never-exiting thread is born inside it. It runs
// in a fresh process, so this is the first background operation there.
func TestBackgroundInProcessOperationInsideABubble(t *testing.T) {
	if os.Getenv("PORTICO_IN_PROCESS_BUBBLE") == "1" {
		previous := inProcessHelper
		inProcessHelper = func(_ io.Reader, out io.Writer) error {
			_, err := out.Write([]byte("ok"))
			return err
		}
		defer func() { inProcessHelper = previous }()
		synctest.Test(t, func(t *testing.T) {
			var got bytes.Buffer
			released := make(chan struct{})
			if err := runInProcess(context.Background(), []byte("{}"), func(r io.Reader) error {
				_, err := io.Copy(&got, r)
				return err
			}, func() { close(released) }, true); err != nil {
				t.Fatal(err)
			}
			<-released
			if got.String() != "ok" {
				t.Fatalf("read %q", got.String())
			}
		})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBackgroundInProcessOperationInsideABubble$", "-test.count=1")
	cmd.Env = append(os.Environ(), "PORTICO_IN_PROCESS_BUBBLE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("background operation inside a bubble: %v\n%s", err, out)
	}
}
