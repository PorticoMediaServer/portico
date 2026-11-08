package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

func loadFixture(t *testing.T, name string) *report {
	t.Helper()
	r, err := loadReport(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func onlyRow(t *testing.T, r *report) *row {
	t.Helper()
	if len(r.Rows) != 1 {
		t.Fatalf("want one class row, got %d", len(r.Rows))
	}
	return r.Rows[0]
}

func profileNamed(t *testing.T, row *row, name string) *profile {
	t.Helper()
	for _, p := range row.Profiles {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("missing profile %s", name)
	return nil
}

func cellNamed(t *testing.T, p *profile, replicate int) *cellResult {
	t.Helper()
	for _, c := range p.Cells {
		if c.Replicate == replicate {
			return c
		}
	}
	return nil
}

func stratumOf(t *testing.T, c *cellResult, population, role string) *stratumRow {
	t.Helper()
	for _, s := range c.Strata {
		if s.Population == population && s.Role == role {
			return s
		}
	}
	t.Fatalf("cell %s has no %s/%s stratum", c.Directory, population, role)
	return nil
}

func reasons(t *testing.T, r *report) string {
	t.Helper()
	var parts []string
	for _, row := range r.Rows {
		parts = append(parts, row.Reasons...)
	}
	return strings.Join(parts, "\n")
}

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	src := filepath.Join("testdata", name)
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestRollupAllPass(t *testing.T) {
	r := loadFixture(t, "allpass")
	if r.Verdict != qual.Pass {
		t.Fatalf("verdict %s, reasons:\n%s", r.Verdict, reasons(t, r))
	}
	row := onlyRow(t, r)
	if row.Verdict != qual.Pass {
		t.Fatalf("row %s", row.Verdict)
	}
	for _, name := range []string{"standard", "legacy8"} {
		p := profileNamed(t, row, name)
		if len(p.BootIDs) != 3 || len(p.Cells) != 3 {
			t.Fatalf("%s: %d boot ids, %d cells", name, len(p.BootIDs), len(p.Cells))
		}
		for _, c := range p.Cells {
			if c.Status != "complete" || c.Target.Flag == nil || !*c.Target.Flag {
				t.Fatalf("%s vm%d: status %s target %v", name, c.Replicate, c.Status, c.Target.Flag)
			}
			if len(c.Strata) != 4 {
				t.Fatalf("%s vm%d: want 4 strata (warmup + 3 measured), got %d", name, c.Replicate, len(c.Strata))
			}
		}
		for _, s := range p.Strata {
			if s.Gated && s.Verdict != qual.Pass {
				t.Fatalf("%s: gated stratum %s/%s is %s", name, s.Population, s.Role, s.Verdict)
			}
			if !s.Gated && s.Population == "warmup" {
				if s.Verdict != "" {
					t.Fatalf("warmup must never carry a gate verdict, got %s", s.Verdict)
				}
				for _, host := range s.Hosts {
					if host.Distribution.P50NS != nil {
						t.Fatal("warmup quantiles must be withheld, not fabricated")
					}
				}
			}
		}
	}
	md := renderMarkdown(r)
	if !strings.Contains(md, "**Overall verdict: PASS**") || !strings.Contains(md, "cell-declared target met") {
		t.Fatalf("markdown missing pass evidence:\n%s", md)
	}
}

func TestRollupFalseTargetFlagCannotPass(t *testing.T) {
	r := loadFixture(t, "false-target")
	if r.Verdict != qual.Fail {
		t.Fatalf("verdict %s, want fail:\n%s", r.Verdict, reasons(t, r))
	}
	if got := reasons(t, r); !strings.Contains(got, "declares its own target false") {
		t.Fatalf("reason does not name the false target flag:\n%s", got)
	}
	p := profileNamed(t, onlyRow(t, r), "legacy8")
	cell := cellNamed(t, p, 2)
	if cell == nil || cell.Target.Flag == nil || *cell.Target.Flag {
		t.Fatalf("legacy8-vm2 flag not false: %+v", cell)
	}
	md := renderMarkdown(r)
	if !strings.Contains(md, "| false |") {
		t.Fatalf("markdown does not show the false target flag:\n%s", md)
	}
	// Every other cell stays visible; a false flag never hides a cell.
	if len(p.Cells) != 3 {
		t.Fatalf("want 3 legacy8 cells retained, got %d", len(p.Cells))
	}
}

func TestRollupShortPopulationWithholdsQuantilesAndCannotPass(t *testing.T) {
	r := loadFixture(t, "short-population")
	if r.Verdict != qual.Inconclusive {
		t.Fatalf("verdict %s, want inconclusive:\n%s", r.Verdict, reasons(t, r))
	}
	if got := reasons(t, r); !strings.Contains(got, "need 5 complete accepted holds per host") {
		t.Fatalf("reason does not name the short population:\n%s", got)
	}
	for _, p := range onlyRow(t, r).Profiles {
		for _, c := range p.Cells {
			s := stratumOf(t, c, "measured", "ack")
			d := s.Hosts[0].Distribution
			if d.P50NS != nil || d.P95NS != nil || d.P99NS != nil || d.MaxNS != nil {
				t.Fatalf("%s vm%d rendered a quantile for a short population: %+v", p.Name, c.Replicate, d)
			}
			if !strings.Contains(d.Unavailable, "short population: 3 < 5") {
				t.Fatalf("%s vm%d unavailable reason %q", p.Name, c.Replicate, d.Unavailable)
			}
		}
	}
	md := renderMarkdown(r)
	if !strings.Contains(md, "need 5 complete accepted holds per host") || strings.Contains(md, "0.000") {
		t.Fatalf("markdown short-population handling:\n%s", md)
	}
}

func TestRollupMissingVMCannotPass(t *testing.T) {
	r := loadFixture(t, "missing-vm")
	if r.Verdict != qual.Inconclusive {
		t.Fatalf("verdict %s, want inconclusive:\n%s", r.Verdict, reasons(t, r))
	}
	if got := reasons(t, r); !strings.Contains(got, "standard-vm2") {
		t.Fatalf("reason does not name the missing VM:\n%s", got)
	}
	row := onlyRow(t, r)
	p := profileNamed(t, row, "standard")
	if len(p.Cells) != 2 || len(p.BootIDs) != 2 {
		t.Fatalf("standard: %d cells, %d boot ids", len(p.Cells), len(p.BootIDs))
	}
	for _, s := range p.Strata {
		if s.Gated && s.Verdict != qual.Inconclusive {
			t.Fatalf("standard stratum %s/%s is %s with a missing VM", s.Population, s.Role, s.Verdict)
		}
	}
	total := 0
	for _, p := range row.Profiles {
		total += len(p.Cells)
	}
	if total != 5 {
		t.Fatalf("want all five retained cells, got %d", total)
	}
}

func TestRollupStoppedCellIsRetainedAndNonPassing(t *testing.T) {
	r := loadFixture(t, "stopped-cell")
	if r.Verdict != qual.Fail {
		t.Fatalf("verdict %s, want fail:\n%s", r.Verdict, reasons(t, r))
	}
	if got := reasons(t, r); !strings.Contains(got, "is stopped; raw prefix retained") {
		t.Fatalf("reason does not retain the stopped prefix:\n%s", got)
	}
	p := profileNamed(t, onlyRow(t, r), "legacy8")
	cell := cellNamed(t, p, 3)
	if cell == nil {
		t.Fatal("stopped cell omitted")
	}
	if cell.Status != "stopped" {
		t.Fatalf("status %s", cell.Status)
	}
	if cell.Records == 0 || cell.Prefix.Attempts == 0 || cell.Prefix.Accepted == 0 || cell.Prefix.Errors == 0 {
		t.Fatalf("raw prefix counts not retained: %+v", cell.Prefix)
	}
	if cell.RawFile != "raw.ndjson" {
		t.Fatalf("raw file name %q", cell.RawFile)
	}
	md := renderMarkdown(r)
	if !strings.Contains(md, "raw `raw.ndjson` kept with") {
		t.Fatalf("markdown does not retain the prefix:\n%s", md)
	}
}

func TestRollupUnavailableBoundNeverPasses(t *testing.T) {
	r := loadFixture(t, "hosted-gate-tiny")
	if r.Verdict != qual.Fail {
		t.Fatalf("verdict %s, want fail:\n%s", r.Verdict, reasons(t, r))
	}
	if got := reasons(t, r); !strings.Contains(got, "bound exceeds 10 ms or is unavailable") {
		t.Fatalf("reason does not name the unavailable bound:\n%s", got)
	}
	for _, p := range onlyRow(t, r).Profiles {
		for _, s := range p.Strata {
			if !s.Gated || s.Gate != gateHosted {
				continue
			}
			for _, host := range s.GateReport.Hosts {
				if host.Verdict != qual.Fail {
					t.Fatalf("%s host verdict %s", p.Name, host.Verdict)
				}
				if host.Holds == nil || host.Holds.P99Upper95 != nil {
					t.Fatalf("%s: expected an unavailable upper bound, got %+v", p.Name, host.Holds)
				}
			}
		}
	}
}

func TestRollupDuplicateBootIdentityIsInvalid(t *testing.T) {
	root := copyFixture(t, "allpass")
	path := filepath.Join(root, "canonical-class-standard-vm2", "receipt.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	// Same actual VM as vm1 but a different replicate label; the host epoch is
	// left alone so the deliberate duplicate is the only defect.
	hostBefore := receipt["host_before"].(map[string]any)
	host := hostBefore["host"].(map[string]any)
	files := host["files"].(map[string]any)
	files["proc/sys/kernel/random/boot_id"] = "11111111-1111-4111-8111-111111111111\n"
	updated, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := loadReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != qual.Invalid {
		t.Fatalf("verdict %s, want invalid:\n%s", r.Verdict, reasons(t, r))
	}
	if got := reasons(t, r); !strings.Contains(got, "duplicate boot identity") && !strings.Contains(got, "boot identity reused") {
		t.Fatalf("reason does not name the duplicate boot identity:\n%s", got)
	}
}

func TestRollupMalformedRawIsInvalidAndKeepsEvidence(t *testing.T) {
	root := copyFixture(t, "allpass")
	path := filepath.Join(root, "canonical-class-legacy8-vm1", "raw.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines[2] = `{"schema":"wrong"}`
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := loadReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != qual.Invalid {
		t.Fatalf("verdict %s, want invalid:\n%s", r.Verdict, reasons(t, r))
	}
	cell := cellNamed(t, profileNamed(t, onlyRow(t, r), "legacy8"), 1)
	if cell == nil || cell.Status != "invalid" {
		t.Fatalf("cell %+v", cell)
	}
	if !strings.Contains(strings.Join(cell.Problems, "\n"), "raw schema") {
		t.Fatalf("raw problem not retained: %v", cell.Problems)
	}
}

func TestRollupRunWritesBothOutputsAndReportsVerdict(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		wantErr bool
	}{
		{"allpass", false},
		{"false-target", true},
	} {
		out := filepath.Join(t.TempDir(), "out")
		var stdout strings.Builder
		err := run([]string{"-artifacts", filepath.Join("testdata", tc.fixture), "-out", out}, &stdout)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: err %v", tc.fixture, err)
		}
		for _, name := range []string{"rollup.json", "rollup.md"} {
			if _, statErr := os.Stat(filepath.Join(out, name)); statErr != nil {
				t.Fatalf("%s: %v", name, statErr)
			}
		}
		if !strings.Contains(stdout.String(), "rollup") {
			t.Fatalf("%s: stdout %q", tc.fixture, stdout.String())
		}
	}
}

func TestRollupRejectsEmptyAndUnflaggedInput(t *testing.T) {
	if _, err := loadReport(t.TempDir()); err == nil {
		t.Fatal("empty artifacts directory accepted")
	}
	var stdout strings.Builder
	if err := run(nil, &stdout); err == nil {
		t.Fatal("missing flags accepted")
	}
}

func TestRollupMissingTargetFlagIsNonPassing(t *testing.T) {
	root := copyFixture(t, "allpass")
	if err := os.Remove(filepath.Join(root, "canonical-class-standard-vm1", "target.json")); err != nil {
		t.Fatal(err)
	}
	r, err := loadReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != qual.Inconclusive {
		t.Fatalf("verdict %s, want inconclusive:\n%s", r.Verdict, reasons(t, r))
	}
	if got := reasons(t, r); !strings.Contains(got, "no target flag") {
		t.Fatalf("reason does not name the missing flag:\n%s", got)
	}
}

func TestRollupMissingRolePopulationCannotPass(t *testing.T) {
	root := copyFixture(t, "allpass")
	path := filepath.Join(root, "canonical-class-legacy8-vm1", "raw.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []map[string]any
	sequence := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"ack"`) {
			continue
		}
		sequence++
		record["sequence"] = sequence
		kept = append(kept, record)
	}
	var out strings.Builder
	for _, record := range kept {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(encoded)
		out.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := loadReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != qual.Inconclusive {
		t.Fatalf("verdict %s, want inconclusive:\n%s", r.Verdict, reasons(t, r))
	}
	p := profileNamed(t, onlyRow(t, r), "legacy8")
	for _, s := range p.Strata {
		if s.Role != "ack" || !s.Gated {
			continue
		}
		if s.Verdict != qual.Inconclusive {
			t.Fatalf("ack stratum %s with a missing host population", s.Verdict)
		}
		if s.Hosts[0].Count != 0 || s.Hosts[0].Distribution.P50NS != nil {
			t.Fatalf("missing host population fabricated evidence: %+v", s.Hosts[0])
		}
	}
}

func TestDisplayWithholdsShortAndUnsupportedQuantiles(t *testing.T) {
	short := display([]time.Duration{time.Millisecond, 2 * time.Millisecond}, qual.Phase1HoldsPerHost)
	if short.P50NS != nil || short.MaxNS != nil || short.P99Upper95 != nil {
		t.Fatalf("short population rendered quantiles: %+v", short)
	}
	if !strings.Contains(short.Unavailable, "short population: 2 < 10000") {
		t.Fatalf("unavailable reason %q", short.Unavailable)
	}
	empty := display(nil, qual.Phase1HoldsPerHost)
	if empty.Count != 0 || empty.Unavailable == "" {
		t.Fatalf("empty population: %+v", empty)
	}
	full := make([]time.Duration, qual.Phase1HoldsPerHost)
	for i := range full {
		full[i] = time.Millisecond
	}
	d := display(full, qual.Phase1HoldsPerHost)
	if d.P50NS == nil || d.P99NS == nil || d.P999NS == nil || d.P99Upper95 == nil {
		t.Fatalf("full population withheld a supported value: %+v", d)
	}
}
