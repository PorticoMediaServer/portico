package read

import "github.com/PorticoMediaServer/portico/server/internal/store/governor"

// rbContainerMetricsSize is one child row's exact variable octets, measured in
// the same snapshot immediately before the payload is copied.
type rbContainerMetricsSize struct {
	id, latest, played int64
}

// rbContainerMetricsOctets validates one closed child octet-preflight window.
// The statement projects (id, latest-aired octets, personal last-played octets)
// followed by the compiled order-key aliases needed by the index's own ORDER BY.
// Every payload column other than those two octet groups is a fixed-size
// primitive. Rejected raw rows keep their place and still count against the cap.
func rbContainerMetricsOctets(rows [][]any, cap, keys int) ([]rbContainerMetricsSize, int64, error) {
	if cap < 1 || keys < 1 || len(rows) > cap {
		return nil, 0, ErrUnqualified
	}
	sizes := make([]rbContainerMetricsSize, 0, len(rows))
	var total int64
	for _, row := range rows {
		if len(row) != 3+keys {
			return nil, 0, ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok || id <= 0 {
			return nil, 0, ErrUnqualified
		}
		latest, ok := row[1].(int64)
		if !ok || latest < 0 || latest > governor.PreparationMaxJobBytes-total {
			return nil, 0, governor.ErrSnapshotBudget
		}
		played, ok := row[2].(int64)
		if !ok || played < 0 || played > governor.PreparationMaxJobBytes-total-latest {
			return nil, 0, governor.ErrSnapshotBudget
		}
		total += latest + played
		sizes = append(sizes, rbContainerMetricsSize{id: id, latest: latest, played: played})
	}
	// Fixed decode metadata, one exact-length Go copy per variable value and the
	// explicit 2x overlap allowance. No columns x maximum length estimate.
	charge := 8192 + int64(len(sizes))*1024 + 2*total
	return sizes, charge, nil
}

// rbContainerMetricsMatches requires the payload rows to be exactly the rows the
// same-snapshot byte leg measured, in the same order and with the same lengths.
func rbContainerMetricsMatches(sizes []rbContainerMetricsSize, rows [][]any) error {
	if len(sizes) != len(rows) {
		return ErrStale
	}
	for i, row := range rows {
		if len(row) < 16 {
			return ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok || id != sizes[i].id {
			return ErrStale
		}
		latest, ok := row[11].(string)
		if !ok || int64(len(latest)) != sizes[i].latest {
			return ErrStale
		}
		if row[15] == nil {
			if sizes[i].played != 0 {
				return ErrStale
			}
			continue
		}
		played, ok := row[15].(string)
		if !ok || int64(len(played)) != sizes[i].played {
			return ErrStale
		}
	}
	return nil
}
