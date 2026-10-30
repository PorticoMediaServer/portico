//go:build devtrust && !release

package main

import (
	"os"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// The end-to-end setup rig shortens the setup window to exercise expiry and start-again
// without waiting thirty minutes. Development trust builds only; it can't lengthen it.
func init() {
	if raw := os.Getenv("PORTICO_DEV_SETUP_LIFETIME_SECONDS"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			claimweb.SetDevelopmentSetupLifetime(time.Duration(seconds) * time.Second)
		}
	}
}
