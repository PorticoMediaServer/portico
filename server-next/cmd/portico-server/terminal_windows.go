//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// useParentConsole lets the program write to the console it was started from. On Windows it is
// built with no console of its own (-H windowsgui), so that started at sign-in it opens no
// window. Started from a console window instead (start-portico.cmd, or typed there), Windows
// then gives it no outputs, so it joins that console: its log shows there and Ctrl+C stops it,
// as before. Outputs it was given (a pipe or a file) are kept.
func useParentConsole() {
	out, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	errs, _ := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if out != 0 || errs != 0 {
		return
	}
	// ATTACH_PARENT_PROCESS. It fails when there is no console to join (started at sign-in, or
	// from Explorer): the log is then in its files only.
	attach := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	if ok, _, _ := attach.Call(uintptr(^uint32(0))); ok == 0 {
		return
	}
	if console, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = console, console
	}
}
