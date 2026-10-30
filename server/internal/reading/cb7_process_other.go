//go:build !linux && !darwin

package reading

import "os/exec"

func startHelper(command *exec.Cmd) error { return command.Start() }

func killHelper(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
