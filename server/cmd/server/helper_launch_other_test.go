//go:build !darwin

package main

import "testing"

func helperProcessStart() (int64, string) { return 0, "Darwin process birth unavailable on this host" }

func logHelperLaunchDiagnostics(t *testing.T, _ string) {
	t.Helper()
	t.Log("empty-source launch: Darwin process/xattr/codesign diagnostics unavailable on this host")
}
