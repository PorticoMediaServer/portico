//go:build linux || darwin

package archive7z

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// The worker's address-space limit leaves it its budget on top of what the Go
// runtime reserved before main: under a flat 1 GiB limit a go1.27 process could
// not grow its heap at all.
func TestLimitedWorkerCanAllocateItsBudget(t *testing.T) {
	if os.Getenv("ARCHIVE7Z_LIMIT_CHILD") == "1" {
		if err := limitProcess(); err != nil {
			t.Fatal(err)
		}
		chunks := make([][]byte, 0, 64)
		for i := 0; i < 64; i++ {
			chunk := make([]byte, 4<<20)
			chunk[len(chunk)-1] = 1
			chunks = append(chunks, chunk)
		}
		runtime.KeepAlive(chunks)
		if len(chunks) != 64 {
			t.Fatal("allocation incomplete")
		}
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestLimitedWorkerCanAllocateItsBudget$")
	command.Env = append(os.Environ(), "ARCHIVE7Z_LIMIT_CHILD=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("a limited worker could not allocate 256 MiB: %v\n%s", err, output)
	}
}
