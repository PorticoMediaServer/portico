package writer

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IP owns the episode-numbering and series source facts; it does not own the
// general settings/event entry. These helpers are the composed effect a future
// UO-owned settings dispatcher calls inside its own authorized turn and saved
// receipt. They authorize nothing, mint no command ID and write no receipt, so
// a caller can never obtain an Applied result from them. Presence is the exact
// saved show-settings marker: it is independent of revision and of whether any
// boolean value stayed false.
const (
	ipShowSettingsRead      stmt.ID = "ip.show_presence.read"
	ipShowSettingsSave      stmt.ID = "ip.show_presence.save"
	ipShowSettingsRestore   stmt.ID = "ip.show_presence.restore"
	ipShowSettingsNumbering stmt.ID = "ip.show_presence.numbering"

	ipShowSeriesTextMax = 4096
)

type ipEpisodeNumbering uint8

const (
	ipEpisodeSeason ipEpisodeNumbering = iota + 1
	ipEpisodeAbsolute
)

type ipShowSettingsInput struct {
	EntityID, ExpectedRevision int64
}

type ipShowSettingsEffect struct {
	EntityID, Revision int64
	Present            bool
}

type ipShowNumberingFacts struct {
	Present   bool
	Absolute  bool
	Library   string
	Effective ipEpisodeNumbering
}

type ipSeriesSourcePair struct {
	Series, Position string
}

type ipSeriesSourceState struct {
	Series, Position string
	Present, Locked  bool
}

func ipShowSettingsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	defs := []stmt.Definition{
		{ID: ipShowSettingsRead, Kind: stmt.Read, SQL: `SELECT show_settings_present,show_settings_revision,kind FROM catalog_entities WHERE id=? AND retired=0`, Params: []stmt.Param{i("entity")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(0)}}}},
		{ID: ipShowSettingsSave, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET show_settings_present=1,show_settings_revision=show_settings_revision+1 WHERE id=? AND kind=2 AND retired=0 AND show_settings_revision=? RETURNING show_settings_revision,show_settings_present`, Params: []stmt.Param{i("entity"), i("revision")}, Cohorts: []stmt.Cohort{{Name: "stale", Bindings: []any{int64(0), int64(0)}}}},
		// The canonical CHECK requires the explicit numbering columns to be at
		// their defaults whenever show_settings_present=0, so inheriting clears
		// them in the same write that clears presence.
		{ID: ipShowSettingsRestore, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET show_settings_present=0,show_settings_revision=show_settings_revision+1,hide_seasons=0,absolute_numbering=0,newest_first=0,show_ranges='' WHERE id=? AND kind=2 AND retired=0 AND show_settings_revision=? AND show_settings_present=1 RETURNING show_settings_revision,show_settings_present`, Params: []stmt.Param{i("entity"), i("revision")}, Cohorts: []stmt.Cohort{{Name: "stale", Bindings: []any{int64(0), int64(0)}}}},
		{ID: ipShowSettingsNumbering, Kind: stmt.Read, SQL: `SELECT e.show_settings_present,e.absolute_numbering,COALESCE(l.preference_episode_numbering,'') FROM catalog_entities AS e CROSS JOIN catalog_libraries AS c ON c.id=e.library_id CROSS JOIN libraries AS l INDEXED BY sqlite_autoindex_libraries_1 ON l.id=c.library_id WHERE e.id=? AND e.kind=2 AND e.retired=0`, Params: []stmt.Param{i("show")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "c"}, Indexes: []string{"sqlite_autoindex_libraries_1"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(0)}}}},
	}
	for x := range defs {
		defs[x].Owner = "store/writer IP show settings effect"
		defs[x].CallSites = []string{"store/writer/ip_show_settings_effects.go"}
	}
	return defs
}

func ipShowSettingsInputValid(in ipShowSettingsInput) bool {
	return in.EntityID > 0 && in.ExpectedRevision > 0 && in.ExpectedRevision < math.MaxInt64
}

func ipShowSettingsCurrent(ctx context.Context, turn *commandTurn, entityID int64) (present, revision int64, err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := ipTextRows(ctx, turn, ipShowSettingsRead, entityID)
	if err != nil {
		return 0, 0, err
	}
	var kind int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&present, &revision, &kind) }); err != nil {
		return 0, 0, errors.Join(ErrConflict, err)
	}
	if _, ok := ipTextFlag(present); !ok || revision <= 0 || kind != 2 {
		return 0, 0, ErrResolveRequired
	}
	return present, revision, nil
}

// applyIPShowSettingsSave records an explicit show-settings save: presence is
// set and the revision advances, even when every saved boolean stays false and
// even at the first save from the embedded default. Values belong to the
// composing settings entry and land under the same revision CAS.
func applyIPShowSettingsSave(ctx context.Context, turn *commandTurn, in ipShowSettingsInput) (out ipShowSettingsEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || !ipShowSettingsInputValid(in) {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	_, revision, err := ipShowSettingsCurrent(ctx, turn, in.EntityID)
	if err != nil {
		return out, err
	}
	if revision != in.ExpectedRevision {
		return out, ErrConflict
	}
	rows, err := ipTextRows(ctx, turn, ipShowSettingsSave, in.EntityID, in.ExpectedRevision)
	if err != nil {
		return out, err
	}
	var next, present int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&next, &present) }); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if present != 1 || next != in.ExpectedRevision+1 {
		return out, ErrResolveRequired
	}
	return ipShowSettingsEffect{EntityID: in.EntityID, Revision: next, Present: true}, nil
}

// applyIPShowSettingsRestore returns the show to library numbering inheritance:
// presence clears, the explicit numbering columns return to their defaults (the
// canonical CHECK forbids them while presence is absent) and the revision
// advances under the same CAS. Restoring an already-inheriting show is a
// conflict, never a silent second change.
func applyIPShowSettingsRestore(ctx context.Context, turn *commandTurn, in ipShowSettingsInput) (out ipShowSettingsEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || !ipShowSettingsInputValid(in) {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	present, revision, err := ipShowSettingsCurrent(ctx, turn, in.EntityID)
	if err != nil {
		return out, err
	}
	if revision != in.ExpectedRevision || present != 1 {
		return out, ErrConflict
	}
	rows, err := ipTextRows(ctx, turn, ipShowSettingsRestore, in.EntityID, in.ExpectedRevision)
	if err != nil {
		return out, err
	}
	var next, saved int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&next, &saved) }); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if saved != 0 || next != in.ExpectedRevision+1 {
		return out, ErrResolveRequired
	}
	return ipShowSettingsEffect{EntityID: in.EntityID, Revision: next}, nil
}

// ipShowSettingsNumbering reads the saved presence marker, the show's absolute
// flag and the current library preference, then resolves the effective episode
// numbering. Presence, not revision, selects inheritance: 0 inherits and 1 uses
// the show's absolute flag even when it is false.
func ipShowSettingsNumberingRead(ctx context.Context, turn *commandTurn, entityID int64) (out ipShowNumberingFacts, err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if entityID <= 0 {
		return out, ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipShowSettingsNumbering, entityID)
	if err != nil {
		return out, err
	}
	var present, absolute int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&present, &absolute, &out.Library) }); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	p, ok1 := ipTextFlag(present)
	a, ok2 := ipTextFlag(absolute)
	if !ok1 || !ok2 {
		return out, ErrResolveRequired
	}
	out.Present, out.Absolute = p, a
	if out.Effective, err = ipResolveEpisodeNumbering(present, absolute, out.Library); err != nil {
		return out, err
	}
	return out, nil
}

func ipResolveEpisodeNumbering(present, absolute int64, library string) (ipEpisodeNumbering, error) {
	p, ok1 := ipTextFlag(present)
	a, ok2 := ipTextFlag(absolute)
	if !ok1 || !ok2 {
		return 0, ErrInvalid
	}
	switch library {
	case "", "season", "absolute":
	default:
		return 0, ErrInvalid
	}
	if p {
		if a {
			return ipEpisodeAbsolute, nil
		}
		return ipEpisodeSeason, nil
	}
	if library == "absolute" {
		return ipEpisodeAbsolute, nil
	}
	return ipEpisodeSeason, nil
}

func ipSeriesSourceText(value string) bool {
	if len(value) > ipShowSeriesTextMax || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ipResolveSeriesSource applies the exact accepted local series precedence: a
// nonempty scalar series wins with its exact position, including an empty one;
// an explicit owner fact (including a locked-empty clear) suppresses the tag
// list instead of being treated as missing; a standalone scalar position with
// no series never attaches to anything. No revision is consulted.
func ipResolveSeriesSource(owner ipSeriesSourceState, local []ipSeriesSourcePair) ([]ipSeriesSourcePair, error) {
	if !ipSeriesSourceText(owner.Series) || !ipSeriesSourceText(owner.Position) {
		return nil, ErrInvalid
	}
	for _, pair := range local {
		if !ipSeriesSourceText(pair.Series) || !ipSeriesSourceText(pair.Position) {
			return nil, ErrInvalid
		}
	}
	if owner.Present || owner.Series != "" {
		if owner.Series == "" {
			return []ipSeriesSourcePair{}, nil
		}
		return []ipSeriesSourcePair{{Series: strings.Clone(owner.Series), Position: strings.Clone(owner.Position)}}, nil
	}
	out := make([]ipSeriesSourcePair, len(local))
	for i, pair := range local {
		out[i] = ipSeriesSourcePair{Series: strings.Clone(pair.Series), Position: strings.Clone(pair.Position)}
	}
	return out, nil
}
