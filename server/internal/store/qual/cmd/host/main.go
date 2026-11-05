// Command host records qualification host-protocol evidence. It does not run
// workload timings or qualify any store, engine, schema or library capacity.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, qual.CaptureHost); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer, capture func() qual.HostEvidence) error {
	flags := flag.NewFlagSet("store-qual-host", flag.ContinueOnError)
	source := flags.String("source", "", "exact source commit SHA")
	archive := flags.String("archive-sha256", "", "scoped source git-archive SHA256")
	profile := flags.String("profile", "", "standard, legacy8, or ram2gb")
	replicate := flags.Int("replicate", 0, "independent hosted job index (1..3)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !digest(*source, 40) || !digest(*archive, 64) || *replicate < 1 || *replicate > 3 {
		return fmt.Errorf("require source SHA, scoped archive SHA256 and replicate 1..3")
	}
	if *profile != "standard" && *profile != "legacy8" && *profile != "ram2gb" {
		return fmt.Errorf("unknown predeclared profile %q", *profile)
	}
	h := capture()
	problems := validate(h, *profile)
	// Emit the evidence even if actual host settings do not match the label.
	// The three-job labels do not themselves prove independent physical hosts.
	receipt := struct {
		Kind      string            `json:"kind"`
		Source    string            `json:"source_sha"`
		Archive   string            `json:"scoped_archive_sha256"`
		Profile   string            `json:"profile"`
		Replicate int               `json:"replicate"`
		Problems  []string          `json:"protocol_errors"`
		Host      qual.HostEvidence `json:"host"`
	}{"host-protocol-check-only", *source, *archive, *profile, *replicate, problems, h}
	if err := json.NewEncoder(out).Encode(receipt); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("host protocol invalid: %s", strings.Join(problems, "; "))
	}
	return nil
}

func digest(s string, length int) bool {
	_, err := hex.DecodeString(s)
	return err == nil && len(s) == length
}

func validate(h qual.HostEvidence, profile string) []string {
	problems := []string{}
	if h.GOOS != "linux" || h.CPUs != 4 {
		problems = append(problems, "expected Linux and four visible CPUs; retain actual hardware stratum")
	}
	for _, name := range []string{
		"proc/cpuinfo", "proc/meminfo", "proc/stat", "proc/loadavg", "proc/diskstats",
		"proc/sys/kernel/osrelease", "proc/sys/kernel/random/boot_id", "proc/self/status",
		"proc/self/mountinfo", "proc/pressure/cpu", "proc/pressure/io", "proc/pressure/memory",
	} {
		if strings.TrimSpace(h.Files[name]) == "" {
			problems = append(problems, "missing required "+name)
		}
	}
	if len(h.Processes) == 0 || len(h.Threads) == 0 || len(h.Cgroups) == 0 {
		problems = append(problems, "missing process, native-thread or cgroup-v2 evidence")
	}
	var memory, swap uint64 = ^uint64(0), ^uint64(0)
	cpu := 1e100
	hasCPUSet := false
	for index, cg := range h.Cgroups {
		for _, name := range []string{"memory.max", "memory.swap.max", "cpu.max", "memory.current", "memory.stat", "memory.events", "cpu.stat", "cpuset.cpus.effective"} {
			key := path.Join(cg.Path, name)
			value, ok := cg.Files[key]
			if !ok {
				// A disabled cpuset controller inherits affinity. The
				// visible ancestor cpuset and runtime.NumCPU retain it.
				if name == "cpuset.cpus.effective" {
					continue
				}
				limit := name == "memory.max" || name == "memory.swap.max" || name == "cpu.max"
				if index > 0 && !limit {
					continue // dynamic counters are required on this process's leaf
				}
				// The real cgroup-v2 hierarchy root has no resource-limit
				// files. Keep their absence in the raw snapshot. Required
				// leaf settings and every visible intermediate limit remain
				// checked; never skip an ancestor merely because it is max.
				if index == len(h.Cgroups)-1 && index > 0 && limit {
					continue
				}
				problems = append(problems, "missing required "+key)
				continue
			}
			value = strings.TrimSpace(value)
			if name == "cpuset.cpus.effective" && value != "" {
				hasCPUSet = true
			}
			if name == "cpu.max" {
				fields := strings.Fields(value)
				if len(fields) != 2 {
					problems = append(problems, "invalid "+key)
					continue
				}
				period, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil || period == 0 {
					problems = append(problems, "invalid "+key)
					continue
				}
				if fields[0] != "max" {
					quota, err := strconv.ParseUint(fields[0], 10, 64)
					if err != nil || quota == 0 {
						problems = append(problems, "invalid "+key)
					} else {
						cpu = min(cpu, float64(quota)/float64(period))
					}
				}
			} else if (name == "memory.max" || name == "memory.swap.max") && value != "max" {
				n, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					problems = append(problems, "invalid "+key)
				} else if name == "memory.max" {
					memory = min(memory, n)
				} else {
					swap = min(swap, n)
				}
			}
		}
	}
	if !hasCPUSet {
		problems = append(problems, "missing visible effective cpuset")
	}
	if profile == "standard" {
		var physicalKiB uint64
		for _, line := range strings.Split(h.Files["proc/meminfo"], "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
				physicalKiB, _ = strconv.ParseUint(fields[1], 10, 64)
			}
		}
		if physicalKiB < (16_000_000_000+1023)/1024 || memory < 16_000_000_000 || cpu < 4 {
			problems = append(problems, "standard host inherits less than 16 GB or four CPU equivalents")
		}
	} else {
		wantMemory := uint64(8 << 30)
		if profile == "ram2gb" {
			wantMemory = 2_000_000_000
		}
		if memory != wantMemory || swap != 0 || cpu != 2 {
			problems = append(problems, fmt.Sprintf("%s actual effective memory/swap/CPU=%d/%d/%g; require %d/0/2", profile, memory, swap, cpu, wantMemory))
		}
	}
	return problems
}
