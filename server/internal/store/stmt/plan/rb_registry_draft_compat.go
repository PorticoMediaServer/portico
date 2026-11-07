package plan

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Local dependency composition only: retain the newer canonical N1 points
// while drafting against #1322. Exclude this file from RB publication once
// the registry owner's current-main rebuild supplies their registrations.
func init() {
	for _, p := range n1OctetPoints {
		p := p
		pointOctets.add(octetShape{p.id, p.sql, "N1 byte preflight requires its exact closed point definition",
			n1OctetDefinition,
			func(ctx context.Context, c *sql.Conn) error {
				return auditN1OctetPoint(ctx, c, stmt.Definition{ID: p.id})
			},
			func(rows Rows) error { return checkN1OctetPoint(stmt.Definition{ID: p.id}, rows) },
		})
	}
}
