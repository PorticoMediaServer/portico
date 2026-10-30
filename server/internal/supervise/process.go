package supervise

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const RestartExitCode = 75
const WorkerEnvironment = "PORTICO_SUPERVISED_WORKER"

// Process keeps a restart request inside the running service or container.
// The pipe is also a lifetime fence: a worker drains when its parent disappears.
func Process(ctx context.Context, executable string, args []string) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			return err
		}
		cmd := exec.Command(executable, args...)
		cmd.Env = append(os.Environ(), WorkerEnvironment+"=1")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = reader, os.Stdout, os.Stderr
		err = cmd.Start()
		reader.Close()
		if err != nil {
			writer.Close()
			return err
		}
		done := make(chan error, 1)
		Go("process.wait", func() { done <- cmd.Wait() })
		select {
		case err = <-done:
			writer.Close()
		case <-ctx.Done():
			writer.Close()
			timer := time.NewTimer(40 * time.Second)
			select {
			case <-done:
			case <-timer.C:
				_ = cmd.Process.Kill()
				<-done
			}
			timer.Stop()
			return nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != RestartExitCode {
			return err
		}
		fmt.Fprintln(os.Stderr, "Restarting Portico")
		// A brief pause bounds a defective worker requesting restart in a loop.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func WatchParent(cancel context.CancelFunc, input io.Reader) {
	Go("process.parent", func() { _, _ = io.Copy(io.Discard, input); cancel() })
}
