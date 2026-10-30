package storage

import (
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func validateInventoryCommand(r request) error {
	if r.Inventory == nil || len(r.Argv) < 2 || len(r.Argv) > mediaexec.MaxGeneratedArguments {
		return errors.New("invalid inventory command")
	}
	for _, arg := range r.Argv {
		if len(arg) > 16384 {
			return errors.New("invalid scan argument")
		}
	}
	return nil
}
