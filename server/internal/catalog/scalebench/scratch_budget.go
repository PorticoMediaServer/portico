package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// The command sets its lane root; bounded unit fixtures use t.TempDir instead.
var scratchDirectory string

const scratchLimit int64 = 8_000_000_000

// Leave headroom for one bounded writer batch and SQLite housekeeping.
const scratchStop int64 = 7_000_000_000

func scratchUsage() (int64, error) {
	if scratchDirectory == "" {
		return 0, nil
	}
	out, err := exec.Command("du", "-s", "-B1", scratchDirectory).Output()
	if err != nil && len(out) == 0 {
		// du also fails when another process removes a file while it walks;
		// it still prints the total of what it saw.
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 1 {
		return 0, fmt.Errorf("empty scratch size")
	}
	return strconv.ParseInt(fields[0], 10, 64)
}

func checkScratchBudget() error {
	bytes, err := scratchUsage()
	if err != nil {
		return err
	}
	if bytes >= scratchStop {
		return fmt.Errorf("scratch headroom exhausted: %d bytes (8 GB hard limit)", bytes)
	}
	return nil
}
