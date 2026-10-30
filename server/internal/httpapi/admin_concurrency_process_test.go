package httpapi

import "testing"

func TestProcessDiagnosticsCarryResourcesPanelTriageFacts(t *testing.T) {
	process := processDiagnostics()
	if process.Goroutines < 1 || process.HeapAllocBytes == 0 || process.HeapSysBytes == 0 || process.ProcessCPU.State == "" || process.CapacityPolicy == "" {
		t.Fatalf("process diagnostics incomplete: %+v", process)
	}
}
