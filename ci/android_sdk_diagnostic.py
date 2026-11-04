"""Branch-only cold-offline refusal and full offline acceptance; never merge."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import xml.etree.ElementTree as ET

root = Path(__file__).resolve().parents[1]
source = 'd7548b244831ad8d770f4647c74061ddb2e6f37c'
out = Path(os.environ['RUNNER_TEMP']) / 'robolectric-controls'
out.mkdir(exist_ok=True)
android = root / 'apps/native/android'
suite = 'tv.getportico.portico.native.PorticoSubtitleSelectionTimelineTest'
variants = ['Google', 'Firetv']
base = ['./gradlew', '--no-daemon', '--continue', '--init-script', str(root / 'ci/robolectric-offline.gradle')]
results = android / 'app/build/test-results'


def verdict(directory, expected):
    for variant in variants:
        path = directory / f'test{variant}DebugUnitTest' / f'TEST-{suite}.xml'
        report = ET.parse(path).getroot()
        assert int(report.attrib['tests']) == 7, (path, report.attrib)
        assert int(report.attrib['skipped']) == 0 and int(report.attrib['errors']) == 0
        assert int(report.attrib['failures']) == (7 if expected == 'fail' else 0), report.attrib
        if expected == 'fail':
            failures = report.findall('.//failure')
            assert len(failures) == 7
            assert all('LocalDependencyResolver' in (failure.text or '') for failure in failures)


if sys.argv[1] == 'cold':
    subprocess.run(['git', 'diff', '--exit-code', source, '--', 'apps', 'ci/robolectric_sdk.py', 'ci/robolectric-sdk.json', 'ci/robolectric-offline.gradle'], cwd=root, check=True)
    (out / 'source-sha.txt').write_text(source + '\n')
    (out / 'workflow-sha.txt').write_bytes(subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root))
    (out / 'source-archive-sha256.txt').write_text(hashlib.sha256(subprocess.check_output(['git', 'archive', source], cwd=root)).hexdigest() + '\n')
    empty = out / 'missing-sdk'
    empty.mkdir()
    env = dict(os.environ, PORTICO_ROBOLECTRIC_SDK_DIR=str(empty))
    tasks = [f':app:test{variant}DebugUnitTest' for variant in variants]
    with (out / 'cold.log').open('w') as log:
        result = subprocess.run(base + tasks + ['--tests', suite, '-x', ':portico-mpv:stageMediaEngine'], cwd=android, env=env, stdout=log, stderr=subprocess.STDOUT)
    assert result.returncode != 0, 'Missing SDK unexpectedly accepted'
    shutil.copytree(results, out / 'cold-results')
    verdict(out / 'cold-results', 'fail')
    # Delete only these tasks' outputs so the subsequent ordinary full suite
    # cannot reuse a cold-run result. Compilation and dependency caches remain.
    subprocess.run(base + [f':app:cleanTest{variant}DebugUnitTest' for variant in variants], cwd=android, env=env, check=True)
else:
    verdict(results, 'pass')
    shutil.copytree(results, out / 'fixed-results')
    (out / 'accepted.txt').write_text('Missing offline SDK refused all 7 cases on each variant; provisioned offline SDK passed all 14 cases, zero skips. Full ordinary JVM suites ran after the refusal control.\n')
