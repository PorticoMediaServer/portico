//go:build !windows

package main

// useParentConsole is for the Windows build only (terminal_windows.go).
func useParentConsole() {}
