//go:build !linux

package writer

import (
	"context"
	"testing"
)

func runCanonicalHeldBatch(*testing.T, context.Context, canonicalHeldConfig, *n1Stream, QualificationPopulationManifest) error {
	return ErrClassTargetUnbound
}
