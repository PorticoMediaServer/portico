package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func writeJSON(path string, r *report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// renderMarkdown renders one row per class, with a per-VM evidence table and a
// per-stratum gate table for each profile. Timings are never merged into a
// single pooled quantile.
func renderMarkdown(r *report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Qualification roll-up\n\n")
	fmt.Fprintf(&b, "Generated from `%s` by `qual/cmd/rollup`. Rows come from raw cell artifacts; timings are never pooled across VMs, profiles, recipes, populations or host epochs.\n\n", r.Artifacts)
	fmt.Fprintf(&b, "**Overall verdict: %s**\n\n", strings.ToUpper(string(r.Verdict)))
	for _, row := range r.Rows {
		renderRow(&b, row)
	}
	for _, note := range r.Notes {
		fmt.Fprintf(&b, "> %s\n", note)
	}
	return b.String()
}

func renderRow(b *strings.Builder, row *row) {
	fmt.Fprintf(b, "## %s — %s\n\n", row.Class, strings.ToUpper(string(row.Verdict)))
	if row.Source != "" {
		fmt.Fprintf(b, "Source `%s` · manifest `%s` · producer run %d/attempt %d\n\n",
			row.Source, row.Manifest, row.ProducerRun, row.ProducerAttempt)
	}
	if len(row.Reasons) > 0 {
		fmt.Fprintf(b, "Non-passing reasons:\n\n")
		for _, reason := range row.Reasons {
			fmt.Fprintf(b, "- %s\n", reason)
		}
		fmt.Fprintln(b)
	}
	for _, profile := range row.Profiles {
		renderProfile(b, profile)
	}
}

func renderProfile(b *strings.Builder, profile *profile) {
	fmt.Fprintf(b, "### %s\n\n", profile.Name)
	fmt.Fprintf(b, "Distinct boot identities: %d", len(profile.BootIDs))
	if len(profile.BootIDs) > 0 {
		fmt.Fprintf(b, " (%s)", strings.Join(shortIDs(profile.BootIDs), ", "))
	}
	fmt.Fprintln(b)
	flags := map[bool]int{}
	declared := 0
	for _, cell := range profile.Cells {
		if cell.Target.Flag != nil {
			declared++
			flags[*cell.Target.Flag]++
		}
	}
	fmt.Fprintf(b, "Cell target flags: %d/%d true, %d false, %d unavailable\n\n",
		flags[true], declared, flags[false], len(profile.Cells)-declared)

	fmt.Fprintf(b, "| VM | Boot | Status | Target | Recipe | Population | Role | N | p50 ms | p95 ms | p99 ms | p99.9 ms | U95 p99 ms | Max ms | Err | Ref |\n")
	fmt.Fprintf(b, "|---|---|---|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, cell := range profile.Cells {
		if len(cell.Strata) == 0 {
			fmt.Fprintf(b, "| vm%d | %s | %s | %s | %s | — | — | 0 | %s | %s | %s | %s | %s | %s | %d | %d |\n",
				cell.Replicate, shortID(cell.BootID), cell.Status, flagText(cell.Target),
				dash(cell.Recipe), unavailable, unavailable, unavailable, unavailable, unavailable, unavailable,
				cell.Prefix.Errors, cell.Prefix.Refusals)
			continue
		}
		for _, stratum := range cell.Strata {
			d := stratum.Hosts[0].Distribution
			host := stratum.Hosts[0]
			fmt.Fprintf(b, "| vm%d | %s | %s | %s | %s | %s | %s | %d | %s | %s | %s | %s | %s | %s | %d | %d |\n",
				cell.Replicate, shortID(cell.BootID), cell.Status, flagText(cell.Target),
				dash(stratum.Recipe), stratum.Population, stratum.Role,
				host.Count, ms(d.P50NS), ms(d.P95NS), ms(d.P99NS), ms(d.P999NS), ms(boundMS(d)),
				ms(d.MaxNS), host.Errors, host.Refusals)
		}
	}
	fmt.Fprintln(b)

	fmt.Fprintf(b, "| Recipe | Population | Role | Gate | Per-VM U95 p99 ms | Verdict | Reason |\n")
	fmt.Fprintf(b, "|---|---|---|---|---|---|---|\n")
	for _, stratum := range profile.Strata {
		var hosts []string
		for i, host := range stratum.Hosts {
			hosts = append(hosts, fmt.Sprintf("vm%d %s", i+1, ms(boundMS(host.Distribution))))
		}
		verdict := string(stratum.Verdict)
		if verdict == "" {
			verdict = "not gated"
		}
		gate := stratum.Gate
		if !stratum.Gated {
			gate = "—"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			dash(stratum.Recipe), stratum.Population, stratum.Role, gate,
			dash(strings.Join(hosts, " · ")), verdict, dash(stratum.Reason))
	}
	fmt.Fprintln(b)

	var retained []*cellResult
	for _, cell := range profile.Cells {
		if cell.Status != "complete" {
			retained = append(retained, cell)
		}
	}
	if len(retained) > 0 {
		fmt.Fprintf(b, "Retained non-passing evidence:\n\n")
		for _, cell := range retained {
			fmt.Fprintf(b, "- `%s`: %s, raw `%s` kept with %d records, %d attempts, %d accepted, %d errors, %d refusals, %d failed bindings\n",
				cell.Directory, cell.Status, cell.RawFile, cell.Records, cell.Prefix.Attempts,
				cell.Prefix.Accepted, cell.Prefix.Errors, cell.Prefix.Refusals, cell.Prefix.FailedBinding)
		}
		fmt.Fprintln(b)
	}
}

const unavailable = "unavailable"

func ms(value *int64) string {
	if value == nil {
		return unavailable
	}
	return fmt.Sprintf("%.3f", float64(*value)/1e6)
}

func boundMS(d *displayDistribution) *int64 {
	if d == nil || d.P99Upper95 == nil {
		return nil
	}
	value := int64(d.P99Upper95.Value)
	return &value
}

func flagText(t targetDecl) string {
	switch {
	case t.Flag == nil:
		return "unavailable"
	case *t.Flag:
		return "true"
	default:
		return "false"
	}
}

func shortID(id string) string {
	if len(id) <= 8 {
		return dash(id)
	}
	return id[:8]
}

func shortIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, shortID(id))
	}
	return out
}

func dash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}
