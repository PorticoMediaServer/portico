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

// WithValues keeps one frozen read graph through a synchronous consumer.
// The consumer may retire source output after entry, must keep the graph frozen,
// and must finish every use before returning. Parent composition owns candidate
// staging, receipts and uncertain COMMIT.
func (d *Driver) WithValues(ctx context.Context, source read.BrowseOutput, values read.SharedChannelRetainedValues, consume func(context.Context, read.SharedChannelRetainedValues) error) (size read.SharedChannelCopySize, err error) {
	if consume == nil {
		return size, ErrInput
	}
	frozen, err := d.Freeze(ctx, source, values)
	if err != nil {
		return size, err
	}
	defer func() {
		err = errors.Join(err, frozen.Close(context.WithoutCancel(ctx)))
		if err != nil {
			size = read.SharedChannelCopySize{}
		}
	}()
	if err = frozen.With(ctx, consume); err != nil {
		return size, err
	}
	return frozen.Size(), nil
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
