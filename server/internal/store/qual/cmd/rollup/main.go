// Command rollup generates qualification roll-up rows from downloaded class
// artifacts. It is offline reporting only: it opens no database, runs no target
// and issues no verdict beyond the reviewed per-host hold rule. Every row keeps
// its per-VM populations, and a row cannot pass when a cell's own target flag is
// false, a cell is stopped, a profile lacks three distinct boot identities, or a
// population is too short for a quantile.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("rollup", flag.ContinueOnError)
	artifacts := flags.String("artifacts", "", "directory holding downloaded canonical-class-* cell artifacts")
	out := flags.String("out", "", "directory for rollup.json and rollup.md")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *artifacts == "" || *out == "" || flags.NArg() != 0 {
		return fmt.Errorf("require -artifacts and -out")
	}
	r, err := loadReport(*artifacts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	jsonPath := filepath.Join(*out, "rollup.json")
	mdPath := filepath.Join(*out, "rollup.md")
	if err := writeJSON(jsonPath, r); err != nil {
		return err
	}
	if err := os.WriteFile(mdPath, []byte(renderMarkdown(r)), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(output, "rollup %s: %s\nmarkdown: %s\n", r.Verdict, jsonPath, mdPath)
	if r.Verdict != qual.Pass {
		return fmt.Errorf("rollup nonpassing: %s", r.Verdict)
	}
	return nil
}
