"""Bound a native test command and reap its process group on interruption."""
import os
import signal
import subprocess


def run_bounded(command, cwd, output, timeout):
    process = subprocess.Popen(command, cwd=cwd, stdout=output,
                               stderr=subprocess.STDOUT, start_new_session=True)
    try:
        return process.wait(timeout=timeout)
    except BaseException:
        # A Python/clang harness may own compiler or JVM children. Terminating
        # only its leader would leave them running during subsequent suites.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
        raise
