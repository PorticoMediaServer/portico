package persistence

import (
	"context"
	"errors"
	"testing"
)

func TestNetworkingClaimsVerifyFreshSchemaAndIdentityBounds(t *testing.T) {
	for name, damage := range map[string]string{
		"fresh":              "",
		"missing bound":      `DROP TRIGGER networking_server_identities_bound_check`,
		"changed bound":      `DROP TRIGGER networking_server_identities_bound_check; CREATE TRIGGER networking_server_identities_bound_check BEFORE INSERT ON networking_server_identities BEGIN SELECT 1; END;`,
		"missing accounting": `DROP TRIGGER networking_server_identities_bound_insert`,
		"unexpected trigger": `CREATE TRIGGER networking_server_identities_extra AFTER INSERT ON networking_server_identities BEGIN SELECT 1; END;`,
	} {
		t.Run(name, func(t *testing.T) {
			db, err := Open(freshDatabaseCopy(t, t.TempDir(), "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if damage != "" {
				if _, err := db.Exec(damage); err != nil {
					t.Fatal(err)
				}
			}
			err = VerifyNetworkingClaims(context.Background(), db)
			if damage == "" {
				if err != nil {
					t.Fatalf("fresh baseline rejected: %v", err)
				}
			} else if !errors.Is(err, ErrNetworkingClaimSchema) {
				t.Fatalf("damaged identity bound accepted: %v", err)
			}
		})
	}
}
