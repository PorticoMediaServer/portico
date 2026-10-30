#!/usr/bin/env python3
"""Self-test only: a disposable local JVM, no Android device or saved app state."""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile

root = Path(__file__).resolve().parents[2]
if not shutil.which('javac') or not shutil.which('java'):
    raise SystemExit('JDK is unavailable; no target attach attempted')
with tempfile.TemporaryDirectory(prefix='modal-trace-', dir=os.environ.get('TMPDIR')) as scratch:
    directory = Path(scratch)
    subprocess.run(['javac', '-J-Xmx128m', '--add-modules', 'jdk.jdi', '-g', '-d', scratch,
                    str(root / 'scripts/debug/TraceModalDismiss.java'),
                    str(root / 'test/fixtures/modal-dismiss/com/facebook/react/views/modal/ReactModalHostView.java')], check=True)
    for scenario in ['inactive-second', 'ambiguous']:
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        target = subprocess.Popen(['java', '-Xmx64m', f'-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:{port}',
                                   '-cp', scratch, 'com.facebook.react.views.modal.ReactModalHostView', scenario], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        observer = None
        try:
            while 'FIXTURE_READY' not in target.stdout.readline():
                if target.poll() is not None:
                    raise RuntimeError('Disposable target exited before readiness')
            output = directory / (scenario + '.json')
            observer = subprocess.Popen(['java', '-Xmx64m', '--add-modules', 'jdk.jdi', '-cp', scratch, 'TraceModalDismiss', str(port), 'showing', '5000', str(output)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            if scenario == 'ambiguous':
                refusal = observer.communicate(timeout=10)[0]
                assert observer.returncode != 0 and 'exactly one showing attached modal; found 2' in refusal, refusal
                assert not output.exists(), 'Ambiguous targets must never arm or capture a breakpoint'
            else:
                ready = observer.stdout.readline()
                if not ready.startswith('READY '):
                    raise RuntimeError(ready + observer.stdout.read())
                target.stdin.write('\n'); target.stdin.flush()
                remaining = observer.communicate(timeout=10)[0]
                assert observer.returncode == 0, remaining
                result = json.loads(output.read_text())
                methods = [frame['method'] for frame in result['frames']]
                assert methods[:2] == ['dismiss', 'onDetachedFromWindow'], result
                assert result['eventThreadSuspensions'] == 1 and result['targetInvocations'] == 0 and result['fieldWrites'] == 0
                assert result['vmSuspendCalls'] == 0 and result['localsRead'] == 0
                target.stdin.write('\n'); target.stdin.flush()
                target.communicate(timeout=5)
                assert target.returncode == 0
            print(json.dumps({'selfTest': 'PASS', 'scenario': scenario, 'disposableJvm': True, 'androidAttached': False,
                              'targetInvocations': 0, 'fieldWrites': 0, 'eventThreadSuspensions': 0 if scenario == 'ambiguous' else 1}))
        finally:
            for process in [observer, target]:
                if process and process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        process.kill(); process.wait()
