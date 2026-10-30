package catalog

import "testing"

func TestAdminScanRecoveryReasonIsAllowlisted(t *testing.T) {
	for _, v := range []struct{ status, raw, code string }{{"failed", "scan_source_unavailable", "source_unavailable"}, {"failed", "secret provider grant", ""}, {"complete", "scan_source_unavailable", ""}} {
		j := AdminJob{Status: v.status, Error: v.raw, Processed: 7}
		adminJobAction(&j)
		if j.ErrorCode != v.code || j.Error == v.raw || j.Processed != 7 {
			t.Fatal(j)
		}
	}
}
