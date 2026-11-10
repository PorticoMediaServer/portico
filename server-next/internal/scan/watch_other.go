//go:build !linux

package scan

import "context"

// watcher is not built for this system yet: every local folder is scanned on its interval.
type watcher struct{}

func newWatcher() *watcher                           { return nil }
func (w *watcher) close()                            {}
func (w *watcher) add(string, string) (bool, bool)   { return false, false }
func (w *watcher) run(context.Context, func(string)) {}

// networkShare cannot be told here: a folder is taken to be on a disk of this machine.
func networkShare(string) bool { return false }
