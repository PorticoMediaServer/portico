// Package governor provides admission and physical resource custody for the new
// store. It is exercised by harnesses only until the data-layer cutover.
//
// Semantics follow G-INTERFACE v2; durable job transitions belong to typed domain
// adapters through the private writer, never to this transient resource ledger.
package governor

import (
	"context"
	"sync/atomic"
	"time"
)

// ContractVersion identifies the frozen semantic contract; it is not a wire API.
const ContractVersion = "G-INTERFACE-v2"
const ContractSHA256 = "0e4af924bac00a9983af226bb0cdc9c1c7dc6a6c41191137d28c0b2c07601f84"

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
type IOClass uint8

const (
	Sequential IOClass = iota
	Seeking
	NetworkIO
)

type DeviceIO struct {
	DeviceID    string
	Class       IOClass
	Outstanding uint16
	Bytes       int64
}
type UnitBudget struct {
	RawRows uint32
	VMOps   uint64
	Pages   uint32
}
type Request struct {
	RestartRisk WALRestartRisk // optional exact prospective metadata; no independent authority
	Budget      UnitBudget     // prospective legal work, checked by private writer per sub-batch
	IO          []DeviceIO     // exact physical-device slices; HDD outstanding <=1
	Key         JobKey
	Owner       OwnerID
	Class       Class
	Need        Amounts
	Writer      bool
	MaxHold     time.Duration
	Deadline    time.Time
	Footprint   []Scope
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
	Cause   RefusalCause
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
	id            uint64
	epoch         uint64
	issuer        *ledger
	deadline      time.Time // immutable original issuer-captured admission deadline
	cancelled     atomic.Bool
	cancelledDone chan struct{} // immutable notification; Cancel closes exactly once
}
type Governor interface {
	// Supervisors construct requests from private purpose registries. Workers
	// receive only grants, never authority to choose their own class.
	Try(Request) Decision // synchronous, original-deadline bounded lock handoff; no external IO/work buffers
	Cancel(*Grant)        // revokes new work; does NOT return physical capacity
}

// Only private drivers/supervisors hold this interface. Workers cannot attest exit.
// Retirement means Rows/Tx/Conn closed, process Wait joined, buffers and body done.
type Retirer interface{ Retired(*Grant) error }
