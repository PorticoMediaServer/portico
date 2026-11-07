package enrich

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// TMDBProducerConfig is trusted composition, not a provider/job memory choice.
// Deadline is the ORIGINAL producer lifecycle; expiry never refunds its memory.
// Missing admission refuses background creation, not startup/local availability.
type TMDBProducerConfig struct {
	Policy    TMDBPolicy
	Owner     governor.OwnerID
	LibraryID int64
	Deadline  time.Time
	HTTP      TMDBHTTPConfig
}

// TMDBConsumer is bound by trusted composition. It must return only after all
// synchronous native use and request copies have ended, and may not retain the
// payload or launch asynchronous work with it. Durable content has its own
// writer budgets. This preparation holding confers no SQL/source authority.
type TMDBConsumer func(context.Context, writer.EvidenceStagePayload) error

// TMDBProducer retains the entire adopted reservation before constructing any
// broker/HTTP pools: cache16MiB + bookkeeping16MiB +48MiB per worker. This fixed
// partition also covers idle/reused buffers and canceled physical acquisition;
// it is not loaned out between items or refunded on clear/abort/ACK/cancel.
// The composition MUST Close it; Close joins physical work before retirement.
type TMDBProducer struct {
	mu                   sync.Mutex
	closeOnce            sync.Once
	closed               bool
	closeErr             error
	active               sync.WaitGroup
	ctx                  context.Context
	cancel               context.CancelFunc
	governor             governor.Governor
	authority            *governor.Authority
	expected             governor.Request
	grant                *governor.Grant
	policy               TMDBPolicy
	credentialGeneration string
	networkBytes         int64
	consume              TMDBConsumer
	transport            *Transport
	workers              chan struct{}
	network              chan int
}

func NewTMDBProducer(ctx context.Context, cfg TMDBProducerConfig, g governor.Governor, a *governor.Authority, wal *governor.WALGate, consume TMDBConsumer) (*TMDBProducer, error) {
	return newTMDBProducer(ctx, cfg, g, a, wal, consume, func(admission NetworkAdmission) (*Transport, error) {
		return NewTMDBHTTPTransport(cfg.Policy, admission, cfg.HTTP)
	})
}

// The transport factory is private mock injection only; production owns the
// fixed-origin HTTP adapter and its credentials. Called AFTER full admission.
func newTMDBProducer(ctx context.Context, cfg TMDBProducerConfig, g governor.Governor, a *governor.Authority, wal *governor.WALGate, consume TMDBConsumer, open func(NetworkAdmission) (*Transport, error)) (out *TMDBProducer, err error) {
	if ctx == nil || ctx.Err() != nil || cfg.Owner == 0 || cfg.LibraryID <= 0 || cfg.Deadline.IsZero() || consume == nil || open == nil || a.VerifyComposition(g, wal) != nil {
		return nil, ErrTMDBPolicy
	}
	limits, err := cfg.Policy.Limits()
	if err != nil {
		return nil, err
	}
	if cfg.HTTP.MaxMetadataBytes <= 0 || cfg.HTTP.MaxMetadataBytes > 16<<20 {
		return nil, ErrTMDBPolicy
	}
	deadline := cfg.Deadline
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if !deadline.After(time.Now()) {
		return nil, context.DeadlineExceeded
	}
	expected := governor.Request{Key: governor.JobKey{Domain: uint16(writer.EvidenceStage), ID: "tmdb-producer"}, Owner: cfg.Owner,
		Class: governor.Background, Need: governor.Amounts{governor.MemoryBytes: limits.MemoryBytes}, Deadline: deadline,
		Footprint: []governor.Scope{{Library: cfg.LibraryID, Domain: uint16(writer.EvidenceStage)}}}
	decision := g.Try(expected)
	if decision.Grant == nil {
		return nil, ErrBusy
	}
	var candidate *TMDBProducer
	defer func() {
		if out == nil {
			if candidate != nil {
				candidate.cancel()
				if candidate.transport != nil {
					candidate.transport.Close()
				}
			}
			g.Cancel(decision.Grant)
			err = errors.Join(err, a.Retired(decision.Grant))
		}
	}()
	if err = a.Check(decision.Grant, expected); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithDeadline(ctx, deadline)
	candidate = &TMDBProducer{ctx: lifetime, cancel: cancel, governor: g, authority: a, expected: expected, grant: decision.Grant,
		policy: cfg.Policy, credentialGeneration: strings.Clone(cfg.HTTP.Authority), networkBytes: limits.ResponseBytes + cfg.HTTP.MaxMetadataBytes, consume: consume,
		workers: make(chan struct{}, limits.Workers), network: make(chan int, limits.Workers)}
	for i := range limits.Workers {
		candidate.network <- i
	}
	candidate.transport, err = open(tmdbNetworkAdmission{candidate})
	if err != nil || candidate.transport == nil {
		return nil, errors.Join(ErrTMDBPolicy, err)
	}
	return candidate, nil
}

// ProcessMovie exposes no payload/retirement handle to a worker. The bound
// consumer runs synchronously while the original whole-profile holding lives.
// Cache hits and canceled acquisitions occupy the SAME bounded worker slots.
func (p *TMDBProducer) ProcessMovie(ctx context.Context, target TMDBMovieTarget) (err error) {
	if p == nil || ctx == nil {
		return ErrTMDBPolicy
	}
	p.mu.Lock()
	if p.closed || p.closeErr != nil {
		p.mu.Unlock()
		return ErrClosed
	}
	select {
	case p.workers <- struct{}{}:
	default:
		p.mu.Unlock()
		return ErrBusy
	}
	p.active.Add(1)
	p.mu.Unlock()
	defer p.active.Done()
	defer func() { <-p.workers }()
	work, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	defer cancel()
	if err = errors.Join(work.Err(), p.ctx.Err()); err != nil {
		return err
	}
	if err = p.authority.Check(p.grant, p.expected); err != nil {
		return err
	}
	key := RequestKey{Provider: "tmdb", Kind: "movie", ExternalID: target.ProviderID, Locale: target.Language,
		Shape: TMDBMovieRequestShape, Authority: p.credentialGeneration}
	acquired, err := p.transport.getDocumentJoined(work, key)
	if err != nil {
		return err
	}
	payload, err := p.prepare(work, target, acquired)
	if err != nil {
		return err
	}
	if err = errors.Join(work.Err(), p.ctx.Err()); err != nil {
		return err
	}
	if err = p.authority.Check(p.grant, p.expected); err != nil {
		return err
	}
	err = p.consume(work, payload)
	return errors.Join(err, work.Err(), p.ctx.Err())
}

func (p *TMDBProducer) prepare(ctx context.Context, target TMDBMovieTarget, acquired AcquisitionDocument) (out writer.EvidenceStagePayload, err error) {
	if err = errors.Join(ctx.Err(), p.ctx.Err()); err != nil {
		return out, err
	}
	if err = p.authority.Check(p.grant, p.expected); err != nil {
		return out, err
	}
	r := p.expected
	r.Key.ID = "tmdb-prepare"
	r.Need = governor.Amounts{governor.CPUMilli: 1000}
	if d, ok := ctx.Deadline(); ok && d.Before(r.Deadline) {
		r.Deadline = d
	}
	decision := p.governor.Try(r)
	if decision.Grant == nil {
		return out, ErrBusy
	}
	defer func() {
		p.governor.Cancel(decision.Grant)
		err = errors.Join(err, p.authority.Retired(decision.Grant), ctx.Err())
		if err != nil {
			out = writer.EvidenceStagePayload{}
		}
	}()
	if err = p.authority.Check(decision.Grant, r); err != nil {
		return out, err
	}
	return PrepareAcquiredTMDBMovie(p.policy, target, acquired)
}

// The retirement bridge is private to composition, absent from the producer's
// worker API. Memory is already inside the whole-profile holding.
type tmdbNetworkAdmission struct{ owner *TMDBProducer }

func (a tmdbNetworkAdmission) Try(ctx context.Context, bytes int64) (func(), error) {
	return a.owner.tryNetwork(ctx, bytes)
}

func (p *TMDBProducer) tryNetwork(ctx context.Context, bytes int64) (func(), error) {
	if ctx == nil || ctx.Err() != nil || p.ctx.Err() != nil || bytes != p.networkBytes {
		return nil, ErrBusy
	}
	if err := p.authority.Check(p.grant, p.expected); err != nil {
		return nil, err
	}
	var slot int
	select {
	case slot = <-p.network:
	default:
		return nil, ErrBusy
	}
	r := p.expected
	r.Key.ID = "tmdb-acquisition"
	r.Need = governor.Amounts{governor.IOBytes: bytes}
	r.IO = []governor.DeviceIO{{DeviceID: [2]string{"tmdb-network-0", "tmdb-network-1"}[slot], Class: governor.NetworkIO, Outstanding: 1, Bytes: bytes}}
	if d, ok := ctx.Deadline(); ok && d.Before(r.Deadline) {
		r.Deadline = d
	}
	decision := p.governor.Try(r)
	if decision.Grant == nil {
		p.network <- slot
		return nil, ErrBusy
	}
	if err := p.authority.Check(decision.Grant, r); err != nil {
		p.governor.Cancel(decision.Grant)
		err = errors.Join(err, p.authority.Retired(decision.Grant))
		p.network <- slot
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			p.governor.Cancel(decision.Grant)
			err := p.authority.Retired(decision.Grant)
			p.mu.Lock()
			p.closeErr = errors.Join(p.closeErr, err)
			p.mu.Unlock()
			p.network <- slot
		})
	}, nil
}

// Close revokes new work, joins physical HTTP and the bound native consumers,
// drops all owned cache/selected references, then retires the original memory.
// A canceled context or stalled consumer never authorizes an early refund.
func (p *TMDBProducer) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		p.cancel()
		p.transport.Close()
		p.active.Wait()
		p.transport = nil
		p.consume = nil
		p.credentialGeneration = ""
		p.governor.Cancel(p.grant)
		err := p.authority.Retired(p.grant)
		p.mu.Lock()
		p.closeErr = errors.Join(p.closeErr, err)
		p.mu.Unlock()
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeErr
}
