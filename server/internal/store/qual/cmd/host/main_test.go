package main

import (
	"bytes"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"os"
	"strings"
	"testing"
)

func evidence(t *testing.T) qual.HostEvidence {
	t.Helper()
	b, e := os.ReadFile("../../testdata/host-valid.json")
	if e != nil {
		t.Fatal(e)
	}
	var h qual.HostEvidence
	if e = json.Unmarshal(b, &h); e != nil {
		t.Fatal(e)
	}
	return h
}
func TestInvalidReceiptStillContainsActualEvidence(t *testing.T) {
	var out bytes.Buffer
	h := evidence(t)
	err := run([]string{"-source", strings.Repeat("a", 40), "-archive-sha256", strings.Repeat("b", 64), "-profile", "ram2gb", "-replicate", "1"}, &out, func() qual.HostEvidence { return h })
	if err == nil {
		t.Fatal("8GiB passed2GB")
	}
	var r struct {
		Kind     string
		Host     qual.HostEvidence
		Problems []string `json:"protocol_errors"`
	}
	if e := json.Unmarshal(out.Bytes(), &r); e != nil {
		t.Fatal(e)
	}
	if r.Kind != "host-protocol-check-only" || len(r.Problems) == 0 || r.Host.Cgroups[0].Files["sys/fs/cgroup/jobs/service/memory.max"] != "8589934592\n" {
		t.Fatal("invalid evidence not retained")
	}
}
