//go:build !devtrust || release

package httpapi

import "testing"

// A build without the devtrust tag, and every release build, has no
// development routes: /v1/dev/e2e and /v1/dev/e2e/sign-in do not exist.
func TestDevE2ERoutesAbsentOutsideDevtrust(t *testing.T) {
	if len(devRoutes) != 0 || len(devContracts) != 0 {
		t.Fatalf("%d development routes in a non-devtrust build", len(devRoutes))
	}
	for _, route := range []string{"GET /v1/dev/e2e", "POST /v1/dev/e2e/sign-in"} {
		if _, ok := routeLanes[route]; ok {
			t.Fatalf("%s is a route", route)
		}
	}
}
