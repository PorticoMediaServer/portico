//go:build !linux && !darwin && !windows

package storage

import "errors"

func inventoryDirectoryCommandHelper(r request) error {
	if err := validateInventoryCommand(r); err != nil {
		return err
	}
	return errors.New("descriptor-bound directory scan execution is unsupported on this server platform")
}

func inventoryCommandHelper(r request) error {
	if err := validateInventoryCommand(r); err != nil {
		return err
	}
	return errors.New("retained-descriptor scan execution is unsupported on this server platform")
}

// Retained-descriptor execution doesn't exist here; these keep the shared
// request builder compiling.
const descriptorInput = "/dev/fd/3"
const scanCPUSeconds = 300
