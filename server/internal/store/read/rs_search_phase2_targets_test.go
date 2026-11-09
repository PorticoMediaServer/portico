package read

// RS search phase-2 qualification targets (packet RS-21).
//
// This file REGISTERS targets. It measures nothing. Registering a target is not
// a measurement: every cell below declares target_flag null, states why it is
// unmeasured, names the refusing start gate that refuses it, and binds its
// evidence to the ACTUAL merged native/domain inventories (no synthetic
// stand-in). Nothing here may claim qualification, and no old SQL prototype,
// warm fixture or missing cell can become a pass.
//
// The per-cell documents are the same closed shape ci/qual_class.py transports
// (portico-class-target-v1). The roll-up reads a null target_flag as
// unavailable/non-passing, so an unmeasured registration cannot pass.
//
// Handoff (this task must not edit hard-coded inventories): the class names
// below are registered through the canonical class-target kit in
// server/internal/store/writer/rs_search_phase2_targets_kit_test.go, NOT in the
// shared writer canonicalHeldClasses() registry, and they are not appended to
// ci/qual_class.py TARGETS. When the owner composition lands, that registry and
// TARGETS carry these names once, with the kit registration moved there.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	// rsSearchTargetVersion and the gate vocabulary mirror the closed transport
	// in ci/qual_class.py. A read target is cell_declared: the normal-writer
	// hosted p99 gate does not apply to a read class.
	rsSearchTargetVersion   = "portico-class-target-v1"
	rsSearchTargetGate      = "cell_declared"
	rsSearchTargetProfile   = "standard"
	rsSearchTargetReplicate = 1
	// rsSearchTargetRouteDebt is B-R08's Search route statement budget. The
	// inventory below is larger; the gap is declared owner FTS debt, never a
	// measured statement count.
	rsSearchTargetRouteDebt = 20
	// rsSearchTargetGroupCount is the closed public group set.
	rsSearchTargetGroupCount = 11
	// rsSearchTargetMinHolds is the per-host population floor ci/qual_class.py
	// enforces for a non-synthetic target. A smaller declared floor would be
	// transport-rejected, so registration refuses it here too.
	rsSearchTargetMinHolds = 10000
	// rsSearchTargetRefusal is the ONE refusing start gate these targets use.
	rsSearchTargetRefusal = "writer.classTargetStartGate in beginRSSearchPhase2Target: ErrClassTargetUnbound while the registered class has no compiled runner"
)

// rsSearchTargetComponent is one independently reported resource component.
// Components are never pooled into one RSS claim: every component needs its own
// target and its own flag, and one unmeasured component keeps the cell
// unavailable.
type rsSearchTargetComponent struct {
	Name   string
	Target string
	Flag   *bool
}

// rsSearchTargetCell is one registered qualification cell. Flag is nil for
// every registered target; a non-nil flag is a validation error, because this
// file cannot mint a measured verdict.
type rsSearchTargetCell struct {
	Class         string
	Population    string
	Target        string
	Gate          string
	MinPopulation int
	Components    []rsSearchTargetComponent
	Evidence      []string
	RefusingStart string
	FollowUps     []string
	Reason        string
	Flag          *bool
}

// rsSearchFollowUps returns the shared owner follow-ups plus any cell-specific
// one. The native debt/retirement/cold-copy follow-ups are explicit on every
// cell so a pass cannot silently drop them.
func rsSearchFollowUps(extra ...string) []string {
	out := []string{
		"owner W/INT: declare the native FTS statement debt against B-R08's 20-route budget",
		"owner W/INT: name native proof retirement and the cold-copy follow-up before any pass",
		"owner W/INT: bind the accepted search publisher/generation; a different engine/fingerprint or unrefreshed bank refuses",
	}
	return append(out, extra...)
}

// rsSearchPhase2Targets is the complete registered target set for RS-21.
// Adding or removing a cell requires updating the kit extension in
// writer/rs_search_phase2_targets_kit_test.go (the class names are duplicated
// there because a _test.go declaration cannot cross packages).
func rsSearchPhase2Targets() []rsSearchTargetCell {
	cells := []rsSearchTargetCell{
		{
			Class:         "rs.search.rss",
			Population:    "canonical-movies-v1-search, 1000000 residents, library7 qualification-movies",
			Target:        "separate component targets for Go heap, native allocation, output payload, retained custody and the cold bank copy; components are never summed",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Components: []rsSearchTargetComponent{
				{Name: "go", Target: "unmeasured peak Go heap and live set during one native invocation per poll"},
				{Name: "native", Target: "unmeasured native SQLite allocation/peak for the same invocation"},
				{Name: "output", Target: "unmeasured output card and payload bytes for the selected page"},
				{Name: "retained", Target: "unmeasured retained bank, stamp and proof custody bytes"},
				{Name: "cold_copy", Target: "unmeasured cold bank copy bytes, reported apart from the retained live bank"},
			},
			Evidence:      []string{"artifact:canonical-movies-v1-search", "transport:portico-canonical-held-class-v1", "sql:read.full.native.bank", "memory:go", "memory:native", "memory:output", "memory:retained", "memory:cold_copy"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS: attach the paired canonical search bank and the ConsumeForeground timeline before any component is measured"),
			Reason:        "UNMEASURED: no canonical 1M paired search-bank composition or per-component sampling is attached; a warm fixture or a summed figure would be a fabricated pass",
		},
		{
			Class:         "rs.search.bank_cold_open",
			Population:    "exported canonical-movies-v1-search bank copied and opened from cold cache at 1000000 residents",
			Target:        "unmeasured cold-cache copy and open of the exported paired search bank, reported apart from any warm open",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"artifact:canonical-movies-v1-search", "owner:cold-bank-copy-open", "sql:read.full.native.bank"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS/W: attach the cold copy/open target after the paired bank export lands"),
			Reason:        "UNMEASURED: the export copy/open path and OS-cold state are not attached; the warm fixture open is a different cell",
		},
		{
			Class:         "rs.search.people_ordinary",
			Population:    "canonical 1M People bank with a trusted credit scope over every library/kind view",
			Target:        "unmeasured ordinary selective People query with exact role, count, portrait and known-for over the complete trusted scope",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"sql:read.full.native.fts.catalog_people_names.first", "sql:read.browse.rs.people.page.credit", "sql:read.browse.rs.people.page.work_source"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS: attach the merged complete PersonPage hydration to the same borrowed search lease"),
			Reason:        "UNMEASURED: no canonical 1M People bank or trusted credit scope is attached; a tiny owned-scope fixture is not this cell",
		},
		{
			Class:         "rs.search.people_rare",
			Population:    "canonical 1M People bank, singleton names",
			Target:        "unmeasured rare/singleton People name under the same complete summary proof",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"sql:read.full.native.fts.catalog_people_names.first", "sql:read.browse.rs.people.page.credit"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps(),
			Reason:        "UNMEASURED: no canonical singleton-name population or summary composition is attached",
		},
		{
			Class:         "rs.search.people_dense",
			Population:    "canonical 1M People bank, dense common names",
			Target:        "unmeasured dense common-name People query with high credit fan-out; summary work stays exact",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"sql:read.full.native.fts.catalog_people_names.first", "sql:read.browse.rs.people.page.credit", "sql:read.browse.rs.people.page.portrait"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS: keep dense-name deadline misses as failed cells, never an underfilled successful page"),
			Reason:        "UNMEASURED: no dense canonical name cohort or exact-summary composition is attached",
		},
		{
			Class:         "rs.search.people_ubiquitous",
			Population:    "canonical 1M People bank, a name present on most residents",
			Target:        "unmeasured ubiquitous-name People query; the unknown query-total disposition stays explicit",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"sql:read.full.native.fts.catalog_people_names.first", "sql:read.browse.rs.people.page.credit"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps(),
			Reason:        "UNMEASURED: no ubiquitous-name cohort is attached and no exact query population may be implied",
		},
		{
			Class:         "rs.search.people_restricted",
			Population:    "canonical 1M People bank, a person invisible in the trusted credit scope",
			Target:        "unmeasured restricted person present in the bank but absent from the trusted scope; the ranked hit refuses rather than shrinking the page",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"sql:read.full.native.fts.catalog_people_names.first", "sql:read.browse.rs.people.page.credit", "owner:trusted-credit-scope-refusal"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS: a missing credit scope must refuse a ranked hit, never underfill a selected top forty"),
			Reason:        "UNMEASURED: the restricted canonical cohort and its refusal witness are not attached",
		},
		{
			Class:         "rs.search.poll_preview_budget",
			Population:    "one native invocation per poll over the canonical 1M bank",
			Target:        "unmeasured one native invocation per poll, preview candidates <=2000 and a MATCH candidate total <=2000 including Pending/ranking work",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"control:one-native-call", "control:preview-2000", "sql:read.full.native.bank"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS: record the single-invocation witness on the actual poll, not a synthetic adapter call count"),
			Reason:        "UNMEASURED: no canonical poll composition or single-invocation witness is attached",
		},
		{
			Class:         "rs.search.group_traversals",
			Population:    "every closed public group over the canonical 1M bank",
			Target:        "unmeasured complete traversal of every selected group (full forty-card keyset) and of all eleven closed groups (People, kinds 1,2,4,5,6,7,8,10,13 and Reading) with exact continuation",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"control:eleven-groups", "sql:read.browse.rs.reading.source", "artifact:canonical-movies-v1-search"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS: unsupported or missing-group sources stay named pending, never an empty successful group"),
			Reason:        "UNMEASURED: no full continuation across the closed group set is attached; fixtures prove only their stated scope",
		},
		{
			Class:         "rs.search.reading_density",
			Population:    "accepted Reading publications/inventory at canonical 1M text and metadata density",
			Target:        "unmeasured real Reading text and metadata density through the accepted publication and inventory points",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"sql:read.browse.rs.reading.source", "sql:read.browse.rs.reading.facts", "sql:read.browse.rs.reading.octets"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS/IP: keep converter recipe41 closed until a trusted converter proof exists"),
			Reason:        "UNMEASURED: no accepted 1M Reading publication/inventory density is attached",
		},
		{
			Class:         "rs.search.field_64kib",
			Population:    "every searchable field at the accepted 64KiB cap on the canonical 1M bank",
			Target:        "unmeasured every searchable field at the accepted 64KiB cap; discovery and verification use the same normalized prefix",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"control:field-64kib", "sql:read.full.native.fragment"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner W: the 64KiB cap stays the common postings limit; a longer field is not silently admitted"),
			Reason:        "UNMEASURED: no 64KiB-field canonical distribution is attached; the constant is bound, the scale is not measured",
		},
		{
			Class:         "rs.search.entropy_latin",
			Population:    "high-entropy Latin fields on the canonical 1M bank",
			Target:        "unmeasured high-entropy Latin field; no candidate is dropped or mis-ranked by normalization",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"control:entropy-latin", "sql:read.full.native.fts.search_fragment_lexical.first"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps(),
			Reason:        "UNMEASURED: no high-entropy Latin canonical cohort is attached",
		},
		{
			Class:         "rs.search.entropy_cjk",
			Population:    "high-entropy CJK fields on the canonical 1M bank",
			Target:        "unmeasured high-entropy CJK field through the accepted sibling normalizer",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"control:entropy-cjk", "sql:read.full.native.fts.search_fragment_grams.first"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner RS: the packed-fragment sibling negative is required evidence for this cell"),
			Reason:        "UNMEASURED: no high-entropy CJK canonical cohort is attached",
		},
		{
			Class:         "rs.search.entropy_name",
			Population:    "high-entropy person names on the canonical 1M People bank",
			Target:        "unmeasured high-entropy person name through the People name bank",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"control:entropy-name", "sql:read.full.native.fts.catalog_people_names.first"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps(),
			Reason:        "UNMEASURED: no high-entropy canonical name cohort is attached",
		},
		{
			Class:         "rs.search.fragment_logical_subject",
			Population:    "independently packed native fragment columns, logical subject/field/generation join",
			Target:        "packed-fragment logical-subject verifier negative: a sibling normalized match must not be dropped by row-local physical chunk verification",
			Gate:          rsSearchTargetGate,
			MinPopulation: 10000,
			Evidence:      []string{"control:logical-subject-verifier", "sql:read.full.native.fragment", "sql:read.full.native.fts.search_fragment_grams.first"},
			RefusingStart: rsSearchTargetRefusal,
			FollowUps:     rsSearchFollowUps("owner HN: the native witness producer and packed-fragment distribution remain gated"),
			Reason:        "UNMEASURED: the merged bounded control proves the negative, but the 1M packed-fragment distribution and native witness producer are not attached",
		},
	}
	return cells
}

// rsSearchTargetDocument is the closed per-cell target declaration transported
// by ci/qual_class.py. TargetFlag is always present and null here: a registered
// target that is not measured stays unavailable.
type rsSearchTargetDocument struct {
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

// rsSearchTargetDocuments generates the per-cell target.json declarations. They
// are generated from the registration, not hand written, and every one carries
// a null flag.
func rsSearchTargetDocuments() []rsSearchTargetDocument {
	cells := rsSearchPhase2Targets()
	out := make([]rsSearchTargetDocument, 0, len(cells))
	for _, cell := range cells {
		out = append(out, rsSearchTargetDocument{
			Version:         rsSearchTargetVersion,
			Class:           cell.Class,
			Profile:         rsSearchTargetProfile,
			Replicate:       rsSearchTargetReplicate,
			Target:          cell.Target,
			TargetFlag:      cell.Flag,
			Reason:          cell.Reason,
			Gate:            cell.Gate,
			MinHoldsPerHost: cell.MinPopulation,
			Synthetic:       false,
		})
	}
	return out
}

// rsSearchTargetInventory is the union of the ACTUAL merged statement
// inventories this family binds: the main native search bank, the RS Reading
// contribution, the RS People search composition, the complete PersonPage,
// the person credit windows and the Reading facts. No synthetic definition is
// added; a declared sql: evidence reference must resolve here.
func rsSearchTargetInventory() map[string]bool {
	out := make(map[string]bool)
	add := func(defs []stmt.Definition) {
		for _, definition := range defs {
			out[string(definition.ID)] = true
		}
	}
	add(NativeFullSearchDefinitions())
	add(RSNativeSearchDefinitions())
	add(RSPeopleSearchDefinitions())
	add(RSPeoplePageDefinitions())
	add(PersonCreditsDefinitions())
	add(ReadingFactsDefinitions())
	return out
}

// rsSearchValidateTargets is the registration gate. It refuses a claimed flag,
// a missing refusing gate, an unmeasured reason that is not stated, a cell with
// no bound actual SQL inventory, and unknown evidence kinds.
func rsSearchValidateTargets(cells []rsSearchTargetCell, inventory map[string]bool) error {
	seen := make(map[string]bool)
	var problems []string
	for _, cell := range cells {
		if cell.Class == "" || len(cell.Class) > 64 || seen[cell.Class] {
			problems = append(problems, "duplicate, empty or oversized class "+cell.Class)
		}
		seen[cell.Class] = true
		if cell.Flag != nil {
			problems = append(problems, cell.Class+": a registered target must stay unmeasured (target_flag must be null)")
		}
		if cell.Gate != rsSearchTargetGate {
			problems = append(problems, cell.Class+": read target must declare the cell_declared gate")
		}
		if cell.Target == "" || len(cell.Target) > 512 {
			problems = append(problems, cell.Class+": target text missing or over 512 characters")
		}
		if cell.Reason == "" || len(cell.Reason) > 1024 || !strings.Contains(cell.Reason, "UNMEASURED") {
			problems = append(problems, cell.Class+": reason must state its unmeasured state")
		}
		if cell.RefusingStart == "" {
			problems = append(problems, cell.Class+": must declare its refusing start gate")
		}
		if cell.MinPopulation < rsSearchTargetMinHolds {
			problems = append(problems, cell.Class+": min_holds_per_host must be at least 10000")
		}
		if len(cell.FollowUps) == 0 {
			problems = append(problems, cell.Class+": owner follow-ups required")
		}
		sqlEvidence := 0
		for _, evidence := range cell.Evidence {
			kind, ref, ok := strings.Cut(evidence, ":")
			if !ok || ref == "" {
				problems = append(problems, cell.Class+": malformed evidence "+evidence)
				continue
			}
			switch kind {
			case "sql":
				sqlEvidence++
				if !inventory[ref] {
					problems = append(problems, cell.Class+": unknown actual SQL inventory "+ref)
				}
			case "artifact", "transport", "memory", "owner", "control":
				if len(ref) > 128 {
					problems = append(problems, cell.Class+": oversized evidence reference")
				}
			default:
				problems = append(problems, cell.Class+": unknown evidence kind "+kind)
			}
		}
		if sqlEvidence == 0 {
			problems = append(problems, cell.Class+": must bind an actual SQL inventory")
		}
		if len(cell.Components) > 0 {
			names := make(map[string]bool)
			for _, component := range cell.Components {
				if component.Name == "" || names[component.Name] || component.Target == "" || len(component.Target) > 512 || component.Flag != nil {
					problems = append(problems, cell.Class+": invalid or measured component "+component.Name)
				}
				names[component.Name] = true
			}
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func TestRSSearchPhase2TargetsAreRegisteredUnmeasured(t *testing.T) {
	cells := rsSearchPhase2Targets()
	if len(cells) != 15 {
		t.Fatalf("registered RS search phase-2 cells changed: %d", len(cells))
	}
	if err := rsSearchValidateTargets(cells, rsSearchTargetInventory()); err != nil {
		t.Fatal(err)
	}
	for _, cell := range cells {
		if cell.Flag != nil {
			t.Fatalf("%s claimed a measured target", cell.Class)
		}
		t.Logf("%s: %s", cell.Class, cell.Reason)
	}
}

func TestRSSearchPhase2TargetDocumentsAreUnmeasured(t *testing.T) {
	want := []string{"version", "class", "profile", "replicate", "target", "target_flag", "reason", "gate", "min_holds_per_host", "synthetic"}
	dir := t.TempDir()
	seen := make(map[string]bool)
	for _, document := range rsSearchTargetDocuments() {
		if seen[document.Class] {
			t.Fatalf("duplicate document for %s", document.Class)
		}
		seen[document.Class] = true
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded) != len(want) {
			t.Fatalf("%s: closed target document changed: %s", document.Class, raw)
		}
		for _, key := range want {
			if _, ok := decoded[key]; !ok {
				t.Fatalf("%s: missing closed target key %s", document.Class, key)
			}
		}
		flag, ok := decoded["target_flag"]
		if !ok || flag != nil {
			t.Fatalf("%s: an unmeasured target published a flag: %v", document.Class, flag)
		}
		if decoded["version"] != rsSearchTargetVersion || decoded["gate"] != rsSearchTargetGate {
			t.Fatalf("%s: target declaration drifted: %s", document.Class, raw)
		}
		path := filepath.Join(dir, document.Class, "target.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		stored, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var reread map[string]any
		if err := json.Unmarshal(stored, &reread); err != nil {
			t.Fatal(err)
		}
		if reread["target_flag"] != nil {
			t.Fatalf("%s: stored target became a passing flag", document.Class)
		}
	}
	if len(seen) != len(rsSearchPhase2Targets()) {
		t.Fatalf("generated documents missing cells: %d", len(seen))
	}
}

func TestRSSearchPhase2TargetValidationRefusesAPassingOrUnboundCell(t *testing.T) {
	inventory := rsSearchTargetInventory()
	mutate := func(change func(*rsSearchTargetCell)) error {
		cells := append([]rsSearchTargetCell(nil), rsSearchPhase2Targets()...)
		change(&cells[0])
		return rsSearchValidateTargets(cells, inventory)
	}
	pass := true
	if err := mutate(func(cell *rsSearchTargetCell) { cell.Flag = &pass }); err == nil {
		t.Fatal("a passing flag was accepted")
	}
	if err := mutate(func(cell *rsSearchTargetCell) { cell.Evidence = []string{"sql:read.browse.rs.absent"} }); err == nil {
		t.Fatal("an absent actual inventory was accepted")
	}
	if err := mutate(func(cell *rsSearchTargetCell) { cell.Reason = "measured elsewhere" }); err == nil {
		t.Fatal("an undeclared unmeasured state was accepted")
	}
	if err := mutate(func(cell *rsSearchTargetCell) { cell.RefusingStart = "" }); err == nil {
		t.Fatal("a target without its refusing start gate was accepted")
	}
	if err := mutate(func(cell *rsSearchTargetCell) { cell.MinPopulation = rsSearchTargetMinHolds - 1 }); err == nil {
		t.Fatal("a target below the qual_class min_holds_per_host floor was accepted")
	}
	if err := mutate(func(cell *rsSearchTargetCell) { cell.Components[0].Flag = &pass }); err == nil {
		t.Fatal("a measured component was accepted")
	}
}

// The handoff above is deliberate: these classes are registered only in the
// writer kit extension, not in canonicalHeldClasses() or ci/qual_class.py
// TARGETS, so a generated target.json is deliberately transport-rejected until
// the owner composition lands. Pin both halves of that state instead of letting
// it drift silently in either direction.
func TestRSSearchPhase2ClassNamesStayDeliberatelyDeferred(t *testing.T) {
	qualification, err := os.ReadFile("../../../../ci/qual_class.py")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := os.ReadFile("../writer/canonical_held_targets_test.go")
	if err != nil {
		t.Fatal(err)
	}
	kit, err := os.ReadFile("../writer/rs_search_phase2_targets_kit_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range rsSearchPhase2Targets() {
		for _, quoted := range []string{`"` + cell.Class + `"`, `'` + cell.Class + `'`} {
			if strings.Contains(string(qualification), quoted) {
				t.Fatalf("%s was registered in ci/qual_class.py TARGETS; move the deferred handoff with it", cell.Class)
			}
			if strings.Contains(string(canonical), quoted) {
				t.Fatalf("%s was registered in canonicalHeldClasses(); move the deferred handoff with it", cell.Class)
			}
		}
		if !strings.Contains(string(kit), `"`+cell.Class+`"`) {
			t.Fatalf("%s is not registered in the writer kit extension", cell.Class)
		}
	}
}

func TestRSSearchPhase2TargetsBindActualInventories(t *testing.T) {
	inventory := rsSearchTargetInventory()
	for _, id := range []string{
		"read.full.native.bank",
		"read.full.native.revision",
		"read.full.native.publication",
		"read.full.native.fragment",
		"read.full.native.entity.head.raw",
		"read.full.native.person.head.raw",
		"read.full.native.fts.search_own_prefix.first",
		"read.full.native.fts.search_fragment_prefix.first",
		"read.full.native.fts.search_fragment_lexical.first",
		"read.full.native.fts.search_fragment_grams.first",
		"read.full.native.fts.catalog_people_names.first",
		"read.browse.rs.reading.source",
		"read.browse.rs.reading.facts",
		"read.browse.rs.reading.octets",
		"read.browse.rs.people.source",
		"read.browse.rs.people.page.credit",
		"read.browse.rs.people.page.work_source",
		"read.browse.rs.people.page.portrait",
	} {
		if !inventory[id] {
			t.Fatalf("required actual statement %s is absent", id)
		}
	}
	if len(NativeFullSearchDefinitions()) <= rsSearchTargetRouteDebt {
		t.Fatalf("native inventory no longer exceeds the B-R08 route budget: %d", len(NativeFullSearchDefinitions()))
	}
	// Reading is the only RS domain that opts into the native bank; the image
	// converter and the complete collection domain stay closed.
	for _, definition := range RSNativeSearchDefinitions() {
		if !strings.HasPrefix(string(definition.ID), "read.browse.rs.reading.") {
			t.Fatalf("RS native contribution opened another domain: %s", definition.ID)
		}
	}
	if facettext.SearchFieldBytes != 64<<10 {
		t.Fatalf("accepted per-field searchable cap changed: %d", facettext.SearchFieldBytes)
	}
	if NativePreviewCandidates != 2000 {
		t.Fatalf("preview candidate budget changed: %d", NativePreviewCandidates)
	}
	if MaxNativeRetained != 128 || MaxNativeStampBytes != 640<<10 {
		t.Fatalf("native retained proof caps changed: %d/%d", MaxNativeRetained, MaxNativeStampBytes)
	}
	if len(rsSearchGroups) != rsSearchTargetGroupCount {
		t.Fatalf("closed group set changed: %d", len(rsSearchGroups))
	}
	if RSSearchGroupBudget != 600*time.Millisecond {
		t.Fatalf("per-group search budget changed: %s", RSSearchGroupBudget)
	}
}

func TestRSSearchPhase2EntropyInputsUseMergedNormalizers(t *testing.T) {
	latin := strings.Repeat("q0Z9x", 200)
	if search.PrepareTitle(latin).Fold() == "" {
		t.Fatal("high-entropy Latin title folded to nothing")
	}
	cjk := strings.Repeat("辛哫", 200)
	if !strings.ContainsFunc(cjk, facettext.CJK) || search.PreparePersonName(cjk) == "" {
		t.Fatal("high-entropy CJK name folded to nothing")
	}
	name := "Ægir " + strings.Repeat("Ω", 200)
	if search.PreparePersonName(name) == "" {
		t.Fatal("high-entropy person name folded to nothing")
	}
}

// rsSearchRowLocalChunkMatch is the deliberately row-local oracle: it verifies
// one physical normalized chunk in isolation. The negative control below shows
// that this verifier drops a match the accepted logical subject retains.
func rsSearchRowLocalChunkMatch(term, normalized string) bool {
	return strings.Contains(normalized, term)
}

func TestRSSearchPhase2PackedFragmentLogicalSubjectSiblingNegative(t *testing.T) {
	s := fullScope(t, "辛哫")
	k := search.FieldKey{Subject: search.ManifestEntity, SubjectID: 1, PublicationID: 1, BankID: 1, InputGeneration: 1, Field: 4, Revision: "09"}
	candidateChunk := strings.Repeat("甲", 200)
	w, err := newLogicalFieldWalk(s, k, "")
	if err != nil {
		t.Fatal(err)
	}
	// The prefix candidate's own physical chunk carries no normalized hit.
	if err := w.observe(t.Context(), k, "", candidateChunk); err != nil {
		t.Fatal(err)
	}
	// The accepted normalized SIBLING row carries the match. Row-local physical
	// chunk verification over the candidate alone would drop it.
	if err := w.observe(t.Context(), k, "", "辛哫"); err != nil {
		t.Fatal(err)
	}
	proof, err := w.finish(t.Context(), true)
	if err != nil || !proof.matched[0] {
		t.Fatalf("packed-fragment sibling match dropped: %v %v", proof, err)
	}
	if counts := populations(t, s, proof, proof); counts[0].count != 1 {
		t.Fatalf("physical siblings inflated the logical population: %d", counts[0].count)
	}
	if rsSearchRowLocalChunkMatch("辛哫", candidateChunk) {
		t.Fatal("row-local oracle matched a chunk without the term: negative control is not negative")
	}
	if !rsSearchRowLocalChunkMatch("辛哫", "辛哫") {
		t.Fatal("row-local oracle cannot match its own term")
	}
	// The sibling rule is not a licence to accept an unrelated logical field: a
	// foreign publication still poisons the walk.
	w, err = newLogicalFieldWalk(s, k, "")
	if err != nil {
		t.Fatal(err)
	}
	foreign := k
	foreign.PublicationID++
	if err := w.observe(t.Context(), foreign, "", "辛哫"); !errors.Is(err, search.ErrFieldContinuation) {
		t.Fatalf("foreign sibling accepted: %v", err)
	}
	if _, err := w.finish(t.Context(), true); err == nil {
		t.Fatal("a poisoned logical walk finished with a proof")
	}
}
