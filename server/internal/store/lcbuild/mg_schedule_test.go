package lcbuild

import (
	"errors"
	"fmt"
	"testing"
	"time"

	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
)

func scheduleConfig(id, timezone string, rules ...librarychannels.Rule) librarychannels.Config {
	if timezone == "" {
		timezone = "UTC"
	}
	return librarychannels.Config{
		Version: librarychannels.ProtocolVersion, ID: id, Name: "Channel", Enabled: true,
		Timezone: timezone, Seed: "seed", DefaultRuleID: rules[0].ID, ViewerAccess: "server-members",
		Quality: librarychannels.Quality{Mode: "automatic"}, Rules: rules,
	}
}

func scheduleRule(id, mode, episodeMode, exhaustion string, libraries []string) librarychannels.Rule {
	return librarychannels.Rule{ID: id, Name: "Rule " + id, Mode: mode, EpisodeMode: episodeMode,
		Exhaustion: exhaustion, MaxConsecutive: 1000,
		Query: librarychannels.Query{LibraryIDs: libraries, Kinds: []string{"movie"}, Order: "title"}}
}

func scheduleBase(t *testing.T, rules ...librarychannels.Rule) (int64, int64) {
	t.Helper()
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC).UnixMilli()
	return start, start + 3*60*1000
}

func scheduleFixture(t *testing.T, rules ...librarychannels.Rule) (int64, int64, librarychannels.Config) {
	t.Helper()
	start, end := scheduleBase(t)
	return start, end, scheduleConfig("channel", "UTC", rules...)
}

// A cut-offset candidate continues from its copied source position, and every
// emitted interval joins the previous edge exactly across separate Plan calls.
func TestMGScheduleSequentialOffsetsAndContinuation(t *testing.T) {
	rule := scheduleRule("rule", "sequential", "none", "loop", []string{"lib"})
	start, end, cfg := scheduleFixture(t, rule)
	producer, err := NewProducer(ScheduleInput{Channel: "channel", Seed: "seed", Start: start, End: end, Config: cfg, RawEOF: true,
		Candidates: []Candidate{
			{Rule: "rule", Library: "lib", Asset: "a1", Source: "s1", Title: "One", SortKey: "a", Item: 101, Ordinal: 0, Duration: 60000, Weight: 1},
			{Rule: "rule", Library: "lib", Asset: "a2", Source: "s2", Title: "Two", SortKey: "b", Item: 102, Ordinal: 1, Duration: 60000, Weight: 1, SourceOffset: 5000},
			{Rule: "rule", Library: "lib", Asset: "a3", Source: "s3", Title: "Three", SortKey: "c", Item: 103, Ordinal: 2, Duration: 60000, Weight: 1},
		},
		State: ScheduleState{Cursor: start}})
	if err != nil {
		t.Fatal(err)
	}
	var all []Slot
	for step := 0; step < 8; step++ {
		batch, err := producer.Plan(1)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, batch.Slots...)
		if batch.Complete {
			break
		}
	}
	if len(all) != 4 {
		t.Fatal("slot inventory", len(all))
	}
	if all[0].Item != 101 || all[0].Offset != 0 || all[1].Item != 102 || all[1].Offset != 5000 ||
		all[3].End != end || all[3].Offset != 0 {
		t.Fatal("continuing offset or generation edge", all)
	}
	if !Contiguous(all, start, end) {
		t.Fatal("non-contiguous schedule", all)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Start != all[i-1].End {
			t.Fatal("continuation gap", i, all)
		}
	}
}

// A stream that has not proven raw EOF can never be a complete schedule.
func TestMGScheduleRequiresRawEOF(t *testing.T) {
	rule := scheduleRule("rule", "sequential", "none", "loop", []string{"lib"})
	start, end, cfg := scheduleFixture(t, rule)
	_, err := NewProducer(ScheduleInput{Channel: "channel", Seed: "seed", Start: start, End: end, Config: cfg,
		State:      ScheduleState{Cursor: start},
		Candidates: []Candidate{{Rule: "rule", Library: "lib", Asset: "a1", Source: "s1", Title: "One", SortKey: "a", Item: 101, Duration: 60000, Weight: 1}}})
	if !errors.Is(err, ErrScheduleIncompleteEOF) {
		t.Fatal("incomplete EOF accepted", err)
	}
}

// Every library a rule names is real input, including the 64th. No component
// window or eight-library product cap truncates the pool.
func TestMGScheduleAllNamedLibraries(t *testing.T) {
	libraries := make([]string, 64)
	for i := range libraries {
		libraries[i] = "lib-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
	}
	rule := scheduleRule("rule", "sequential", "none", "loop", libraries)
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC).UnixMilli()
	end := start + int64(len(libraries))*60*1000
	cfg := scheduleConfig("channel", "UTC", rule)
	var candidates []Candidate
	for i, library := range libraries {
		candidates = append(candidates, Candidate{Rule: "rule", Library: library, Asset: "a", Source: "s", Title: "T", SortKey: fmt.Sprintf("%02d", i), Item: int64(101 + i), Ordinal: int64(i), Duration: 60000, Weight: 1})
	}
	producer, err := NewProducer(ScheduleInput{Channel: "channel", Seed: "seed", Start: start, End: end, Config: cfg, RawEOF: true, Candidates: candidates, State: ScheduleState{Cursor: start}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var all []Slot
	for step := 0; step < 16; step++ {
		batch, err := producer.Plan(ScheduleRows)
		if err != nil {
			t.Fatal(err)
		}
		for _, slot := range batch.Slots {
			all = append(all, slot)
			for _, c := range candidates {
				if c.Item == slot.Item {
					seen[c.Library] = true
				}
			}
		}
		if batch.Complete {
			break
		}
	}
	if len(seen) != len(libraries) || !Contiguous(all, start, end) {
		t.Fatal("named libraries truncated", len(seen), len(all))
	}
}

// A seven-civil-day window crosses the DST transition without a hole, and the
// stored instants (not a 168-hour guess) are the schedule edges.
func TestMGScheduleDSTWindowIsContiguous(t *testing.T) {
	window, err := librarychannels.SevenDayWindow("2026-03-08", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start, end := window.Start.UTC.UnixMilli(), window.End.UTC.UnixMilli()
	if start < 0 || end-start != 167*60*60*1000 {
		t.Fatal("fixture window", start, end)
	}
	rule := scheduleRule("rule", "sequential", "none", "loop", []string{"lib"})
	cfg := scheduleConfig("channel", "America/New_York", rule)
	candidate := Candidate{Rule: "rule", Library: "lib", Asset: "a", Source: "s", Title: "T", SortKey: "a", Item: 101, Duration: 30 * 60 * 1000, Weight: 1}
	producer, err := NewProducer(ScheduleInput{Channel: "channel", Seed: "seed", Start: start, End: end, Config: cfg, RawEOF: true, Candidates: []Candidate{candidate}, State: ScheduleState{Cursor: start}})
	if err != nil {
		t.Fatal(err)
	}
	var all []Slot
	for step := 0; step < 64; step++ {
		batch, err := producer.Plan(ScheduleRows)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, batch.Slots...)
		if batch.Complete {
			break
		}
	}
	if len(all) != 334 || !Contiguous(all, start, end) {
		t.Fatal("DST schedule", len(all))
	}
}

// No eligible media is a truthful slate covering the window, never a truncated
// "complete" or a fabricated item.
func TestMGScheduleEmptySlate(t *testing.T) {
	rule := scheduleRule("rule", "sequential", "none", "loop", []string{"lib"})
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC).UnixMilli()
	end := start + 90*60*1000
	cfg := scheduleConfig("channel", "UTC", rule)
	producer, err := NewProducer(ScheduleInput{Channel: "channel", Seed: "seed", Start: start, End: end, Config: cfg, RawEOF: true, State: ScheduleState{Cursor: start}})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := producer.Plan(ScheduleRows)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Slots) != 3 || !batch.Complete || !Contiguous(batch.Slots, start, end) {
		t.Fatal("empty slate", batch)
	}
	for _, slot := range batch.Slots {
		if slot.Item != 0 || slot.Offset != 0 || slot.Slate != "no-eligible-media" {
			t.Fatal("fabricated empty-slate media", slot)
		}
	}
}

// A block selects its rule for the whole covered window; a shuffle bag is
// exhausted once, then the explicit slate applies. The same frozen input
// produces the same slots (no wall clock or process-random draw).
func TestMGScheduleBlockBagAndDeterminism(t *testing.T) {
	bag := scheduleRule("bag", "shuffle-bag", "none", "slate", []string{"lib"})
	base := scheduleRule("base", "sequential", "none", "loop", []string{"lib"})
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC).UnixMilli()
	end := start + 3*60*60*1000
	cfg := scheduleConfig("channel", "UTC", bag, base)
	cfg.Blocks = []librarychannels.Block{{ID: "night", Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, StartMinute: 0, EndMinute: 0, Priority: 5, RuleID: "bag", Anchor: "channel-cursor", Overrun: "finish"}}
	candidates := []Candidate{
		{Rule: "bag", Library: "lib", Asset: "a", Source: "s", Title: "A", SortKey: "a", Item: 101, Ordinal: 0, Duration: 40 * 60 * 1000, Weight: 1},
		{Rule: "bag", Library: "lib", Asset: "b", Source: "s", Title: "B", SortKey: "b", Item: 102, Ordinal: 1, Duration: 40 * 60 * 1000, Weight: 1},
		{Rule: "bag", Library: "lib", Asset: "c", Source: "s", Title: "C", SortKey: "c", Item: 103, Ordinal: 2, Duration: 40 * 60 * 1000, Weight: 1},
	}
	plan := func() (ScheduleBatch, []Slot) {
		producer, err := NewProducer(ScheduleInput{Channel: "channel", Seed: "seed", Start: start, End: end, Config: cfg, RawEOF: true, Candidates: candidates, State: ScheduleState{Cursor: start}})
		if err != nil {
			t.Fatal(err)
		}
		var all []Slot
		for step := 0; step < 8; step++ {
			batch, err := producer.Plan(ScheduleRows)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, batch.Slots...)
			if batch.Complete {
				return batch, all
			}
		}
		t.Fatal("block bag did not complete")
		return ScheduleBatch{}, nil
	}
	first, slots := plan()
	_, second := plan()
	if !first.Complete || len(slots) != 5 || !Contiguous(slots, start, end) {
		t.Fatal("block-covered bag", len(slots), slots)
	}
	seen := map[int64]bool{}
	for i, slot := range slots {
		if i < 3 {
			if slot.Rule != "bag" || slot.Slate != "" || seen[slot.Item] {
				t.Fatal("block/bag slot", slot)
			}
			seen[slot.Item] = true
			continue
		}
		if slot.Item != 0 || slot.Slate != "no-eligible-media" || slot.Rule != "bag" {
			t.Fatal("exhausted bag slate", slot)
		}
	}
	if len(seen) != 3 || len(second) != len(slots) {
		t.Fatal("bag exhausted or nondeterministic", seen, len(second))
	}
	for i := range slots {
		if slots[i] != second[i] {
			t.Fatal("frozen input produced different slots", i)
		}
	}
}

// Gaps, unnamed libraries and contradictory continuation facts are refused
// before any slot is accepted.
func TestMGScheduleRefusesMalformedFacts(t *testing.T) {
	rule := scheduleRule("rule", "sequential", "none", "loop", []string{"lib"})
	start, end, cfg := scheduleFixture(t, rule)
	base := ScheduleInput{Channel: "channel", Seed: "seed", Start: start, End: end, Config: cfg, RawEOF: true,
		Candidates: []Candidate{{Rule: "rule", Library: "lib", Asset: "a", Source: "s", Title: "T", SortKey: "a", Item: 101, Ordinal: 0, Duration: 60000, Weight: 1}},
		State:      ScheduleState{Cursor: start}}
	for name, mutate := range map[string]func(*ScheduleInput){
		"ordinal-gap":     func(in *ScheduleInput) { in.Candidates[0].Ordinal = 1 },
		"unnamed-library": func(in *ScheduleInput) { in.Candidates[0].Library = "other" },
		"offset-beyond":   func(in *ScheduleInput) { in.Candidates[0].SourceOffset = in.Candidates[0].Duration },
		"cursor-before":   func(in *ScheduleInput) { in.State.Cursor = start - 1 },
		"non-dense-rule":  func(in *ScheduleInput) { in.Candidates[0].Rule = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			candidates := append([]Candidate(nil), base.Candidates...)
			in.Candidates = candidates
			mutate(&in)
			if _, err := NewProducer(in); !errors.Is(err, ErrScheduleInput) {
				t.Fatal("malformed producer fact accepted", err)
			}
		})
	}
}
