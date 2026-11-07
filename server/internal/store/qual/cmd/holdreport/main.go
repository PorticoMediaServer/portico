// Command holdreport applies the reviewed hosted statistical gate to ONE
// externally verified recipe/profile/population. It opens no database and
// establishes neither provenance nor an overall qualification verdict.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

const maxInput = 32 << 20

type cohort struct {
	Source     string           `json:"source_sha"`
	Profile    string           `json:"profile"`
	Recipe     string           `json:"recipe"`
	Population string           `json:"population"`
	Hosts      []qual.HostHolds `json:"hosts"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("holdreport", flag.ContinueOnError)
	input := flags.String("input", "", "one verified cohort JSON; integer nanoseconds")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *input == "" || flags.NArg() != 0 {
		return fmt.Errorf("require -input with one externally verified cohort")
	}
	f, err := os.Open(*input)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxInput+1))
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > maxInput {
		return fmt.Errorf("cohort input exceeds32MiB")
	}
	keys := json.NewDecoder(bytes.NewReader(data))
	keys.UseNumber()
	if err := uniqueKeys(keys, 0); err != nil {
		return err
	}
	var c cohort
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing cohort input")
	}
	_, err = hex.DecodeString(c.Source)
	if err != nil || len(c.Source) != 40 || (c.Profile != "standard" && c.Profile != "legacy8") ||
		c.Recipe == "" || len(c.Recipe) > 256 || c.Population == "" || len(c.Population) > 256 {
		return fmt.Errorf("source/profile/recipe/population identity required")
	}
	r := qual.EvaluateHostedHolds(c.Hosts)
	result := struct {
		Kind       string          `json:"kind"`
		Source     string          `json:"source_sha"`
		Profile    string          `json:"profile"`
		Recipe     string          `json:"recipe"`
		Population string          `json:"population"`
		Report     qual.HoldReport `json:"hosted_hold_gate"`
	}{"hosted-hold-statistical-gate-only", c.Source, c.Profile, c.Recipe, c.Population, r}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return err
	}
	if r.Verdict != qual.Pass {
		return fmt.Errorf("hosted hold gate nonpassing: %s", r.Verdict)
	}
	return nil
}

// Duplicate keys must not replace a refusal count or a host population before
// the typed decoder sees it. This is a bounded offline input, not a JSON API.
func uniqueKeys(d *json.Decoder, depth int) error {
	if depth > 16 {
		return fmt.Errorf("cohort JSON nesting exceeds16")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	if delim != '{' && delim != '[' {
		return fmt.Errorf("unexpected JSON delimiter")
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or malformed cohort JSON key")
			}
			seen[name] = true
		}
		if err := uniqueKeys(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
