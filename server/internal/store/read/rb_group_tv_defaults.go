package read

import (
	"context"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// RBTVDefaults is a harness-only source resolver, separate from explicit TV
// groupOrder. ProvisionalInheritance applies the lead's temporary rule: revision
// one with all default settings inherits the CURRENT library episode numbering.
// It is NOT evidence that an explicit saved settings row was absent. IP's
// presence marker must replace this rule before final workspace qualification.
// Source values and the actual preference revision remain in the result; a
// caller-supplied settings struct can never substitute for this source read.
type RBTVDefaults struct {
	Show                                             int64
	HideSeasons, AbsoluteNumbering, NewestFirst      bool
	Ranges                                           string
	SettingsRevision                                 int64
	LibraryNumbering                                 string
	LibraryPreferenceRevision                        int64
	SavedAbsoluteNumbering, LibraryPreferencePresent bool
	ProvisionalInheritance                           bool
	fence                                            browseFence
}

const rbTVDefaultsPolicy readPolicyID = 32553

func rbTVDefaultsRow(q browseQuery, in BrowseInput, f browseFence) ([]any, error) {
	if f.maximumAge >= 0 || f.allowUnrated == 0 {
		return nil, ErrUnqualified
	}
	rows, e := q("rb.tv.defaults", in.policy.subject)
	if e != nil {
		return nil, e
	}
	if len(rows) != 1 {
		return nil, ErrDenied
	}
	v := rows[0]
	if v[1] != in.Library || v[2] != int64(2) || v[3] != int64(0) {
		return nil, ErrDenied
	}
	if v[4] == nil {
		if f.allowUnrated == 0 {
			return nil, ErrDenied
		}
	} else if f.maximumAge >= 0 && v[4].(int64) > f.maximumAge {
		return nil, ErrDenied
	}
	if v[10] != "tv" && v[10] != "anime" {
		return nil, ErrUnqualified
	}
	if len(v[8].(string)) > 1024 {
		return nil, ErrNotReady
	}
	return v, nil
}
func init() {
	registerReadPolicy(readPolicyRecipe{id: rbTVDefaultsPolicy, domain: "rb-tv-provisional-defaults", kinds: []int64{2}, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
		if in.policy.subject <= 0 {
			return "", ErrInput
		}
		v, e := rbTVDefaultsRow(q, in, f)
		if e != nil {
			return "", e
		}
		b, e := json.Marshal(v)
		return string(b), e
	}})
}
func (r *BrowseReader) RBTVSourceDefaults(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, view BrowseInput, show int64) (RBTVDefaults, BrowseReadResult, error) {
	if r == nil {
		return RBTVDefaults{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.pool, view.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBTVDefaults, BrowseReadResult, error) {
		view.Now = at
		return r.rbTVSourceDefaults(ctx, request, budget, out, view, show)
	})
}
func (r *BrowseReader) rbTVSourceDefaults(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, view BrowseInput, show int64) (RBTVDefaults, BrowseReadResult, error) {
	if show <= 0 || view.Kind != 2 || view.After != nil {
		return RBTVDefaults{}, BrowseReadResult{}, ErrInput
	}
	view.policy = readPolicyBinding{recipe: rbTVDefaultsPolicy, subject: show}
	return publishRead(ctx, r.snapshots, request, budget, out, view, []readStep[RBTVDefaults]{func(_ context.Context, q browseQuery, f browseFence, d *RBTVDefaults) error {
		v, e := rbTVDefaultsRow(q, view, f)
		if e != nil {
			return e
		}
		d.Show = show
		d.HideSeasons = v[5] == int64(1)
		d.AbsoluteNumbering = v[6] == int64(1)
		d.SavedAbsoluteNumbering = d.AbsoluteNumbering
		d.LibraryPreferencePresent = v[11] == int64(1)
		d.NewestFirst = v[7] == int64(1)
		d.Ranges = v[8].(string)
		d.SettingsRevision = v[9].(int64)
		d.LibraryNumbering = "season"
		d.fence = f
		if v[11] == int64(1) {
			d.LibraryNumbering = v[13].(string)
			d.LibraryPreferenceRevision = v[12].(int64)
		}
		d.ProvisionalInheritance = d.SettingsRevision == 1 && !d.HideSeasons && !d.AbsoluteNumbering && !d.NewestFirst && d.Ranges == ""
		if d.ProvisionalInheritance {
			d.AbsoluteNumbering = d.LibraryNumbering == "absolute"
		}
		return nil
	}})
}
func RBTVDefaultsDefinitions() []stmt.Definition {
	return []stmt.Definition{{ID: statementID("rb.tv.defaults"), SQL: `SELECT e.id,e.library_id,e.kind,e.retired,e.admission_own_age,e.hide_seasons,e.absolute_numbering,e.newest_first,e.show_ranges,e.show_settings_revision,l.kind,l.preference_present,l.preference_revision,l.preference_episode_numbering FROM catalog_entities AS e CROSS JOIN catalog_libraries AS c ON c.id=e.library_id CROSS JOIN libraries AS l INDEXED BY sqlite_autoindex_libraries_1 ON l.id=c.library_id WHERE e.id=?`, Owner: "store/read", CallSites: []string{"store/read/rb_group_tv_defaults.go:BrowseReader.RBTVSourceDefaults"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "show", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(-1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "c"}, Indexes: []string{"sqlite_autoindex_libraries_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}}}
}
