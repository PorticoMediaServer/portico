// Package main generates qualification roll-up rows from downloaded class
// artifacts. It is offline reporting: it opens no database, runs no target and
// mints no runtime authority. Every timing remains per VM, per profile, per
// recipe and per population; the reviewed hold statistics are reused, never
// re-derived.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

const (
	rollupVersion = "portico-qualification-rollup-v1"
	rawSchema     = "portico-canonical-held-class-v1"
	targetVersion = "portico-class-target-v1"

	// gateHosted is the normal-write rule. A component class whose own target
	// differs declares gateDeclared and is never forced through it.
	gateHosted   = "hosted_p99_u95_10ms"
	gateDeclared = "cell_declared"

	defaultMinHolds = qual.Phase1HoldsPerHost
	maxLineBytes    = 4 << 20 // the transport accepts at most 2 MiB per record
	maxProblems     = 8
)

type report struct {
	Version   string       `json:"version"`
	Artifacts string       `json:"artifacts"`
	Verdict   qual.Verdict `json:"verdict"`
	Rows      []*row       `json:"rows"`
	Notes     []string     `json:"notes,omitempty"`
}

type row struct {
	Class           string       `json:"class"`
	Source          string       `json:"source_sha"`
	Manifest        string       `json:"manifest_sha256"`
	ProducerRun     int64        `json:"producer_run"`
	ProducerAttempt int64        `json:"producer_attempt"`
	Verdict         qual.Verdict `json:"verdict"`
	Reasons         []string     `json:"reasons,omitempty"`
	Profiles        []*profile   `json:"profiles"`
}

type profile struct {
	Name    string        `json:"profile"`
	BootIDs []string      `json:"boot_ids"`
	Cells   []*cellResult `json:"cells"`
	Strata  []*stratumRow `json:"strata"`
}

type cellResult struct {
	Directory string        `json:"directory"`
	Class     string        `json:"class"`
	Profile   string        `json:"profile"`
	Replicate int           `json:"replicate"`
	BootID    string        `json:"boot_id"`
	HostEpoch string        `json:"host_epoch"`
	Status    string        `json:"status"`
	ExitCode  *int          `json:"target_exit_code"`
	Recipe    string        `json:"recipe"`
	RawFile   string        `json:"raw_file"`
	Records   int           `json:"raw_records"`
	Prefix    prefixCounts  `json:"retained_prefix"`
	Target    targetDecl    `json:"target"`
	Problems  []string      `json:"problems,omitempty"`
	Strata    []*stratumRow `json:"strata"`
}

type prefixCounts struct {
	Attempts      int `json:"attempts"`
	Accepted      int `json:"accepted"`
	Errors        int `json:"errors"`
	Refusals      int `json:"refusals"`
	Fenced        int `json:"fenced"`
	FailedBinding int `json:"failed_bindings"`
}

type targetDecl struct {
	Declared        bool   `json:"declared"`
	Description     string `json:"target,omitempty"`
	Flag            *bool  `json:"target_flag"`
	Reason          string `json:"reason,omitempty"`
	Gate            string `json:"gate"`
	MinHoldsPerHost int    `json:"min_holds_per_host"`
	Synthetic       bool   `json:"synthetic"`
}

// stratumRow is one recipe/population/role view. Timings are never pooled:
// Hosts carries each VM's own distribution and the verdict is the reviewed
// per-host rule over those separate populations.
type stratumRow struct {
	Recipe     string           `json:"recipe"`
	Population string           `json:"population"`
	Role       string           `json:"role"`
	Gated      bool             `json:"gated"`
	Gate       string           `json:"gate"`
	Floor      int              `json:"min_holds_per_host"`
	Reason     string           `json:"reason,omitempty"`
	Hosts      []stratumHost    `json:"hosts"`
	Verdict    qual.Verdict     `json:"verdict,omitempty"`
	GateReport *qual.HoldReport `json:"hosted_hold_gate,omitempty"`
}

type stratumHost struct {
	Cell         string               `json:"cell"`
	Profile      string               `json:"profile"`
	Replicate    int                  `json:"replicate"`
	BootID       string               `json:"boot_id"`
	Count        int                  `json:"count"`
	Errors       int                  `json:"errors"`
	Refusals     int                  `json:"refusals"`
	Fenced       int                  `json:"fenced"`
	Distribution *displayDistribution `json:"distribution"`
}

// displayDistribution mirrors the reviewed Distribution but withholds every
// quantile the population cannot support. A withheld value is null, never zero.
type displayDistribution struct {
	Count       int         `json:"count"`
	P50NS       *int64      `json:"p50_ns"`
	P95NS       *int64      `json:"p95_ns"`
	P99NS       *int64      `json:"p99_ns"`
	P999NS      *int64      `json:"p999_ns"`
	MaxNS       *int64      `json:"max_ns"`
	P99Upper95  *qual.Bound `json:"p99_upper_95"`
	Unavailable string      `json:"unavailable_reason,omitempty"`
}

type stratumKey struct {
	Recipe     string
	Population string
	Role       string
}

type cellData struct {
	cellResult
	source      string
	manifest    string
	producerRun int64
	producerAtt int64
	strata      map[stratumKey]*cellStratum
	haveFinal   bool
	finalOK     bool
	integrity   []string
}

type cellStratum struct {
	attempts int
	errors   int
	refusals int
	fenced   int
	holds    []time.Duration
}

// ---------------------------------------------------------------------------
// Discovery and loading

func loadReport(artifacts string) (*report, error) {
	abs, err := filepath.Abs(artifacts)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("artifacts path is not a directory: %s", abs)
	}
	dirs, err := findCellDirs(abs)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no cell artifacts found under %s (need */receipt.json)", abs)
	}
	r := &report{Version: rollupVersion, Artifacts: abs, Verdict: qual.Pass}
	cells := make([]*cellData, 0, len(dirs))
	for _, dir := range dirs {
		c := loadCell(dir)
		cells = append(cells, c)
	}
	assemble(r, cells)
	return r, nil
}

// findCellDirs returns every directory holding a receipt.json, including the
// artifacts root. Ordering is stable so output is deterministic.
func findCellDirs(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "receipt.json")); err == nil {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs, err
}

type receiptFile struct {
	Version  string `json:"version"`
	Status   string `json:"status"`
	Declared struct {
		Source          string `json:"source"`
		Manifest        string `json:"manifest"`
		Name            string `json:"name"`
		Profile         string `json:"profile"`
		Replicate       int    `json:"replicate"`
		ProducerRun     int64  `json:"producer_run"`
		ProducerAttempt int64  `json:"producer_attempt"`
	} `json:"declared"`
	SourceCommit   string `json:"source_commit"`
	TargetExitCode *int   `json:"target_exit_code"`
	HostEpoch      string `json:"host_epoch"`
	ActualConfig   struct {
		HostEpoch string `json:"host_epoch"`
		Class     string `json:"class"`
		Profile   string `json:"profile"`
	} `json:"actual_config"`
	HostBefore struct {
		BootID string `json:"boot_id"`
		Host   struct {
			Files map[string]string `json:"files"`
		} `json:"host"`
	} `json:"host_before"`
	TargetFlag   *bool  `json:"target_flag"`
	Target       string `json:"target"`
	TargetReason string `json:"target_reason"`
}

func loadCell(dir string) *cellData {
	c := &cellData{
		cellResult: cellResult{Directory: dir, Strata: []*stratumRow{}},
		strata:     map[stratumKey]*cellStratum{},
	}
	data, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
	if err != nil {
		c.Problems = appendLimited(c.Problems, "unreadable receipt: "+err.Error())
		c.Status = "invalid"
		return c
	}
	var receipt receiptFile
	if err := json.Unmarshal(data, &receipt); err != nil {
		c.Problems = appendLimited(c.Problems, "invalid receipt JSON: "+err.Error())
		c.Status = "invalid"
		return c
	}
	c.Class = firstNonEmpty(receipt.Declared.Name, receipt.ActualConfig.Class)
	c.Profile = firstNonEmpty(receipt.Declared.Profile, receipt.ActualConfig.Profile)
	c.Replicate = receipt.Declared.Replicate
	c.ExitCode = receipt.TargetExitCode
	c.HostEpoch = firstNonEmpty(receipt.ActualConfig.HostEpoch, receipt.HostEpoch)
	c.BootID = strings.TrimSpace(firstNonEmpty(
		receipt.HostBefore.Host.Files["proc/sys/kernel/random/boot_id"],
		receipt.HostBefore.BootID))
	c.source = receipt.Declared.Source
	c.manifest = receipt.Declared.Manifest
	c.producerRun = receipt.Declared.ProducerRun
	c.producerAtt = receipt.Declared.ProducerAttempt
	if receipt.SourceCommit != "" && c.source != "" && receipt.SourceCommit != c.source {
		c.integrity = append(c.integrity, "receipt source_commit disagrees with declared source")
	}
	c.Target = loadTarget(dir, &receipt, c)

	raw, name, err := openRaw(dir)
	if err != nil {
		c.Problems = appendLimited(c.Problems, "no readable raw stream: "+err.Error())
		c.Status = "invalid"
		return c
	}
	defer raw.Close()
	c.RawFile = name
	reduceRaw(raw, c)

	if c.Recipe == "" {
		c.Recipe = "(prefix: no final recipe)"
	}
	relabelRecipe(c)
	c.Strata = renderCellStrata(c)

	complete := receipt.Status == "owner-execution-complete" && c.haveFinal && c.finalOK
	if c.ExitCode != nil && *c.ExitCode != 0 {
		complete = false
	}
	c.Problems = append(c.Problems, c.integrity...)
	switch {
	case len(c.integrity) > 0:
		c.Status = "invalid"
	case c.Prefix.FailedBinding > 0:
		c.Status = "refused"
	case !complete:
		c.Status = "stopped"
	case c.ExitCode == nil:
		c.Problems = appendLimited(c.Problems, "complete cell has no target exit code")
		c.Status = "invalid"
	default:
		c.Status = "complete"
	}
	return c
}

// loadTarget reads the cell's own target declaration. target.json is the
// primary contract; receipt target fields are accepted so a cell need not add
// a second file. No declaration means unavailable, never a pass.
func loadTarget(dir string, receipt *receiptFile, c *cellData) targetDecl {
	t := targetDecl{Gate: gateHosted, MinHoldsPerHost: defaultMinHolds}
	path := filepath.Join(dir, "target.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var decl struct {
			Version         string `json:"version"`
			Class           string `json:"class"`
			Profile         string `json:"profile"`
			Replicate       int    `json:"replicate"`
			Target          string `json:"target"`
			TargetFlag      *bool  `json:"target_flag"`
			Reason          string `json:"reason"`
			Gate            string `json:"gate"`
			MinHoldsPerHost int    `json:"min_holds_per_host"`
			Synthetic       bool   `json:"synthetic"`
		}
		if err := json.Unmarshal(data, &decl); err != nil {
			c.Problems = appendLimited(c.Problems, "target.json is not valid JSON: "+err.Error())
			return t
		}
		t.Declared = true
		t.Description = strings.TrimSpace(decl.Target)
		t.Flag = decl.TargetFlag
		t.Reason = strings.TrimSpace(decl.Reason)
		t.Gate = decl.Gate
		t.MinHoldsPerHost = decl.MinHoldsPerHost
		t.Synthetic = decl.Synthetic
		if decl.Version != targetVersion {
			c.Problems = appendLimited(c.Problems, "target.json version must be "+targetVersion)
			t.Flag = nil
		}
		if decl.Class != "" && decl.Class != c.Class {
			c.Problems = appendLimited(c.Problems, "target.json class disagrees with receipt")
			t.Flag = nil
		}
		if decl.Profile != "" && decl.Profile != c.Profile {
			c.Problems = appendLimited(c.Problems, "target.json profile disagrees with receipt")
			t.Flag = nil
		}
		if decl.Replicate != 0 && decl.Replicate != c.Replicate {
			c.Problems = appendLimited(c.Problems, "target.json replicate disagrees with receipt")
			t.Flag = nil
		}
	} else if receipt.TargetFlag != nil || receipt.Target != "" {
		t.Declared = true
		t.Flag = receipt.TargetFlag
		t.Description = strings.TrimSpace(receipt.Target)
		t.Reason = strings.TrimSpace(receipt.TargetReason)
	}
	if t.Gate == "" {
		t.Gate = gateHosted
	}
	if t.Gate != gateHosted && t.Gate != gateDeclared {
		c.Problems = appendLimited(c.Problems, "target gate must be "+gateHosted+" or "+gateDeclared)
		t.Flag = nil
	}
	if t.MinHoldsPerHost == 0 {
		t.MinHoldsPerHost = defaultMinHolds
	}
	if t.MinHoldsPerHost < 0 {
		c.Problems = appendLimited(c.Problems, "target min_holds_per_host is negative")
		t.Flag = nil
	}
	if t.MinHoldsPerHost < defaultMinHolds && !t.Synthetic {
		c.Problems = appendLimited(c.Problems, fmt.Sprintf(
			"target min_holds_per_host %d is below %d and is not marked synthetic",
			t.MinHoldsPerHost, defaultMinHolds))
		t.Flag = nil
	}
	return t
}

func openRaw(dir string) (io.ReadCloser, string, error) {
	for _, name := range []string{"raw.ndjson.gz", "raw.ndjson"} {
		path := filepath.Join(dir, name)
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if strings.HasSuffix(name, ".gz") {
			gz, gzErr := gzip.NewReader(file)
			if gzErr != nil {
				file.Close()
				return nil, "", gzErr
			}
			return &compoundCloser{Reader: gz, closers: []io.Closer{gz, file}}, name, nil
		}
		return file, name, nil
	}
	return nil, "", os.ErrNotExist
}

type compoundCloser struct {
	io.Reader
	closers []io.Closer
}

func (c *compoundCloser) Close() error {
	var errs []error
	for _, closer := range c.closers {
		errs = append(errs, closer.Close())
	}
	return errors.Join(errs...)
}

// ---------------------------------------------------------------------------
// Raw reduction

func reduceRaw(raw io.Reader, c *cellData) {
	scanner := bufio.NewScanner(raw)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	line := 0
	for scanner.Scan() {
		line++
		c.Records++
		record, err := decodeRecord(scanner.Bytes())
		if err != nil {
			c.integrity = appendLimited(c.integrity, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		if err := checkIdentity(record, c, line); err != nil {
			c.integrity = appendLimited(c.integrity, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		switch stringField(record, "kind") {
		case "final":
			c.haveFinal = true
			if complete, ok := record["complete"].(bool); ok && complete {
				c.finalOK = true
			}
			if c.Recipe == "" {
				c.Recipe = stringField(record, "recipe")
			}
			if message := stringField(record, "error"); message != "" {
				c.Problems = appendLimited(c.Problems, "owner final reports an error: "+truncate(message, 160))
			}
		case "failed_binding":
			c.Prefix.FailedBinding++
			if reason := stringField(record, "reason"); reason != "" {
				c.Problems = appendLimited(c.Problems, "binding unavailable: "+truncate(reason, 160))
			}
		case "checkpoint":
			// Checkpoint/service records are maintenance, never writer holds.
		case "attempt":
			reduceAttempt(record, c)
		}
	}
	if err := scanner.Err(); err != nil {
		c.integrity = appendLimited(c.integrity, "raw stream read error: "+err.Error())
	}
	if c.Records == 0 {
		c.integrity = appendLimited(c.integrity, "raw stream is empty")
	}
}

func reduceAttempt(record map[string]any, c *cellData) {
	attempt := record
	if nested, ok := record["attempt"].(map[string]any); ok {
		attempt = nested
	}
	c.Prefix.Attempts++
	role := firstNonEmpty(stringField(attempt, "turn_role"), operationRole(attempt), "attempt")
	population := firstNonEmpty(stringField(attempt, "original_population"), "unspecified")
	recipe := stringField(attempt, "recipe")
	var hold *int64
	if clock, ok := attempt["owner_clock"].(map[string]any); ok {
		hold = numberField(clock, "hold_ns")
	}
	if hold == nil {
		if window, ok := attempt["owner_window"].(map[string]any); ok {
			hold = numberField(window, "hold_ns")
		}
	}
	errText := stringField(attempt, "error")
	refusal, refusalSet := boolField(attempt, "refusal")
	refused := refusalSet && refusal != nil && *refusal
	if admission, ok := attempt["admission"].(map[string]any); ok {
		if value, present := admission["refusal"]; present && value != nil {
			refused = true
		}
	}
	fenced, fencedSet := boolField(attempt, "frame_fenced")
	fencedOn := fencedSet && fenced != nil && *fenced
	complete, completeSet := boolField(attempt, "complete")
	incomplete := completeSet && complete != nil && !*complete
	if outcome := stringField(attempt, "physical_outcome"); outcome != "" && outcome != "committed" {
		incomplete = true
	}
	accepted := hold != nil && *hold > 0 && errText == "" && !refused && !fencedOn && !incomplete

	key := stratumKey{Recipe: recipe, Population: population, Role: role}
	s := c.strata[key]
	if s == nil {
		s = &cellStratum{}
		c.strata[key] = s
	}
	s.attempts++
	if errText != "" {
		s.errors++
		c.Prefix.Errors++
	}
	if refused {
		s.refusals++
		c.Prefix.Refusals++
	}
	if fencedOn {
		s.fenced++
		c.Prefix.Fenced++
	}
	if accepted {
		s.holds = append(s.holds, time.Duration(*hold))
		c.Prefix.Accepted++
	}
}

// relabelRecipe moves attempts that carried no recipe of their own under the
// cell recipe, so a single customer row never splits into a nameless stratum.
func relabelRecipe(c *cellData) {
	for key, s := range c.strata {
		if key.Recipe != "" {
			continue
		}
		delete(c.strata, key)
		key.Recipe = c.Recipe
		if existing, ok := c.strata[key]; ok {
			existing.merge(s)
		} else {
			c.strata[key] = s
		}
	}
}

func operationRole(attempt map[string]any) string {
	operation := stringField(attempt, "operation")
	phase := stringField(attempt, "phase")
	switch {
	case operation == "":
		return ""
	case phase == "":
		return operation
	default:
		return operation + "/" + phase
	}
}

func renderCellStrata(c *cellData) []*stratumRow {
	keys := make([]stratumKey, 0, len(c.strata))
	for key := range c.strata {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	out := make([]*stratumRow, 0, len(keys))
	for _, key := range keys {
		s := c.strata[key]
		out = append(out, &stratumRow{
			Recipe: key.Recipe, Population: key.Population, Role: key.Role,
			Gated: gatedPopulation(key.Population),
			Gate:  c.Target.Gate,
			Floor: c.Target.MinHoldsPerHost,
			Hosts: []stratumHost{{
				Cell: c.Directory, Profile: c.Profile, Replicate: c.Replicate,
				BootID: c.BootID, Count: len(s.holds), Errors: s.errors,
				Refusals: s.refusals, Fenced: s.fenced,
				Distribution: display(s.holds, c.Target.MinHoldsPerHost),
			}},
		})
	}
	return out
}

func (k stratumKey) less(o stratumKey) bool {
	if k.Recipe != o.Recipe {
		return k.Recipe < o.Recipe
	}
	if k.Population != o.Population {
		return k.Population < o.Population
	}
	return k.Role < o.Role
}

func (s *cellStratum) merge(o *cellStratum) {
	s.attempts += o.attempts
	s.errors += o.errors
	s.refusals += o.refusals
	s.fenced += o.fenced
	s.holds = append(s.holds, o.holds...)
}

// gatedPopulation reports whether a population is held to the normal-write
// gate. Warmup is context only, and error/rollback paths have their own budget
// that this tool does not compute.
func gatedPopulation(population string) bool {
	lower := strings.ToLower(population)
	return lower != "warmup" && !strings.Contains(lower, "error") && !strings.Contains(lower, "rollback")
}

// display withholds every quantile the population cannot support. It never
// converts a short or empty population into zero-valued quantiles.
func display(holds []time.Duration, floor int) *displayDistribution {
	if len(holds) == 0 {
		return &displayDistribution{Unavailable: "no complete accepted holds"}
	}
	d, err := qual.Summarize(holds)
	if err != nil {
		return &displayDistribution{Count: len(holds), Unavailable: err.Error()}
	}
	out := &displayDistribution{Count: d.Count}
	if d.Count < floor {
		out.Unavailable = fmt.Sprintf("short population: %d < %d complete accepted holds", d.Count, floor)
		return out
	}
	p50, p95, p99, max := int64(d.P50), int64(d.P95), int64(d.P99), int64(d.Max)
	out.P50NS, out.P95NS, out.P99NS, out.MaxNS = &p50, &p95, &p99, &max
	if d.Count >= qual.Phase1HoldsPerHost {
		p999 := int64(d.P999)
		out.P999NS = &p999
	}
	if d.P99Upper95 != nil {
		out.P99Upper95 = d.P99Upper95
	}
	return out
}

// ---------------------------------------------------------------------------
// Assembly and verdict

func assemble(r *report, cells []*cellData) {
	byClass := map[string][]*cellData{}
	for _, c := range cells {
		byClass[c.Class] = append(byClass[c.Class], c)
	}
	classes := make([]string, 0, len(byClass))
	for class := range byClass {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	allBoots := map[string]string{}
	for _, c := range cells {
		if c.BootID == "" {
			continue
		}
		if other, seen := allBoots[c.BootID]; seen {
			c.Problems = appendLimited(c.Problems, "boot identity reused by "+filepath.Base(other))
			if c.Status == "complete" {
				c.Status = "invalid"
			}
			continue
		}
		allBoots[c.BootID] = c.Directory
	}
	for _, class := range classes {
		r.Rows = append(r.Rows, assembleRow(class, byClass[class]))
	}
	synthetic, declaredGate := 0, 0
	for _, c := range cells {
		if c.Target.Synthetic {
			synthetic++
		}
	}
	for _, row := range r.Rows {
		r.Verdict = worst(r.Verdict, row.Verdict)
		for _, p := range row.Profiles {
			for _, s := range p.Strata {
				if s.Gated && s.Gate == gateDeclared {
					declaredGate++
				}
			}
		}
	}
	if synthetic > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf(
			"%d cell(s) declare a synthetic population floor; this report is a fixture/component view, not qualification evidence.", synthetic))
	}
	if declaredGate > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf(
			"%d gated strata use cell-declared targets instead of the normal-write p99 rule; their cells' own flags carry the target verdict.", declaredGate))
	}
}

func assembleRow(class string, cells []*cellData) *row {
	row := &row{Class: class, Verdict: qual.Pass}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Profile != cells[j].Profile {
			return cells[i].Profile < cells[j].Profile
		}
		return cells[i].Replicate < cells[j].Replicate
	})
	for _, c := range cells {
		if row.Source == "" {
			row.Source, row.Manifest = c.source, c.manifest
			row.ProducerRun, row.ProducerAttempt = c.producerRun, c.producerAtt
			continue
		}
		if c.source != row.Source || c.manifest != row.Manifest ||
			c.producerRun != row.ProducerRun || c.producerAtt != row.ProducerAttempt {
			row.Reasons = appendUnique(row.Reasons, "cells do not share source/manifest/producer pins")
			row.Verdict = worst(row.Verdict, qual.Invalid)
		}
	}
	profiles := map[string][]*cellData{}
	var names []string
	for _, c := range cells {
		if _, ok := profiles[c.Profile]; !ok {
			names = append(names, c.Profile)
		}
		profiles[c.Profile] = append(profiles[c.Profile], c)
	}
	for _, expected := range []string{"standard", "legacy8"} {
		if !slices.Contains(names, expected) {
			names = append(names, expected)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		group := assembleProfile(name, profiles[name])
		row.Profiles = append(row.Profiles, group.profile)
		row.Verdict = worst(row.Verdict, group.verdict)
		row.Reasons = appendUnique(row.Reasons, group.reasons...)
	}
	return row
}

type profileGroup struct {
	*profile
	verdict      qual.Verdict
	reasons      []string
	missing      bool
	missingNames []string
}

func assembleProfile(name string, cells []*cellData) *profileGroup {
	p := &profileGroup{profile: &profile{Name: name}, verdict: qual.Pass}
	byReplicate := map[int]*cellData{}
	for _, c := range cells {
		if c.Replicate < 1 || c.Replicate > 3 {
			p.reasons = appendUnique(p.reasons, fmt.Sprintf("%s: cell %s has invalid replicate %d", name, filepath.Base(c.Directory), c.Replicate))
			p.verdict = worst(p.verdict, qual.Invalid)
			continue
		}
		if other, seen := byReplicate[c.Replicate]; seen {
			p.reasons = appendUnique(p.reasons, fmt.Sprintf("%s: duplicate replicate %d (%s and %s)",
				name, c.Replicate, filepath.Base(other.Directory), filepath.Base(c.Directory)))
			p.verdict = worst(p.verdict, qual.Invalid)
			continue
		}
		byReplicate[c.Replicate] = c
	}
	for replicate := 1; replicate <= 3; replicate++ {
		if _, ok := byReplicate[replicate]; !ok {
			p.missing = true
			p.missingNames = append(p.missingNames, fmt.Sprintf("%s-vm%d", name, replicate))
			p.verdict = worst(p.verdict, qual.Inconclusive)
		}
	}
	if p.missing {
		p.reasons = appendUnique(p.reasons, fmt.Sprintf("%s: missing VM(s) %s", name, strings.Join(p.missingNames, ", ")))
	}
	boots := map[string]string{}
	for replicate := 1; replicate <= 3; replicate++ {
		c := byReplicate[replicate]
		if c == nil {
			continue
		}
		p.Cells = append(p.Cells, &c.cellResult)
		if c.BootID == "" {
			c.Problems = appendLimited(c.Problems, "missing actual boot identity")
			c.Status = "invalid"
		} else if other, seen := boots[c.BootID]; seen {
			c.Problems = appendLimited(c.Problems, "duplicate boot identity within profile (with "+other+")")
			c.Status = "invalid"
			p.verdict = worst(p.verdict, qual.Invalid)
		} else {
			boots[c.BootID] = filepath.Base(c.Directory)
			p.BootIDs = append(p.BootIDs, c.BootID)
		}
		switch c.Status {
		case "complete":
		case "stopped", "refused":
			p.reasons = appendUnique(p.reasons, fmt.Sprintf(
				"%s: cell %s is %s; raw prefix retained (%d records, %d attempts, %d accepted)",
				name, filepath.Base(c.Directory), c.Status, c.Records, c.Prefix.Attempts, c.Prefix.Accepted))
			p.verdict = worst(p.verdict, qual.Fail)
		default:
			p.reasons = appendUnique(p.reasons, fmt.Sprintf("%s: cell %s is invalid", name, filepath.Base(c.Directory)))
			p.verdict = worst(p.verdict, qual.Invalid)
		}
		for _, problem := range c.Problems {
			p.reasons = appendUnique(p.reasons, fmt.Sprintf("%s/%s: %s", name, filepath.Base(c.Directory), problem))
		}
		switch {
		case !c.Target.Declared || c.Target.Flag == nil:
			p.reasons = appendUnique(p.reasons, fmt.Sprintf(
				"%s: cell %s has no target flag (unavailable, nonpassing)", name, filepath.Base(c.Directory)))
			p.verdict = worst(p.verdict, qual.Inconclusive)
		case !*c.Target.Flag:
			p.reasons = appendUnique(p.reasons, fmt.Sprintf("%s: cell %s declares its own target false%s",
				name, filepath.Base(c.Directory), targetSuffix(c.Target)))
			p.verdict = worst(p.verdict, qual.Fail)
		}
	}
	if len(p.BootIDs) != 3 && !p.missing {
		p.reasons = appendUnique(p.reasons, fmt.Sprintf("%s: need three distinct boot identities; have %d", name, len(p.BootIDs)))
		p.verdict = worst(p.verdict, qual.Invalid)
	}
	p.Strata = gateStrata(byReplicate)
	for _, stratum := range p.Strata {
		if stratum.Verdict == "" || stratum.Verdict == qual.Pass {
			continue
		}
		p.verdict = worst(p.verdict, stratum.Verdict)
		if stratum.Reason != "" {
			p.reasons = appendUnique(p.reasons, fmt.Sprintf("%s: %s/%s/%s: %s",
				name, stratum.Recipe, stratum.Population, stratum.Role, stratum.Reason))
		}
	}
	return p
}

func gateStrata(cells map[int]*cellData) []*stratumRow {
	keys := map[stratumKey]bool{}
	floor := 0
	complete := 0
	for _, c := range cells {
		floor = max(floor, c.Target.MinHoldsPerHost)
		if c.Status == "complete" {
			complete++
			for key := range c.strata {
				keys[key] = true
			}
		}
	}
	if floor == 0 {
		floor = defaultMinHolds
	}
	if complete == 0 {
		// Nothing complete: keep whatever prefix exists visible rather than
		// reporting an empty row. The row is non-passing because of cell status.
		for _, c := range cells {
			for key := range c.strata {
				keys[key] = true
			}
		}
	}
	ordered := make([]stratumKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].less(ordered[j]) })
	out := make([]*stratumRow, 0, len(ordered))
	for _, key := range ordered {
		sr := &stratumRow{Recipe: key.Recipe, Population: key.Population, Role: key.Role,
			Gated: gatedPopulation(key.Population), Gate: gateOf(cells), Floor: floor}
		hosts := make([]qual.HostHolds, 0, 3)
		for replicate := 1; replicate <= 3; replicate++ {
			c := cells[replicate]
			if c == nil {
				continue
			}
			s := c.strata[key]
			host := stratumHost{Cell: c.Directory, Profile: c.Profile, Replicate: replicate, BootID: c.BootID}
			hh := qual.HostHolds{HostID: firstNonEmpty(c.BootID, "missing-boot:"+filepath.Base(c.Directory))}
			if s != nil {
				host.Count, host.Errors, host.Refusals, host.Fenced = len(s.holds), s.errors, s.refusals, s.fenced
				host.Distribution = display(s.holds, floor)
				hh.CompleteHolds = append(hh.CompleteHolds, s.holds...)
				hh.FailedCommands = s.errors + s.fenced
				hh.Refusals = s.refusals
			} else {
				host.Distribution = &displayDistribution{Unavailable: "cell has no observations for this recipe/population/role"}
			}
			hh.ProtocolErrors = append(hh.ProtocolErrors, c.Problems...)
			hosts = append(hosts, hh)
			sr.Hosts = append(sr.Hosts, host)
		}
		if !sr.Gated {
			sr.Reason = "not gated by the normal-write rule; retained as reported evidence"
			out = append(out, sr)
			continue
		}
		gate := evaluateGate(hosts, floor, sr.Gate == gateHosted)
		sr.GateReport = &gate
		sr.Verdict = gate.Verdict
		if gate.Verdict != qual.Pass {
			sr.Reason = gateReason(gate)
		} else if sr.Gate == gateDeclared {
			sr.Reason = "cell-declared target met; the normal-write p99 gate does not apply to this class"
		}
		out = append(out, sr)
	}
	return out
}

// gateOf picks the stricter gate when cells disagree: any hosted declaration
// keeps the normal-write rule on the whole stratum.
func gateOf(cells map[int]*cellData) string {
	gate := gateDeclared
	for _, c := range cells {
		if c == nil || c.Target.Gate != gateDeclared {
			return gateHosted
		}
	}
	return gate
}

func gateReason(gate qual.HoldReport) string {
	var reasons []string
	reasons = append(reasons, gate.Reasons...)
	for i, host := range gate.Hosts {
		if host.Verdict == qual.Pass {
			continue
		}
		reasons = appendUnique(reasons, fmt.Sprintf("vm%d (%s): %s", i+1, host.Verdict, strings.Join(host.Reasons, ", ")))
	}
	if len(reasons) == 0 {
		return string(gate.Verdict)
	}
	return strings.Join(reasons, "; ")
}

// evaluateGate applies the reviewed per-host rule. At the standard 10,000 floor
// with the hosted p99 gate it delegates to the reviewed implementation. A
// synthetic fixture or component class that declares a lower floor, or whose own
// target is not the normal-write p99, gets the identical population/failure
// checks with only those parts changed. The population floor is always enforced.
func evaluateGate(hosts []qual.HostHolds, floor int, p99 bool) qual.HoldReport {
	if p99 && floor >= qual.Phase1HoldsPerHost {
		return qual.EvaluateHostedHolds(hosts)
	}
	r := qual.HoldReport{Verdict: qual.Pass, Hosts: make([]qual.HostReport, 0, len(hosts))}
	seen := make(map[string]bool, len(hosts))
	var minMedian, maxMedian time.Duration
	invalid, failed, incomplete := false, false, len(hosts) < qual.HostsPerProfile
	for _, host := range hosts {
		h := qual.HostReport{
			HostID: host.HostID, FailedCommands: host.FailedCommands,
			Refusals: host.Refusals, Verdict: qual.Pass,
			Reasons: append([]string(nil), host.ProtocolErrors...),
		}
		if host.HostID == "" || seen[host.HostID] {
			h.Reasons = append(h.Reasons, "missing or duplicate independent host identity")
		}
		seen[host.HostID] = true
		if host.FailedCommands < 0 || host.Refusals < 0 {
			h.Reasons = append(h.Reasons, "negative command count")
		}
		if len(host.CompleteHolds) > 0 {
			d, err := qual.Summarize(host.CompleteHolds)
			if err != nil {
				h.Reasons = append(h.Reasons, err.Error())
			} else {
				h.Holds = &d
				if minMedian == 0 || d.P50 < minMedian {
					minMedian = d.P50
				}
				maxMedian = max(maxMedian, d.P50)
			}
		}
		if len(h.Reasons) > 0 {
			h.Verdict = qual.Invalid
			invalid = true
		} else if host.FailedCommands > 0 || host.Refusals > 0 {
			h.Verdict = qual.Fail
			failed = true
			if host.FailedCommands > 0 {
				h.Reasons = append(h.Reasons, "failed commands retained in raw evidence")
			}
			if host.Refusals > 0 {
				h.Reasons = append(h.Reasons, "writer quantum refusal budget is zero")
			}
		} else if len(host.CompleteHolds) < floor {
			h.Verdict = qual.Inconclusive
			incomplete = true
			h.Reasons = append(h.Reasons, fmt.Sprintf("need %d complete accepted holds per host", floor))
		} else if p99 && (h.Holds.P99Upper95 == nil || h.Holds.P99Upper95.Value > qual.WriterP99Budget) {
			h.Verdict = qual.Fail
			failed = true
			h.Reasons = append(h.Reasons, "per-host upper 95% p99 bound exceeds 10 ms or is unavailable")
		}
		r.Hosts = append(r.Hosts, h)
	}
	if minMedian > 0 {
		r.MedianRatio = float64(maxMedian) / float64(minMedian)
		r.Split = r.MedianRatio >= 4
	}
	if len(hosts) < qual.HostsPerProfile {
		r.Reasons = append(r.Reasons, "need at least three independent VMs per profile")
	}
	if r.Split {
		r.Reasons = append(r.Reasons, "fourfold median split: retain cohort and rerun on at least three fresh VMs")
	}
	switch {
	case invalid:
		r.Verdict = qual.Invalid
	case r.Split:
		r.Verdict = qual.Inconclusive
	case failed:
		r.Verdict = qual.Fail
	case incomplete:
		r.Verdict = qual.Inconclusive
	}
	return r
}

// ---------------------------------------------------------------------------
// Helpers

func checkIdentity(record map[string]any, c *cellData, line int) error {
	if schema := stringField(record, "schema"); schema != rawSchema {
		return fmt.Errorf("raw schema %q is not %q", schema, rawSchema)
	}
	if sequence := numberField(record, "sequence"); sequence == nil || *sequence != int64(line) {
		return fmt.Errorf("raw sequence is not the retained line number")
	}
	for _, pair := range []struct{ field, want string }{
		{"class", c.Class}, {"profile", c.Profile}, {"declared_source_commit", c.source},
	} {
		if pair.want != "" && stringField(record, pair.field) != pair.want {
			return fmt.Errorf("raw %s disagrees with the cell receipt", pair.field)
		}
	}
	if epoch := stringField(record, "host_epoch"); epoch != "" && c.HostEpoch != "" && epoch != c.HostEpoch {
		return fmt.Errorf("raw host_epoch disagrees with the cell receipt")
	}
	return nil
}

func decodeRecord(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing data after record")
	}
	return record, nil
}

func stringField(object map[string]any, key string) string {
	if value, ok := object[key].(string); ok {
		return value
	}
	return ""
}

func numberField(object map[string]any, key string) *int64 {
	switch value := object[key].(type) {
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return &n
		}
		if f, err := value.Float64(); err == nil {
			n := int64(f)
			return &n
		}
	case float64:
		n := int64(value)
		return &n
	}
	return nil
}

func boolField(object map[string]any, key string) (*bool, bool) {
	value, ok := object[key]
	if !ok {
		return nil, false
	}
	b, ok := value.(bool)
	if !ok {
		return nil, true
	}
	return &b, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func appendUnique(list []string, values ...string) []string {
	for _, value := range values {
		if value == "" || slices.Contains(list, value) {
			continue
		}
		list = append(list, value)
	}
	return list
}

func appendLimited(list []string, value string) []string {
	if len(list) >= maxProblems {
		return list
	}
	return appendUnique(list, value)
}

func targetSuffix(t targetDecl) string {
	if t.Description != "" {
		return " (" + t.Description + ")"
	}
	return ""
}

func truncate(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func worst(a, b qual.Verdict) qual.Verdict {
	rank := func(v qual.Verdict) int {
		switch v {
		case qual.Invalid:
			return 3
		case qual.Fail:
			return 2
		case qual.Inconclusive:
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}
