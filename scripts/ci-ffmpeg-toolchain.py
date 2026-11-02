#!/usr/bin/env python3
"""Cold hosted engine builds and exact-run native evidence. Never publishes."""
import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import subprocess
import sys
import tarfile
import threading
import time

ROOT = Path(__file__).resolve().parents[1]
TARGETS = {
    'windows-x64': ('ubuntu-24.04', 'windows-2025', False),
    'linux-x64': ('ubuntu-24.04', 'ubuntu-24.04', False),
    'linux-arm64': ('ubuntu-24.04-arm', 'ubuntu-24.04-arm', False),
    'macos-arm64': ('macos-15', 'macos-15', False),
}

# Development receiver fixture only. Never selected by packaging or a publisher.
WINDOWS_NATIVE_RETRY = dict(target='windows-x64',
    name='engine-cold-windows-x64-dffa072cfd908716c7a310da6957394eae15c9fe-NOT-FOR-USE',
    engineSourceSHA='dffa072cfd908716c7a310da6957394eae15c9fe',
    archiveSHA256='8bf3c62c5e2b4481d9397f342ae8fa5bb44b573030c554ffa7c81fd3cd39a1b2',
    requirementsSHA256='d9f8cb1940d6cb48bb9f65b40e15b76006ec9cf97c55a32795d1ecfcf84d63be',
    sourcesLockSHA256='e025e8bfff2ea346670985e629e550e6024f6c385f4b0b535bc080a24bfd2cad',
    createdAt='2026-11-01T23:13:44Z', expiresAt='2026-11-15T23:13:21Z',
    origin=dict(kind='github-actions', repository='PorticoMediaServer/portico',
                runId=41346289584, runAttempt=1, artifactId=13105206141))


def module(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / (name + '.py'))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def atomic(path, value):
    temporary = path.with_suffix('.new')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


def context(env=None, root=ROOT):
    env = os.environ if env is None else env
    expected = 'PorticoMediaServer/portico/.github/workflows/ffmpeg-toolchain.yml@'
    if (env.get('GITHUB_ACTIONS') != 'true' or env.get('RUNNER_ENVIRONMENT') != 'github-hosted' or
            env.get('GITHUB_REPOSITORY') != 'PorticoMediaServer/portico' or
            not env.get('GITHUB_WORKFLOW_REF', '').startswith(expected + 'refs/heads/') or
            not env.get('GITHUB_REF', '').startswith('refs/heads/') or
            env.get('GITHUB_EVENT_NAME') != 'workflow_dispatch'):
        raise ValueError('dedicated same-repository hosted branch workflow required')
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
    if sha != env.get('GITHUB_SHA') or not re.fullmatch(r'[a-f0-9]{40}', sha):
        raise ValueError('exact workflow source checkout required')
    for key in ('GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT'):
        if not re.fullmatch(r'[1-9][0-9]*', env.get(key, '')):
            raise ValueError('exact workflow run/attempt required')
    return {'engineSourceSHA': sha, 'runId': int(env['GITHUB_RUN_ID']),
            'runAttempt': int(env['GITHUB_RUN_ATTEMPT']), 'context': 'github-actions'}


def matrix(selected):
    if selected != 'all' and selected not in TARGETS:
        raise ValueError('unknown cold-build target')
    return {'include': [dict(target=target, buildRunner=values[0], nativeRunner=values[1],
                             optional=values[2]) for target, values in TARGETS.items()
                        if selected in ('all', target)]}


def artifact_pages(run_id, per_page=100):
    if type(run_id) is not int or run_id <= 0 or not 1 <= per_page <= 100:
        raise ValueError('positive exact run and bounded page size required')
    # gh rejects --slurp together with --jq. Keep fetching and flattening
    # separate; the same real command is exercised by the hosted control.
    raw = subprocess.check_output(['gh', 'api', '--paginate', '--slurp',
        f'repos/PorticoMediaServer/portico/actions/runs/{run_id}/artifacts?per_page={per_page}'])
    pages = json.loads(raw)
    if not isinstance(pages, list) or any(not isinstance(p, dict) or not isinstance(p.get('artifacts'), list) for p in pages):
        raise ValueError('paginated artifact response changed')
    return [item for page in pages for item in page['artifacts']]


def native_matrix(selected, artifacts, source_sha, build_result, run_id):
    """Only schedule receivers whose exact-run construction artifact exists."""
    if not re.fullmatch(r'[a-f0-9]{40}', source_sha):
        raise ValueError('exact construction source required')
    if build_result not in ('success', 'failure', 'cancelled'):
        raise ValueError('completed construction result required')
    rows, missing = [], []
    for row in matrix(selected)['include']:
        expected = 'engine-cold-' + row['target'] + '-' + source_sha + '-NOT-FOR-USE'
        matches = [item for item in artifacts if item.get('name') == expected]
        if not matches:
            missing.append(row['target'])
            continue
        item = matches[0]
        if (len(matches) != 1 or item.get('expired') is not False or
                type(item.get('id')) is not int or item['id'] <= 0 or
                not 0 < item.get('size_in_bytes', 0) <= 3 * 1024**3 or
                not re.fullmatch(r'sha256:[a-f0-9]{64}', item.get('digest', '')) or
                item.get('workflow_run', {}).get('id') != run_id or
                item.get('workflow_run', {}).get('head_sha') != source_sha):
            raise ValueError('ambiguous or changed construction artifact: ' + expected)
        rows.append(dict(row, artifactId=item['id']))
    if missing and build_result == 'success':
        raise ValueError('successful construction is missing its exact artifacts')
    return {'matrix': {'include': rows}, 'missingConstruction': missing,
            'nativeEngineChecks': False, 'qualified': False, 'release': False}


def native_retry_pin(target, now=None, replay_run_id=None):
    pin = json.loads(json.dumps(WINDOWS_NATIVE_RETRY))
    if target != pin['target']:
        raise ValueError('only the explicitly pinned Windows development artifact may be retried')
    if replay_run_id is not None and str(replay_run_id) != str(pin['origin']['runId']):
        raise ValueError('replay run differs from the exact artifact pin')
    created = datetime.fromisoformat(pin['createdAt'].replace('Z', '+00:00'))
    expires = datetime.fromisoformat(pin['expiresAt'].replace('Z', '+00:00'))
    if not created <= (now or datetime.now(timezone.utc)) < expires or not timedelta(0) < expires - created <= timedelta(days=14):
        raise ValueError('development artifact expired, future or outside its 14-day window')
    return pin


def check_retry_transfer(sender, pin, archive_sha):
    producer = dict(engineSourceSHA=pin['engineSourceSHA'], runId=pin['origin']['runId'],
                    runAttempt=pin['origin']['runAttempt'], context='github-actions')
    if (any(sender.get(key) != value or type(sender.get(key)) is not type(value) for key, value in producer.items()) or
            sender.get('target') != pin['target'] or sender.get('constructionPassed') is not True or
            sender.get('qualified') is not False or sender.get('release') is not False or
            sender.get('bundleArchiveSHA256') != archive_sha):
        raise ValueError('pinned development construction identity or bytes changed')
    return producer


def cohort_replay_pin(target, run_id, now=None):
    value = json.loads((ROOT / 'third_party/ffmpeg/development-native-replays.json').read_text())
    if value.get('schemaVersion') != 1 or value.get('qualified') is not False or value.get('release') is not False:
        raise ValueError('development-only replay pin required')
    pin = value['runs'].get(str(run_id), {}).get(target)
    if pin is None: raise ValueError('target/run has no reviewed exact artifact pin')
    origin = pin['origin']; source = pin['engineSourceSHA']
    if (pin['target'] != target or pin['name'] != 'engine-cold-' + target + '-' + source + '-NOT-FOR-USE' or
            not re.fullmatch('[a-f0-9]{40}', source) or not re.fullmatch('[a-f0-9]{64}', pin['archiveSHA256']) or
            origin.get('repository') != 'PorticoMediaServer/portico' or origin.get('kind') != 'github-actions' or
            str(origin.get('runId')) != str(run_id) or type(origin.get('artifactId')) is not int or origin['artifactId'] <= 0 or
            type(origin.get('runAttempt')) is not int or origin['runAttempt'] <= 0):
        raise ValueError('replay artifact locator/source changed')
    created = datetime.fromisoformat(pin['createdAt'].replace('Z', '+00:00'))
    expires = datetime.fromisoformat(pin['expiresAt'].replace('Z', '+00:00'))
    if not created <= (now or datetime.now(timezone.utc)) < expires or not timedelta(0) < expires - created <= timedelta(days=14):
        raise ValueError('replay pin expired or outside its 14-day window')
    for name, field in (('requirements.v1.json', 'requirementsSHA256'), ('sources.lock.json', 'sourcesLockSHA256')):
        if digest(ROOT / 'third_party/ffmpeg' / name) != pin[field]: raise ValueError('replay contract differs from reviewed pin')
    return pin


def replay_matrix(selected, run_id, now=None):
    if str(run_id) == str(WINDOWS_NATIVE_RETRY['origin']['runId']):
        if selected not in ('all', 'windows-x64'): raise ValueError('original retry is Windows-only')
        native_retry_pin('windows-x64', replay_run_id=run_id, now=now)
        return matrix('windows-x64')
    pins = json.loads((ROOT / 'third_party/ffmpeg/development-native-replays.json').read_text())['runs'].get(str(run_id), {})
    rows = [row for row in matrix(selected)['include'] if row['target'] in pins]
    if not rows: raise ValueError('no reviewed replay targets for this run')
    for row in rows: cohort_replay_pin(row['target'], run_id, now=now)
    return dict(include=rows)


def run(command, log, env=None, timeout=315 * 60):
    started = time.monotonic()
    with log.open('wb') as output:
        # Kill the command's owned process group on budget expiry; cleanup and
        # evidence export still have time before Actions' 330-minute limit.
        process = subprocess.Popen([str(x) for x in command], cwd=ROOT, env=env,
                                   stdout=output, stderr=subprocess.STDOUT,
                                   start_new_session=os.name != 'nt')
        try:
            code = process.wait(timeout=timeout)
        except BaseException:
            if os.name == 'nt':
                subprocess.run(['taskkill.exe', '/PID', str(process.pid), '/T', '/F'], check=False)
            else:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
            process.wait()
            raise
    if code:
        raise RuntimeError(log.name + ': exit ' + str(code))
    return round(time.monotonic() - started, 3)


def resources(path):
    result = {'freeDiskBytes': shutil.disk_usage(path).free, 'OS': platform.platform(),
              'machine': platform.machine(), 'python': sys.version,
              'runnerImage': os.environ.get('ImageOS'), 'runnerImageVersion': os.environ.get('ImageVersion')}
    if Path('/proc/meminfo').is_file():
        result['meminfo'] = Path('/proc/meminfo').read_text()
    return result


class DiskSampler:
    """Sample filesystem pressure; state explicitly that short spikes may escape."""
    def __init__(self, work, output):
        self.work, self.output = work, output
        self.stop = threading.Event()
        self.record = {'intervalSeconds': 5, 'samples': 0, 'scope':
                       'Filesystem usage sampled during this build; not an exact continuous peak.'}
        self.thread = threading.Thread(target=self.sample, daemon=True)

    def observe(self):
        usage = shutil.disk_usage(self.work)
        if not self.record['samples']:
            self.record['initialFreeBytes'] = usage.free
            self.record['minimumFreeBytes'] = usage.free
            self.record['maximumUsedBytes'] = usage.used
        self.record.update(samples=self.record['samples'] + 1,
                           minimumFreeBytes=min(usage.free, self.record['minimumFreeBytes']),
                           maximumUsedBytes=max(usage.used, self.record['maximumUsedBytes']))
        self.record['maximumAdditionalUsedBytes'] = max(0, self.record['initialFreeBytes'] - self.record['minimumFreeBytes'])
        atomic(self.output, self.record)

    def sample(self):
        try:
            self.observe()
            while not self.stop.wait(5):
                self.observe()
        except Exception as error:
            self.record['samplingFailure'] = str(error)

    def finish(self):
        self.stop.set(); self.thread.join(timeout=10)
        if self.thread.is_alive():
            self.record['samplingFailure'] = 'sampler did not stop'
        else:
            self.observe()
        return dict(self.record)


def preserve_diagnostics(directories, evidence):
    for directory in directories:
        if not directory.exists():
            continue
        for path in directory.rglob('*'):
            relative = path.relative_to(directory)
            if (path.is_file() and not path.is_symlink() and path.suffix in ('.json', '.log') and
                    '.git' not in relative.parts and
                    not {'recipe', 'prefix', 'build', 'bundle'} & set(relative.parts)):
                destination = evidence / 'custody' / directory.name / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                size = path.stat().st_size
                if size <= 4 * 1024**2:
                    shutil.copyfile(path, destination)
                elif path.suffix == '.log':
                    # Large successful FFmpeg compiler output must not hide
                    # its terminal failure when the hosted machine disappears.
                    # Preserve a bounded head/tail with explicit original hash
                    # and byte ranges; never present it as the complete log.
                    head_size, tail_size = 64 * 1024, 1024**2
                    with path.open('rb') as source:
                        head = source.read(head_size)
                        source.seek(-tail_size, os.SEEK_END)
                        tail = source.read(tail_size)
                    destination.with_name(destination.name + '.head.log').write_bytes(head)
                    destination.with_name(destination.name + '.tail.log').write_bytes(tail)
                    atomic(destination.with_name(destination.name + '.ranges.json'), {
                        'originalBytes': size, 'originalSHA256': digest(path),
                        'complete': False, 'headFile': destination.name + '.head.log',
                        'tailFile': destination.name + '.tail.log', 'head': [0, len(head)],
                        'tail': [size - len(tail), size]})


def stage_timings(work, scratch, target):
    stages = {}
    native_log = work / 'evidence/stage-times.tsv'
    active = None
    if native_log.is_file():
        for line in native_log.read_text().splitlines():
            event, name, timestamp = line.split('\t'); timestamp = int(timestamp)
            if event == 'start':
                active = (name, timestamp)
            elif event == 'end' and active is not None and active[0] == name:
                stages[name] = stages.get(name, 0) + max(0, timestamp - active[1]); active = None
        if active is not None:
            stages[active[0]] = stages.get(active[0], 0) + max(0, int(time.time()) - active[1])
    dependency_log = scratch / 'ffmpeg-build' / target / 'linux-deps/stage-times.jsonl'
    if dependency_log.is_file():
        for line in dependency_log.read_text().splitlines():
            value = json.loads(line)
            stages[value['stage']] = stages.get(value['stage'], 0) + value['seconds']
    producer = work / 'state/report.json'
    if producer.is_file():
        stages.update(json.loads(producer.read_text()).get('phaseTimingsSeconds', {}))
    return stages


def cache_usage(work, scratch, target):
    result = {'dependencyReceiptHits': 0, 'dependencyReceiptMisses': 0,
              'cachedBuildKitOperationLogLines': 0, 'receiptReadFailures': []}
    build_root = scratch / 'ffmpeg-build' / target
    for directory in (build_root / 'dependency-cache-receipts', build_root / 'linux-deps/dependency-cache-receipts'):
        if directory.is_dir():
            for path in directory.glob('*.json'):
                try:
                    if path.stat().st_size > 128 * 1024**2: raise ValueError('receipt exceeds count bound')
                    hit = json.loads(path.read_text()).get('cacheHit')
                    if type(hit) is bool:
                        result['dependencyReceiptHits' if hit else 'dependencyReceiptMisses'] += 1
                except (OSError, ValueError) as error:
                    result['receiptReadFailures'].append(path.name + ': ' + type(error).__name__)
    producer = work / 'state/report.json'
    if producer.is_file():
        rows = json.loads(producer.read_text()).get('stages', {})
        result['windowsDependencyStages'] = {state: sum(row.get('state') == state for name, row in rows.items()
            if name not in ('portico-ffmpeg', 'portico-engine-output')) for state in ('passed', 'cached', 'failed', 'blocked')}
    for parent in (work / 'state', build_root):
        path = parent / 'source-cache-restore.json'
        if path.is_file(): result['sourceSnapshotsRestored'] = json.loads(path.read_text()).get('restored')
    log = work / 'evidence/build.log'
    if log.is_file():
        with log.open('rb') as stream:
            result['cachedBuildKitOperationLogLines'] = sum(bool(re.fullmatch(rb'#\d+ CACHED\s*', line)) for line in stream)
    result['scope'] = 'Actual dependency receipt flags and cached operation log lines; no cache hit qualifies an engine.'
    return result


def build(target, work):
    if os.environ.get('PORTICO_ENGINE_BUILD_CONTEXT', 'development') != 'development':
        raise ValueError('development-only producer refuses qualified/release/publisher contexts')
    receipt = dict(schemaVersion=1, target=target, **context(), qualified=False, release=False,
                   cacheRequested=bool(os.environ.get('PORTICO_ENGINE_STAGE_CACHE') or os.environ.get('PORTICO_ENGINE_REGISTRY_CACHE') == '1'), startedUTC=time.strftime('%FT%TZ', time.gmtime()), phases={})
    work.mkdir(parents=True, exist_ok=False)
    evidence = work / 'evidence'; evidence.mkdir()
    product = work / 'product'; product.mkdir()
    started = time.monotonic()
    receipt['resourcesBefore'] = resources(work)
    receipt['mesonPin'] = module('ffmpeg-build-tools').pin(ROOT / 'scripts/requirements-ffmpeg-meson.txt')
    sampler = DiskSampler(work, evidence / 'disk-samples.json'); sampler.thread.start()
    env = dict(os.environ, PORTICO_BUILD_JOBS='2', MAKEFLAGS='-j1', NUMJOBS='2',
               CMAKE_BUILD_PARALLEL_LEVEL='2', CARGO_BUILD_JOBS='2', RAYON_NUM_THREADS='2')
    scratch = work / '.scratch/hardware-r29'
    for name in ('tmp', 'go-tmp'):
        (scratch / name).mkdir(parents=True)
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    env.update(PORTICO_LANE_SCRATCH=str(scratch), TMPDIR=str(scratch / 'tmp'), GOTMPDIR=str(scratch / 'go-tmp'),
               PORTICO_ENGINE_TIMINGS=str(evidence / 'stage-times.tsv'),
               PORTICO_DISC_SOURCE_COMMIT=lock['discHelper']['sourceCommit'],
               PORTICO_DISC_FIXTURES=str(ROOT / lock['discHelper']['fixtureManifest']),
               PORTICO_DISC_FIXTURES_SHA256=lock['discHelper']['fixtureManifestSHA256'],
               PORTICO_FFMPEG_PHASE='full', PORTICO_BUILD_SOURCE_NETWORK='host')
    if target == 'linux-arm64':
        env['PORTICO_ENGINE_REPEAT_COMPILES'] = '1'
    try:
        for key in ('PORTICO_FFMPEG_SOURCE_ONLY', 'PORTICO_FFMPEG_DEPS_ONLY'):
            if env.get(key, '0') != '0':
                raise ValueError('partial source/dependency builds forbidden')
        receipt['phases']['sourceAuthoritySeconds'] = run(
            [sys.executable, ROOT / 'scripts/check-ffmpeg-build-inputs.py', '--output', evidence / 'source-authority.json'],
            evidence / 'source-authority.log', env, timeout=60)
        if target.startswith('macos-'):
            expected = 'arm64'
            if platform.system() != 'Darwin' or platform.machine().lower() != expected:
                raise ValueError('matching native Mac build runner required')
            tool = Path(subprocess.check_output(['/usr/bin/xcrun', '--find', 'libtool'], text=True).strip())
            if not tool.is_absolute() or not tool.is_file() or not os.access(tool, os.X_OK):
                raise ValueError('absolute executable Apple libtool required')
            env['MACOS_LIBTOOL'] = str(tool)
            receipt['appleLibtool'] = str(tool)
            receipt['appleSDK'] = {
                'xcode': subprocess.check_output(['xcodebuild', '-version'], text=True).strip(),
                'version': subprocess.check_output(['/usr/bin/xcrun', '--show-sdk-version'], text=True).strip(),
                'clang': subprocess.check_output(['/usr/bin/xcrun', 'clang', '--version'], text=True).strip()}
        if target == 'windows-x64':
            state = work / 'state'
            receipt['phases']['compileSeconds'] = run([sys.executable, ROOT / 'scripts/build-engine.py',
                target, '--context', 'github-actions', '--state', state, '--repeat-compiles'], evidence / 'build.log', env, timeout=300 * 60)
            report = json.loads((state / 'report.json').read_text())
            raw = Path(report['rawOutputRoot'])
            if report.get('rawCompilation') is not True or len(report['rawArchives']) != 1:
                raise ValueError('complete raw engine output required')
            name, expected = next(iter(report['rawArchives'].items()))
            if digest(raw / name) != expected:
                raise ValueError('raw engine archive changed')
            receipt['phases']['assemblySeconds'] = run([sys.executable, ROOT / 'scripts/assemble-engine.py', target,
                '--raw', raw / name, '--recipe', state / 'recipe', '--build-report', state / 'report.json',
                '--output', work / 'constructed'], evidence / 'assembly.log', env,
                timeout=max(600, 315 * 60 - int(time.monotonic() - started)))
            bundle = work / 'constructed/bundle'
            shutil.copyfile(state / 'report.json', evidence / 'producer.json')
            shutil.copyfile(work / 'constructed/construction-receipt.json', evidence / 'construction.json')
        else:
            receipt['phases']['compileAndPackageSeconds'] = run(['bash', ROOT / 'scripts/build-ffmpeg.sh', target,
                scratch / 'ffmpeg-build' / target], evidence / 'build.log', env)
            bundle = scratch / 'ffmpeg-build' / target / 'bundle'
        run([sys.executable, ROOT / 'scripts/verify-ffmpeg-manifest.py', bundle], evidence / 'manifest.log', env, 60)
        # One opaque tar preserves executable modes across Actions' ZIP
        # transport. The receiver still performs bounded safe extraction.
        archive = product / 'bundle.tar'
        with tarfile.open(archive, 'w') as output:
            output.add(bundle, arcname='bundle')
        receipt.update(constructionPassed=True, bundleArchiveSHA256=digest(archive),
                       toolchainManifestSHA256=digest(bundle / 'toolchain-manifest.json'),
                       correspondingSourceSHA256=digest(bundle / 'corresponding-source.tar.xz'))
        atomic(product / 'transfer.json', receipt)
        (product / 'NOT-FOR-USE.txt').write_text('Cold engine construction intermediate. NOT-FOR-USE; no release or complete acceptance.\n')
    except BaseException as error:
        receipt.update(constructionPassed=False, failure=type(error).__name__ + ': ' + str(error))
        if target.startswith('macos-'):
            try:
                receipt['prepackCapture'] = module('capture-engine-prepack').capture(
                    target, scratch / 'ffmpeg-build' / target, work / 'prepack', receipt, ROOT)
            except Exception as capture_error:
                receipt['prepackCaptureFailure'] = type(capture_error).__name__ + ': ' + str(capture_error)
    finally:
        # Preserve bounded raw source/compiler receipts and failure logs before
        # the hosted runner is discarded. No compiler image push or local job.
        try:
            preserve_diagnostics((work / 'state', scratch / 'ffmpeg-build' / target), evidence)
        except Exception as error:
            receipt['diagnosticCollectionFailure'] = type(error).__name__ + ': ' + str(error)
            receipt['constructionPassed'] = False
        receipt.update(elapsedSeconds=round(time.monotonic() - started, 3), resourcesAfter=resources(work))
        receipt['sampledDisk'] = sampler.finish()
        receipt['stageSeconds'] = stage_timings(work, scratch, target)
        receipt['cacheUsage'] = cache_usage(work, scratch, target)
        for tool_receipt in (scratch / 'ffmpeg-build' / target / 'meson-tool.json',
                             scratch / 'ffmpeg-build' / target / 'linux-deps/meson-tool.json'):
            if tool_receipt.is_file():
                receipt['mesonTool'] = json.loads(tool_receipt.read_text())
        producer = work / 'state/report.json'
        if producer.is_file() and 'mesonTool' in json.loads(producer.read_text()):
            receipt['mesonTool'] = json.loads(producer.read_text())['mesonTool']
        if receipt['stageSeconds']:
            name = max(receipt['stageSeconds'], key=receipt['stageSeconds'].get)
            receipt['longestStage'] = {'name': name, 'seconds': receipt['stageSeconds'][name],
                'scope': 'Recorded build stage intervals/commands; includes source and prerequisite work in that stage.'}
        atomic(evidence / 'cold-build.json', receipt)
    print(json.dumps(receipt), flush=True)
    return int(not receipt.get('constructionPassed'))


def check_native_transfer(sender, target, qualifier):
    # A failed-job rerun retains immutable successful construction artifacts.
    # Keep the producer's attempt; never relabel it as the qualifier's attempt.
    attempt = sender.get('runAttempt')
    if (type(attempt) is not int or not 1 <= attempt <= qualifier['runAttempt'] or
            any(sender.get(k) != v or type(sender.get(k)) is not type(v)
                for k, v in qualifier.items() if k != 'runAttempt') or
            sender.get('target') != target or sender.get('constructionPassed') is not True or
            sender.get('qualified') is not False or sender.get('release') is not False):
        raise ValueError('cold engine exact-run/target/producer-attempt mismatch')
    return {k: sender[k] for k in qualifier}


def native(target, transfer, evidence):
    provenance = context()
    module('verify-native-engine').native(target, 'github-actions', os.environ)
    evidence.mkdir(parents=True, exist_ok=False)
    started = time.monotonic()
    receipt = dict(schemaVersion=1, target=target, **provenance, qualified=False, release=False)
    try:
        sender = json.loads((transfer / 'transfer.json').read_text())
        receipt['inputProducer'] = check_native_transfer(sender, target, provenance)
        receipt['replay'] = sender['runAttempt'] != provenance['runAttempt']
        if digest(transfer / 'bundle.tar') != sender['bundleArchiveSHA256']:
            raise ValueError('cold engine archive hash mismatch')
        module('fetch-engine').extract(transfer / 'bundle.tar', evidence / 'expanded')
        bundle = evidence / 'expanded/bundle'
        if (digest(bundle / 'toolchain-manifest.json') != sender['toolchainManifestSHA256'] or
                digest(bundle / 'corresponding-source.tar.xz') != sender['correspondingSourceSHA256']):
            raise ValueError('cold bundle/source changed in transfer')
        # Small sealed metadata survives separately from the large opaque
        # bundle. Missing release-only metadata does not stop app/media testing.
        receipt['releaseMetadataHolds'] = []
        for name in ('toolchain-manifest.json', 'repeat-build.json'):
            path = bundle / name
            try:
                if path.is_file() and not path.is_symlink() and path.stat().st_size <= 4 * 1024**2:
                    shutil.copyfile(path, evidence / name)
                else:
                    receipt['releaseMetadataHolds'].append(name + ' missing, linked or exceeds 4MiB')
            except OSError as error:
                receipt['releaseMetadataHolds'].append(name + ' capture failed: ' + type(error).__name__)
        for tool in (bundle / 'bin').iterdir():
            tool.chmod(0o755)
        module('verify-native-engine').verify(target, bundle, evidence / 'gates', 'github-actions')
        receipt['nativeEngineChecks'] = True
    except BaseException as error:
        receipt.update(nativeEngineChecks=False, failure=type(error).__name__ + ': ' + str(error))
    finally:
        receipt['elapsedSeconds'] = round(time.monotonic() - started, 3)
        atomic(evidence / 'cold-native.json', receipt)
    print(json.dumps(receipt), flush=True)
    return int(not receipt.get('nativeEngineChecks'))


def native_retry(target, work):
    if os.environ.get('PORTICO_ENGINE_BUILD_CONTEXT') != 'development':
        raise ValueError('cross-run native retry is development-only')
    qualification = context()
    module('verify-native-engine').native(target, 'github-actions', os.environ)
    replay = os.environ.get('REPLAY_RUN_ID')
    pin = (cohort_replay_pin(target, replay) if replay and replay != str(WINDOWS_NATIVE_RETRY['origin']['runId'])
           else native_retry_pin(target, replay_run_id=replay or None))
    work.mkdir(parents=True, exist_ok=False)
    evidence = work / 'evidence'; evidence.mkdir()
    receipt = dict(schemaVersion=1, kind='development-native-retry', target=target, replay=True,
        qualifier=qualification, inputArtifact=pin, qualified=False, release=False,
        readyForPublication=False, nativeEngineChecks=False)
    try:
        if (digest(ROOT / 'third_party/ffmpeg/requirements.v1.json') != pin['requirementsSHA256'] or
                digest(ROOT / 'third_party/ffmpeg/sources.lock.json') != pin['sourcesLockSHA256']):
            raise ValueError('pinned development engine contracts differ from the current qualifier')
        intake = module('fetch-engine')
        raw = work / 'artifact.zip'; intake.remote(pin, raw)
        if digest(raw) != pin['archiveSHA256']:
            raise ValueError('pinned development artifact SHA-256 changed')
        intake.extract(raw, work / 'transfer')
        transfer = work / 'transfer'
        sender = json.loads((transfer / 'transfer.json').read_text())
        receipt['inputProducer'] = check_retry_transfer(sender, pin, digest(transfer / 'bundle.tar'))
        intake.extract(transfer / 'bundle.tar', work / 'expanded')
        bundle = work / 'expanded/bundle'
        if (digest(bundle / 'toolchain-manifest.json') != sender['toolchainManifestSHA256'] or
                digest(bundle / 'corresponding-source.tar.xz') != sender['correspondingSourceSHA256'] or
                digest(bundle / 'requirements.v1.json') != pin['requirementsSHA256'] or
                digest(bundle / 'sources.lock.json') != pin['sourcesLockSHA256']):
            raise ValueError('pinned development manifest/source/contracts changed')
        # Complete corresponding-source custody remains mandatory on reuse.
        intake.verify_bundle_source(bundle, target, pin['engineSourceSHA'])
        os.environ.pop('GH_TOKEN', None)  # Compiled media tools do not need API credentials.
        module('verify-native-engine').verify(target, bundle, evidence / 'gates', 'github-actions')
        receipt['nativeEngineChecks'] = True
    except Exception as error:
        receipt['failure'] = type(error).__name__ + ': ' + str(error)
    finally:
        atomic(evidence / 'development-native-retry.json', receipt)
    print(json.dumps(receipt), flush=True)
    return int(not receipt['nativeEngineChecks'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('phase', choices=('plan', 'plan-replay', 'artifact-pages', 'native-plan', 'build', 'native', 'native-retry'))
    parser.add_argument('--target', choices=('all', *TARGETS), default='all')
    parser.add_argument('--work', type=Path)
    parser.add_argument('--transfer', type=Path)
    parser.add_argument('--artifacts', type=Path)
    parser.add_argument('--build-result')
    parser.add_argument('--github-output', type=Path)
    parser.add_argument('--run-id', type=int)
    args = parser.parse_args()
    if args.phase == 'plan':
        print(json.dumps(matrix(args.target)))
    elif args.phase == 'plan-replay':
        print(json.dumps(replay_matrix(args.target, args.run_id)))
    elif args.phase == 'artifact-pages':
        if not args.run_id or not args.artifacts: parser.error('exact run and artifact output required')
        atomic(args.artifacts, artifact_pages(args.run_id))
    elif args.phase == 'native-plan':
        provenance = context()
        if not args.artifacts or not args.work or not args.github_output:
            parser.error('native-plan requires artifact metadata, work and GitHub output')
        result = native_matrix(args.target, json.loads(args.artifacts.read_text()),
                               provenance['engineSourceSHA'], args.build_result, provenance['runId'])
        args.work.mkdir(parents=True, exist_ok=False)
        atomic(args.work / 'native-plan.json', dict(result, **provenance))
        with args.github_output.open('a') as output:
            output.write('matrix=' + json.dumps(result['matrix'], separators=(',', ':')) + '\n')
            output.write('available=' + ('true' if result['matrix']['include'] else 'false') + '\n')
        print(json.dumps(result))
    elif args.target == 'all' or args.work is None:
        parser.error('one target and a fresh work directory required')
    elif args.phase == 'native-retry':
        raise SystemExit(native_retry(args.target, args.work.absolute()))
    elif args.phase == 'build':
        raise SystemExit(build(args.target, args.work.absolute()))
    else:
        if args.transfer is None:
            parser.error('native phase requires exact-run transfer')
        raise SystemExit(native(args.target, args.transfer, args.work.absolute()))
