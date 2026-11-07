package read

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestRBChannelOwnedCopyCountsPrivateRoots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := rbChannelFixture(t)
		scope, info, _, err := r.OpenSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, rbChannelScopeInput())
		if err != nil {
			t.Fatal(err)
		}
		number := int64(7)
		fence := rbChannelFence(scope, 1)
		cursor := &SharedChannelCursor{fence: fence, item: 1}
		batch := SharedChannelBatch{Fence: fence, Items: []SharedChannelPlayable{{Item: BrowseItem{ID: 1, Title: "電影_é"}, Path: "/media/file", SourceRoot: "/media", SeasonNumber: &number}}, After: cursor, RawFetched: 4, RawConsumed: 4}
		values := SharedChannelRetainedValues{Scope: scope, Info: &info, Batch: &batch, Cursor: cursor}
		before, err := r.MeasureSharedChannelValues(t.Context(), f.output, values)
		if err != nil {
			t.Fatal(err)
		}
		oldRoot := scope.library.root
		scope.library.root = strings.Repeat("界", 40)
		batch.Fence = rbChannelFence(scope, 1)
		cursor.fence = batch.Fence
		after, err := r.MeasureSharedChannelValues(t.Context(), f.output, values)
		if err != nil {
			t.Fatal(err)
		}
		// Scope, batch fence, and the single shared opaque cursor each own one
		// root copy. Use UTF-8 BYTES; cursor aliases must not count a fourth node.
		if after.PayloadBytes-before.PayloadBytes != 3*int64(len(scope.library.root)-len(oldRoot)) {
			t.Fatal("private root size or cursor alias count differs", before, after)
		}
		scope.library.root = oldRoot
		batch.Fence = rbChannelFence(scope, 1)
		cursor.fence = batch.Fence
		if before.ChargeBytes < 2*before.PayloadBytes+4096 || before.ChargeBytes%4096 != 0 {
			t.Fatal("copy envelope", before)
		}
		lease := func(bytes int64, id string) BrowseOutput {
			q := governor.Request{Key: governor.JobKey{Domain: 395, ID: id}, Owner: 1, Class: governor.Background, Need: governor.Amounts{governor.MemoryBytes: bytes}, Deadline: time.Now().Add(time.Hour)}
			d := f.governor.Try(q)
			if d.Grant == nil {
				t.Fatal(d.Refusal)
			}
			t.Cleanup(func() {
				f.governor.Cancel(d.Grant)
				if e := f.authority.Retired(d.Grant); e != nil {
					t.Error(e)
				}
			})
			return BrowseOutput{Authority: f.authority, Grant: d.Grant, Request: q}
		}
		under := lease(before.ChargeBytes-1, "under-copy")
		if owned, e := r.CloneSharedChannelValues(t.Context(), f.output, under, values); !errors.Is(e, governor.ErrSnapshotBudget) || owned.Scope != nil {
			t.Fatal("copied before full reservation", owned, e)
		}
		retained := lease(before.ChargeBytes, "owned-copy")
		usage := f.authority.Usage()
		owned, err := r.CloneSharedChannelValues(t.Context(), f.output, retained, values)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*owned.Batch, batch) || !reflect.DeepEqual(*owned.Info, info) || owned.Scope.issuer != r || owned.Scope.stamp != scope.stamp || owned.Cursor != owned.Batch.After || f.authority.Usage() != usage {
			t.Fatal("copy changed values, issuer, fences or custody")
		}
		for _, pair := range [][2]string{{scope.library.root, owned.Scope.library.root}, {batch.Fence.library.root, owned.Batch.Fence.library.root}, {cursor.fence.library.root, owned.Cursor.fence.library.root}, {scope.input.Library, owned.Scope.input.Library}, {info.RequiredLibraries[0], owned.Info.RequiredLibraries[0]}, {batch.Items[0].Item.Title, owned.Batch.Items[0].Item.Title}, {batch.Items[0].Path, owned.Batch.Items[0].Path}} {
			if len(pair[0]) > 0 && unsafe.StringData(pair[0]) == unsafe.StringData(pair[1]) {
				t.Fatal("retained an original string backing alias")
			}
		}
		if owned.Batch.Items[0].SeasonNumber == &number || owned.Scope == scope || owned.Cursor == cursor {
			t.Fatal("retained an original mutable node")
		}
		number = 99
		batch.Items[0].Item.Title = "changed"
		cursor.item = 99
		if *owned.Batch.Items[0].SeasonNumber != 7 || owned.Batch.Items[0].Item.Title != "電影_é" || owned.Cursor.item != 1 {
			t.Fatal("source mutation changed owned copy")
		}
		if _, e := r.CloneSharedChannelValues(t.Context(), f.output, f.output, values); !errors.Is(e, ErrInput) {
			t.Fatal("reused source output instead of separate retention", e)
		}
	})
}
