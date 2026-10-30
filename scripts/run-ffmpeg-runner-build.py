#!/usr/bin/env python3
"""Build one pinned Linux bundle in the temporary exact-commit remote runner."""
import argparse
import ctypes
import datetime
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import stat
import subprocess


QUALIFICATION_REPORTS = (
    'feature-report-bundle.json', 'disc-report-bundle.json',
    'runtime-report-bundle.json', 'abi-report-bundle.json', 'repeat-build.json',
    'linux-deps/dependency-inputs.json', 'linux-output/dovi-profile-build.json',
    'linux-output/dovi-video-build.json', 'linux-hdr-gate/runtime/report.json',
    'linux-hdr-gate/hdr10plus-report.json', 'linux-hdr-gate/join-runtime/report.json',
)


def qualification_report(job_root, name):
    """Read one fixed text report without following directory or file links."""
    directory = os.open(job_root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        parts = ('.scratch', 'hardware-r29', 'ffmpeg-build', 'linux-x64', *Path(name).parts)
        for part in parts[:-1]:
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                            dir_fd=directory)
            os.close(directory)
            directory = child
        descriptor = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                             dir_fd=directory)
        try:
            info = os.fstat(descriptor)
            limit = 2 << 20
            if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
                raise ValueError('Report must be a regular text file of at most 2 MiB')
            chunks, size = [], 0
            while True:
                chunk = os.read(descriptor, min(65536, limit + 1 - size))
                if not chunk:
                    break
                chunks.append(chunk)
                size += len(chunk)
                if size > limit:
                    raise ValueError('Report grew beyond its text bound')
            raw = b''.join(chunks)
        finally:
            os.close(descriptor)
    finally:
        os.close(directory)
    text = raw.decode('utf-8')
    record = {'state': 'produced', 'bytes': len(raw),
              'sha256': hashlib.sha256(raw).hexdigest()}
    try:
        record['report'] = json.loads(text)
    except ValueError:
        # A truncated failed report is evidence, never a successful gate.
        record.update(state='invalid-json', text=text)
    return record


def collect_qualification_reports(job_root, source_commit, build_succeeded, failure):
    """Preserve bounded target diagnostics before mandatory job cleanup."""
    output = Path(os.environ['PORTICO_RUNNER_OUTPUT'])
    if (output.is_symlink() or output.resolve(strict=True) != job_root / 'outputs' or
            not output.is_dir()):
        raise ValueError('Qualification diagnostics must use this runner job output directory')
    reports = {}
    for name in QUALIFICATION_REPORTS:
        try:
            reports[name] = qualification_report(job_root, name)
        except FileNotFoundError:
            reports[name] = {'state': 'not-produced'}
        except (OSError, ValueError) as error:
            reports[name] = {'state': 'refused', 'reason': str(error)}
    record = {'schema': 1, 'sourceCommit': source_commit,
              'host': os.uname().nodename, 'canonicalBuildCommandSucceeded': build_succeeded,
              'failure': failure, 'reports': reports,
              'scope': 'Text diagnostics only. Missing, invalid or refused reports do not qualify a bundle; the final artifact receipt and command status remain authoritative.'}
    temporary = output / ('hardware-qualification.log.new-' + str(os.getpid()))
    try:
        with temporary.open('x') as stream:
            json.dump(record, stream, indent=2)
            stream.write('\n')
        temporary.chmod(0o644)
        temporary.replace(output / 'hardware-qualification.log')
    finally:
        temporary.unlink(missing_ok=True)


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--phase', choices=('working', 'full'), default='working')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    if (root.name != 'source' or root.parent.parent.parent.name != 'jobs' or
            root.parent.parent.name != 'hardware-r29'):
        raise ValueError('Use only this lane in the published temporary exact-commit remote runner')
    job_root, host_work = root.parent, root.parents[3]
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    owner_spec = importlib.util.spec_from_file_location('ffmpeg_runner_output_owner',
        root / 'scripts/repair-ffmpeg-runner-ownership.py')
    owner = importlib.util.module_from_spec(owner_spec)
    owner_spec.loader.exec_module(owner)
    def stop_owned_job(signum, _frame):
        # A dropped SSH/runner parent must not orphan a Docker compiler. Only
        # this existing lane's labelled containers are in our custody.
        for received in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
            signal.signal(received, signal.SIG_IGN)
        if os.getpgrp() == os.getpid():
            os.killpg(os.getpid(), signal.SIGTERM)
        found = subprocess.run(['docker', 'ps', '-q', '--filter', 'label=portico.lane=hardware-r29'],
                               stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, timeout=10)
        ids = found.stdout.split()
        if ids:
            subprocess.run(['docker', 'stop', '-t', '15', *ids], timeout=35)
        owner.restore_job_ownership(job_root, lock['linuxSourceBuild']['compilerImage'])
        raise SystemExit(128 + signum)

    for received in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(received, stop_owned_job)
    parent = os.getppid()
    if ctypes.CDLL(None, use_errno=True).prctl(1, signal.SIGTERM, 0, 0, 0) != 0:
        raise OSError(ctypes.get_errno(), 'Cannot bind compiler custody to the remote runner parent')
    if os.getppid() != parent:
        stop_owned_job(signal.SIGTERM, None)
    os.environ['PORTICO_RUNNER_SOURCE_COMMIT'] = args.source_commit
    scratch = job_root / '.scratch/hardware-r29'
    scratch.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, PORTICO_LANE_SCRATCH=str(scratch), PORTICO_BUILD_JOBS='2',
        PORTICO_FFMPEG_PHASE=args.phase,
        PORTICO_DISC_SOURCE_COMMIT=lock['discHelper']['sourceCommit'],
        PORTICO_DISC_FIXTURES=str(root / lock['discHelper']['fixtureManifest']),
        PORTICO_DISC_FIXTURES_SHA256=lock['discHelper']['fixtureManifestSHA256'],
        PORTICO_BUILD_SOURCE_NETWORK='host',
        TMPDIR=str(scratch / 'tmp'), GOTMPDIR=str(scratch / 'go-tmp'))
    for directory in ('tmp', 'go-tmp'):
        (scratch / directory).mkdir()
    fs = os.statvfs(job_root)
    available = int(next(line.split()[1] for line in Path('/proc/meminfo').read_text().splitlines()
                         if line.startswith('MemAvailable:'))) * 1024
    # This is this job's actual phase preflight, in addition to the shared
    # runner. It is not a retry loop or a separate admission mechanism.
    disk_peak, ram_cap = 18 * 2**30, 4 * 2**30
    preflight = {'host': os.uname().nodename, 'sourceCommit': args.source_commit,
        'availableDiskBytes': fs.f_bavail * fs.f_frsize, 'availableRAMBytes': available,
        'additionalDiskPeakBytes': disk_peak, 'containerRAMCapBytes': ram_cap,
        'compilerWorkers': 2, 'scratch': str(scratch),
        'retainedArtifactRoot': str(host_work / 'artifacts/hardware-r29'),
        'scope': 'One source-built Linux-x64 target; no physical GPU or other-target claim'}
    print(json.dumps({'preflight': preflight}, indent=2), flush=True)
    if preflight['availableDiskBytes'] < disk_peak + 20 * 2**30 or available < ram_cap:
        print('REFUSED: this FFmpeg job peak does not fit; continue code-only', flush=True)
        return 75
    collect_qualification_reports(job_root, args.source_commit, None,
        'Canonical build command has not started; source/preflight details remain in runner.log.')
    subprocess.run(['python3', str(root / 'scripts/check-ffmpeg-source.py'),
                    '--archive-commit', args.source_commit, '--archive-rejections'],
                   cwd=root, env=env, check=True)
    # Verify capability before a lengthy source dependency intake. No sudo,
    # daemon configuration, host package installation or private auth is used.
    subprocess.run(['docker', 'version', '--format', '{{.Server.Version}}'], check=True)
    active = subprocess.check_output(['docker', 'ps', '-q', '--filter', 'label=portico.lane=hardware-r29'], text=True)
    if active.strip():
        print('REFUSED: an owned hardware compiler is already active; no duplicate job', flush=True)
        return 75
    subprocess.run(['docker', 'run', '--rm', '--name', 'portico-hardware-r29-source-network',
        '--label', 'portico.lane=hardware-r29', '--network', env['PORTICO_BUILD_SOURCE_NETWORK'],
        '--cpus', '0.25', '--memory', '128m', '--memory-swap', '128m',
        lock['linuxSourceBuild']['compilerImage'], 'bash', '-c',
        'timeout 10 getent ahostsv4 github.com gitlab.freedesktop.org index.crates.io'], check=True)
    build_succeeded, build_failure = False, None
    try:
        subprocess.run(['bash', str(root / 'scripts/build-ffmpeg.sh'), 'linux-x64'],
                       cwd=root, env=env, check=True)
        build_succeeded = True
    except BaseException as error:
        build_failure = (type(error).__name__ + ': ' + str(error))[:4000]
        raise
    finally:
        try:
            owner.restore_job_ownership(job_root, lock['linuxSourceBuild']['compilerImage'])
        finally:
            collect_qualification_reports(job_root, args.source_commit, build_succeeded, build_failure)
    work = scratch / 'ffmpeg-build/linux-x64'
    working = args.phase == 'working'
    archive = work / ('portico-ffmpeg-linux-x64-working.tar.xz' if working else 'portico-ffmpeg-linux-x64.tar.xz')
    manifest = work / 'bundle' / ('working-manifest.json' if working else 'toolchain-manifest.json')
    if not archive.is_file() or not manifest.is_file():
        raise ValueError('Successful canonical build did not produce the required bundle')
    artifact_parent = host_work / 'artifacts/hardware-r29' / args.source_commit
    artifact_parent.mkdir(parents=True, exist_ok=True)
    destination = artifact_parent / job_root.name
    staging = destination.with_name(destination.name + '.new')
    staging.mkdir()
    try:
        # This is the requested retained product artifact, not the disposable
        # source/tmp tree. Preserve one payload only and return text by reference.
        archive.rename(staging / archive.name)
        shutil.copyfile(manifest, staging / manifest.name)
        reports = [work / name for name in ('feature-report-bundle.json', 'disc-report-bundle.json',
                   'runtime-report-bundle.json', 'abi-report-bundle.json', 'repeat-build.json')]
        reports += [work / 'linux-output' / name for name in
                    ('dovi-profile-build.json', 'dovi-video-build.json')]
        for path in reports:
            if working and not path.exists():
                continue
            shutil.copyfile(path, staging / path.name)
        if working:
            for tool in ('ffmpeg', 'ffprobe', 'portico-disc'):
                shutil.copy2(work / 'bundle/bin' / tool, Path(os.environ['PORTICO_RUNNER_OUTPUT']) / tool)
        hashes = {p.name: digest(p) for p in sorted(staging.iterdir()) if p.is_file()}
        receipt = {'schema': 1, 'host': os.uname().nodename,
            'at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
            'buildSourceCommit': args.source_commit, 'ffmpegSourceCommit': lock['ffmpeg']['commit'],
            'artifactPath': str(destination / archive.name), 'files': hashes,
            'qualifiedTargetVerification': not working, 'mainConsumerAdmission': False,
            'retention': 'Retain this verified product until explicit consumer release; per-job source/tmp is disposable.',
            'scope': ('First working Linux source bundle with licences and corresponding source; full target/repeat qualification pending.' if working else 'One Linux source bundle/generated fixtures/ABI/licences/repeat proof. No physical GPU, real Atmos/JOC, mastered FEL, other targets or full HW1-HW7 completion.'),
            'consumerFence': 'This exact product source must be normally merged on MAIN before feature consumers use the artifact.'}
        (staging / 'artifact-receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
        staging.rename(destination)
    except BaseException:
        # A partial artifact was never announced or handed to a consumer.
        shutil.rmtree(staging)
        raise
    print(json.dumps({'retainedWorkingArtifact' if working else 'retainedVerifiedArtifact': receipt}, indent=2), flush=True)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
