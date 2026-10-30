package mediaanalysis

import (
	"context"
	"database/sql"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// ReadFacts are private physical lookup preconditions, never a playback grant.
// Only a real scan/segment invocation can supply the sealed accepting checker.
type ReadFacts struct {
	Source                                    catalog.LibrarySource         `json:"-"`
	Consent                                   catalog.SourceAnalysisConsent `json:"-"`
	ObjectID, ObjectRevision, ItemID, AssetID string                        `json:"-"`
	Size, ModifiedNS                          int64                         `json:"-"`
}

type readContinuation struct {
	check func(context.Context, *sql.Tx) error
	close func() error
}

// ReadInvocation retains exact analysis authority through byte IO, decoder exit
// and publication. Its construction and checker are private to actual work.
type ReadInvocation struct {
	facts  ReadFacts
	db     *sql.DB
	life   context.Context
	cancel context.CancelFunc
	check  func(context.Context, *sql.Tx) error
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	inputs []readContinuation
}

type readInvocationKey struct{}

func (s *Service) readContext(ctx context.Context, facts ReadFacts, check func(context.Context, *sql.Tx) error) (context.Context, *ReadInvocation) {
	life, cancel := context.WithCancel(ctx)
	i := &ReadInvocation{facts: facts, db: s.db, life: life, cancel: cancel, check: check, done: make(chan struct{})}
	context.AfterFunc(life, func() { _ = i.Close() })
	return context.WithValue(life, readInvocationKey{}, i), i
}

func ReadInvocationFromContext(ctx context.Context) (*ReadInvocation, bool) {
	if ctx == nil {
		return nil, false
	}
	i, ok := ctx.Value(readInvocationKey{}).(*ReadInvocation)
	return i, ok && i != nil && i.check != nil
}
func (i *ReadInvocation) Facts() ReadFacts          { return i.facts }
func (i *ReadInvocation) Lifetime() context.Context { return i.life }

// Retire cancels only this original analysis; it can grant no replacement work.
func (i *ReadInvocation) Retire() {
	if i.cancel != nil {
		i.cancel()
	}
}

func (i *ReadInvocation) CheckTx(ctx context.Context, tx *sql.Tx) (err error) {
	defer func() {
		if err != nil {
			i.Retire()
		}
	}()
	if ctx == nil || tx == nil || i.check == nil {
		return ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	i.mu.Lock()
	closed := i.closed
	i.mu.Unlock()
	if closed || i.life.Err() != nil {
		return context.Canceled
	}
	if err := catalog.CheckSourceAnalysisConsentTx(ctx, tx, i.facts.Consent); err != nil {
		return err
	}
	if err := i.check(ctx, tx); err != nil {
		return err
	}
	return i.life.Err()
}

// Retain must succeed before provider IO. Its checker is restrictive and stays
// alive after a decoder input closes, until final publication/owned cleanup.
func (i *ReadInvocation) Retain(check func(context.Context, *sql.Tx) error, close func() error) error {
	if check == nil || close == nil {
		return ErrUnsupported
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.life.Err() != nil {
		return context.Canceled
	}
	if len(i.inputs) >= 8 {
		return ErrBudget
	}
	i.inputs = append(i.inputs, readContinuation{check, close})
	return nil
}

func (i *ReadInvocation) checkRetainedTx(ctx context.Context, tx *sql.Tx) (err error) {
	defer func() {
		if err != nil {
			i.Retire()
		}
	}()
	if err := i.CheckTx(ctx, tx); err != nil {
		return err
	}
	i.mu.Lock()
	inputs := append([]readContinuation(nil), i.inputs...)
	i.mu.Unlock()
	for _, input := range inputs {
		if err := input.check(ctx, tx); err != nil {
			return err
		}
	}
	return i.life.Err()
}
func (i *ReadInvocation) Check(ctx context.Context) error {
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return i.checkRetainedTx(ctx, tx)
	}
	view, err := dbwork.BeginSnapshot(ctx, i.db)
	if err != nil {
		return err
	}
	defer view.Rollback()
	return i.checkRetainedTx(ctx, view.Tx())
}
func (i *ReadInvocation) Close() error {
	i.mu.Lock()
	if i.closed {
		done := i.done
		i.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	i.closed = true
	inputs := i.inputs
	i.inputs = nil
	i.mu.Unlock()
	if i.done != nil {
		defer close(i.done)
	}
	i.Retire()
	var first error
	for _, input := range inputs {
		if err := input.close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func checkReadPublicationTx(ctx context.Context, tx *sql.Tx) error {
	if i, ok := ReadInvocationFromContext(ctx); ok {
		return i.checkRetainedTx(ctx, tx)
	}
	return nil
}

type authorityInput struct {
	Input
	invocation *ReadInvocation
}

func (i *authorityInput) Validate(ctx context.Context) error {
	if err := i.invocation.Check(ctx); err != nil {
		return err
	}
	if err := i.Input.Validate(ctx); err != nil {
		return err
	}
	return i.invocation.Check(ctx)
}
func (i *authorityInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if err := i.invocation.Check(ctx); err != nil {
		return nil, err
	}
	data, err := i.Input.ReadExtent(ctx, offset, length)
	if err != nil {
		return nil, err
	}
	if err = i.invocation.Check(ctx); err != nil {
		return nil, err
	}
	return data, nil
}
