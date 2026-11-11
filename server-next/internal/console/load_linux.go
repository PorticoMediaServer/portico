//go:build linux

package console

import (
	"bufio"
	"bytes"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// machine reads the processor and memory used by the whole machine and by this process, from
// /proc and getrusage: a few small reads per sample. The processor is a share of every core
// since the last sample. Portico's own figures are this process alone: a conversion is a child
// process and shows in the machine's figures.
type machine struct {
	busy, total uint64        // the machine's processor time, from /proc/stat
	self        time.Duration // this process's processor time
	at          time.Time     // when they were read
}

func (m *machine) sample(now time.Time, into map[string]float64) {
	if busy, total, ok := readStat(); ok {
		if m.total > 0 && total > m.total && busy >= m.busy {
			into["cpu"] = round(percent(float64(busy-m.busy) / float64(total-m.total)))
		}
		m.busy, m.total = busy, total
	}
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
		self := time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
		if !m.at.IsZero() && now.After(m.at) && self >= m.self {
			into["processCPU"] = round(percent(float64(self-m.self) / (float64(now.Sub(m.at)) * float64(runtime.NumCPU()))))
		}
		m.self, m.at = self, now
	}
	if total, available, ok := readMeminfo(); ok && total > available {
		into["memory"] = round(percent(float64(total-available) / float64(total)))
		if rss, ok := readResident(); ok {
			into["processMemory"] = round(percent(float64(rss) / float64(total)))
		}
	}
}

// readStat reads the machine's processor time from the first line of /proc/stat: busy and
// in all, in clock ticks. Waiting for a disk counts as idle.
func readStat() (busy, total uint64, ok bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := bytes.Cut(raw, []byte("\n"))
	fields := strings.Fields(string(line))
	// cpu user nice system idle iowait irq softirq steal (guest time is inside user already)
	if len(fields) < 9 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var idle uint64
	for i, f := range fields[1:9] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return total - idle, total, true
}

// readMeminfo reads the machine's memory and how much of it is available, in bytes.
func readMeminfo() (total, available uint64, ok bool) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	found := 0
	lines := bufio.NewScanner(file)
	for lines.Scan() && found < 2 {
		fields := strings.Fields(lines.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, found = v*1024, found+1
		case "MemAvailable:":
			available, found = v*1024, found+1
		}
	}
	return total, available, found == 2
}

// readResident reads the memory this process holds, in bytes.
func readResident() (uint64, bool) {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	return pages * uint64(os.Getpagesize()), err == nil
}
