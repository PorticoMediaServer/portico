package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
)

// devRoutes are development-only routes a devtrust build adds (dev_e2e.go).
// Every other build, and every release build, has none.
var devRoutes []func(Dependencies, *http.ServeMux)
var devContracts []func(*apikit.Registry) error
