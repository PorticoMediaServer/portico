package backup

import "github.com/PorticoMediaServer/portico/server/internal/diskspace"

func checkFreeSpace(path string, estimate int64) error {
	free, err := diskspace.Free(path)
	if err != nil {
		return err
	}
	need := estimate + estimate/10
	if need < estimate {
		need = estimate
	}
	if free < need {
		return ErrInsufficientDisk
	}
	return nil
}
