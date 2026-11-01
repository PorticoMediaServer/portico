package httpapi

import "github.com/PorticoMediaServer/portico/server/internal/catalog"

// rowTotal is a Home row's total, or -1 when the row carries none.
func rowTotal(r catalog.HomeRow) int {
	if r.Total == nil {
		return -1
	}
	return *r.Total
}
