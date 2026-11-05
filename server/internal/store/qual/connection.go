package qual

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

const requiredSQLiteVersion = "3.53.4"

var ErrConnectionEvidence = errors.New("qualification connection evidence invalid")

// ReadLimitEvidence distinguishes observed fixed caps from an unasserted zero
// policy. Neither mode is a physical memory or snapshot-admission verdict.
type ReadLimitEvidence struct {
	Mode   string                 `json:"mode"`
	Actual *sqliteconn.ReadLimits `json:"actual"`
}

// ConnectionEvidence is values-only evidence, not workload qualification.
// At is the assertion start in UTC. AssertionElapsedNS measures the observer,
// never writer custody or usable-open latency. A failed helper assertion may
// stop partway through readback; Actual/Enforcement remain null on helper failure
// rather than representing unobserved settings as legitimate zero values.
type ConnectionEvidence struct {
	Kind               string                  `json:"kind"`
	At                 time.Time               `json:"at"`
	AssertionElapsedNS int64                   `json:"assertion_elapsed_ns"`
	Expected           sqliteconn.Config       `json:"expected"`
	Actual             *sqliteconn.Settings    `json:"actual"`
	Enforcement        *sqliteconn.Enforcement `json:"enforcement"`
	ReadLimits         ReadLimitEvidence       `json:"read_limits"`
	PolicyValid        bool                    `json:"policy_valid"`
	Error              string                  `json:"error,omitempty"`
}

// CaptureConnection delegates policy to the merged shared helper. Obtain the
// connection through sqliteconn.Open/Connector. Call outside a transaction,
// after private durability selection and BEFORE timed work, and attach fresh
// evidence to every acquisition's receipt, including after possible replacement.
// A failed check invalidates timing eligibility. No setting is repaired here.
func CaptureConnection(ctx context.Context, conn *sql.Conn, cfg sqliteconn.Config) (ConnectionEvidence, error) {
	started := time.Now()
	r := ConnectionEvidence{Kind: "connection-policy-check-only", At: started.UTC(), Expected: cfg}
	r.ReadLimits.Mode = "preserve_existing_unasserted"
	if cfg.ReadLimits != (sqliteconn.ReadLimits{}) {
		r.ReadLimits.Mode = "fixed_reader"
	}
	s, err := sqliteconn.Assert(ctx, conn, cfg)
	var enforcement sqliteconn.Enforcement
	if err == nil {
		// Settings includes FK but not the other enforcement values. Retain
		// their actual readback using the same owner, not another policy check.
		enforcement, err = sqliteconn.AssertEnforcement(ctx, conn)
	}
	if err == nil {
		r.Actual, r.Enforcement = &s, &enforcement
		if cfg.ReadLimits != (sqliteconn.ReadLimits{}) {
			r.ReadLimits.Actual = &s.ReadLimits
		}
		if s.Engine != requiredSQLiteVersion {
			err = fmt.Errorf("require bundled SQLite %s, got %q", requiredSQLiteVersion, s.Engine)
		}
	}
	r.AssertionElapsedNS = time.Since(started).Nanoseconds()
	if err != nil {
		err = fmt.Errorf("%w: %w", ErrConnectionEvidence, err)
		r.Error = err.Error()
		return r, err
	}
	r.PolicyValid = true
	return r, nil
}
