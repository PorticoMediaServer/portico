package main

import "errors"

// restartSelf cannot replace a process on Windows; the owner is told to restart by hand, and
// the restart route never asks for it there.
func restartSelf() error { return errors.New("restart Portico by hand") }
