// Package artifactlease pins a private immutable artifact until every reader's
// actual descriptor retires. Never explicitly unlock a descriptor: it may have
// been duplicated, transferred by SCM_RIGHTS, or inherited by a decoder.
package artifactlease

import "errors"

var ErrBusy = errors.New("artifact has a physical reader")
var ErrUnsupported = errors.New("physical artifact retirement unsupported")
