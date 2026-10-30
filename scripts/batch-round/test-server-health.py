#!/usr/bin/env python3
"""Private test watchdog: readiness reports; repeated failed liveness restarts only this test service."""
import json
import os
from pathlib import Path
import subprocess
import urllib.request

ROOT = Path('/home/runner/work/test-server-v3')


def probe(path, key):
    try:
        with urllib.request.urlopen('http://127.0.0.1:32500' + path, timeout=8) as response:
            return response.status == 200 and json.load(response).get(key) is True
    except (OSError, ValueError, TimeoutError):
        return False


def main():
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        raise SystemExit('Test watchdog is never run in CI')
    if Path.home() != Path('/home/runner') or not ROOT.is_dir():
        raise SystemExit('Only the managed Contabo test installation is admitted')
    counter = ROOT / 'health-failures'
    if probe('/health/ready', 'ready'):
        counter.unlink(missing_ok=True)
        return
    if probe('/health/live', 'alive'):
        counter.unlink(missing_ok=True)
        print('Test server is alive but not ready; readiness remains a failure, no restart')
        raise SystemExit(1)
    try:
        failures = int(counter.read_text()) + 1
    except (OSError, ValueError):
        failures = 1
    temporary = counter.with_suffix('.tmp')
    temporary.write_text(str(failures))
    temporary.replace(counter)
    if failures >= 3:
        subprocess.run(['systemctl', '--user', 'restart', 'portico-test-server.service'], check=True)
        counter.unlink(missing_ok=True)
    raise SystemExit(1)


if __name__ == '__main__':
    main()
