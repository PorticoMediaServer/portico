// Package lcbuild composes MG's background schedule producer with RB's closed
// read port. It is separate from writer; catalogue reads and opaque read proofs
// remain RB-owned. No product factory or qualified workload is installed here.
package lcbuild

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

var ErrInput = errors.New("lcbuild: invalid retained-read composition")

type AdmissionRefusal struct{ Refusal governor.Refusal }

func (e *AdmissionRefusal) Error() string {
	return fmt.Sprintf("lcbuild: retained memory refused (%d)", e.Refusal.Code)
}

type channelPort interface {
	OpenSharedChannelScope(context.Context, governor.Request, time.Duration, read.BrowseOutput, read.SharedChannelScopeInput) (*read.SharedChannelScope, read.SharedChannelScopeInfo, read.BrowseReadResult, error)
	SharedChannelWindow(context.Context, governor.Request, time.Duration, read.BrowseOutput, read.SharedChannelInput) (read.SharedChannelBatch, read.BrowseReadResult, error)
	MeasureSharedChannelValues(context.Context, read.BrowseOutput, read.SharedChannelRetainedValues) (read.SharedChannelCopySize, error)
	CloneSharedChannelValues(context.Context, read.BrowseOutput, read.BrowseOutput, read.SharedChannelRetainedValues) (read.SharedChannelRetainedValues, error)
}

// WithScope opens RB's actual stored channel/generation scope and retains its
// copied values through the consumer. No viewer, config bytes or query supplied
// by the worker can replace the authority that RB reads from native storage.
func (d *Driver) WithScope(ctx context.Context, request governor.Request, budget time.Duration, source read.BrowseOutput, in read.SharedChannelScopeInput, consume func(context.Context, read.SharedChannelRetainedValues) error) (read.BrowseReadResult, read.SharedChannelCopySize, error) {
	if d == nil || ctx == nil || d.port == nil || !d.authority.Owns(d.governor) || consume == nil || request.Class != governor.Background || request.Owner != d.owner || in.Generation != d.generation {
		return read.BrowseReadResult{}, read.SharedChannelCopySize{}, ErrInput
	}
	scope, info, cost, err := d.port.OpenSharedChannelScope(ctx, request, budget, source, in)
	if err != nil {
		return cost, read.SharedChannelCopySize{}, err
	}
	size, err := d.WithValues(ctx, source, read.SharedChannelRetainedValues{Scope: scope, Info: &info}, consume)
	return cost, size, err
}

// Driver belongs to trusted composition, not the candidate worker. Its job key
// is an already-owned immutable generation ID. Only the concrete RB reader is
// accepted by the public constructor; workers cannot select a read provider,
// resource class, retirement authority or SQL callback.
type Driver struct {
	port       channelPort
	governor   governor.Governor
	authority  *governor.Authority
	generation string
	owner      governor.OwnerID
}

func NewDriver(reader *read.SharedChannelReader, g governor.Governor, a *governor.Authority, generation string, owner governor.OwnerID) (*Driver, error) {
	if reader == nil || !a.Owns(g) || generation == "" || len(generation) > 128 || !utf8.ValidString(generation) || strings.ContainsAny(generation, "\x00\r\n") || owner == 0 {
		return nil, ErrInput
	}
	return &Driver{reader, g, a, strings.Clone(generation), owner}, nil
}

// WithValues measures before allocating a retained copy, admits DISTINCT actual
// memory custody, and keeps it through the synchronous consumer's last use.
// The consumer must not retain aliases after returning. It may release the
// original source output once invoked: the owned copy is complete at that point.
// A cancelled context refuses use but does not refund buffers still in use.
// Parent composition owns candidate staging, receipts and uncertain COMMIT.
func (d *Driver) WithValues(ctx context.Context, source read.BrowseOutput, values read.SharedChannelRetainedValues, consume func(context.Context, read.SharedChannelRetainedValues) error) (size read.SharedChannelCopySize, err error) {
	if d == nil || ctx == nil || d.port == nil || !d.authority.Owns(d.governor) || consume == nil || source.Authority != d.authority || source.Request.Owner != d.owner || source.Request.Class != governor.Background || source.Request.Writer || values.Info == nil || values.Info.Generation != d.generation {
		return size, ErrInput
	}
	if err = d.authority.CheckContext(ctx, source.Grant, source.Request); err != nil {
		return size, err
	}
	measured, err := d.port.MeasureSharedChannelValues(ctx, source, values)
	if err != nil {
		return size, err
	}
	if measured.PayloadBytes <= 0 || measured.ChargeBytes < measured.PayloadBytes {
		return size, ErrInput
	}
	deadline := source.Request.Deadline
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	request := governor.Request{Key: governor.JobKey{Domain: 860, ID: d.generation}, Owner: d.owner, Class: governor.Background, Deadline: deadline,
		Need: governor.Amounts{governor.MemoryBytes: measured.ChargeBytes}}
	decision := d.governor.Try(request)
	if decision.Grant == nil {
		if decision.Refusal == nil {
			return size, ErrInput
		}
		return size, &AdmissionRefusal{*decision.Refusal}
	}
	retained := read.BrowseOutput{Authority: d.authority, Grant: decision.Grant, Request: request}
	var owned read.SharedChannelRetainedValues
	defer func() {
		// This wrapper has observed consumer return (also on panic). Discard
		// its last usable references before attesting memory retirement.
		owned = read.SharedChannelRetainedValues{}
		d.governor.Cancel(retained.Grant)
		err = errors.Join(err, d.authority.Retired(retained.Grant))
		if err != nil {
			size = read.SharedChannelCopySize{}
		}
	}()
	owned, err = d.port.CloneSharedChannelValues(ctx, source, retained, values)
	if err != nil {
		return size, err
	}
	if err = d.authority.CheckContext(ctx, retained.Grant, request); err != nil {
		return size, err
	}
	if err = consume(ctx, owned); err != nil {
		return size, err
	}
	if err = d.authority.CheckContext(ctx, retained.Grant, request); err != nil {
		return size, err
	}
	return measured, nil
}

// WithWindow consumes exactly one RB window. Raw EOF, rejected raw work and the
// opaque continuation are preserved by RB's clone, never reconstructed from a
// count of eligible Items. An empty non-EOF window remains pending work.
func (d *Driver) WithWindow(ctx context.Context, request governor.Request, budget time.Duration, source read.BrowseOutput, in read.SharedChannelInput, info read.SharedChannelScopeInfo, consume func(context.Context, read.SharedChannelRetainedValues) error) (read.BrowseReadResult, read.SharedChannelCopySize, error) {
	if d == nil || ctx == nil || d.port == nil || !d.authority.Owns(d.governor) || consume == nil || request.Class != governor.Background || request.Owner != d.owner || info.Generation != d.generation {
		return read.BrowseReadResult{}, read.SharedChannelCopySize{}, ErrInput
	}
	batch, cost, err := d.port.SharedChannelWindow(ctx, request, budget, source, in)
	if err != nil {
		return cost, read.SharedChannelCopySize{}, err
	}
	size, err := d.WithValues(ctx, source, read.SharedChannelRetainedValues{Scope: in.Scope, Info: &info, Batch: &batch, Cursor: batch.After}, consume)
	return cost, size, err
}
