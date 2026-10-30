//go:build !(linux || darwin)

package storage

import (
	"io"
	"os"
)

func helperInput() io.Reader { return os.Stdin }
