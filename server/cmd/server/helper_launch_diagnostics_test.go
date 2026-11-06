package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type helperLaunchRecord struct {
	dir, prefix                    string
	processStart, goInit, testMain int64
	processStartError              string
}

// This is the earliest marker in this test package's variable initialization,
// after imported packages initialize. It is NOT the runtime's internal start.
// OS process birth covers the nice/taskpolicy/exec launch chain, not a proof
// that the final image has executed. All three timestamps use the host clock;
// they diagnose startup only and are never qualification/visibility clocks.
var helperLaunch = registerStorageHelperLaunch()

func registerStorageHelperLaunch() helperLaunchRecord {
	r := helperLaunchRecord{goInit: time.Now().UnixNano()}
	if len(os.Args) != 2 || os.Args[1] != "--portico-storage-helper" {
		return r
	}
	r.processStart, r.processStartError = helperProcessStart()
	r.dir = os.TempDir()
	if _, err := os.Stat(filepath.Join(r.dir, helperDiagnosticMarker)); err != nil {
		return r
	}
	for slot := 0; slot < helperDiagnosticSlots; slot++ {
		prefix := filepath.Join(r.dir, fmt.Sprintf("helper-%d", slot))
		f, err := os.OpenFile(prefix+".init", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			continue
		}
		r.prefix = prefix
		_, _ = f.WriteString(r.text("go_package_init"))
		_ = f.Close()
		break
	}
	return r
}

func (r helperLaunchRecord) text(stage string) string {
	return fmt.Sprintf("pid=%d ppid=%d stage=%s process_start_os_ns=%d process_start_error=%q go_package_init_ns=%d testmain_entry_ns=%d\n",
		os.Getpid(), os.Getppid(), stage, r.processStart, r.processStartError, r.goInit, r.testMain)
}

func recordHelperTestMainEntry() {
	helperLaunch.testMain = time.Now().UnixNano()
	if helperLaunch.prefix != "" {
		publishHelperDiagnostic(helperLaunch.prefix+".started", []byte(helperLaunch.text("TestMain_entry")))
	}
}

type boundedLaunchOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *boundedLaunchOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const limit = 4096
	n := min(len(p), limit-len(b.data))
	b.data = append(b.data, p[:n]...)
	b.truncated = b.truncated || n < len(p)
	return len(p), nil
}
