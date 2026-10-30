#!/usr/bin/env python3
"""Prepare and supervise the 300k real-scan round. Plan-only by default."""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
import urllib.request

from orchestrate import private_file, private_dir
from quota import private_json


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root', required=True, help='absent ~/astra-scratch/P2-65 child; retained until collected')
    p.add_argument('--server-binary', required=True)
    p.add_argument('--node', required=True, help='qualified Node24.21.0')
    p.add_argument('--source', required=True, help='real direct-play source, ffprobe duration >=7350s')
    p.add_argument('--capabilities', required=True, help='private captured real client capability document')
    p.add_argument('--users', type=int, default=10)
    p.add_argument('--port', type=int, default=32665)
    p.add_argument('--execute', action='store_true')
    a = p.parse_args()
    if not 1 <= a.users <= 20 or not 1024 <= a.port <= 65535:
        p.error('users1..20 and unprivileged port required')
    here = Path(__file__).resolve().parent
    repo = here.parents[2]
    root = Path(a.root).absolute()
    base = Path.home() / 'astra-scratch' / 'P2-65'
    if not root.is_relative_to(base) or root == base:
        p.error('root must be an absent owned scratch child')
    if not a.execute:
        print(json.dumps({'mode': 'plan', 'root': str(root), 'files': 300000,
                          'users': a.users, 'identities': a.users + 2, 'kernelGiB': 8,
                          'stopGiB': 7, 'ingestDeadlineSeconds': 7200,
                          'loadSeconds': 7200, 'status': 'UNRUN; no changes'}))
        return
    if os.getuid() != 0 or os.environ.get('PORTICO_FIXTURE_DISPOSABLE') != 'yes' or 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        p.error('explicit disposable root environment required; never CI')
    private_file(a.capabilities)
    for binary in (a.server_binary, a.node):
        if not Path(binary).is_file() or not os.access(binary, os.X_OK):
            p.error('executable input missing')
    if subprocess.check_output([a.node, '-p', 'process.versions.node'], text=True).strip() != '24.21.0':
        p.error('Node24.21.0 required')
    if root.exists():
        p.error('root already exists; preserve the earlier round')
    root = private_dir(root, create=True)
    private = private_dir(root / 'private', create=True)
    evidence = private_dir(root / 'evidence', create=True)
    quota = root / 'quota'
    volume = quota / 'volume'
    appdata = volume / 'appdata'
    factory = here.parent / 'provision-scale.py'
    def interrupted(_signum, _frame):
        raise RuntimeError('interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    server = watcher = load = None
    log_handles = []
    result = {'kind': 'p2-65-ingest-supervisor', 'passed': False, 'files': 300000,
              'users': a.users, 'startedAt': time.time(), 'root': str(root)}

    def call(name, args, timeout=None):
        with (evidence / (name + '.log')).open('xb') as output:
            os.chmod(output.name, 0o600)
            process = subprocess.Popen(args, cwd=repo, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                began = time.monotonic()
                while process.poll() is None:
                    if watcher is not None and watcher.poll() is not None:
                        raise RuntimeError('quota observer stopped during ' + name)
                    if server is not None and server.poll() is not None:
                        raise RuntimeError('server stopped during ' + name)
                    if load is not None and load.poll() is not None and name == 'ingest':
                        raise RuntimeError('user window ended before ingest')
                    if timeout is not None and time.monotonic()-began > timeout:
                        raise TimeoutError(name)
                    try: process.wait(timeout=.25)
                    except subprocess.TimeoutExpired: pass
                if process.returncode != 0:
                    raise RuntimeError(name + ' failed')
            finally:
                stop(process)

    def child(name, args, env=None):
        output = (evidence / (name + '.log')).open('xb')
        os.chmod(output.name, 0o600)
        log_handles.append(output)
        return subprocess.Popen(args, cwd=repo, stdout=output, stderr=subprocess.STDOUT,
                                env=env, start_new_session=True)

    def stop(process):
        if process is not None and process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                result['forcedStop'] = True
                result['passed'] = False

    try:
        call('quota-create', [sys.executable, str(here / 'quota.py'), 'create', '--root', str(quota), '--execute'])
        runtime_tmp = private_dir(volume / 'runtime-tmp', create=True)
        os.environ['TMPDIR'] = str(runtime_tmp)
        call('baseline-media', [sys.executable, str(factory), 'media', '--root', str(volume / 'baseline'),
                               '--source', a.source, '--count', '1', '--distinct-copies', '--window-seconds', '7290',
                               '--out', str(private / 'media.json'), '--execute'])
        call('bootstrap', [sys.executable, str(factory), 'bootstrap', '--server-binary', a.server_binary,
                          '--appdata', str(appdata), '--media-facts', str(private / 'media.json'),
                          '--capabilities', a.capabilities, '--port', str(a.port), '--window-seconds', '7290', '--execute'])
        env = {**os.environ, 'PORTICO_DATA_DIR': str(appdata), 'PORTICO_BIND': f'127.0.0.1:{a.port}',
               'PORTICO_SETUP_HOSTS': f'localhost:{a.port},127.0.0.1:{a.port}',
               'HOME': str(appdata), 'TMPDIR': str(runtime_tmp)}
        server = child('server', [a.server_binary], env)
        private_json(private / 'process.json', {'pid': server.pid, 'appdata': str(appdata)})
        watcher = child('quota-watch', [sys.executable, str(here / 'quota.py'), 'watch', '--root', str(quota),
                                       '--pid', str(server.pid), '--notify-pid', str(os.getpid()), '--max-seconds', '7500',
                                       '--out', str(evidence / 'quota.json'), '--control', str(private / 'control.json'), '--execute'])
        target = f'http://127.0.0.1:{a.port}'
        for _ in range(120):
            if server.poll() is not None:
                raise RuntimeError('fixture server exited')
            try:
                with urllib.request.urlopen(target + '/v1/readiness', timeout=2) as response:
                    if json.load(response).get('ready'):
                        break
            except (OSError, ValueError):
                pass
            time.sleep(1)
        else:
            raise RuntimeError('fixture did not become ready')
        call('declare-libraries', [sys.executable, str(here / 'provision.py'), '--base', target,
                                  '--credentials', str(appdata / 'owner-credentials.json'),
                                  '--root', str(volume / 'media'), '--out', str(private / 'libraries.json'), '--execute'])
        # Declare all source libraries before accounts, so their real grants include them.
        call('identities', [sys.executable, str(factory), 'identities', '--target', target,
                            '--owner-credentials', str(appdata / 'owner-credentials.json'),
                            '--capabilities', a.capabilities, '--members', str(a.users),
                            '--custody', str(private / 'custody.json'), '--out', str(private / 'identities.json'), '--execute'])
        call('manifest', [sys.executable, str(factory), 'manifest', '--identities', str(private / 'identities.json'),
                          '--prep-receipt', str(appdata / 'prep-receipt.json'),
                          '--workload', str(appdata / 'workload-template.json'), '--out', str(private / 'manifest-base.json'), '--execute'])
        libs = json.loads((private / 'libraries.json').read_text())['libraries']
        manifest = json.loads((private / 'manifest-base.json').read_text())
        for user in manifest['users']:
            user['libraryId'], user['pivot'] = libs['movie'], 'movies'
        private_json(private / 'manifest.json', manifest)
        mutations = private_dir(private / 'mutations', create=True)
        control = private / 'control.json'
        private_json(control, {'phase': 'baseline', 'event': 'baseline', 'ingestedItems': 0})
        load = child('load', [a.node, '--experimental-transform-types', '--no-warnings',
                              str(here.parent / 'load.ts'), '--execute', '--ingest', '--users', str(a.users),
                              '--seconds', '7200', '--rotate-sessions', '--target', target,
                              '--manifest', str(private / 'manifest.json'), '--mutation-store', str(mutations),
                              '--appdata', str(appdata), '--control', str(control), '--out', str(evidence / 'load.json')])
        call('ingest', [sys.executable, str(here / 'orchestrate.py'), '--execute', '--acknowledge-300k',
                        '--total', '300000', '--phase-size', '50000', '--base', target,
                        '--credentials', str(appdata / 'owner-credentials.json'), '--media', str(here / 'media.py'),
                        '--scratch', str(volume / 'media'), '--facts', str(evidence / 'media-facts.json'),
                        '--marker', str(evidence / 'markers.jsonl'), '--control', str(control), '--appdata', str(appdata),
                        '--library-movies', libs['movie'], '--library-tv', libs['tv'], '--library-music', libs['music']], timeout=7200)
        load_rc = load.wait(timeout=7350)
        result['loadExit'] = load_rc
        if load_rc != 0:
            raise RuntimeError('user load gates failed')
        call('summary', [sys.executable, str(here / 'summarize.py'), '--markers', str(evidence / 'markers.jsonl'),
                          '--load', str(evidence / 'load.json'), '--out', str(evidence / 'trend.json')])
        result['passed'] = True
    except Exception as error:
        # Keep only the error class; HTTP bodies and command arguments can hold credentials.
        result['failure'] = type(error).__name__
    finally:
        stop(load)
        stop(server)
        if watcher is not None:
            try:
                watcher.wait(timeout=5)
            except subprocess.TimeoutExpired:
                stop(watcher)
            quota_receipt = evidence / 'quota.json'
            if watcher.returncode != 0 or not quota_receipt.exists():
                result['passed'] = False
                result['quotaFailure'] = 'observer failed or receipt missing'
            else:
                measured = json.loads(quota_receipt.read_text())
                result['memory'] = measured
                if measured.get('outcome') != 'server-stopped' or measured.get('rssProcessBoundExceeded') or measured.get('peakTreeRSSBytes', 0) <= 0 or measured.get('peakAllocatedBytes', 0) >= 7*1024**3:
                    result['passed'] = False
                    result['quotaFailure'] = 'deadline/budget/memory evidence failed'
        for output in log_handles:
            output.close()
        result['finishedAt'] = time.time()
        private_json(evidence / 'supervisor.json', result)
    print(json.dumps({'passed': result['passed'], 'evidence': str(evidence),
                      'cleanup': f'collect private inputs/evidence, then quota.py cleanup --root {quota} --execute'}))
    if not result['passed']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
