package writer

import (
	"crypto/sha256"
	"math"
	"testing"
)

func mgGuideProtocolFixture() (mgGuideProgress, []mgGuideStageDelta) {
	stages := []mgGuideStageDelta{
		{mgGuideChannels, "source", "new", 2, 0, 2, 100, sha256.Sum256([]byte("two channels"))},
		{mgGuideProgrammes, "source", "new", 3, 0, 8, 800, sha256.Sum256([]byte("first programmes"))},
		{mgGuideProgrammes, "source", "new", 4, 8, 1, 100, sha256.Sum256([]byte("last programme"))},
		{mgGuideChoices, "source", "new", 5, 0, 2, 50, sha256.Sum256([]byte("choices"))},
	}
	m := mgGuideManifest{source: "source", generation: "new", previousGeneration: "old", expectedRevision: 7, expected: [3]int64{2, 9, 2}}
	m.expectedChain = mgGuideInitialChain(m)
	for _, d := range stages {
		m.expectedChain = mgGuideNextChain(m.expectedChain, d)
	}
	return mgGuideProgress{manifest: m, chain: mgGuideInitialChain(m), revision: 1, sequence: 1}, stages
}

func TestMGGuideProtocolStagesSealAndPublish(t *testing.T) {
	p, stages := mgGuideProtocolFixture()
	for _, d := range stages {
		before := p
		var err error
		p, err = planMGGuideStage(p, d)
		if err != nil {
			t.Fatal(err)
		}
		if p.revision != before.revision+1 || p.sequence != before.sequence+1 || p.bytes != before.bytes+d.bytes || p.sealed {
			t.Fatal("incomplete stage delta", p)
		}
	}
	if p.staged != ([3]int64{2, 9, 2}) || p.bytes != 1050 {
		t.Fatal("staging totals", p)
	}
	p, err := planMGGuideSeal(p, 6)
	if err != nil || !p.sealed {
		t.Fatal("seal", p, err)
	}
	for _, state := range []string{"active", "disabled"} {
		head := mgGuideHead{"source", "old", state, 7, false}
		got, err := planMGGuidePublication(p, head, true)
		if err != nil || got != (mgGuideHead{"source", "new", state, 7, false}) {
			t.Fatal("refresh must retain state/config revision", got, err)
		}
		got, err = planMGGuidePublication(p, head, false)
		if err != nil || got.revision != 8 {
			t.Fatal("config change revision", got, err)
		}
	}
}

func TestMGGuideProtocolRejectsPartialOrChangedStages(t *testing.T) {
	p, stages := mgGuideProtocolFixture()
	for _, tc := range []struct {
		name   string
		mutate func(*mgGuideProgress, *mgGuideStageDelta)
	}{
		{"hole", func(_ *mgGuideProgress, d *mgGuideStageDelta) { d.ordinal = 1 }},
		{"sequence", func(_ *mgGuideProgress, d *mgGuideStageDelta) { d.sequence++ }},
		{"other-generation", func(_ *mgGuideProgress, d *mgGuideStageDelta) { d.generation = "other" }},
		{"other-source", func(_ *mgGuideProgress, d *mgGuideStageDelta) { d.source = "other" }},
		{"too-many-rows", func(_ *mgGuideProgress, d *mgGuideStageDelta) { d.rows = 9 }},
		{"byte-budget", func(_ *mgGuideProgress, d *mgGuideStageDelta) { d.bytes = mgGuideStageBytes + 1 }},
		{"byte-overflow", func(p *mgGuideProgress, _ *mgGuideStageDelta) { p.bytes = math.MaxInt64 }},
		{"revision-overflow", func(p *mgGuideProgress, _ *mgGuideStageDelta) { p.revision = math.MaxInt64 }},
		{"sealed", func(p *mgGuideProgress, _ *mgGuideStageDelta) { p.sealed = true }},
		{"over-count", func(_ *mgGuideProgress, d *mgGuideStageDelta) { d.rows = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, delta := p, stages[0]
			tc.mutate(&before, &delta)
			got, err := planMGGuideStage(before, delta)
			if err == nil || got != (mgGuideProgress{}) {
				t.Fatal("bad stage accepted/nonzero failure", got, err)
			}
		})
	}
	if got, err := planMGGuideSeal(p, 2); err == nil || got != (mgGuideProgress{}) {
		t.Fatal("partial generation sealed", got, err)
	}
	first, err := planMGGuideStage(p, stages[0])
	if err != nil {
		t.Fatal(err)
	}
	if got, err := planMGGuideStage(first, stages[0]); err == nil || got != (mgGuideProgress{}) {
		t.Fatal("duplicate stage must resolve original receipt", got, err)
	}
	// Changed body with exactly the same counts cannot satisfy the old seal.
	stages[1].payload = sha256.Sum256([]byte("different programmes"))
	for _, d := range stages {
		p, err = planMGGuideStage(p, d)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, err := planMGGuideSeal(p, 6); err == nil || got != (mgGuideProgress{}) {
		t.Fatal("changed content accepted by counts alone", got, err)
	}
}

func TestMGGuidePublicationFencesSameRevisionRefresh(t *testing.T) {
	p, stages := mgGuideProtocolFixture()
	var err error
	for _, d := range stages {
		p, err = planMGGuideStage(p, d)
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err = planMGGuideSeal(p, 6)
	if err != nil {
		t.Fatal(err)
	}
	for _, head := range []mgGuideHead{
		{"source", "concurrent-head", "active", 7, false}, // refresh can preserve config revision
		{"source", "old", "active", 8, false},
		{"source", "old", "active", 7, true},
		{"other", "old", "active", 7, false},
	} {
		if got, err := planMGGuidePublication(p, head, true); err == nil || got != (mgGuideHead{}) {
			t.Fatal("stale head published", got, err)
		}
	}
	if got, err := planMGGuideStage(p, stages[0]); err == nil || got != (mgGuideProgress{}) {
		t.Fatal("sealed generation mutated", got, err)
	}
}

func TestMGGuideManifestHeaderAndNoProgrammeGuide(t *testing.T) {
	p, _ := mgGuideProtocolFixture()
	changed := p
	changed.manifest.previousGeneration = "another"
	if validMGGuideProgress(changed) {
		t.Fatal("initial manifest header changed without a new commitment")
	}
	// A real channel with no listings is valid; zero channels is not a guide.
	m := mgGuideManifest{source: "source", generation: "first", expected: [3]int64{1, 0, 1}}
	stages := []mgGuideStageDelta{
		{mgGuideChannels, "source", "first", 2, 0, 1, 64, sha256.Sum256([]byte("channel"))},
		{mgGuideChoices, "source", "first", 3, 0, 1, 16, sha256.Sum256([]byte("choice"))},
	}
	m.expectedChain = mgGuideInitialChain(m)
	for _, d := range stages {
		m.expectedChain = mgGuideNextChain(m.expectedChain, d)
	}
	p = mgGuideProgress{manifest: m, chain: mgGuideInitialChain(m), revision: 1, sequence: 1}
	var err error
	for _, d := range stages {
		p, err = planMGGuideStage(p, d)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, err := planMGGuidePublication(p, mgGuideHead{source: "source"}, false); err == nil || got != (mgGuideHead{}) {
		t.Fatal("unsealed complete generation published", got, err)
	}
	sealed, err := planMGGuideSeal(p, 4)
	if err != nil {
		t.Fatal("no-programme immutable generation", err)
	}
	got, err := planMGGuidePublication(sealed, mgGuideHead{source: "source"}, false)
	if err != nil || got != (mgGuideHead{source: "source", generation: "first", state: "active", revision: 1}) {
		t.Fatal("first publication", got, err)
	}
	if got, err := planMGGuidePublication(sealed, mgGuideHead{source: "source"}, true); err == nil || got != (mgGuideHead{}) {
		t.Fatal("refresh invented a source", got, err)
	}
}

func TestMGGuideZeroChannelsNeverPublishes(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		m := mgGuideManifest{source: "source", generation: "empty"}
		head := mgGuideHead{source: "source"}
		if refresh {
			m.previousGeneration = "old"
			m.expectedRevision = 7
			head = mgGuideHead{"source", "old", "active", 7, false}
		}
		m.expectedChain = mgGuideInitialChain(m)
		p := mgGuideProgress{manifest: m, chain: m.expectedChain, revision: 1, sequence: 1}
		if validMGGuideProgress(p) {
			t.Fatal("zero-channel source accepted", refresh)
		}
		if got, err := planMGGuideSeal(p, 2); err == nil || got != (mgGuideProgress{}) {
			t.Fatal("zero-channel guide sealed", refresh, got, err)
		}
		p.sealed = true
		if got, err := planMGGuidePublication(p, head, refresh); err == nil || got != (mgGuideHead{}) {
			t.Fatal("zero-channel guide published", refresh, got, err)
		}
	}
}
