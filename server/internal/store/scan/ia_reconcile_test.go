package scan

import (
	"errors"
	"strings"
	"testing"
)

func iaReconcileCurrent() BasicInput {
	b := basicInput()
	b.File.RelativePath = "Show/episode.mkv"
	b.File.ObjectIdentity = "stable-native-id"
	return b
}

func TestIAReconcileUnchangedAndChangedFiles(t *testing.T) {
	accepted := iaReconcileCurrent()
	previous := accepted.File
	plan, err := PlanReconcile(ReconcileInput{Current: accepted, AcceptedBasic: &accepted, AtPath: &previous}, 1<<20)
	if err != nil || plan.Kind != ReconcileUnchanged || plan.Basic != ReuseBasic {
		t.Fatalf("unchanged rescan schedules work: %+v %v", plan, err)
	}
	for _, row := range []struct {
		name string
		edit func(*BasicInput)
		kind ReconcileKind
	}{
		{"changed bytes", func(b *BasicInput) { b.File.Size++; b.File.Revision = "new" }, ReconcileRefresh},
		{"replacement", func(b *BasicInput) { b.File.Size++; b.File.ObjectIdentity = "different"; b.File.Revision = "new" }, ReconcileReplacement},
		{"inode renumbered", func(b *BasicInput) { b.File.ObjectIdentity = "different" }, ReconcileRefresh},
		{"root rebound", func(b *BasicInput) { b.File.Source.Incarnation = "new" }, ReconcileRefresh},
		{"policy changed", func(b *BasicInput) { b.PolicyRevision++ }, ReconcileUnchanged},
		{"sidecar changed", func(b *BasicInput) { b.NFO.Revision = "new" }, ReconcileUnchanged},
	} {
		t.Run(row.name, func(t *testing.T) {
			current := accepted
			row.edit(&current)
			plan, err := PlanReconcile(ReconcileInput{Current: current, AcceptedBasic: &accepted, AtPath: &previous}, 1<<20)
			if err != nil || plan.Kind != row.kind || plan.Basic != ProbeRequired || plan.Previous != previous {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
		})
	}
}

func TestIAReconcileMovesRequireCompleteUnambiguousNativeEvidence(t *testing.T) {
	current := iaReconcileCurrent()
	old := current.File
	old.RelativePath = "Show/old-name.mkv"
	input := ReconcileInput{Current: current, MoveCandidates: []FileWitness{old}}
	if p, err := PlanReconcile(input, 1<<20); err != nil || p.Kind != ReconcileNeedLookup {
		t.Fatalf("unproved candidate window consumed: %+v %v", p, err)
	}
	input.MoveWindowComplete = true
	if p, err := PlanReconcile(input, 1<<20); err != nil || p.Kind != ReconcileRelocation || p.Previous != old || p.Basic != ProbeRequired {
		t.Fatalf("exact move candidate lost: %+v %v", p, err)
	}
	// Two hardlink locations must not arbitrarily select one old location.
	other := old
	other.RelativePath = "Show/another-link.mkv"
	input.MoveCandidates = []FileWitness{old, other}
	if p, err := PlanReconcile(input, 1<<20); err != nil || p.Kind != ReconcileAmbiguous || p.Previous != (FileWitness{}) {
		t.Fatalf("ambiguous native identity selected: %+v %v", p, err)
	}
	for _, edit := range []func(*FileWitness){
		func(w *FileWitness) { w.ObjectIdentity = "" },
		func(w *FileWitness) { w.Revision = "changed" },
		func(w *FileWitness) { w.Source.Incarnation = "old-incarnation" },
	} {
		candidate := old
		edit(&candidate)
		input.MoveCandidates = []FileWitness{candidate}
		if p, err := PlanReconcile(input, 1<<20); err != nil || p.Kind != ReconcileNewLocation {
			t.Fatalf("weak move evidence selected: %+v %v", p, err)
		}
	}
	old.Source.LibraryID++
	input.MoveCandidates = []FileWitness{old}
	if p, err := PlanReconcile(input, 1<<20); !errors.Is(err, ErrInvalid) || p.Kind != 0 {
		t.Fatalf("cross-library identity matched: %+v %v", p, err)
	}
}

func TestIAReconcileBudgetsAllRawInputs(t *testing.T) {
	current := iaReconcileCurrent()
	input := ReconcileInput{Current: current, MoveWindowComplete: true}
	input.MoveCandidates = make([]FileWitness, 3)
	if p, err := PlanReconcile(input, 1<<20); !errors.Is(err, ErrCapacity) || p.Kind != 0 {
		t.Fatalf("wide candidate list admitted: %+v %v", p, err)
	}
	input.MoveCandidates = nil
	input.Current.NFO.Revision = strings.Repeat("x", 2048)
	if p, err := PlanReconcile(input, 2048); !errors.Is(err, ErrCapacity) || p.Kind != 0 {
		t.Fatalf("sidecar evidence escaped budget: %+v %v", p, err)
	}
	input.Current = current
	input.Current.Available = false
	if p, err := PlanReconcile(input, 1<<20); !errors.Is(err, ErrInvalid) || p.Kind != 0 {
		t.Fatalf("offline observation used as present: %+v %v", p, err)
	}
}

func TestIACoverageNeverInfersAbsenceFromPartialOrForeignDirectory(t *testing.T) {
	file := iaReconcileCurrent().File
	coverage := DirectoryCoverage{Source: file.Source, RunID: "run", Directory: "Show", Identity: "directory", Revision: "revision", EOF: true, Verified: true}
	if !MayCheckMissing(coverage, file, "old-run") {
		t.Fatal("complete exact parent cannot nominate an unseen object")
	}
	for _, change := range []struct {
		name string
		edit func(*DirectoryCoverage)
	}{
		{"no EOF", func(c *DirectoryCoverage) { c.EOF = false }},
		{"not verified", func(c *DirectoryCoverage) { c.Verified = false }},
		{"protected", func(c *DirectoryCoverage) { c.Protected = true }},
		{"unstable", func(c *DirectoryCoverage) { c.Unstable = true }},
		{"failed", func(c *DirectoryCoverage) { c.Failed = true }},
		{"other parent", func(c *DirectoryCoverage) { c.Directory = "Show/Other" }},
		{"prefix sibling", func(c *DirectoryCoverage) { c.Directory = "Sho" }},
		{"other library", func(c *DirectoryCoverage) { c.Source.LibraryID++ }},
		{"other incarnation", func(c *DirectoryCoverage) { c.Source.Incarnation = "new" }},
		{"no run", func(c *DirectoryCoverage) { c.RunID = "" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			c := coverage
			change.edit(&c)
			if MayCheckMissing(c, file, "old-run") {
				t.Fatal("ineligible coverage nominated missing file")
			}
		})
	}
	if MayCheckMissing(coverage, file, "run") {
		t.Fatal("current run's seen object nominated missing")
	}
}
