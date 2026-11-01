#!/usr/bin/env python3
"""Cold hosted engine builds and exact-run native evidence. Never publishes."""
import argparse
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
    'macos-x64': ('macos-15-intel', 'macos-15-intel', True),
}


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
            if (path.is_file() and not path.is_symlink() and path.stat().st_size <= 4 * 1024**2 and
                    path.suffix in ('.json', '.log') and '.git' not in relative.parts and
                    not {'recipe', 'prefix', 'build', 'bundle'} & set(relative.parts)):
                destination = evidence / 'custody' / directory.name / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(path, destination)


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


def build(target, work):
    receipt = dict(schemaVersion=1, target=target, **context(), qualified=False, release=False,
                   cold=True, startedUTC=time.strftime('%FT%TZ', time.gmtime()), phases={})
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
    try:
        for key in ('PORTICO_FFMPEG_SOURCE_ONLY', 'PORTICO_FFMPEG_DEPS_ONLY'):
            if env.get(key, '0') != '0':
                raise ValueError('partial source/dependency builds forbidden')
        receipt['phases']['sourceAuthoritySeconds'] = run(
            [sys.executable, ROOT / 'scripts/check-ffmpeg-build-inputs.py', '--output', evidence / 'source-authority.json'],
            evidence / 'source-authority.log', env, timeout=60)
        if target.startswith('macos-'):
            expected = 'arm64' if target == 'macos-arm64' else 'x86_64'
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
                target, '--context', 'github-actions', '--state', state], evidence / 'build.log', env)
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
                timeout=max(60, 315 * 60 - int(time.monotonic() - started)))
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


def native(target, transfer, evidence):
    provenance = context()
    module('verify-native-engine').native(target, 'github-actions', os.environ)
    evidence.mkdir(parents=True, exist_ok=False)
    started = time.monotonic()
    receipt = dict(schemaVersion=1, target=target, **provenance, qualified=False, release=False)
    try:
        sender = json.loads((transfer / 'transfer.json').read_text())
        if (any(sender.get(key) != value for key, value in provenance.items()) or
                sender.get('target') != target or sender.get('constructionPassed') is not True or
                sender.get('qualified') is not False or sender.get('release') is not False or
                digest(transfer / 'bundle.tar') != sender['bundleArchiveSHA256']):
            raise ValueError('cold engine exact-run/target/hash mismatch')
        module('fetch-engine').extract(transfer / 'bundle.tar', evidence / 'expanded')
        bundle = evidence / 'expanded/bundle'
        if (digest(bundle / 'toolchain-manifest.json') != sender['toolchainManifestSHA256'] or
                digest(bundle / 'corresponding-source.tar.xz') != sender['correspondingSourceSHA256']):
            raise ValueError('cold bundle/source changed in transfer')
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


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('phase', choices=('plan', 'build', 'native'))
    parser.add_argument('--target', choices=('all', *TARGETS), default='all')
    parser.add_argument('--work', type=Path)
    parser.add_argument('--transfer', type=Path)
    args = parser.parse_args()
    if args.phase == 'plan':
        print(json.dumps(matrix(args.target)))
    elif args.target == 'all' or args.work is None:
        parser.error('one target and a fresh work directory required')
    elif args.phase == 'build':
        raise SystemExit(build(args.target, args.work.absolute()))
    else:
        if args.transfer is None:
            parser.error('native phase requires exact-run transfer')
        raise SystemExit(native(args.target, args.transfer, args.work.absolute()))
