//go:build windows

package artifactlease

import "os"

func SingleLink(os.FileInfo) bool { return false }
