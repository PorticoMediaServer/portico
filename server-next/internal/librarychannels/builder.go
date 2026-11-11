// Package librarychannels makes always-on channels from the owner's own films and shows: the
// builder document the owner edits, the schedule built from it a week ahead, and who may see
// a channel. Only internal/db runs SQL; this package decides what plays and when.
package librarychannels

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Limits on one channel's document. Most people never come near them.
const (
	MaxBlocks      = 500
	MaxPinned      = 500
	MaxWeights     = 100
	MaxConditions  = 100
	MaxBuilderSize = 2 << 20
)

// Selection is what one part of a channel plays: the channel's own choice, or a block's copy.
// Titles and libraries are named by public id.
type Selection struct {
	Libraries []string `json:"libraries"`
	// Kinds is movie, show or both.
	Kinds      []string              `json:"kinds"`
	Match      string                `json:"match,omitempty"` // all (the default) or any
	Conditions []db.ChannelCondition `json:"conditions,omitempty"`
	// Mentions is words that must all appear, as whole words, in the title or the summary.
	Mentions        string   `json:"mentions,omitempty"`
	AddedWithinDays int      `json:"addedWithinDays,omitempty"`
	Always          []string `json:"always,omitempty"`
	Never           []string `json:"never,omitempty"`
	// Order is shuffle (the default: every title once before any again), random (repeats
	// allowed), inOrder, inOrderThenShuffle, or leastPlayed.
	Order string `json:"order,omitempty"`
	// Sort is the order of inOrder: title (the default), release, year, recent, firstAdded,
	// rating or episode (show then episode).
	Sort string `json:"sort,omitempty"`
	// Episodes is how a show's episodes play: showOrder (the default: when the show comes up,
	// its next episode), mixed (each episode picked like a film), marathon, rotate (one
	// episode of each show in turn, shows by title) or random.
	Episodes string `json:"episodes,omitempty"`
	// Marathon is how many episodes of one show play in a row: 12 unless set, 2 to 100.
	Marathon int `json:"marathon,omitempty"`
	// WhenDone is again (the default) or slate, once everything has played.
	WhenDone string `json:"whenDone,omitempty"`
	// Weights make a title or show play more or less often under random: 0.25 to 10.
	Weights []Weight `json:"weights,omitempty"`
}

// Weight is how much more or less often one title or show plays.
type Weight struct {
	ID     string  `json:"id"`
	Weight float64 `json:"weight"`
}

// Builder is a Library Channel's whole document.
type Builder struct {
	Selection
	// AvoidRepeats keeps a title from playing again within that many programmes: 5 unless set.
	AvoidRepeats *int    `json:"avoidRepeats,omitempty"`
	Blocks       []Block `json:"blocks,omitempty"`
	Quality      Quality `json:"quality"`
	LogoOnScreen Logo    `json:"logoOnScreen"`
}

// Block is a weekly stretch of hours with its own selection.
type Block struct {
	ID   string   `json:"id"`
	Name string   `json:"name,omitempty"`
	Days []string `json:"days"`
	From string   `json:"from"`
	To   string   `json:"to"`
	// Selection is the block's own copy; none plays the channel's own selection.
	Selection *Selection `json:"selection,omitempty"`
	// Overrun is finish (the default), cut or fit.
	Overrun string `json:"overrun,omitempty"`
	// Resume is pickUp (the default) or restart.
	Resume string `json:"resume,omitempty"`
	// IfEmpty is channel (the default: what the channel plays otherwise) or slate.
	IfEmpty string `json:"ifEmpty,omitempty"`
}

// Quality is auto (each viewer's own settings), original (never converted) or limited.
type Quality struct {
	Mode    string `json:"mode,omitempty"`
	MaxKbps int    `json:"maxKbps,omitempty"`
}

// Logo is the on-screen logo the apps draw over the picture.
type Logo struct {
	On     bool   `json:"on,omitempty"`
	Corner string `json:"corner,omitempty"` // topRight (the default), topLeft, bottomRight, bottomLeft
	Size   string `json:"size,omitempty"`   // small, medium (the default), large
}

// Recipe is a block preset: what a block copies when it is added from the preset.
type Recipe struct {
	// Clauses include or exclude values of a field (Exclude), all of which must hold.
	Clauses []db.ChannelCondition `json:"clauses"`
	// Order is shuffle, inOrder, recent or leastPlayed.
	Order    string   `json:"order"`
	Minutes  int      `json:"minutes"`
	StartsAt string   `json:"startsAt,omitempty"`
	Days     []string `json:"days,omitempty"`
}

// FieldError names the field a save is refused for, and why, in a sentence to show.
type FieldError struct{ Field, Message string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

func fieldError(field, format string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

var weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Parse reads a builder document.
func Parse(raw string) (*Builder, error) {
	var b Builder
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return nil, fieldError("builder", "The channel's settings could not be read.")
	}
	return &b, nil
}

// AvoidWithin is the "Avoid repeats within" number in force.
func (b *Builder) AvoidWithin() int {
	if b.AvoidRepeats == nil {
		return 5
	}
	return *b.AvoidRepeats
}

// Libraries is every library the channel and its blocks draw from: who may see the channel
// follows this list.
func (b *Builder) Libraries() []string {
	var out []string
	add := func(ids []string) {
		for _, id := range ids {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	add(b.Selection.Libraries)
	for _, blk := range b.Blocks {
		if blk.Selection != nil {
			add(blk.Selection.Libraries)
		}
	}
	return out
}

// Pinned is every title the document pins, always or never, in any selection.
func (b *Builder) Pinned() []string {
	out := slices.Concat(b.Always, b.Never)
	for _, blk := range b.Blocks {
		if blk.Selection != nil {
			out = append(out, slices.Concat(blk.Selection.Always, blk.Selection.Never)...)
		}
	}
	return out
}

// HasUnknownConditions says the document holds conditions this server cannot test: they are
// kept as they are and the builder says so.
func (b *Builder) HasUnknownConditions() bool {
	all := slices.Clone(b.Conditions)
	for _, blk := range b.Blocks {
		if blk.Selection != nil {
			all = append(all, blk.Selection.Conditions...)
		}
	}
	for _, c := range all {
		if !c.Known() {
			return true
		}
	}
	return false
}

// Check refuses a document that cannot be saved, naming the field. libraryKinds maps each
// library that exists to its kind, and titleLibraries each pinned title to its library.
func (b *Builder) Check(libraryKinds, titleLibraries map[string]string) error {
	if err := b.Selection.check("", libraryKinds, titleLibraries); err != nil {
		return err
	}
	if b.AvoidRepeats != nil && (*b.AvoidRepeats < 0 || *b.AvoidRepeats > 1000) {
		return fieldError("avoidRepeats", "Avoid repeats within 0 to 1,000 programs.")
	}
	switch b.Quality.Mode {
	case "", "auto", "original":
	case "limited":
		if b.Quality.MaxKbps < 500 || b.Quality.MaxKbps > 200_000 {
			return fieldError("quality.maxKbps", "Enter a bitrate from 500 to 200,000 kbps.")
		}
	default:
		return fieldError("quality.mode", "Choose Automatic, Original only or Limited bitrate.")
	}
	if !oneOf(b.LogoOnScreen.Corner, "topRight", "topLeft", "bottomRight", "bottomLeft") ||
		!oneOf(b.LogoOnScreen.Size, "small", "medium", "large") {
		return fieldError("logoOnScreen", "Choose a corner and a size for the logo.")
	}
	if len(b.Blocks) > MaxBlocks {
		return fieldError("blocks", "A channel holds up to %d blocks.", MaxBlocks)
	}
	ids := map[string]bool{}
	for n, blk := range b.Blocks {
		field := fmt.Sprintf("blocks[%d]", n)
		if blk.ID == "" || ids[blk.ID] {
			return fieldError(field+".id", "Each block needs its own id.")
		}
		ids[blk.ID] = true
		if len(blk.Days) == 0 {
			return fieldError(field+".days", "Choose at least one day.")
		}
		for _, d := range blk.Days {
			if !slices.Contains(weekdays, d) {
				return fieldError(field+".days", "%q is not a day; use mon to sun.", d)
			}
		}
		from, okFrom := clock(blk.From)
		to, okTo := clock(blk.To)
		if !okFrom || !okTo || from == to {
			return fieldError(field+".from", "Enter a start and an end such as 19:00 and 23:00.")
		}
		if !oneOf(blk.Overrun, "finish", "cut", "fit") || !oneOf(blk.Resume, "pickUp", "restart") || !oneOf(blk.IfEmpty, "channel", "slate") {
			return fieldError(field, "Choose what happens at the end, each time it starts and if nothing matches.")
		}
		if blk.Selection != nil {
			if err := blk.Selection.check(field+".selection.", libraryKinds, titleLibraries); err != nil {
				return err
			}
		}
	}
	return b.checkOverlap()
}

func oneOf(value string, choices ...string) bool {
	return value == "" || slices.Contains(choices, value)
}

func (s *Selection) check(prefix string, libraryKinds, titleLibraries map[string]string) error {
	if len(s.Libraries) == 0 {
		return fieldError(prefix+"libraries", "Choose at least one library.")
	}
	for _, id := range s.Libraries {
		switch libraryKinds[id] {
		case db.LibraryMovies, db.LibraryShows, db.LibraryAnime:
		case "":
			return fieldError(prefix+"libraries", "A chosen library no longer exists.")
		default:
			return fieldError(prefix+"libraries", "A Library Channel plays only Movies, TV Shows and Anime libraries.")
		}
	}
	if len(s.Kinds) == 0 {
		return fieldError(prefix+"kinds", "Choose movies, TV shows, or both.")
	}
	for _, k := range s.Kinds {
		if k != "movie" && k != "show" {
			return fieldError(prefix+"kinds", "Choose movies, TV shows, or both.")
		}
	}
	if !oneOf(s.Match, "all", "any") {
		return fieldError(prefix+"match", "Match all of these or any of these.")
	}
	count := 0
	for _, c := range s.Conditions {
		count++
		if c.Conditions != nil || c.Match != "" {
			if !oneOf(c.Match, "all", "any") {
				return fieldError(prefix+"conditions", "A group matches all of these or any of these.")
			}
			for _, inner := range c.Conditions {
				count++
				if inner.Conditions != nil || inner.Match != "" {
					return fieldError(prefix+"conditions", "A group cannot hold another group.")
				}
			}
		}
	}
	if count > MaxConditions {
		return fieldError(prefix+"conditions", "A channel holds up to %d conditions.", MaxConditions)
	}
	if s.AddedWithinDays < 0 {
		return fieldError(prefix+"addedWithinDays", "Choose how recently titles were added.")
	}
	for _, list := range []struct {
		name string
		ids  []string
	}{{"always", s.Always}, {"never", s.Never}} {
		if len(list.ids) > MaxPinned {
			return fieldError(prefix+list.name, "Each list of pinned titles holds up to %d.", MaxPinned)
		}
		for _, id := range list.ids {
			library, ok := titleLibraries[id]
			if !ok {
				return fieldError(prefix+list.name, "A pinned title no longer exists, or is not a movie or a show.")
			}
			if !slices.Contains(s.Libraries, library) {
				return fieldError(prefix+list.name, "A pinned title is not in a library this channel draws from.")
			}
		}
	}
	if !oneOf(s.Order, "shuffle", "random", "inOrder", "inOrderThenShuffle", "leastPlayed") {
		return fieldError(prefix+"order", "Choose a play order.")
	}
	if !oneOf(s.Sort, "title", "release", "year", "recent", "firstAdded", "rating", "episode") {
		return fieldError(prefix+"sort", "Choose what In order sorts by.")
	}
	if !oneOf(s.Episodes, "showOrder", "mixed", "marathon", "rotate", "random") {
		return fieldError(prefix+"episodes", "Choose how a show's episodes play.")
	}
	if s.Marathon != 0 && (s.Marathon < 2 || s.Marathon > 100) {
		return fieldError(prefix+"marathon", "A marathon is 2 to 100 episodes.")
	}
	if !oneOf(s.WhenDone, "again", "slate") {
		return fieldError(prefix+"whenDone", "Choose Start again or Show a slate.")
	}
	if len(s.Weights) > MaxWeights {
		return fieldError(prefix+"weights", "Up to %d titles can play more or less often.", MaxWeights)
	}
	for _, w := range s.Weights {
		if w.Weight < 0.25 || w.Weight > 10 {
			return fieldError(prefix+"weights", "Play a title from a quarter as often to ten times as often.")
		}
		if library, ok := titleLibraries[w.ID]; !ok || !slices.Contains(s.Libraries, library) {
			return fieldError(prefix+"weights", "A title to play more or less often is not in a library this channel draws from.")
		}
	}
	return nil
}

// Weighted lists the titles whose weights must be looked up, with the pinned ones.
func (b *Builder) Weighted() []string {
	var out []string
	for _, w := range b.Weights {
		out = append(out, w.ID)
	}
	for _, blk := range b.Blocks {
		if blk.Selection != nil {
			for _, w := range blk.Selection.Weights {
				out = append(out, w.ID)
			}
		}
	}
	return out
}

// clock reads "19:00" as minutes after midnight.
func clock(text string) (int, bool) {
	h, m, ok := strings.Cut(text, ":")
	hours, errH := strconv.Atoi(h)
	minutes, errM := strconv.Atoi(m)
	if !ok || errH != nil || errM != nil || hours < 0 || hours > 23 || minutes < 0 || minutes > 59 || len(m) != 2 {
		return 0, false
	}
	return hours*60 + minutes, true
}

const week = 7 * 24 * 60

// checkOverlap refuses two blocks that share a minute of the week, naming the other block.
func (b *Builder) checkOverlap() error {
	type stretch struct{ from, to, block int }
	var all []stretch
	for n, blk := range b.Blocks {
		from, _ := clock(blk.From)
		to, _ := clock(blk.To)
		length := (to - from + 24*60) % (24 * 60)
		for _, d := range blk.Days {
			start := slices.Index(weekdays, d)*24*60 + from
			end := start + length
			if end > week { // past Saturday midnight: the rest is early Sunday
				all = append(all, stretch{start, week, n}, stretch{0, end - week, n})
			} else {
				all = append(all, stretch{start, end, n})
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].from < all[j].from })
	for n := 1; n < len(all); n++ {
		if all[n].from < all[n-1].to {
			first := b.Blocks[all[n-1].block]
			name := first.Name
			if name == "" {
				name = first.From + " to " + first.To
			}
			if all[n-1].block == all[n].block {
				return fieldError(fmt.Sprintf("blocks[%d]", all[n].block), "This block overlaps itself on consecutive days.")
			}
			return fieldError(fmt.Sprintf("blocks[%d]", all[n].block), "This block overlaps %s. Two blocks can't share any hours.", name)
		}
	}
	return nil
}

// CheckRecipe refuses a block preset that cannot be saved.
func CheckRecipe(r *Recipe) error {
	if r.Minutes < 5 || r.Minutes > 1440 {
		return fieldError("recipe.minutes", "Minutes, from 5 to 1,440.")
	}
	if r.StartsAt != "" {
		if _, ok := clock(r.StartsAt); !ok {
			return fieldError("recipe.startsAt", "Enter a start such as 19:00.")
		}
	}
	for _, d := range r.Days {
		if !slices.Contains(weekdays, d) {
			return fieldError("recipe.days", "%q is not a day; use mon to sun.", d)
		}
	}
	if !oneOf(r.Order, "shuffle", "inOrder", "recent", "leastPlayed") {
		return fieldError("recipe.order", "Choose Shuffle, In order, Recently added or Least played.")
	}
	if len(r.Clauses) > MaxConditions {
		return fieldError("recipe.clauses", "A preset holds up to %d clauses.", MaxConditions)
	}
	for _, c := range r.Clauses {
		if c.Conditions != nil || c.Match != "" {
			return fieldError("recipe.clauses", "A preset's clauses cannot be grouped.")
		}
	}
	return nil
}

// LoadZone reads a channel's time zone, refusing one this server does not know.
func LoadZone(name string) (*time.Location, error) {
	if name == "" || strings.EqualFold(name, "local") {
		return nil, fieldError("timeZone", "Enter a time zone like America/New_York.")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fieldError("timeZone", "Enter a time zone like America/New_York.")
	}
	return loc, nil
}
