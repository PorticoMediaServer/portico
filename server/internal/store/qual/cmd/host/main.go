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
	return qual.ValidateHost(h, profile)
}
