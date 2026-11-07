package read

import (
	"context"
	"errors"
	"reflect"
	"time"

	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

var _ SharedChannelReadPort = (*SharedChannelReader)(nil)

// SharedChannelWindow consumes at most four raw source IDs. Source proof uses
// independently admitted quanta, then ALL positive/negative observations join
// one fresh authorized publication snapshot. No SQL capability escapes to MG.
func (r *SharedChannelReader) SharedChannelWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in SharedChannelInput) (SharedChannelBatch, BrowseReadResult, error) {
	if r == nil {
		return SharedChannelBatch{}, BrowseReadResult{}, ErrInput
	}
	if request.Class != governor.Foreground {
		return r.channelWindow(ctx, request, budget, out, in, nil)
	}
	return liveRead(ctx, request, budget, out, r.pool, time.Now(), func(ctx context.Context, request governor.Request, budget time.Duration, _ time.Time) (SharedChannelBatch, BrowseReadResult, error) {
		return r.channelWindow(ctx, request, budget, out, in, nil)
	})
}

// Revalidation re-reads actual rule membership, bindings and every copied value.
// The resulting snapshot is an observation, not permission for a later write.
// MG must retain/recheck this fence in its accepting publication custody.
func (r *SharedChannelReader) RevalidateSharedChannelBatch(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, scope *SharedChannelScope, frozen SharedChannelBatch) (SharedChannelValidatedBatch, BrowseReadResult, error) {
	if r == nil {
		return SharedChannelValidatedBatch{}, BrowseReadResult{}, ErrInput
	}
	in := SharedChannelInput{Scope: scope, Kind: frozen.Fence.Kind}
	var batch SharedChannelBatch
	var result BrowseReadResult
	var err error
	if request.Class == governor.Foreground {
		batch, result, err = liveRead(ctx, request, budget, out, r.pool, time.Now(), func(ctx context.Context, request governor.Request, budget time.Duration, _ time.Time) (SharedChannelBatch, BrowseReadResult, error) {
			return r.channelWindow(ctx, request, budget, out, in, &frozen)
		})
	} else {
		batch, result, err = r.channelWindow(ctx, request, budget, out, in, &frozen)
	}
	return SharedChannelValidatedBatch{Fence: batch.Fence, Items: batch.Items}, result, err
}

type rbChannelObservation struct {
	meta           rbChannelMeta
	metadataHash   [32]byte
	full           bool
	projectionHash [32]byte
}

func (r *SharedChannelReader) channelWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in SharedChannelInput, frozen *SharedChannelBatch) (batch SharedChannelBatch, result BrowseReadResult, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if in.Scope == nil || in.Scope.issuer != r || in.Kind != 1 && in.Kind != 4 {
		return batch, result, ErrInput
	}
	// Three authority rows, four source rows and four bounded65-row source checks
	// are all charged in the final snapshot, including over-cap refusal paths.
	if request.Budget.RawRows < uint32(3+rbChannelRaw+rbChannelRaw*rbChannelProjectionRaw) {
		return batch, result, ErrInput
	}
	if err = r.custody(ctx, request, out, budget); err != nil {
		return batch, result, err
	}
	scope := in.Scope
	fence := rbChannelFence(scope, in.Kind)
	if in.After != nil && in.After.fence != fence {
		return batch, result, ErrStale
	}
	if frozen != nil && (len(frozen.Items) > rbChannelRaw || !rbChannelSamePublicFence(frozen.Fence, fence)) {
		return batch, result, ErrStale
	}
	deadline := earliest(request.Deadline, out.Request.Deadline, time.Now().Add(min(budget, time.Second)))
	operation, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var observations [rbChannelRaw]rbChannelObservation
	used, raw := 0, 0
	after := int64(0)
	if in.After != nil {
		after = in.After.item
	}
	var rawDigest [32]byte
	retained := int64(65536)
	reserve := func(value any) error {
		n := rbChannelRetainedBytes(value)
		if n > out.Request.Need[governor.MemoryBytes]/2-retained {
			return governor.ErrSnapshotBudget
		}
		retained += n
		return nil
	}
	result.Attempts = 1
	err = r.channelQuantum(operation, request, deadline, out, scope, &result, false, func(q browseQuery, query librarychannels.Query) error {
		if _, e := rbChannelCompileQuery(query, in.Kind); e != nil {
			return e
		}
		var rows [][]any
		if frozen == nil {
			var e error
			rows, e = q("rb.channel.candidates", scope.library.id, in.Kind, after, int64(rbChannelRaw))
			if e != nil {
				return e
			}
			raw = len(rows)
			rawDigest, e = rbChannelRowsDigest(rows)
			if e != nil {
				return e
			}
		} else {
			seen := [rbChannelRaw]int64{}
			for i, value := range frozen.Items {
				if value.Item.ID <= 0 {
					return ErrInput
				}
				for _, id := range seen[:i] {
					if id == value.Item.ID {
						return ErrInput
					}
				}
				seen[i] = value.Item.ID
				found, e := q("rb.channel.item", value.Item.ID, scope.library.id, in.Kind)
				if e != nil {
					return e
				}
				if len(found) != 1 {
					return ErrStale
				}
				rows = append(rows, found[0])
			}
		}
		for _, row := range rows {
			meta, e := rbChannelMetaValue(row, in.Kind)
			if e != nil {
				return e
			}
			if meta.ID <= 0 {
				return ErrUnqualified
			}
			digest, e := rbChannelRowsDigest([][]any{row})
			if e != nil {
				return e
			}
			if e = reserve(meta); e != nil {
				return e
			}
			observations[used] = rbChannelObservation{meta: meta, metadataHash: digest}
			used++
			if frozen == nil {
				batch.After = &SharedChannelCursor{fence: fence, item: meta.ID}
			}
		}
		return nil
	})
	if err != nil {
		return SharedChannelBatch{}, result, err
	}
	var values [rbChannelRaw]SharedChannelPlayable
	kept := 0
	for i := range observations[:used] {
		observation := &observations[i]
		meta := observation.meta
		var value SharedChannelPlayable
		matched := false
		err = r.channelQuantum(operation, request, deadline, out, scope, &result, false, func(q browseQuery, query librarychannels.Query) error {
			program, e := rbChannelCompileQuery(query, in.Kind)
			if e != nil {
				return e
			}
			rows, e := q("rb.channel.item", meta.ID, scope.library.id, in.Kind)
			if e != nil {
				return e
			}
			digest, e := rbChannelRowsDigest(rows)
			if e != nil {
				return e
			}
			if digest != observation.metadataHash {
				return ErrStale
			}
			if !meta.Eligible || !rbChannelRuleMatches(query, program, meta, scope.input.Library, scope.stamp.createdMS) {
				return nil
			}
			proof, bindingErr := readHomeWholePlayableBinding(q, scope.library.id, meta.ID)
			if errors.Is(bindingErr, errHomeBindingBudget) {
				return ErrNotReady
			}
			if bindingErr != nil && !errors.Is(bindingErr, errHomeBindingUnresolved) {
				return bindingErr
			}
			projection, e := q("rb.channel.check", meta.ID, scope.library.id, in.Kind, int64(rbChannelProjectionRaw))
			if e != nil {
				return e
			}
			if len(projection) == 0 {
				return ErrStale
			}
			if len(projection) == rbChannelProjectionRaw {
				return ErrNotReady
			}
			for _, row := range projection {
				if len(row) != 87 {
					return ErrUnqualified
				}
				d, e := rbChannelRowsDigest([][]any{row[:29]})
				if e != nil {
					return e
				}
				if d != observation.metadataHash {
					return ErrStale
				}
			}
			observation.projectionHash, e = rbChannelRowsDigest(projection)
			if e != nil {
				return e
			}
			observation.full = true
			// An unresolved source remains negative only while the WHOLE raw source
			// projection stays unchanged at publication. It cannot become a false EOF.
			if bindingErr != nil {
				return nil
			}
			if meta.Kind == 4 && projection[0][72] != "whole_source" {
				return nil
			}
			if projection[0][38] != proof.Binding.Asset || projection[0][39] != scope.library.id {
				return ErrStale
			}
			objectFound := false
			for _, row := range projection {
				if row[50] == proof.Binding.Object && row[51] == proof.Binding.ObjectRevision && row[52] == proof.Binding.Source && row[62] == proof.Binding.SourceIncarnation && row[63] == proof.Binding.SourceGeneration && row[64] == proof.Binding.SourceRoot && row[65] == proof.Binding.SourceConfiguredRoot {
					objectFound = true
				}
			}
			if !objectFound {
				return ErrStale
			}
			file := projection[0][29:38]
			if e = reserve(proof.Binding); e != nil {
				return e
			}
			if e = reserve(file); e != nil {
				return e
			}
			value, e = rbChannelPlayable(meta, proof.Binding, file, scope)
			matched = e == nil
			return e
		})
		if err != nil {
			return SharedChannelBatch{}, result, err
		}
		if !matched {
			if frozen != nil {
				return SharedChannelBatch{}, result, ErrStale
			}
			continue
		}
		values[kept] = value
		kept++
	}
	if frozen != nil {
		if kept != len(frozen.Items) {
			return SharedChannelBatch{}, result, ErrStale
		}
		for i, value := range values[:kept] {
			if !reflect.DeepEqual(value, frozen.Items[i]) {
				return SharedChannelBatch{}, result, ErrStale
			}
		}
	}
	// One snapshot contains the complete final authority, metadata traversal,
	// positive source bindings AND negative observations. At most eight calls.
	err = r.channelQuantum(operation, request, deadline, out, scope, &result, true, func(q browseQuery, _ librarychannels.Query) error {
		if frozen == nil {
			rows, e := q("rb.channel.candidates", scope.library.id, in.Kind, after, int64(rbChannelRaw))
			if e != nil {
				return e
			}
			digest, e := rbChannelRowsDigest(rows)
			if e != nil {
				return e
			}
			if digest != rawDigest {
				return ErrStale
			}
		}
		for _, observation := range observations[:used] {
			if !observation.full {
				continue
			}
			rows, e := q("rb.channel.check", observation.meta.ID, scope.library.id, in.Kind, int64(rbChannelProjectionRaw))
			if e != nil {
				return e
			}
			if len(rows) == rbChannelProjectionRaw {
				return ErrStale
			}
			digest, e := rbChannelRowsDigest(rows)
			if e != nil {
				return e
			}
			if digest != observation.projectionHash {
				return ErrStale
			}
		}
		return nil
	})
	if err == nil {
		err = errors.Join(operation.Err(), r.a.CheckContext(operation, out.Grant, out.Request))
	}
	if err != nil {
		return SharedChannelBatch{}, result, err
	}
	batch.Fence = fence
	batch.Items = append([]SharedChannelPlayable(nil), values[:kept]...)
	if frozen == nil {
		batch.RawFetched, batch.RawConsumed = raw, raw
		batch.RawEOF = raw < rbChannelRaw
		if batch.RawEOF {
			batch.After = nil
		}
	}
	return batch, result, nil
}
