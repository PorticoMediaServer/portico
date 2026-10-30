//go:build windows

package livechannels

import (
	"errors"
	"os"
)

var ErrPhysicalBusy = errors.New("Physical source retirement is unavailable on this platform.")

func lockPhysical(*os.File) error { return ErrPhysicalBusy }
