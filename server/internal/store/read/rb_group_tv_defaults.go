package read

import (
	"context"
	"encoding/binary"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// RBTVDefaults is a harness-only source resolver, separate from explicit TV
// groupOrder. SettingsPresent is the catalog_entities.show_settings_present
// source fact: zero inherits the CURRENT library episode numbering even when the
// settings revision is greater than one, and one is an explicit owner save even
// when every saved field is default at revision one. Revision and stored values
// never stand in for presence. Source values and the actual preference revision
// remain in the result; a caller-supplied settings struct can never substitute
// for this source read.
type RBTVDefaults struct {
	Show                                             int64
	HideSeasons, AbsoluteNumbering, NewestFirst      bool
	Ranges                                           string
	SettingsRevision                                 int64
	LibraryNumbering                                 string
	LibraryPreferenceRevision                        int64
	SavedAbsoluteNumbering, LibraryPreferencePresent bool
	SettingsPresent                                  bool
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
	if v[14] != int64(0) && v[14] != int64(1) {
		return nil, ErrUnqualified
	}
	return v, nil
}
func init() {
	registerReadPolicy(readPolicyRecipe{id: rbTVDefaultsPolicy, domain: "rb-tv-defaults", kinds: []int64{2}, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
		if in.policy.subject <= 0 {
			return "", ErrInput
		}
		v, e := rbTVDefaultsRow(q, in, f)
		if e != nil {
			return "", e
		}
		return rbTVDefaultsWitness(v)
	}})
}

// Preserve exact SQLite strings, including malformed UTF-8, in the source
// witness. JSON replacement characters cannot identify an unchanged row.
func rbTVDefaultsWitness(row []any) (string, error) {
	var b []byte
	for _, value := range row {
		switch v := value.(type) {
		case nil:
			b = append(b, 0)
		case int64:
			b = binary.BigEndian.AppendUint64(append(b, 1), uint64(v))
		case string:
			b = binary.BigEndian.AppendUint64(append(b, 2), uint64(len(v)))
			b = append(b, v...)
		default:
			return "", ErrUnqualified
		}
	}
	return string(b), nil
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
		d.SettingsPresent = v[14] == int64(1)
		if !d.SettingsPresent {
			// Presence zero inherits the CURRENT library numbering at any
			// settings revision; stored booleans are then default by the
			// catalog_entities CHECK and are not explicit evidence.
			d.AbsoluteNumbering = d.LibraryNumbering == "absolute"
		}
		return nil
	}})
}
func RBTVDefaultsDefinitions() []stmt.Definition {
	return []stmt.Definition{{ID: statementID("rb.tv.defaults"), SQL: `SELECT e.id,e.library_id,e.kind,e.retired,e.admission_own_age,e.hide_seasons,e.absolute_numbering,e.newest_first,e.show_ranges,e.show_settings_revision,l.kind,l.preference_present,l.preference_revision,l.preference_episode_numbering,e.show_settings_present FROM catalog_entities AS e CROSS JOIN catalog_libraries AS c ON c.id=e.library_id CROSS JOIN libraries AS l INDEXED BY sqlite_autoindex_libraries_1 ON l.id=c.library_id WHERE e.id=?`, Owner: "store/read", CallSites: []string{"store/read/rb_group_tv_defaults.go:BrowseReader.RBTVSourceDefaults"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "show", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(-1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "c"}, Indexes: []string{"sqlite_autoindex_libraries_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}}}
}
