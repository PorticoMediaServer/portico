package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
)

// ShowEditionIdentityKey scopes an internal identity key without modifying a
// provider's actual identifier or a person's edition label. Empty scope keeps
// the existing identity exactly as it is.
func ShowEditionIdentityKey(key, edition string) string {
	if edition == "" {
		return key
	}
	return "show-edition:" + base64.RawURLEncoding.EncodeToString([]byte(edition)) + ":" + key
}

func findClaimProviderTx(ctx context.Context, tx *sql.Tx, library int64, kind Kind, p ProviderIdentity, edition string) (int64, error) {
	if kind != Show {
		return FindByProviderTx(ctx, tx, library, kind, p)
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT e.id FROM catalog_external_ids x INDEXED BY catalog_external_ids_lookup
 CROSS JOIN catalog_entities e ON e.id=x.entity_id CROSS JOIN catalog_shows s ON s.entity_id=e.id
 WHERE x.provider=? AND x.provider_kind=? AND x.provider_id=? AND e.library_id=? AND e.kind=? AND s.source_edition=? ORDER BY e.id LIMIT 1`, p.Provider, p.Kind, p.ID, library, int(Show), edition).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// A specific source key moved to another edition scope only by an explicit
// merge must remain there on rescan. The existing redirect is the evidence;
// a broad title alias alone never overrides an edition namespace.
func mergedShowAliasTx(ctx context.Context, tx *sql.Tx, c TitleClaim, providers []ProviderIdentity) (int64, error) {
	for _, key := range c.Keys {
		if !strings.HasPrefix(key, "show:folder:") {
			continue
		}
		held, err := findLiveTx(ctx, tx, c.Library, Show, key)
		if err != nil {
			return 0, err
		}
		if held == 0 {
			continue
		}
		var edition string
		var redirected bool
		if err = tx.QueryRowContext(ctx, `SELECT source_edition,EXISTS(SELECT 1 FROM catalog_merged_ids r WHERE r.entity_id=s.entity_id) FROM catalog_shows s WHERE entity_id=?`, held).Scan(&edition, &redirected); err != nil {
			return 0, err
		}
		if edition == c.SourceEdition || !redirected {
			continue
		}
		compatible := true
		for _, p := range providers {
			ok, err := providerCompatibleTx(ctx, tx, held, p)
			if err != nil {
				return 0, err
			}
			compatible = compatible && ok
		}
		if compatible {
			return held, nil
		}
	}
	return 0, nil
}
