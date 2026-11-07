package read

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Optional nodes let MG retain only a cursor, scope/metadata, a source batch or
// validated values. The input is frozen during measurement and copying.
type SharedChannelRetainedValues struct {
	Scope     *SharedChannelScope
	Info      *SharedChannelScopeInfo
	Batch     *SharedChannelBatch
	Cursor    *SharedChannelCursor
	Validated *SharedChannelValidatedBatch
}
type SharedChannelCopySize struct {
	// Exact owned Go value/backing storage: actual UTF-8 bytes, fixed struct and
	// slice storage, and independent nullable values. Includes PRIVATE roots.
	PayloadBytes int64
	// Separately admitted page-aligned allocator/copy envelope. This is a
	// logical reservation, not a claim about allocator RSS or host qualification.
	ChargeBytes int64
}

func (r *SharedChannelReader) channelCopyCustody(ctx context.Context, out BrowseOutput) error {
	if r == nil || ctx == nil || out.Authority != r.a || out.Grant == nil || out.Request.Writer {
		return ErrInput
	}
	for resource, n := range out.Request.Need {
		if resource != int(governor.MemoryBytes) && n != 0 {
			return ErrInput
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if out.Request.Class == governor.Foreground {
		if out.live == nil {
			return ErrInput
		}
		if err := out.live.checkContext(ctx); err != nil {
			return err
		}
	}
	return r.a.CheckContext(ctx, out.Grant, out.Request)
}
func rbChannelStorage[T any]() int64 { return int64(reflect.TypeFor[T]().Size()) }
func rbChannelStringBytes(v reflect.Value) int64 {
	if v.Kind() == reflect.String {
		return int64(v.Len())
	}
	if v.Kind() == reflect.Struct {
		var n int64
		for i := 0; i < v.NumField(); i++ {
			n += rbChannelStringBytes(v.Field(i))
		}
		return n
	}
	if v.Kind() == reflect.Array {
		var n int64
		for i := 0; i < v.Len(); i++ {
			n += rbChannelStringBytes(v.Index(i))
		}
		return n
	}
	// Pointers are explicit graph nodes below. In particular, the immutable
	// issuer is shared composition custody, never a job-owned reader/pool copy.
	return 0
}
func rbChannelItemsBytes(items []SharedChannelPlayable) int64 {
	n := int64(len(items)) * rbChannelStorage[SharedChannelPlayable]()
	for _, item := range items {
		n += rbChannelStringBytes(reflect.ValueOf(item))
		for _, p := range []*int64{item.SeasonNumber, item.EpisodeNumber, item.TrackNumber, item.DiscNumber} {
			if p != nil {
				n += rbChannelStorage[int64]()
			}
		}
	}
	return n
}
func (r *SharedChannelReader) channelCopyShape(values SharedChannelRetainedValues) (SharedChannelCopySize, error) {
	var size SharedChannelCopySize
	if values.Scope == nil || values.Scope.issuer != r {
		return size, ErrInput
	}
	if values.Info != nil && *values.Info != values.Scope.info {
		return size, ErrInput
	}
	checkFence := func(f SharedChannelFence) bool {
		return (f.Kind == 1 || f.Kind == 4) && f == rbChannelFence(values.Scope, f.Kind)
	}
	if values.Batch != nil {
		if len(values.Batch.Items) > rbChannelRaw || !checkFence(values.Batch.Fence) {
			return size, ErrInput
		}
		if values.Batch.After != nil && values.Batch.After.fence != values.Batch.Fence {
			return size, ErrInput
		}
	}
	if values.Validated != nil && (len(values.Validated.Items) > rbChannelRaw || !checkFence(values.Validated.Fence)) {
		return size, ErrInput
	}
	if values.Cursor != nil && !checkFence(values.Cursor.fence) {
		return size, ErrInput
	}
	n := rbChannelStorage[SharedChannelRetainedValues]() + rbChannelStorage[SharedChannelScope]() + rbChannelStringBytes(reflect.ValueOf(*values.Scope))
	if values.Info != nil && values.Info != &values.Scope.info {
		n += rbChannelStorage[SharedChannelScopeInfo]() + rbChannelStringBytes(reflect.ValueOf(*values.Info))
	}
	var batchCursor *SharedChannelCursor
	if values.Batch != nil {
		n += rbChannelStorage[SharedChannelBatch]() + rbChannelStringBytes(reflect.ValueOf(values.Batch.Fence)) + rbChannelItemsBytes(values.Batch.Items)
		batchCursor = values.Batch.After
		if batchCursor != nil {
			n += rbChannelStorage[SharedChannelCursor]() + rbChannelStringBytes(reflect.ValueOf(*batchCursor))
		}
	}
	if values.Cursor != nil && values.Cursor != batchCursor {
		n += rbChannelStorage[SharedChannelCursor]() + rbChannelStringBytes(reflect.ValueOf(*values.Cursor))
	}
	if values.Validated != nil {
		n += rbChannelStorage[SharedChannelValidatedBatch]() + rbChannelStringBytes(reflect.ValueOf(values.Validated.Fence)) + rbChannelItemsBytes(values.Validated.Items)
	}
	if n <= 0 || n > (math.MaxInt64-8191)/2 {
		return size, ErrInput
	}
	size.PayloadBytes = n
	size.ChargeBytes = (2*n + 4096 + 4095) / 4096 * 4096
	return size, nil
}

// MeasureSharedChannelValues counts actual result-owned storage while original
// output custody is live. MG reserves ChargeBytes BEFORE calling Clone. A nil
// optional node allocates nothing; shared cursor/info nodes are counted once.
func (r *SharedChannelReader) MeasureSharedChannelValues(ctx context.Context, out BrowseOutput, values SharedChannelRetainedValues) (SharedChannelCopySize, error) {
	if err := r.channelCopyCustody(ctx, out); err != nil {
		return SharedChannelCopySize{}, err
	}
	size, err := r.channelCopyShape(values)
	if err != nil {
		return SharedChannelCopySize{}, err
	}
	if size.PayloadBytes > out.Request.Need[governor.MemoryBytes] {
		return SharedChannelCopySize{}, governor.ErrSnapshotBudget
	}
	if err = r.channelCopyCustody(ctx, out); err != nil {
		return SharedChannelCopySize{}, err
	}
	return size, nil
}

// CloneSharedChannelValues requires a DISTINCT admitted memory-only grant.
// Neither grant is cancelled/retired here: MG holds the retained grant until all
// copied values/consumers exit. The original output remains held through copy.
func (r *SharedChannelReader) CloneSharedChannelValues(ctx context.Context, out, retained BrowseOutput, values SharedChannelRetainedValues) (SharedChannelRetainedValues, error) {
	var empty SharedChannelRetainedValues
	size, err := r.MeasureSharedChannelValues(ctx, out, values)
	if err != nil {
		return empty, err
	}
	if retained.Authority != r.a || retained.Grant == nil || retained.Grant == out.Grant || retained.Request.Writer || retained.Request.Owner != out.Request.Owner || retained.Request.Class != out.Request.Class {
		return empty, ErrInput
	}
	for resource, n := range retained.Request.Need {
		if resource != int(governor.MemoryBytes) && n != 0 {
			return empty, ErrInput
		}
	}
	if retained.Request.Need[governor.MemoryBytes] < size.ChargeBytes {
		return empty, governor.ErrSnapshotBudget
	}
	if err = r.a.CheckContext(ctx, retained.Grant, retained.Request); err != nil {
		return empty, err
	}
	owned := SharedChannelRetainedValues{Scope: rbCloneChannelScope(values.Scope)}
	if values.Info != nil {
		if values.Info == &values.Scope.info {
			owned.Info = &owned.Scope.info
		} else {
			info := rbCloneChannelInfo(*values.Info)
			owned.Info = &info
		}
	}
	if values.Batch != nil {
		batch := *values.Batch
		batch.Fence = rbCloneChannelFence(batch.Fence)
		batch.Items = rbCloneChannelItems(batch.Items)
		batch.After = rbCloneChannelCursor(batch.After)
		owned.Batch = &batch
	}
	if values.Cursor != nil {
		if values.Batch != nil && values.Cursor == values.Batch.After {
			owned.Cursor = owned.Batch.After
		} else {
			owned.Cursor = rbCloneChannelCursor(values.Cursor)
		}
	}
	if values.Validated != nil {
		v := *values.Validated
		v.Fence = rbCloneChannelFence(v.Fence)
		v.Items = rbCloneChannelItems(v.Items)
		owned.Validated = &v
	}
	if err = errors.Join(r.channelCopyCustody(ctx, out), r.a.CheckContext(ctx, retained.Grant, retained.Request)); err != nil {
		return empty, err
	}
	return owned, nil
}
func rbCloneChannelInfo(info SharedChannelScopeInfo) SharedChannelScopeInfo {
	for _, p := range []*string{&info.Database, &info.Channel, &info.Generation, &info.Rule, &info.Library} {
		*p = strings.Clone(*p)
	}
	for i := range info.RequiredLibraries {
		info.RequiredLibraries[i] = strings.Clone(info.RequiredLibraries[i])
	}
	return info
}
func rbCloneChannelScope(scope *SharedChannelScope) *SharedChannelScope {
	if scope == nil {
		return nil
	}
	v := *scope
	for _, p := range []*string{&v.input.Channel, &v.input.Generation, &v.input.Rule, &v.input.Library, &v.library.root} {
		*p = strings.Clone(*p)
	}
	v.info = rbCloneChannelInfo(v.info)
	return &v
}
func rbCloneChannelFence(f SharedChannelFence) SharedChannelFence {
	for _, p := range []*string{&f.Database, &f.Channel, &f.Generation, &f.Rule, &f.Library, &f.library.root} {
		*p = strings.Clone(*p)
	}
	return f
}
func rbCloneChannelCursor(cursor *SharedChannelCursor) *SharedChannelCursor {
	if cursor == nil {
		return nil
	}
	v := *cursor
	v.fence = rbCloneChannelFence(v.fence)
	return &v
}
func rbCloneChannelItems(items []SharedChannelPlayable) []SharedChannelPlayable {
	if len(items) == 0 {
		return nil
	}
	owned := make([]SharedChannelPlayable, len(items))
	for i, item := range items {
		for _, p := range []*string{&item.Item.Title, &item.Token, &item.Path, &item.Container, &item.Object, &item.ObjectRevision, &item.Source, &item.SourceIncarnation, &item.SourceRoot, &item.SourceConfiguredRoot, &item.Numbering, &item.AiredAt, &item.ReleaseDate, &item.ReleaseOrderKey, &item.TitleOrder, &item.TitleOrderKeepArticles, &item.AddedAt} {
			*p = strings.Clone(*p)
		}
		for _, p := range []**int64{&item.SeasonNumber, &item.EpisodeNumber, &item.TrackNumber, &item.DiscNumber} {
			if *p != nil {
				value := **p
				*p = &value
			}
		}
		owned[i] = item
	}
	return owned
}
