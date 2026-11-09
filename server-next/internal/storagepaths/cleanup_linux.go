package storagepaths

import "os"

func cleanupMounts() ([]Mount, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseMountInfo(f), nil
}
