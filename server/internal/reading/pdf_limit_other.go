//go:build !linux

package reading

import "errors"

// limitPDFProcess refuses to parse where the hard Linux child limits cannot be
// applied (including Darwin), so PDF counts stay unknown rather than running
// without an address-space bound.
func limitPDFProcess() error { return errors.New("pdf text extractor limits unavailable") }
