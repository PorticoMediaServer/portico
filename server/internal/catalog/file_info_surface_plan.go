package catalog

import (
	"context"
	"encoding/binary"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Flat file readers need the directory's GLOBAL group count and split result.
// Compose two existing bounded numeric reducers; no page walk or Options call
// is used to infer them. Their fixed memory is inside the same shared budget.
func fileInfoSurfacePlan(files, groups readprep.Plan) (readprep.Plan, error) {
	if files.New == nil || groups.New == nil || files.InitialBytes < 0 || groups.InitialBytes < 0 || files.InitialBytes > math.MaxInt64-groups.InitialBytes-8192 {
		return readprep.Plan{}, readprep.ErrInvalid
	}
	whole := files.InitialBytes + groups.InitialBytes + 8192
	return readprep.Plan{InitialBytes: whole, New: func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if e := b.Resize(whole); e != nil {
			return nil, e
		}
		child, e := b.WithOverhead(whole - files.InitialBytes)
		if e != nil {
			return nil, e
		}
		work, e := files.New(ctx, child)
		if e != nil {
			return nil, e
		}
		return &fileInfoSurfaceWork{whole: whole, firstBytes: files.InitialBytes, next: groups, work: work}, nil
	}}, nil
}

type fileInfoSurfaceWork struct {
	whole, firstBytes int64
	next              readprep.Plan
	work              readprep.Computation
	first             []byte
	stage             int
}

func (w *fileInfoSurfaceWork) AccountedBytes() int64 { return w.whole }
func (w *fileInfoSurfaceWork) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	if e := b.Resize(w.whole); e != nil {
		return readprep.StepResult{}, e
	}
	bytes := w.firstBytes
	if w.stage != 0 {
		bytes = w.next.InitialBytes
	}
	child, e := b.WithOverhead(w.whole - bytes)
	if e != nil {
		return readprep.StepResult{}, e
	}
	if w.work == nil {
		w.work, e = w.next.New(ctx, child)
		if e != nil {
			return readprep.StepResult{}, e
		}
		return readprep.StepResult{}, nil
	}
	out, e := w.work.Step(ctx, child)
	if e != nil || !out.Done {
		return out, e
	}
	if w.stage == 0 {
		w.first = out.Payload
		w.work = nil
		w.stage = 1
		return readprep.StepResult{}, nil
	}
	summary, e := decodeFileInfoGroupSummary(out.Payload)
	if e != nil {
		return readprep.StepResult{}, e
	}
	if len(w.first) > math.MaxUint32 {
		return readprep.StepResult{}, readprep.ErrInvalid
	}
	payload := make([]byte, 14+len(w.first))
	payload[0] = 0xf1
	binary.BigEndian.PutUint32(payload[1:5], uint32(len(w.first)))
	binary.BigEndian.PutUint64(payload[5:13], uint64(summary.Total))
	if summary.CanSplit {
		payload[13] = 1
	}
	copy(payload[14:], w.first)
	w.first = nil
	w.work = nil
	w.next = readprep.Plan{}
	return readprep.StepResult{Done: true, Payload: payload}, nil
}
func splitFileInfoSurfacePayload(raw []byte) ([]byte, int64, bool, error) {
	if len(raw) == 0 || raw[0] != 0xf1 {
		return raw, 0, false, nil
	}
	if len(raw) < 14 || len(raw)-14 != int(binary.BigEndian.Uint32(raw[1:5])) || raw[13] > 1 {
		return nil, 0, false, readprep.ErrInvalid
	}
	total := int64(binary.BigEndian.Uint64(raw[5:13]))
	if total < 0 {
		return nil, 0, false, readprep.ErrInvalid
	}
	return raw[14:], total, raw[13] == 1, nil
}
