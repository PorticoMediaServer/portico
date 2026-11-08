package read

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// mgGuideBoundary mutates the world after the first guide business window so
// the fresh publication fence is exercised. It forwards the reader's raised
// statement forecast, unlike a plain browse boundary.
type mgGuideBoundary struct {
	*governor.Snapshots
	phase         int
	afterBusiness func()
}

func (s *mgGuideBoundary) Read(ctx context.Context, request governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	return s.ReadForecasted(ctx, request, budget, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: 8}, read)
}

func (s *mgGuideBoundary) ReadForecasted(ctx context.Context, request governor.Request, budget time.Duration, forecast governor.SnapshotForecast, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.phase++
	phase := s.phase
	return s.Snapshots.ReadForecasted(ctx, request, budget, forecast, func(scope *governor.ReadScope) error {
		err := read(scope)
		if phase == 1 && s.afterBusiness != nil {
			s.afterBusiness()
		}
		return err
	})
}

func TestMGGuideCommittedGenerationAndCompleteInterval(t *testing.T) {
	f, r := mgGuideFixture(t)
	// A staging import is invisible to the committed guide, even while the
	// writer holds its own uncommitted work.
	f.exec(t, "BEGIN IMMEDIATE")
	defer f.conn.ExecContext(context.Background(), "ROLLBACK")
	f.exec(t, `UPDATE live_operations SET input_hash='staged' WHERE request_id='mg-import'`)
	page, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, mgGuideInput(f, 3))
	if err != nil {
		t.Fatal(err)
	}
	if page.Source.Generation != "mg-old" || page.Source.Revision != 1 || !page.Exhausted || page.Next != nil || len(page.Channels) != 3 || page.Programmes != 4 {
		t.Fatalf("committed guide %+v", page)
	}
	channel := page.Channels[0]
	if channel.ID != "mg-channel" || channel.Name != "Télé 📺" || channel.Number != "1" || channel.Group != "News" || !channel.HasLogo || channel.LogoURL != "https://example.invalid/logo" {
		t.Fatalf("channel %+v", channel)
	}
	var ids []string
	for _, p := range channel.Programmes {
		ids = append(ids, p.ID)
	}
	// Half-open overlap: the row ending exactly at the start and the row
	// starting exactly at the end are excluded; ties order by id.
	if !reflect.DeepEqual(ids, []string{"mg-overlap", "mg-a", "mg-b", "mg-c"}) {
		t.Fatalf("interval membership/order %v", ids)
	}
	first := channel.Programmes[1]
	if first.Title != "Émission 📺" || first.SeriesID != "series" || first.EpisodeID != "episode" || first.NewEvidence != "new" || first.Description != "Résumé 漢字" || first.Facts != `{"rating":"TV-G"}` || !first.HasMetadata || !first.HasIcon || first.IconURL != "https://example.invalid/icon" {
		t.Fatalf("hydrated programme %+v", first)
	}
	plain := channel.Programmes[2]
	if plain.Facts != "{}" || plain.HasMetadata || plain.HasIcon || plain.IconURL != "" || plain.Title != "Tie one" {
		t.Fatalf("defaulted programme %+v", plain)
	}
	if page.Channels[1].ID != "mg-channel-1" || len(page.Channels[1].Programmes) != 0 || page.Channels[2].ID != "mg-channel-2" {
		t.Fatalf("empty interval channels %+v", page.Channels[1:])
	}
}

func TestMGGuideChannelKeysetAndHeadReplacement(t *testing.T) {
	f, r := mgGuideFixture(t)
	in := mgGuideInput(f, 2)
	page, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(page.Channels) != 2 || page.Exhausted || page.Next == nil || page.Channels[0].Position != 0 || page.Channels[1].Position != 1 {
		t.Fatalf("first keyset page %+v err%v", page, err)
	}
	in.After = page.Next
	next, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(next.Channels) != 1 || next.Channels[0].Position != 2 || !next.Exhausted || next.Next != nil {
		t.Fatalf("continuation page %+v err%v", next, err)
	}

	// Continue an old-generation anchor after the source head was replaced: the
	// continuation is stale, never silently re-anchored into the new generation.
	f, r = mgGuideFixture(t)
	page, _, err = r.page(t.Context(), foregroundRequest(), time.Second, f.output, mgGuideInput(f, 2))
	if err != nil || page.Next == nil {
		t.Fatalf("anchor %+v err%v", page, err)
	}
	f.exec(t, `UPDATE live_sources SET active_generation='mg-new',published_at='2026-11-07T01:00:00Z' WHERE id='mg-source';
UPDATE live_operations SET status='published',result_state='active',result_revision=2 WHERE request_id='mg-import'`)
	in = mgGuideInput(f, 2)
	in.After = page.Next
	stale, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(stale, MGGuidePage{}) {
		t.Fatalf("continuation across replacement %+v err%v", stale, err)
	}
	fresh, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, mgGuideInput(f, 3))
	if err != nil || fresh.Source.Generation != "mg-new" || len(fresh.Channels) != 1 || fresh.Channels[0].Name != "New channel" || len(fresh.Channels[0].Programmes) != 1 || fresh.Channels[0].Programmes[0].Title != "Replacement" {
		t.Fatalf("published generation %+v err%v", fresh, err)
	}
}

func TestMGGuideDenialAndRevocation(t *testing.T) {
	for _, tc := range []struct {
		name, mutation, source string
		denied                 bool
	}{
		{name: "revoked", mutation: `UPDATE authorization_session_families SET revoked=1`},
		{name: "token-retired", mutation: `UPDATE authorization_family_tokens SET retired=1`},
		{name: "library-not-all", mutation: `UPDATE direct_memberships SET allowed_libraries='["movies"]'`},
		{name: "owner-only", mutation: `UPDATE live_source_identities SET settings_viewer_access='owner-only'`},
		{name: "disabled", mutation: `UPDATE live_sources SET state='disabled'`},
		{name: "removing", mutation: `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='remove',removal_revision=1,removal_removed_at='2026-11-07T00:00:00Z'`},
		{name: "blocked-labels", mutation: `INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('p','["kids"]')`, source: ""},
		{name: "missing-source", source: "mg-missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mgGuideFixture(t)
			if tc.mutation != "" {
				f.exec(t, tc.mutation)
			}
			in := mgGuideInput(f, 3)
			if tc.source != "" {
				in.Source = tc.source
			}
			page, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, in)
			want := ErrDenied
			if tc.name == "blocked-labels" {
				want = ErrUnqualified
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(page, MGGuidePage{}) {
				t.Fatalf("denial %+v err%v", page, err)
			}
		})
	}
	// Owner authority sees an owner-only source without widening anyone else.
	f, r := mgGuideFixture(t)
	f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
	f.exec(t, `UPDATE live_source_identities SET settings_viewer_access='owner-only'`)
	if page, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, mgGuideInput(f, 3)); err != nil || page.Source.Generation != "mg-old" {
		t.Fatalf("owner guide %+v err%v", page, err)
	}
}

func TestMGGuidePublicationFence(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		want           error
		attempts       int
		revision       int64
	}{
		{name: "head", mutation: `UPDATE live_sources SET revision=2 WHERE id='mg-source'`, attempts: 2, revision: 2},
		{name: "rating", mutation: `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',7,1)`, want: ErrDenied},
		{name: "revoked", mutation: `UPDATE authorization_session_families SET revoked=1`, want: ErrDenied},
		{name: "viewer-library", mutation: `UPDATE direct_memberships SET allowed_libraries='["movies"]'`, want: ErrDenied},
		{name: "replaced", mutation: `UPDATE live_sources SET active_generation='mg-new' WHERE id='mg-source'`, attempts: 2, revision: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mgGuideFixture(t)
			r.snapshots = &mgGuideBoundary{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, tc.mutation) }}
			page, cost, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, mgGuideInput(f, 3))
			if tc.want != nil {
				if !errors.Is(err, tc.want) || !reflect.DeepEqual(page, MGGuidePage{}) {
					t.Fatalf("publication denial %+v err%v", page, err)
				}
				return
			}
			if err != nil || cost.Attempts != tc.attempts || page.Source.Revision != tc.revision {
				t.Fatalf("publication retry %+v attempts%d err%v", page, cost.Attempts, err)
			}
			if tc.name == "replaced" && page.Source.Generation != "mg-new" {
				t.Fatalf("replacement generation %+v", page.Source)
			}
		})
	}
	// Cancellation during the business window returns ZERO page, never a draft.
	f, r := mgGuideFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	r.snapshots = &mgGuideBoundary{Snapshots: f.snapshots, afterBusiness: cancel}
	page, _, err := r.page(ctx, foregroundRequest(), time.Second, f.output, mgGuideInput(f, 3))
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(page, MGGuidePage{}) {
		t.Fatalf("cancel %+v err%v", page, err)
	}
}

func TestMGGuideByteAndCapacityRefusal(t *testing.T) {
	t.Run("byte-overflow", func(t *testing.T) {
		f, r := mgGuideFixture(t)
		f.exec(t, `UPDATE live_channel_versions SET name=? WHERE generation_id='mg-old' AND channel_id='mg-channel'`, strings.Repeat("x", 70000))
		page, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, mgGuideInput(f, 3))
		if !errors.Is(err, governor.ErrSnapshotBudget) || !reflect.DeepEqual(page, MGGuidePage{}) {
			t.Fatalf("byte overflow %+v err%v", page, err)
		}
	})
	t.Run("programme-capacity", func(t *testing.T) {
		f, r := mgGuideFixture(t)
		f.exec(t, "BEGIN")
		for i := 0; i < 1001; i++ {
			f.exec(t, `INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES('mg-old',?,'mg-channel',?,'Bulk','2026-11-07T02:00:00Z','2026-11-07T02:30:00Z','bulk')`, fmt.Sprintf("bulk-%04d", i), fmt.Sprintf("bulk-key-%04d", i))
		}
		f.exec(t, "COMMIT")
		page, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, mgGuideInput(f, 1))
		if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, MGGuidePage{}) {
			t.Fatalf("capacity overflow %+v err%v", page, err)
		}
	})
	t.Run("input", func(t *testing.T) {
		f, r := mgGuideFixture(t)
		for _, mutate := range []func(*MGGuideInput){
			func(in *MGGuideInput) { in.Start = "yesterday" },
			func(in *MGGuideInput) { in.End = in.Start },
			func(in *MGGuideInput) { in.End = "2026-11-30T00:00:00Z" },
			func(in *MGGuideInput) { in.Source = "" },
			func(in *MGGuideInput) { in.View.Limit = 0 },
			func(in *MGGuideInput) { in.Start = "2026-11-07T00:00:00.5Z" },
		} {
			in := mgGuideInput(f, 3)
			mutate(&in)
			page, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if !errors.Is(err, ErrInput) || !reflect.DeepEqual(page, MGGuidePage{}) {
				t.Fatalf("input %+v err%v", page, err)
			}
		}
	})
}

func TestMGGuideLiveConsumer(t *testing.T) {
	f := composeReadFixture(t, mgGuideSource(t), MGGuideHarnessDefinitions(), readfixture.LiveConfig(time.Now), false, mgGuidePlanHash(t))
	r, err := NewMGGuideReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	q, request, output := liveRequests("mg-guide-live")
	consumed := false
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, err := r.Channels(ctx, request, time.Second, out, mgGuideInput(f, 3))
		if err != nil {
			return err
		}
		if page.Source.Generation != "mg-old" || len(page.Channels) != 3 || page.Programmes != 4 {
			t.Fatalf("live guide %+v", page)
		}
		consumed = true
		return nil
	})
	if err != nil || !consumed {
		t.Fatal("live consumer", err)
	}
}
