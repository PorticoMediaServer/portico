package writer

import (
	"strings"
	"testing"
)

func TestMPHLSDomainDraftCanonicalWindowMapping(t *testing.T) {
	defs := mpHLSDomainDefinitions()
	seen := map[string]string{}
	for _, d := range defs {
		seen[string(d.ID)] = d.SQL
		if strings.Contains(d.SQL, "mp_hls_window_custody") {
			t.Fatal("parallel window custody", d.ID)
		}
	}
	insert := seen["mp.hls.window_insert"]
	for _, field := range []string{"mp_physical_generation", "mp_execution_id", "mp_source_request_id", "mp_writes_retired", "mp_exit_confirmed", "mp_execution_generation", "mp_revision", "mp_end_index", "mp_plan_digest", "mp_cleaned", "mp_exit_digest", "mp_cleanup_cursor"} {
		if !strings.Contains(insert, field) {
			t.Error("omitted initial native fact", field)
		}
	}
	if seen["mp.hls.window_custody_insert"] != "" || seen["mp.hls.window_restart"] != "" {
		t.Fatal("split row initialization/restart")
	}
	reclaim := seen["mp.hls.window_reclaim"]
	for _, fence := range []string{"mp_execution_id=?", "mp_execution_generation=?", "mp_revision=?", "mp_source_request_id=?", "mp_physical_generation=?", "mp_exit_confirmed=1", "mp_cleaned=1", "NOT EXISTS"} {
		if !strings.Contains(reclaim, fence) {
			t.Error("missing delayed-producer fence", fence)
		}
	}
	for _, id := range []string{"mp.hls.artifact_insert", "mp.hls.window_cancel", "mp.hls.window_exit", "mp.hls.window_publish_revision", "mp.hls.window_control_revision"} {
		sql := seen[id]
		for _, field := range []string{"mp_execution_id", "mp_execution_generation", "mp_source_request_id"} {
			if !strings.Contains(sql, field) {
				t.Errorf("%s missing %s", id, field)
			}
		}
	}
	for _, d := range mpSessionDomainDefinitions() {
		if strings.Contains(d.SQL, "mp_hls_window_custody") {
			t.Fatal("parallel cleanup authority", d.ID)
		}
	}
}
func TestMPHLSDomainDraftResultDataBudget(t *testing.T) {
	r := mpHLSEffect{state: "preparing", asset: "asset", mode: "hls", media: "media", execution: "occurrence", incarnation: [16]byte{1}, revision: 5, generation: 2, physicalPending: true, windowExecution: "window-2", windowGeneration: 2, windowRevision: 3, windowStart: 10, windowEnd: 20, planDigest: strings.Repeat("a", 64), highestProduced: -1, highestServed: -1}
	b, err := mpHLSResultDataBounds(r)
	if err != nil {
		t.Fatal(err)
	}
	r.windowExecution += "-longer"
	longer, err := mpHLSResultDataBounds(r)
	if err != nil || longer.textBytes-b.textBytes != 7 {
		t.Fatal("not actual text octets", longer, b, err)
	}
	r.windowGeneration = 0
	if _, err = mpHLSResultDataBounds(r); err == nil {
		t.Fatal("same-key execution generation missing")
	}
}
