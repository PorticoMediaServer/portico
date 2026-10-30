package playbackv1

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func (s *Service) journeyKeysQuery(ctx context.Context, tx *sql.Tx, actor identity.Principal, journey JourneySelector) (selectorKeys, error) {
	if s.JourneyKeys == nil {
		return selectorKeys{}, ErrUnsupportedSelector
	}
	ids, err := s.JourneyKeys(ctx, tx, actor, journey)
	if err != nil {
		return selectorKeys{}, err
	}
	if len(ids) != journey.Length || !validRadioBatch(ids, 128) || ids[0] != journey.From || ids[len(ids)-1] != journey.To {
		return selectorKeys{}, ErrUnsupportedSelector
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			return selectorKeys{}, ErrUnsupportedSelector
		}
		seen[id] = struct{}{}
	}
	return selectorKeys{query: `SELECT value FROM json_each(?) ORDER BY CAST(key AS INTEGER)`, args: []any{jsonList(ids)}, kind: "selector"}, nil
}
