//go:build !windows

package main

func serviceCommand() (bool, error) { return false, nil }
