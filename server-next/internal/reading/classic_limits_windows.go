package reading

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

// Keep the job open for the decoder process's lifetime.
var archiveJob windows.Handle

func archiveLimits() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_PROCESS_TIME
	info.BasicLimitInformation.PerProcessUserTimeLimit = 60 * 10_000_000
	info.ProcessMemoryLimit = 1 << 30
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	if err = windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(job)
		return err
	}
	archiveJob = job
	return nil
}
