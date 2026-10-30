package storagepaths

import (
	"fmt"
	"os"
	"golang.org/x/sys/windows"
)

func recordingDirectoryIdentity(folder *os.File, _ os.FileInfo) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(folder.Fd()), &info); err != nil { return "", err }
	return fmt.Sprintf("windows:%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
