package lcbuild

// This file is the pure schedule producer that sits above RB's retained
// candidate stream. It copies the merged librarychannels selection semantics
// (ordered walk, bag, weighted draw, blocks, overrun, fallback, dedupe) and the
// MG immutable stage/seal contract: every fact is values-only, every emitted
// interval is contiguous, and publication is never claimed before raw EOF. It
// opens no SQL, keeps no catalogue callback and supplies no retirement,
// generation or playback authority.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
)

var (
	// ErrScheduleInput is a malformed or unbounded producer fact.
	ErrScheduleInput = errors.New("lcbuild: invalid schedule input")
	// ErrScheduleIncompleteEOF refuses a pool that has not proven raw EOF.
	ErrScheduleIncompleteEOF = errors.New("lcbuild: candidate stream has no raw EOF")
	// ErrScheduleNoProgress refuses a plan that cannot advance the cursor.
	ErrScheduleNoProgress = errors.New("lcbuild: schedule made no progress")
)

const (
	ScheduleRows   = 8
	scheduleBytes  = 256 << 10
	scheduleMaxDur = int64(7 * 24 * 60 * 60 * 1000)
	scheduleWindow = int64(8 * 24 * 60 * 60 * 1000)
	scheduleSlate  = int64(30 * 60 * 1000)
	// History is retained fixed-width facts, one per prior occurrence. The
	// bounded input is at least the largest legal dedupe/consecutive window.
	ScheduleHistory = 1024
	drawProposals   = 64
	drawRounds      = 8
)

// Candidate is one immutable RB/RS-produced candidate at the retained history
// cutoff. Ordinals are dense from zero per rule; SourceOffset is the copied
// continuation start within the whole playable asset, and is zero otherwise.
type Candidate struct {
	Rule, Library, Asset, Source, Title, SortKey string
	Item, Ordinal                                int64
	Duration, Show, Season, Episode              int64
	Weight                                       float64
	SourceOffset                                 int64
}

// HistoryEntry is the minimal prior-occurrence fact used by dedupe and the
// consecutive-show limit. Callers pass newest first, bounded by the configured
// window; entries never carry a viewer or current authority.
type HistoryEntry struct {
	Item, Show int64
}

// RuleCursor is the native continuation of one rule.
type RuleCursor struct {
	Draw, Cycle                   int64
	LastSort, LastShow, LastBlock string
	Done                          bool
}

// UsedMark records the cycle in which one item was used by a rule.
type UsedMark struct {
	Rule        string
	Item, Cycle int64
}

// ScheduleState is the complete continuation. It is copied in and out; the
// producer never aliases caller slices or strings into a receipt.
type ScheduleState struct {
	Cursor int64
	Rules  map[string]RuleCursor
	Used   []UsedMark
}

// ScheduleInput freezes one bounded producer turn.
type ScheduleInput struct {
	Channel, Seed string
	Start, End    int64
	Config        librarychannels.Config
	RawEOF        bool
	Candidates    []Candidate
	History       []HistoryEntry
	State         ScheduleState
}

// Slot is one contiguous occurrence. Item zero is the explicit slate; Offset is
// a native continuation start and is zero for a slate or a first play.
type Slot struct {
	Occurrence, Rule, Block, Title string
	Item, Start, End, Offset       int64
	Slate                          string
}

// ScheduleBatch is a bounded, contiguous prefix and its exact continuation.
// Complete is true only when raw EOF was proven and the final edge is reached.
type ScheduleBatch struct {
	Slots    []Slot
	State    ScheduleState
	Complete bool
}

// Producer owns frozen input and the current continuation only. It is not a
// read port, scheduler registration or authority to publish a generation.
type Producer struct {
	channel, seed string
	start, end    int64
	config        librarychannels.Config
	rules         map[string]librarychannels.Rule
	blocks        []librarychannels.ResolvedBlock
	pool          map[string][]Candidate
	history       []HistoryEntry
	state         ScheduleState
	used          map[usedKey]int64
}

type usedKey struct {
	rule string
	item int64
}

func scheduleDigest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func scheduleText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, '\x00')
}

func scheduleIn(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// NewProducer validates and freezes one complete candidate stream. Incomplete
// EOF, a catalogue/config/library mismatch, a gap or an out-of-range offset is
// refused before any slot exists. Libraries are not truncated to a component
// window: every library named by a rule is accepted.
func NewProducer(in ScheduleInput) (*Producer, error) {
	if in.Channel == "" || len(in.Channel) > 128 || !scheduleText(in.Channel) ||
		in.Seed == "" || len(in.Seed) > 128 || !scheduleText(in.Seed) ||
		in.Start < 0 || in.End <= in.Start || in.End-in.Start > scheduleWindow ||
		len(in.Candidates) > 1_000_000 || len(in.History) > ScheduleHistory {
		return nil, ErrScheduleInput
	}
	if !in.RawEOF {
		return nil, ErrScheduleIncompleteEOF
	}
	if in.Config.ID != in.Channel {
		return nil, ErrScheduleInput
	}
	cfg, err := librarychannels.Normalize(in.Config)
	if err != nil || librarychannels.Validate(cfg) != nil {
		return nil, ErrScheduleInput
	}
	if in.State.Cursor < in.Start || in.State.Cursor > in.End {
		return nil, ErrScheduleInput
	}
	rules := make(map[string]librarychannels.Rule, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rules[r.ID] = r
	}
	if _, ok := rules[cfg.DefaultRuleID]; !ok {
		return nil, ErrScheduleInput
	}
	p := &Producer{
		channel: in.Channel, seed: in.Seed, start: in.Start, end: in.End,
		config: cfg, rules: rules, pool: make(map[string][]Candidate),
		history: append([]HistoryEntry(nil), in.History...),
		state:   ScheduleState{Cursor: in.State.Cursor, Rules: map[string]RuleCursor{}, Used: nil},
		used:    map[usedKey]int64{},
	}
	for id, c := range in.State.Rules {
		if _, ok := rules[id]; !ok || c.Draw < 0 || c.Cycle < 0 || len(c.LastSort) > scheduleBytes ||
			len(c.LastShow) > 256 || len(c.LastBlock) > 256 || !scheduleText(c.LastSort) ||
			!scheduleText(c.LastShow) || !scheduleText(c.LastBlock) {
			return nil, ErrScheduleInput
		}
		p.state.Rules[id] = c
	}
	for _, u := range in.State.Used {
		if _, ok := rules[u.Rule]; !ok || u.Item <= 0 || u.Cycle < 0 {
			return nil, ErrScheduleInput
		}
		p.used[usedKey{u.Rule, u.Item}] = u.Cycle
	}
	total := 0
	for _, c := range in.Candidates {
		if _, ok := rules[c.Rule]; !ok || c.Item <= 0 || c.Ordinal < 0 || !scheduleText(c.Rule) ||
			!scheduleText(c.Library) || !scheduleText(c.Asset) || !scheduleText(c.Source) ||
			!scheduleText(c.Title) || !scheduleText(c.SortKey) || c.Duration <= 0 ||
			c.Duration > scheduleMaxDur || c.Show < 0 || c.Season < 0 || c.Episode < 0 ||
			c.Weight <= 0 || math.IsNaN(c.Weight) || math.IsInf(c.Weight, 0) ||
			c.SourceOffset < 0 || c.SourceOffset >= c.Duration ||
			!scheduleIn(rules[c.Rule].Query.LibraryIDs, c.Library) {
			return nil, ErrScheduleInput
		}
		for _, s := range []string{c.Rule, c.Library, c.Asset, c.Source, c.Title, c.SortKey} {
			if len(s) > scheduleBytes-total {
				return nil, ErrScheduleInput
			}
			total += len(s)
		}
		p.pool[c.Rule] = append(p.pool[c.Rule], c)
	}
	for id, list := range p.pool {
		sort.Slice(list, func(i, j int) bool { return list[i].Ordinal < list[j].Ordinal })
		seen := map[int64]bool{}
		for index, c := range list {
			if c.Ordinal != int64(index) || seen[c.Item] {
				return nil, ErrScheduleInput
			}
			seen[c.Item] = true
		}
		p.pool[id] = list
	}
	blocks, err := scheduleBlocks(cfg, in.Start, in.End)
	if err != nil {
		return nil, err
	}
	p.blocks = blocks
	return p, nil
}

// Plan returns at most limit contiguous slots. State advances only when the
// whole batch is produced; a caller retry can never duplicate a slot. A pool
// that cannot be advanced is an explicit refusal, not a partial "complete".
func (p *Producer) Plan(limit int) (ScheduleBatch, error) {
	if p == nil || limit < 1 || limit > ScheduleRows {
		return ScheduleBatch{}, ErrScheduleInput
	}
	state := ScheduleState{Cursor: p.state.Cursor, Rules: map[string]RuleCursor{}, Used: nil}
	for id, c := range p.state.Rules {
		state.Rules[id] = c
	}
	used := make(map[usedKey]int64, len(p.used))
	for k, v := range p.used {
		used[k] = v
	}
	history := append([]HistoryEntry(nil), p.history...)
	out := ScheduleBatch{State: state, Slots: []Slot{}}
	if state.Cursor >= p.end {
		out.Complete = true
		out.State = copyState(state, used)
		return out, nil
	}
	for len(out.Slots) < limit {
		if state.Cursor >= p.end {
			break
		}
		block := scheduleSlotAt(p.config, p.blocks, state.Cursor, p.end)
		if block.Interval.End.UTC.UnixMilli() <= state.Cursor {
			return ScheduleBatch{}, ErrScheduleInput
		}
		rule, ok := p.rules[block.RuleID]
		if !ok {
			return ScheduleBatch{}, ErrScheduleInput
		}
		remaining := block.Interval.End.UTC.UnixMilli() - state.Cursor
		candidate, found := p.choose(&state, used, rule, block, remaining)
		if !found && block.Fallback != "" {
			fallback, ok := p.rules[block.Fallback]
			if !ok {
				return ScheduleBatch{}, ErrScheduleInput
			}
			candidate, found = p.choose(&state, used, fallback, block, remaining)
			if found {
				rule = fallback
			}
		}
		slot := Slot{Rule: rule.ID, Block: block.ID, Start: state.Cursor}
		if found {
			slot.Item = candidate.Item
			slot.Title = candidate.Title
			slot.Offset = candidate.SourceOffset
			playable := candidate.Duration - candidate.SourceOffset
			slot.End = state.Cursor + playable
			if block.Overrun == "cut" && slot.End > block.Interval.End.UTC.UnixMilli() {
				slot.End = block.Interval.End.UTC.UnixMilli()
			}
			// The generation window is the stored edge: a final full play is
			// clipped there rather than stored beyond the immutable header.
			if slot.End > p.end {
				slot.End = p.end
			}
		} else {
			slot.Title = "Unavailable program"
			slot.Slate = "no-eligible-media"
			slot.End = state.Cursor + scheduleSlate
			if slot.End > block.Interval.End.UTC.UnixMilli() {
				slot.End = block.Interval.End.UTC.UnixMilli()
			}
		}
		if slot.End <= slot.Start || slot.End > p.end {
			return ScheduleBatch{}, ErrScheduleInput
		}
		slot.Occurrence = scheduleDigest(p.channel, slot.Rule, strconv.FormatInt(slot.Start, 10),
			strconv.FormatInt(slot.End, 10), strconv.FormatInt(slot.Item, 10),
			strconv.FormatInt(slot.Offset, 10))[:48]
		out.Slots = append(out.Slots, slot)
		history = append([]HistoryEntry{{slot.Item, showFor(p.pool[slot.Rule], slot.Item)}}, history...)
		if len(history) > ScheduleHistory {
			history = history[:ScheduleHistory]
		}
		state.Cursor = slot.End
	}
	if len(out.Slots) == 0 {
		return ScheduleBatch{}, ErrScheduleNoProgress
	}
	if state.Cursor < p.end {
		// Incomplete windows are pending work, never a complete schedule.
		out.Complete = false
	} else {
		out.Complete = true
	}
	out.State = copyState(state, used)
	p.state = ScheduleState{Cursor: state.Cursor, Rules: map[string]RuleCursor{}, Used: nil}
	for id, c := range state.Rules {
		p.state.Rules[id] = c
	}
	p.used = used
	p.history = history
	return out, nil
}

func copyState(state ScheduleState, used map[usedKey]int64) ScheduleState {
	out := ScheduleState{Cursor: state.Cursor, Rules: map[string]RuleCursor{}, Used: []UsedMark{}}
	for id, c := range state.Rules {
		out.Rules[id] = c
	}
	keys := make([]usedKey, 0, len(used))
	for k := range used {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].rule != keys[j].rule {
			return keys[i].rule < keys[j].rule
		}
		return keys[i].item < keys[j].item
	})
	for _, k := range keys {
		out.Used = append(out.Used, UsedMark{Rule: k.rule, Item: k.item, Cycle: used[k]})
	}
	return out
}

func showFor(pool []Candidate, item int64) int64 {
	for _, c := range pool {
		if c.Item == item {
			return c.Show
		}
	}
	return 0
}

func scheduleBlocks(c librarychannels.Config, start, end int64) ([]librarychannels.ResolvedBlock, error) {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return nil, ErrScheduleInput
	}
	from := time.UnixMilli(start).In(loc).AddDate(0, 0, -1)
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	last := time.UnixMilli(end).In(loc).AddDate(0, 0, 1)
	lastCivil := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, time.UTC)
	out := []librarychannels.ResolvedBlock{}
	for days := 0; !day.After(lastCivil); days++ {
		if days > 11 {
			return nil, ErrScheduleInput
		}
		for _, b := range c.Blocks {
			match := false
			for _, d := range b.Weekdays {
				if d == int(day.Weekday()) {
					match = true
				}
			}
			if !match {
				continue
			}
			v, err := librarychannels.ResolveBlock(day.Format("2006-01-02"), b.StartMinute, b.EndMinute, c.Timezone)
			if err != nil {
				return nil, ErrScheduleInput
			}
			if v.End.UTC.After(time.UnixMilli(start)) && v.Start.UTC.Before(time.UnixMilli(end)) && v.End.UTC.After(v.Start.UTC) {
				out = append(out, librarychannels.ResolvedBlock{ID: b.ID + ":" + day.Format("2006-01-02"), RuleID: b.RuleID, Interval: v, Priority: b.Priority, Overrun: b.Overrun, Anchor: b.Anchor, Fallback: b.FallbackRuleID})
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return out, nil
}

// scheduleSlotAt copies the merged librarychannels block winner: the default
// rule covers the gap at the lowest priority, and the next edge is the first
// instant where another block wins (including a DST-resolved boundary).
func scheduleSlotAt(c librarychannels.Config, blocks []librarychannels.ResolvedBlock, at, end int64) librarychannels.ResolvedBlock {
	defaultSlot := func(instant int64) librarychannels.ResolvedBlock {
		return librarychannels.ResolvedBlock{ID: "default", RuleID: c.DefaultRuleID, Priority: -100001, Overrun: "finish", Anchor: "channel-cursor", Interval: librarychannels.BlockInterval{Start: librarychannels.Boundary{UTC: time.UnixMilli(instant)}, End: librarychannels.Boundary{UTC: time.UnixMilli(end)}}}
	}
	winner := func(instant int64) librarychannels.ResolvedBlock {
		selected := defaultSlot(instant)
		for _, b := range blocks {
			if b.Interval.Start.UTC.UnixMilli() <= instant && b.Interval.End.UTC.UnixMilli() > instant && (b.Priority > selected.Priority || b.Priority == selected.Priority && b.ID < selected.ID) {
				selected = b
			}
		}
		return selected
	}
	selected := winner(at)
	edges := []int64{end}
	for _, b := range blocks {
		for _, edge := range []int64{b.Interval.Start.UTC.UnixMilli(), b.Interval.End.UTC.UnixMilli()} {
			if edge > at && edge < end {
				edges = append(edges, edge)
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })
	next := end
	for _, edge := range edges {
		if edge == end || winner(edge).ID != selected.ID {
			next = edge
			break
		}
	}
	selected.Interval.End.UTC = time.UnixMilli(next)
	return selected
}

type historyFacts struct {
	recent      map[int64]bool
	lastGroup   int64
	consecutive int
}

func scheduleHistoryFacts(history []HistoryEntry, dedupe, maxConsecutive int) historyFacts {
	out := historyFacts{recent: map[int64]bool{}}
	for index, h := range history {
		group := h.Show
		if group == 0 {
			group = h.Item
		}
		if index == 0 {
			out.lastGroup = group
		}
		if index == out.consecutive && group != 0 && group == out.lastGroup {
			out.consecutive++
		}
		if index < dedupe && h.Item != 0 {
			out.recent[h.Item] = true
		}
		if index+1 >= maxConsecutive && index+1 >= dedupe {
			break
		}
	}
	return out
}

func (p *Producer) unused(pool []Candidate, used map[usedKey]int64, rule string, cycle int64) bool {
	for _, c := range pool {
		if u, ok := used[usedKey{rule, c.Item}]; !ok || u != cycle {
			return true
		}
	}
	return false
}

func (p *Producer) earliest(pool []Candidate, used map[usedKey]int64, rule string, cycle int64, c Candidate) bool {
	if c.Show == 0 {
		return true
	}
	for _, earlier := range pool {
		if earlier.Show != c.Show {
			continue
		}
		if u, ok := used[usedKey{rule, earlier.Item}]; ok && u == cycle {
			continue
		}
		if earlier.Season < c.Season || earlier.Season == c.Season &&
			(earlier.Episode < c.Episode || earlier.Episode == c.Episode && earlier.Item < c.Item) {
			return false
		}
	}
	return true
}

func (p *Producer) eligible(c Candidate, facts historyFacts, cycle int64, used map[usedKey]int64, rule string,
	lastSort string, sequential, inOrder, bag bool, remaining int64, overrun string) bool {
	if facts.recent[c.Item] {
		return false
	}
	if u, ok := used[usedKey{rule, c.Item}]; bag && ok && u == cycle {
		return false
	}
	group := c.Show
	if group == 0 {
		group = c.Item
	}
	if facts.lastGroup != 0 && facts.consecutive >= p.rules[rule].MaxConsecutive && group == facts.lastGroup {
		return false
	}
	if overrun == "fit" && c.Duration-c.SourceOffset > remaining {
		return false
	}
	if sequential && c.SortKey <= lastSort {
		return false
	}
	if inOrder && !p.earliest(p.pool[rule], used, rule, cycle, c) {
		return false
	}
	return true
}

func (p *Producer) maxWeight(r librarychannels.Rule) float64 {
	top := 1.0
	for _, w := range r.Weights {
		top = math.Max(top, w.Weight)
	}
	return top
}

func unit(parts ...string) [16]byte {
	raw, _ := hex.DecodeString(scheduleDigest(parts...)[:32])
	var out [16]byte
	copy(out[:], raw)
	return out
}

func unitAt(b [16]byte, half int) float64 {
	v := binary.BigEndian.Uint64(b[half*8:]) >> 11
	return float64(v) / float64(uint64(1)<<53)
}

// choose is the pure form of librarychannels.selectCandidate. It mutates only
// the batch-local state and used map; history/lists stay frozen.
func (p *Producer) choose(state *ScheduleState, used map[usedKey]int64, r librarychannels.Rule,
	block librarychannels.ResolvedBlock, remaining int64) (Candidate, bool) {
	cursor := state.Rules[r.ID]
	if block.Anchor == "block-start" && cursor.LastBlock != block.ID {
		cursor = RuleCursor{Cycle: cursor.Cycle + 1, LastBlock: block.ID}
	}
	if cursor.Done {
		return Candidate{}, false
	}
	facts := scheduleHistoryFacts(p.history, r.DeduplicationWindow, r.MaxConsecutive)
	inOrder := r.EpisodeMode == "in-order" || r.EpisodeMode == "marathon" || r.EpisodeMode == "rotate"
	pool := p.pool[r.ID]
	for attempt := 0; attempt < 2; attempt++ {
		sequential := (r.Mode == "sequential" || r.Mode == "sequential-then-shuffle" && cursor.Cycle == 0 || r.EpisodeMode == "rotate") && r.EpisodeMode != "randomized"
		bag := r.Mode != "weighted-random" || inOrder || r.Exhaustion == "slate"
		var eligible []Candidate
		for _, c := range pool {
			if p.eligible(c, facts, cursor.Cycle, used, r.ID, cursor.LastSort, sequential, inOrder, bag, remaining, block.Overrun) {
				eligible = append(eligible, c)
			}
		}
		if r.EpisodeMode == "marathon" && cursor.LastShow != "" && !(facts.lastGroup != 0 && strconv.FormatInt(facts.lastGroup, 10) == cursor.LastShow && facts.consecutive >= r.MaxConsecutive) {
			lastShow, _ := strconv.ParseInt(cursor.LastShow, 10, 64)
			var stayed []Candidate
			for _, c := range eligible {
				if c.Show == lastShow {
					stayed = append(stayed, c)
				}
			}
			if len(stayed) > 0 {
				eligible = stayed
			}
		}
		if len(eligible) > 0 {
			var chosen Candidate
			if sequential {
				sort.SliceStable(eligible, func(i, j int) bool {
					if eligible[i].SortKey != eligible[j].SortKey {
						return eligible[i].SortKey < eligible[j].SortKey
					}
					return eligible[i].Item < eligible[j].Item
				})
				chosen = eligible[0]
			} else if c, ok := p.propose(eligible, pool, r, cursor, block); ok {
				chosen = c
			} else if c, ok := p.counted(eligible, r, cursor, block); ok {
				chosen = c
			} else {
				chosen = eligible[0]
			}
			cursor.Draw++
			cursor.LastSort = chosen.SortKey
			cursor.LastShow = ""
			if chosen.Show != 0 {
				cursor.LastShow = strconv.FormatInt(chosen.Show, 10)
			}
			cursor.LastBlock = block.ID
			state.Rules[r.ID] = cursor
			used[usedKey{r.ID, chosen.Item}] = cursor.Cycle
			return chosen, true
		}
		// Constraints (dedupe/fit/consecutive) can hide a whole cycle without
		// exhausting it. Only an actually empty cycle is exhaustion.
		if p.unused(pool, used, r.ID, cursor.Cycle) {
			break
		}
		if r.Exhaustion == "slate" {
			cursor.Done = true
			state.Rules[r.ID] = cursor
			break
		}
		cursor.Cycle++
		cursor.LastSort, cursor.LastShow = "", ""
	}
	state.Rules[r.ID] = cursor
	return Candidate{}, false
}

// propose mirrors the merged rejection-sampling draw: uniform ordinals over the
// dense pool, accepted with probability weight/maxWeight for a weighted rule.
func (p *Producer) propose(eligible, pool []Candidate, r librarychannels.Rule, cursor RuleCursor, block librarychannels.ResolvedBlock) (Candidate, bool) {
	if len(pool) == 0 {
		return Candidate{}, false
	}
	key := p.seed + ":" + r.ID + ":" + cursor.LastBlock
	top := 0.0
	if r.Mode == "weighted-random" {
		top = p.maxWeight(r)
	}
	eligibleSet := make(map[int64]bool, len(eligible))
	for _, c := range eligible {
		eligibleSet[c.Item] = true
	}
	for round := 0; round < drawRounds; round++ {
		for index := 0; index < drawProposals; index++ {
			b := unit(key, strconv.FormatInt(cursor.Draw, 10), strconv.Itoa(round*drawProposals+index))
			ordinal := int64(unitAt(b, 0) * float64(len(pool)))
			threshold := unitAt(b, 1) * top
			if ordinal < 0 || ordinal >= int64(len(pool)) {
				continue
			}
			c := pool[ordinal]
			if !eligibleSet[c.Item] || c.Weight <= threshold {
				continue
			}
			return c, true
		}
	}
	return Candidate{}, false
}

func (p *Producer) counted(eligible []Candidate, r librarychannels.Rule, cursor RuleCursor, block librarychannels.ResolvedBlock) (Candidate, bool) {
	if len(eligible) == 0 {
		return Candidate{}, false
	}
	key := p.seed + ":" + r.ID + ":" + cursor.LastBlock
	sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].Ordinal < eligible[j].Ordinal })
	if r.Mode != "weighted-random" {
		ticket := unitAt(unit(key, strconv.FormatInt(cursor.Draw, 10)), 0) * float64(len(eligible))
		index := int(ticket)
		if index >= len(eligible) {
			index = len(eligible) - 1
		}
		return eligible[index], true
	}
	total := 0.0
	for _, c := range eligible {
		total += c.Weight
	}
	if total <= 0 {
		return Candidate{}, false
	}
	ticket := unitAt(unit(key, strconv.FormatInt(cursor.Draw, 10)), 0) * total
	run := 0.0
	for _, c := range eligible {
		run += c.Weight
		if run > ticket {
			return c, true
		}
	}
	return eligible[len(eligible)-1], true
}

// Contiguous proves a whole plan is gap-free and complete for its window. The
// writer verifier repeats the same proof independently from stored rows.
func Contiguous(slots []Slot, start, end int64) bool {
	if len(slots) == 0 || start < 0 || end <= start {
		return false
	}
	cursor := start
	for _, slot := range slots {
		if slot.Start != cursor || slot.End <= slot.Start || slot.End > end || slot.Offset < 0 ||
			(len(slot.Slate) > 0 && (slot.Item != 0 || slot.Offset != 0)) {
			return false
		}
		cursor = slot.End
	}
	return cursor == end
}
