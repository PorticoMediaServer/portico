// Package governor is an executable phase-0 contract, not product code.
package governor

import (
	"context"
	"time"
)

type JobKey struct {
	Domain uint16
	ID     string
}
type OwnerID uint64
type Generation uint64
type ReceiptID string

// Lease is durable row ownership. It is not a resource reservation or authority.
type Lease struct {
	Fence      SourceFence // immutable captured source witness; Renew cannot rebase it
	Key        JobKey
	Generation Generation
	Owner      OwnerID
	ExpiresAt  time.Time
}
type Job[C any] struct {
	Key         JobKey
	Revision    uint64
	Lease       Lease
	Attempts    uint32
	NextAttempt time.Time
	Cursor      C
}
type SourceFence struct {
	SourceID    string
	Incarnation string
	Revision    string
}
type Ready struct {
	Key       JobKey
	Revision  uint64
	NotBefore time.Time
	Fence     SourceFence
	Stage     Request
}
type ReadyQuery struct {
	After JobKey
	Limit uint16
}
type Outcome uint8

const (
	Continued Outcome = iota
	Applied
	Retry
	Superseded
	PermanentFailure
)

type Advance[C any] struct {
	ExpectedRevision uint64
	Fence            SourceFence
	Expected         C
	Next             C
	Outcome          Outcome
	NextAttempt      time.Time
	Receipt          ReceiptID
}

// Implementations are concrete per-domain adapters with typed cursors and payloads.
// Every transition uses the private writer; no generic JSON/payload table is implied.
type Jobs[C any] interface {
	Ready(context.Context, *Grant, ReadyQuery) ([]Ready, error)
	Claim(context.Context, *Grant, Ready, OwnerID, time.Duration) (Job[C], error)
	Renew(context.Context, *Grant, Lease, time.Duration) (Lease, error)
	Advance(context.Context, *Grant, Lease, Advance[C]) (Job[C], error)
}

type Class uint8

const (
	Recovery Class = iota
	Foreground
	ScanMinimum
	Background
)

type Resource uint8

const (
	Reader Resource = iota
	Helper
	MemoryBytes
	CPUMilli
	IOBytes
	ScratchBytes
	WALBytes
	QueueBytes
	ResourceCount
)

type Amounts [ResourceCount]int64
type Scope struct {
	Library int64
	Domain  uint16
	Source  int64
} // zero = wildcard
// Volume/device quantities use qualified physical resource IDs in production.
type Request struct {
	Key       JobKey
	Owner     OwnerID
	Class     Class
	Need      Amounts
	Writer    bool
	MaxHold   time.Duration
	Deadline  time.Time
	Footprint []Scope
}
type RefusalCode uint8

const (
	Capacity RefusalCode = iota + 1
	Deadline
	Unqualified
	Conflict
	FailSafe
	Invalid
)

type Refusal struct {
	Code    RefusalCode
	RetryAt time.Time
}
type Decision struct {
	Grant   *Grant
	Refusal *Refusal
} // exactly one set
// Unexported identity/epoch: only the private governor can mint grants. Caller
// context/priority strings, a Lease, and a readiness hint cannot construct one.
type Grant struct {
	id    uint64
	epoch uint64
}
type Governor interface {
	Try(Request) Decision // synchronous, bounded, no waiting and no allocation/IO
	Cancel(*Grant)        // revokes new work; does NOT return physical capacity
}

// Only private drivers/supervisors hold this interface. Workers cannot attest exit.
// Retirement means Rows/Tx/Conn closed, process Wait joined, buffers and body done.
type Retirer interface{ Retired(*Grant) error }
