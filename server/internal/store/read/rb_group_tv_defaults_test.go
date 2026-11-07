package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

//go:embed rb_group_tv_defaults_test.go
var rbTVDefaultsJournal string

func rbTVDefaultsHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rbTermHash(t)+rbTVDefaultsJournal)))
}
func rbTVDefaultsSource(t *testing.T) *browseFixture {
	f := rbTermSource(t)
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `UPDATE catalog_subjects SET kind=2 WHERE id=1`)
	f.exec(t, `UPDATE catalog_entities SET kind=2 WHERE id=1`)
	f.exec(t, "COMMIT")
	f.exec(t, `UPDATE libraries SET kind='tv',preference_present=1,preference_revision=1,preference_group_collections=0,preference_local_tags='off',preference_lyrics_source='files',preference_title_source='tags',preference_episode_numbering='absolute',preference_find_extras=0,preference_sort_ignore_articles=1 WHERE id='movies'`)
	f.input.Kind = 2
	return f
}
func TestRBTVProvisionalSourceDefaults(t *testing.T) {
	f := rbTVDefaultsSource(t)
	defs := append(browseDefinitions(), RBTVDefaultsDefinitions()...)
	// Both are actual captures of this test's merged schema and custom journal.
	captures := map[string]plan.Golden{}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g := captureBrowse(t, f, RBTVDefaultsDefinitions(), stats, rbTVDefaultsHash(t))
		if _, e := stmt.New(RBTVDefaultsDefinitions(), g.Receipt()); e != nil {
			t.Fatal(e)
		}
		captures[string(stats)] = g
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		b, e := json.Marshal(captures)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "rb-group-tv-defaults-capture.json"), b, 0644); e != nil {
			t.Fatal(e)
		}
	}
	f = rbTVDefaultsSource(t)
	f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, rbTVDefaultsHash(t))
	read := func() RBTVDefaults {
		t.Helper()
		q, request, output := liveRequests("rb-tv-defaults")
		var p RBTVDefaults
		_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			var e error
			p, _, e = f.reader.RBTVSourceDefaults(ctx, request, time.Second, out, f.input, 1)
			if e == nil {
				assertLiveUsage(t, f, 1, 0)
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		assertLiveUsage(t, f, 0, 0)
		return p
	}
	p := read()
	if !p.ProvisionalInheritance || !p.AbsoluteNumbering || p.SavedAbsoluteNumbering || p.LibraryPreferenceRevision != 1 || !p.LibraryPreferencePresent {
		t.Fatal("provisional inherit is not absence proof", p)
	}
	f.exec(t, `UPDATE libraries SET preference_episode_numbering='season',preference_revision=2 WHERE id='movies'`)
	p = read()
	if !p.ProvisionalInheritance || p.AbsoluteNumbering || p.LibraryPreferenceRevision != 2 {
		t.Fatal("current library preference", p)
	}
	f.exec(t, `UPDATE libraries SET preference_episode_numbering='absolute',preference_revision=3 WHERE id='movies'`)
	f.exec(t, `UPDATE catalog_entities SET show_settings_revision=2 WHERE id=1`)
	p = read()
	if p.ProvisionalInheritance || p.AbsoluteNumbering || p.SettingsRevision != 2 {
		t.Fatal("saved false suppresses inheritance", p)
	}
	f.exec(t, `UPDATE catalog_entities SET show_settings_revision=1,hide_seasons=1 WHERE id=1`)
	p = read()
	if p.ProvisionalInheritance || !p.HideSeasons || p.AbsoluteNumbering {
		t.Fatal("nondefault revision one is explicit", p)
	}
	// Changes without a revision bump also fail the actual source-value witness.
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE catalog_entities SET absolute_numbering=1 WHERE id=1`) }}
	q, request, output := liveRequests("rb-tv-defaults-drift")
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		p, _, e := f.reader.RBTVSourceDefaults(ctx, request, time.Second, out, f.input, 1)
		if e == nil || !reflect.DeepEqual(p, RBTVDefaults{}) {
			t.Fatal("drift leak", p, e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("PROVISIONAL lead rule; actual Reader Capture both statistics schema=%s fixture=%s", captures["analyzed"].Environment.SchemaSHA256, rbTVDefaultsHash(t))
}
