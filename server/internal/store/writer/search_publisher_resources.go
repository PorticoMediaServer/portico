package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// The publisher job namespace is the registry's publisher partition without the
// independent reclaimer, which shares the profile field but not the job receipt
// namespace. A new job purpose needs its registry line only.
func searchPublisherPurpose(purpose CommandID) bool {
	return purposeUsesPartition(purpose, publisherPartition) && purpose != SearchFragmentReclaim
}

// Resource eligibility is wider than the publisher-job receipt namespace.
func searchPublisherResourcePurpose(purpose CommandID) bool {
	return searchPublisherPurpose(purpose) || purpose == SearchFragmentReclaim
}

// Compatibility is sampled from the held, policy-verified physical connection,
// never from preparation, a previous Capture or another command's connection.
func (t *commandTurn) searchPublisherEngine(ctx context.Context, purpose CommandID) (out searchpolicy.Engine, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !searchPublisherResourcePurpose(purpose) || ctx == nil || t == nil || t.transaction == nil {
		return out, ErrInvalid
	}
	tx := t.transaction
	if tx.descriptor.ID != purpose || tx.conn == nil || !tx.policyVerified {
		return out, ErrInvalid
	}
	if tx.done || !tx.begun {
		return out, ErrClosed
	}
	defer tx.guardPanic()
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	actual, err := sqliteconn.AttestEngine(ctx, tx.conn)
	t.panicAfter(err)
	if err != nil {
		return out, err
	}
	return searchpolicy.Engine{Version: actual.Version, SourceID: actual.SourceID}, nil
}

func (t *commandTurn) searchPublisherTimeMS(ctx context.Context, purpose CommandID, leaseExpiresMS int64) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !searchPublisherResourcePurpose(purpose) {
		return 0, ErrInvalid
	}
	return t.commandTimeMS(ctx, purpose, leaseExpiresMS)
}

func (t *commandTurn) requireSearchPublisherMemory(ctx context.Context, purpose CommandID, requiredBytes uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if !searchPublisherResourcePurpose(purpose) {
		return ErrInvalid
	}
	return t.requireCommandMemory(ctx, purpose, requiredBytes, "search_publisher_memory_partition")
}

func checkSearchPublisherTerminal(ctx context.Context, turn *commandTurn, purpose CommandID, leaseExpiresMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	_, err = turn.searchPublisherTimeMS(ctx, purpose, leaseExpiresMS)
	return err
}
