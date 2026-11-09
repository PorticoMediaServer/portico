package plan

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Domain files contribute exact points during package initialization. This is
// private compiled policy, never an exported registration API or a request's
// function allowlist. Each contribution owns its shape and native/key/EQP audits;
// future domains add a file without extending Capture's dispatch again.
type octetShape struct {
	id            stmt.ID
	sql, mismatch string
	definition    func(stmt.Definition) bool
	audit         func(context.Context, *sql.Conn) error
	point         func(Rows) error
}

type octetRegistry struct {
	byID  map[stmt.ID]octetShape
	bySQL map[string]struct{}
}

var pointOctets = octetRegistry{byID: make(map[stmt.ID]octetShape), bySQL: make(map[string]struct{})}

func (r *octetRegistry) add(s octetShape) {
	if s.id == "" || s.sql == "" || s.mismatch == "" || s.definition == nil || s.audit == nil || s.point == nil {
		panic("plan: incomplete compiled octet point")
	}
	if _, exists := r.byID[s.id]; exists {
		panic("plan: duplicate compiled octet point ID")
	}
	// Distinct approved command IDs may intentionally use the same exact point
	// SQL. Parser membership conveys no ID/definition/native/EQP authority.
	r.byID[s.id], r.bySQL[s.sql] = s, struct{}{}
}

func (r *octetRegistry) match(d stmt.Definition) (octetShape, bool) {
	s, ok := r.byID[d.ID]
	return s, ok && d.SQL == s.sql && s.definition(d)
}

// Parser-only lookup. This never mints authority: Capture still requires the
// matching ID/definition plus live native/key/EQP audits before a receipt.
func closedOctetSQL(sql string) bool {
	_, ok := pointOctets.bySQL[sql]
	return ok
}

func closedOctetDefinition(d stmt.Definition) bool {
	_, ok := pointOctets.match(d)
	return ok
}

// Definition-aware lexical lint. It applies exactly the closed-octet allowance
// Capture applies to an exact registered point definition; membership alone
// grants no authority, and Capture still re-checks the native key and EQP.
func LintSQLDefinition(d stmt.Definition) []LintIssue {
	return lintSQL(d.SQL, closedOctetDefinition(d))
}

func checkOctetDefinition(d stmt.Definition) error {
	if s, registered := pointOctets.byID[d.ID]; registered {
		if _, exact := pointOctets.match(d); !exact {
			return fmt.Errorf("plan %s: %s", d.ID, s.mismatch)
		}
	}
	return nil
}

func auditOctetDefinition(ctx context.Context, conn *sql.Conn, d stmt.Definition) error {
	if s, exact := pointOctets.match(d); exact {
		return s.audit(ctx, conn)
	}
	return nil
}

func checkOctetPoint(d stmt.Definition, eqp Rows) error {
	if s, exact := pointOctets.match(d); exact {
		if err := s.point(eqp); err != nil {
			return fmt.Errorf("plan %s: %w", d.ID, err)
		}
	}
	return nil
}
