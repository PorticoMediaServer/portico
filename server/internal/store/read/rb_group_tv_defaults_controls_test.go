package read

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// TestRBTVDefaultsSavedFields proves that a separately saved nondefault show
// field is explicit presence even at settings revision one.
func TestRBTVDefaultsSavedFields(t *testing.T) {
	t.Run("newest-first", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := rbTVDefaultsSource(t)
			f.exec(t, `UPDATE catalog_entities SET newest_first=1,show_settings_present=1 WHERE id=2001`)
			defs := append(browseDefinitions(), RBTVDefaultsDefinitions()...)
			f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, rbTVDefaultsHash(t))
			q, request, output := liveRequests("rb-tv-defaults-newest-first")
			var p RBTVDefaults
			consumption, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
				var e error
				p, _, e = f.reader.RBTVSourceDefaults(ctx, request, time.Second, out, f.input, 2001)
				if e == nil {
					assertLiveUsage(t, f, 1, 0)
				}
				return e
			})
			if e != nil || consumption.Refusal != nil {
				t.Fatal("saved newest-first read", e, consumption.Refusal)
			}
			assertLiveUsage(t, f, 0, 0)
			if p.Show != 2001 || p.SettingsRevision != 1 || !p.SettingsPresent || !p.NewestFirst || p.AbsoluteNumbering || p.SavedAbsoluteNumbering {
				t.Fatal("saved newest-first must suppress inheritance", p)
			}
			if !p.LibraryPreferencePresent || p.LibraryNumbering != "absolute" || p.LibraryPreferenceRevision != 1 {
				t.Fatal("saved newest-first changed library preference facts", p)
			}
		})
	})

	t.Run("show-ranges", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := rbTVDefaultsSource(t)
			f.exec(t, `UPDATE catalog_entities SET show_ranges='1-3',show_settings_present=1 WHERE id=2001`)
			defs := append(browseDefinitions(), RBTVDefaultsDefinitions()...)
			f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, rbTVDefaultsHash(t))
			q, request, output := liveRequests("rb-tv-defaults-show-ranges")
			var p RBTVDefaults
			consumption, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
				var e error
				p, _, e = f.reader.RBTVSourceDefaults(ctx, request, time.Second, out, f.input, 2001)
				if e == nil {
					assertLiveUsage(t, f, 1, 0)
				}
				return e
			})
			if e != nil || consumption.Refusal != nil {
				t.Fatal("saved show-ranges read", e, consumption.Refusal)
			}
			assertLiveUsage(t, f, 0, 0)
			if p.Show != 2001 || p.SettingsRevision != 1 || !p.SettingsPresent || p.NewestFirst || p.AbsoluteNumbering || p.SavedAbsoluteNumbering || p.Ranges != "1-3" {
				t.Fatal("saved show-ranges must suppress inheritance", p)
			}
			if !p.LibraryPreferencePresent || p.LibraryNumbering != "absolute" || p.LibraryPreferenceRevision != 1 {
				t.Fatal("saved show-ranges changed library preference facts", p)
			}
		})
	})

	t.Run("saved-absolute", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := rbTVDefaultsSource(t)
			f.exec(t, `UPDATE libraries SET preference_episode_numbering='season' WHERE id='movies'`)
			f.exec(t, `UPDATE catalog_entities SET absolute_numbering=1,show_settings_present=1 WHERE id=2001`)
			defs := append(browseDefinitions(), RBTVDefaultsDefinitions()...)
			f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, rbTVDefaultsHash(t))
			q, request, output := liveRequests("rb-tv-defaults-saved-absolute")
			var p RBTVDefaults
			consumption, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
				var e error
				p, _, e = f.reader.RBTVSourceDefaults(ctx, request, time.Second, out, f.input, 2001)
				if e == nil {
					assertLiveUsage(t, f, 1, 0)
				}
				return e
			})
			if e != nil || consumption.Refusal != nil {
				t.Fatal("saved absolute read", e, consumption.Refusal)
			}
			assertLiveUsage(t, f, 0, 0)
			if p.Show != 2001 || p.SettingsRevision != 1 || !p.SettingsPresent || p.NewestFirst || !p.AbsoluteNumbering || !p.SavedAbsoluteNumbering || p.Ranges != "" {
				t.Fatal("saved absolute must not inherit seasonal library", p)
			}
			if !p.LibraryPreferencePresent || p.LibraryNumbering != "season" || p.LibraryPreferenceRevision != 1 {
				t.Fatal("saved absolute changed library preference facts", p)
			}
		})
	})
}
