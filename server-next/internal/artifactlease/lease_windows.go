//go:build windows

package artifactlease

import "os"

// Windows recording capture/retirement is not advertised. Other artifact
// consumers may still read; collection fails closed until a Windows lease
// adapter with inherited-handle retirement is supplied.
func Shared(*os.File) error    { return nil }
func Exclusive(*os.File) error { return ErrUnsupported }
