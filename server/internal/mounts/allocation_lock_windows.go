//go:build windows

package mounts

func (s *Service) allocationLock() (func(), error) { return nil, ErrUnavailable }
