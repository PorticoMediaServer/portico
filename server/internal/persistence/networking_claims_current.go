package persistence

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"regexp"
	"strings"
)

var ErrNetworkingClaimSchema = errors.New("networking claim schema is not current")

// VerifyNetworkingClaims checks that the networking claim tables are exactly the declared shape and version. It creates nothing: a
// database without them, or with any other shape, is refused.
func VerifyNetworkingClaims(ctx context.Context, db *sql.DB) error {
	gated, e := dbwork.BeginSnapshot(ctx, db)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var foreignKeys int
	if e = tx.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); e != nil {
		return e
	}
	if foreignKeys != 1 {
		return ErrNetworkingClaimSchema
	}
	var marker int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='networking_claim_schema'`).Scan(&marker); e != nil {
		return e
	}
	if marker == 0 {
		return ErrNetworkingClaimSchema
	}
	var version int
	if e = tx.QueryRowContext(ctx, `SELECT version FROM networking_claim_schema WHERE singleton=1`).Scan(&version); e != nil || version != 3 {
		return ErrNetworkingClaimSchema
	}
	return validateNetworkingSchema(ctx, tx)
}

// networkingClaimsSQL is the claim-owned statements of the baseline, the one
// declaration of the shape: the claim tables, their index and triggers, and the
// server identity table with its immutability and storage-bound triggers.
func networkingClaimsSQL() string {
	baseline := BaselineSQL()
	re := regexp.MustCompile(`(?m)^CREATE (?:UNIQUE )?(?:TABLE|INDEX|TRIGGER) ([a-z_]+)`)
	var out strings.Builder
	for _, m := range re.FindAllStringSubmatchIndex(baseline, -1) {
		name := baseline[m[2]:m[3]]
		if !strings.HasPrefix(name, "networking_claim_") && name != "networking_server_identities" && !strings.HasPrefix(name, "networking_server_identities_") && !strings.HasPrefix(name, "networking_identity_no_") {
			continue
		}
		statement := baseline[m[0]:]
		terminator := ";\n"
		if strings.HasPrefix(statement, "CREATE TRIGGER") {
			terminator = "END;\n"
		}
		end := strings.Index(statement, terminator)
		if end < 0 {
			panic("unterminated baseline statement " + name)
		}
		out.WriteString(statement[:end+len(terminator)])
	}
	return out.String()
}

// Compare every declared object to the compiled schema, including constraints,
// indexes and triggers in the claim-owned namespace. Remote route tables have
// their own installer and are not claim-schema objects. A marker is not sufficient proof of a current schema.
func validateNetworkingSchema(ctx context.Context, tx *sql.Tx) error {
	networkingCurrentDDL := networkingClaimsSQL()
	expected := map[string]string{}
	re := regexp.MustCompile(`(?i)CREATE (?:UNIQUE )?(?:TABLE|INDEX|TRIGGER) (?:IF NOT EXISTS )?([a-z_]+)`)
	matches := re.FindAllStringSubmatchIndex(networkingCurrentDDL, -1)
	for i, m := range matches {
		end := len(networkingCurrentDDL)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		statement := networkingCurrentDDL[m[0]:end]
		if strings.HasPrefix(strings.ToUpper(statement), "CREATE TRIGGER") {
			end = strings.Index(statement, "END;") + len("END")
		} else {
			end = strings.Index(statement, ";")
		}
		if end < 1 {
			return ErrNetworkingClaimSchema
		}
		name := networkingCurrentDDL[m[2]:m[3]]
		expected[name] = normalizeNetworkingSQL(statement[:end])
	}
	rows, e := tx.QueryContext(ctx, `SELECT name,sql FROM sqlite_master WHERE (name GLOB 'networking_claim_*' OR tbl_name='networking_server_identities') AND type IN('table','index','trigger') AND sql IS NOT NULL`)
	if e != nil {
		return e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, statement string
		if e = rows.Scan(&name, &statement); e != nil {
			return e
		}
		want, ok := expected[name]
		if !ok || normalizeNetworkingSQL(statement) != want {
			return ErrNetworkingClaimSchema
		}
		count++
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if count != len(expected) {
		return ErrNetworkingClaimSchema
	}
	return ctx.Err()
}
func normalizeNetworkingSQL(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	return strings.ReplaceAll(v, " IF NOT EXISTS ", " ")
}
