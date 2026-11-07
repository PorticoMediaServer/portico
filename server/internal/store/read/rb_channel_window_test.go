package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func rbChannelPlayableFixture(t *testing.T) (*browseFixture, *SharedChannelReader, *SharedChannelScope) {
	t.Helper()
	f, r := rbChannelFixture(t)
	seedRBChannelPlayable(t, f)
	s, _, _, e := r.OpenSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, rbChannelScopeInput())
	if e != nil {
		t.Fatal(e)
	}
	return f, r, s
}

// BEGIN_RB_CHANNEL_PLAYABLE_JOURNAL
func seedRBChannelPlayable(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, `UPDATE catalog_entities SET playback_origin_incarnation=metadata_incarnation,playback_origin_revision=1 WHERE id=1`)
	f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(1,'home-1',1,'/media/home-1',1000,1,'mkv','h264','aac',1920,1080,120)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1,1,1)`)
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,generation,identity_confirmed) VALUES('binding-source','movies','/media','/media','binding-inc',3,1)`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('binding-object','binding-source','home-1','binding-inc','home-1','object-rev','{}',1000,1)`)
	f.exec(t, `UPDATE catalog_assets SET playback_origin_incarnation=x'02020202020202020202020202020202',playback_origin_revision=1`)
	f.exec(t, `UPDATE catalog_asset_links SET playback_origin_incarnation=x'03030303030303030303030303030303',playback_origin_revision=1`)
	f.exec(t, `UPDATE libraries SET playback_origin_incarnation=x'04040404040404040404040404040404',playback_origin_revision=1 WHERE id='movies'`)
}

// END_RB_CHANNEL_PLAYABLE_JOURNAL

func TestRBChannelWindowWholeBindingAndRawContinuation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r, s := rbChannelPlayableFixture(t)
		in := SharedChannelInput{Scope: s, Kind: 1}
		batch, cost, e := r.SharedChannelWindow(t.Context(), rbChannelRequest(), time.Second, f.output, in)
		if e != nil || len(batch.Items) != 1 || batch.RawEOF || batch.RawFetched != 4 || batch.RawConsumed != 4 || batch.After == nil || batch.After.item != 4 || len(cost.LookupWindows) != 5 || len(cost.PublicationWindows) != 1 {
			t.Fatal(batch, cost, e)
		}
		v := batch.Items[0]
		if v.Item.ID != 1 || v.Asset != 1 || v.Object != "binding-object" || v.Source != "binding-source" || v.SourceRoot != "/media" || v.DurationSeconds != 120 || v.Size != 1000 || v.ModifiedNS != 1 || v.BindingHash == ([32]byte{}) {
			t.Fatal(v)
		}
		validated, _, e := r.RevalidateSharedChannelBatch(t.Context(), rbChannelRequest(), time.Second, f.output, s, batch)
		if e != nil || !reflect.DeepEqual(validated.Items, batch.Items) || validated.Fence != batch.Fence {
			t.Fatal(validated, e)
		}
		batch.Items[0].DurationSeconds++
		bad, _, e := r.RevalidateSharedChannelBatch(t.Context(), rbChannelRequest(), time.Second, f.output, s, batch)
		if !errors.Is(e, ErrStale) || bad.Items != nil {
			t.Fatal("frozen value tamper", bad, e)
		}
		in.After = batch.After
		next, _, e := r.SharedChannelWindow(t.Context(), rbChannelRequest(), time.Second, f.output, in)
		if e != nil || len(next.Items) != 0 || next.RawEOF || next.RawConsumed != 4 || next.After == nil || next.After.item != 8 {
			t.Fatal("rejected prefix is raw progress", next, e)
		}
		in.After = &SharedChannelCursor{fence: batch.Fence, item: 1100}
		last, _, e := r.SharedChannelWindow(t.Context(), rbChannelRequest(), time.Second, f.output, in)
		if e != nil || !last.RawEOF || last.RawConsumed != 0 || last.After != nil {
			t.Fatal("genuine raw EOF", last, e)
		}
	})
}

type rbChannelBoundarySnapshots struct {
	browseSnapshots
	reads, at int
	mutate    func()
}

func (s *rbChannelBoundarySnapshots) Read(ctx context.Context, q governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	result, e := s.browseSnapshots.Read(ctx, q, b, fn)
	s.reads++
	if e == nil && s.reads == s.at {
		s.mutate()
	}
	return result, e
}

func TestRBChannelWindowAtomicPositiveAndNegativePublication(t *testing.T) {
	for _, test := range []struct {
		name, before, mutate string
		after, at            int
	}{
		{"prepared-revision", "", `UPDATE catalog_assets SET prepared_source_revision=prepared_source_revision+1 WHERE id=1`, 0, 5},
		{"source-root", "", `UPDATE library_sources SET root='/changed' WHERE id='binding-source'`, 0, 5},
		{"unavailable-became-playable", `UPDATE catalog_assets SET available=0 WHERE id=1`, `UPDATE catalog_assets SET available=1 WHERE id=1`, 0, 5},
		{"retired-became-live", `UPDATE catalog_entities SET retired=1 WHERE id=1`, `UPDATE catalog_entities SET retired=0 WHERE id=1`, 0, 5},
		{"empty-tail-insert", "", `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(1101,x'ffffffffffffffffffffffffffffffff',1,1,'Tail','tail','t','tail','t',0,x'ffffffffffffffffffffffffffffffff',x'ffffffffffffffffffffffffffffffff')`, 1100, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r, s := rbChannelPlayableFixture(t)
				if test.before != "" {
					f.exec(t, test.before)
				}
				r.snapshots = &rbChannelBoundarySnapshots{browseSnapshots: r.snapshots, at: test.at, mutate: func() {
					if test.after > 0 {
						f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1101,x'ffffffffffffffffffffffffffffffff',1,1)`)
					}
					f.exec(t, test.mutate)
				}}
				in := SharedChannelInput{Scope: s, Kind: 1}
				if test.after > 0 {
					in.After = &SharedChannelCursor{fence: rbChannelFence(s, 1), item: int64(test.after)}
				}
				batch, _, e := r.SharedChannelWindow(t.Context(), rbChannelRequest(), time.Second, f.output, in)
				if !errors.Is(e, ErrStale) || !reflect.DeepEqual(batch, SharedChannelBatch{}) {
					t.Fatal("published stale positive/negative/EOF", batch, e)
				}
			})
		})
	}
}
