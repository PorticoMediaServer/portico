//go:build !linux

package reading

import (
	"context"
	"os"
)

// No unconfined portability fallback. Other runtime targets need Release review.
func (c *MobiConverter) run(context.Context, *os.File) ([]byte, error) { return nil, ErrUnavailable }

func validateMobiBundle(MobiBundle) error { return ErrUnavailable }
