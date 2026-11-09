// Package mutationreceipt defines the public mutation receipt protocol.
// It has no database, driver registration, worker or application dependencies.
// Authority and persisted retirement fences are checked by the owning store.
package mutationreceipt

import (
	"errors"
	"strconv"
	"strings"
)

var (
	ErrInvalidKey = errors.New("invalid mutation key")
	ErrExpired    = errors.New("mutation receipt expired")
	ErrConflict   = errors.New("mutation key conflicts with original request")
	ErrCapacity   = errors.New("mutation receipt capacity exhausted")
)

const (
	RetentionMs           int64 = 30 * 24 * 60 * 60 * 1000
	FutureSkewMs          int64 = 5 * 60 * 1000
	MaxIDBytes                  = 128
	MaxOperationBytes           = 96
	MaxStatusBytes              = 32
	ReceiptChargeBytes          = 4096
	AccountChargeBytes          = 1024
	BaseChargeBytes             = 65536
	GlobalByteLimit             = 32 * 1024 * 1024
	OrdinaryByteLimit           = 30 * 1024 * 1024
	ReservedStopBytes           = 2 * 1024 * 1024
	PruneLimit                  = 32
	MaxReceiptRecordBytes       = 1024
)

// Result is deliberately closed: no arbitrary response body or JSON field.
type Result struct {
	Status         string `json:"status"`
	TargetID       string `json:"targetId,omitempty"`
	Revision       int64  `json:"revision,omitempty"`
	Generation     int    `json:"generation,omitempty"`
	PositionMs     int64  `json:"positionMs,omitempty"`
	PositionStatus string `json:"positionStatus,omitempty"`
}
type Receipt struct {
	Key       string `json:"key"`
	Operation string `json:"operation"`
	Result
	CommittedMs int64 `json:"committedMs"`
	ExpiresMs   int64 `json:"expiresMs"`
}

// JSONSchema keeps the compact receipt wire shape closed, including outcome
// enums. Strings stay assignable from existing domain adapters; Save and SQL
// independently enforce these same closed sets before anything is retained.
func (Receipt) JSONSchema() map[string]any {
	boundedString := func(n int) map[string]any { return map[string]any{"type": "string", "maxLength": n} }
	number := func(n int64) map[string]any { return map[string]any{"type": "integer", "minimum": 0, "maximum": n} }
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"key", "operation", "status", "committedMs", "expiresMs"},
		"properties": map[string]any{
			"key":       map[string]any{"type": "string", "pattern": `^p251_[0-9]{13}_[0-9a-f]{32}$`, "minLength": 51, "maxLength": 51},
			"operation": boundedString(MaxOperationBytes),
			"status":    map[string]any{"type": "string", "enum": []string{"applied", "already_stopped", "conflict", "invalid", "terminal", "deleted", "removed", "partial"}},
			"targetId":  boundedString(MaxIDBytes), "revision": number(9007199254740991), "generation": number(2147483647), "positionMs": number(9007199254740991),
			"positionStatus": map[string]any{"type": "string", "enum": []string{"", "applied", "already_stopped", "not_renderer", "policy_rejected", "unchanged"}},
			"committedMs":    number(9999999999999), "expiresMs": number(9999999999999 + RetentionMs),
		},
	}
}

// ErrRootGone means the request names a mutation root that is not the live owner
// for the authenticated scope: it was deleted, its profile lifetime or
// membership incarnation rotated, or the family, profile or installation no
// longer match. A request under a gone root must not apply and its old result
// must not be returned, so callers roll the transaction back instead of treating
// it as an absent target.
var ErrRootGone = errors.New("mutation root is gone")

// ValidRootID applies the 22-character root grammar at protocol boundaries.
func ValidRootID(v string) bool {
	if len(v) != 22 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func hexLower(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ParseKey checks only the public key grammar; persisted cutoff and authority
// checks belong to the store accepting the command.
func ParseKey(key string) (int64, error) {
	if len(key) != 51 || !strings.HasPrefix(key, "p251_") || key[18] != '_' || !hexLower(key[19:], 32) {
		return 0, ErrInvalidKey
	}
	for _, c := range key[5:18] {
		if c < '0' || c > '9' {
			return 0, ErrInvalidKey
		}
	}
	issued, _ := strconv.ParseInt(key[5:18], 10, 64)
	if issued <= 0 {
		return 0, ErrInvalidKey
	}
	return issued, nil
}

// IssuedMs checks the wire grammar and wall-clock window. Persisted retirement
// fences must also be checked; use Check/Lookup rather than this parser alone.
func IssuedMs(key string, now int64) (int64, error) {
	issued, err := ParseKey(key)
	if err != nil {
		return 0, err
	}
	if now < 0 || now > 9999999999999 || issued > now+FutureSkewMs {
		return 0, ErrInvalidKey
	}
	if issued < now-RetentionMs {
		return 0, ErrExpired
	}
	return issued, nil
}
