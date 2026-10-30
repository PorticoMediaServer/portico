//go:build !linux && !darwin

package archive7z

import "errors"

func limitProcess() error {
	return errors.New("bounded archive decoding is unavailable on this platform")
}
