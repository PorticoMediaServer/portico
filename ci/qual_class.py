#!/usr/bin/env python3
"""Canonical per-class execution transport; never a qualification verdict."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import threading
import subprocess
import time

import qual_fixture as q
import qual_spool as spool

CHECKPOINT_CASES = dict(checkpoint_1mib='1MiB', checkpoint_8mib='8MiB', checkpoint_64mib='64MiB', checkpoint_hardcap='HardCap')
TARGETS = {
    'inventory_n1': 'TestCanonicalHeldInventoryN1',
    'basic_accept': 'TestCanonicalHeldBasic',
    'staged_enrich': 'TestCanonicalHeldStagedEnrich',
    'progress': 'TestCanonicalHeldProgress',
    'security': 'TestCanonicalHeldSecurity',
    'wc_reclaim': 'TestCanonicalHeldWCReclaim',
    'publisher_fragment': 'TestCanonicalHeldPublisherFragment',
    'publisher_flip': 'TestCanonicalHeldPublisherFlip',
}
TARGETS.update({key:'TestDiagnosticConcurrentKeeper/'+case for key,case in CHECKPOINT_CASES.items()})
COMPATIBILITY_PATHS = [
    'server/go.mod', 'server/go.sum', 'server/internal/store/schema/schema.sql',
    'server/internal/store/writer/qualification_population_seed_test.go',
    'server/internal/store/writer/scan_metadata.go', 'server/internal/sorttext',
]
ATTEMPTS = 10000
# Transport-only ceiling: 30k role records at the retained ~235kB/record
# exceed the old4GiB guard. Streaming keeps this independent of process RAM.
MAX_STREAM_BYTES = 16 << 30
RAW_MEMORY_BYTES = spool.MEMORY_BYTES


def identity(value, name, maximum=20):
    q.require(isinstance(value, str) and re.fullmatch(r'[1-9][0-9]{0,' + str(maximum-1) + '}', value), 'invalid ' + name)
    return int(value)


def inputs(env):
    purpose=env.get('INPUT_PURPOSE','qualification')
    q.require(purpose in ('qualification','screening'),'closed purpose required')
    source = env.get('INPUT_SOURCE_COMMIT', '')
    manifest = env.get('INPUT_MANIFEST_SHA256', '')
    name = env.get('INPUT_CLASS', '')
    profile = env.get('PROFILE', '')
    q.require(re.fullmatch(q.COMMIT, source) and re.fullmatch(q.SHA, manifest), 'full source/manifest pins required')
    q.require(name in TARGETS and profile in ('standard', 'legacy8'), 'closed class/profile required')
    q.require(name not in CHECKPOINT_CASES or purpose=='screening','checkpoint components are SCREENING only')
    return dict(source=source, manifest=manifest, name=name, profile=profile, purpose=purpose,
                replicate=q.uint(env.get('REPLICATE', ''), 1, 3, 'replicate'),
                producer_run=identity(env.get('INPUT_PRODUCER_RUN', ''), 'producer run'),
                producer_attempt=identity(env.get('INPUT_PRODUCER_ATTEMPT', '1'), 'producer attempt', 6),
                workflow_run=identity(env.get('GITHUB_RUN_ID', ''), 'workflow run'),
                workflow_attempt=identity(env.get('GITHUB_RUN_ATTEMPT', ''), 'workflow attempt', 6))


def verify_stream(path, cfg, compressed=False):
    """Only wire/pin/completion verification. No quantiles or accepted-count inference."""
    q.regular(path, spool.COMPRESSED_BYTES if compressed else MAX_STREAM_BYTES)
    first = last = None
    count, size, digest = 0, 0, hashlib.sha256()
    with (gzip.open(path, 'rb') if compressed else path.open('rb')) as f:
        while line := f.readline((2 << 20) + 2):
            q.require(len(line) <= (2 << 20) + 1 and line.endswith(b'\n'), 'incomplete/oversized raw record')
            size += len(line); digest.update(line)
            q.require(size <= MAX_STREAM_BYTES, 'decompressed raw bound exceeded')
            record = json.loads(line, object_pairs_hook=q.no_duplicates)
            count += 1
            q.require(count <= 1000000 and type(record) is dict, 'raw record bound/type')
            q.require(record.get('schema') == 'portico-canonical-held-class-v1'
                      and type(record.get('sequence')) is int and record['sequence'] == count,
                      'raw schema/sequence mismatch')
            for raw, key in [('run_id', 'run_id'), ('host_epoch', 'host_epoch'),
                             ('declared_source_commit', 'source_commit'), ('profile', 'profile'), ('class', 'class')]:
                q.require(record.get(raw) == cfg[key], 'raw identity mismatch: ' + raw)
            q.require(record.get('kind') != 'failed_binding', 'source-bound class binding unavailable')
            if first is None:
                first = record
            else:
                q.require(last.get('kind') != 'final', 'record after final')
            last = record
    q.require(first and first.get('kind') == 'start' and last.get('kind') == 'final'
              and last.get('complete') is True, 'missing/incomplete owner final')
    return dict(records=count, raw_sha256=digest.hexdigest(), owner_final=last,
                qualification_verdict=None, verdict_reason='Owner raw evidence still needs per-recipe reduction and protocol gates')


def verify_checkpoint_stream(path, case):
    q.regular(path,spool.COMPRESSED_BYTES)
    count=0
    with gzip.open(path,'rb') as f:
        for line in iter(lambda:f.readline((2<<20)+2),b''):
            q.require(len(line)<=(2<<20)+1,'oversized checkpoint record')
            if b'GV_KEEPER_CONCURRENT_V1 ' not in line: continue
            record=json.loads(line.split(b'GV_KEEPER_CONCURRENT_V1 ',1)[1],object_pairs_hook=q.no_duplicates)
            q.require(record['Cell']==case and record['Kind']=='concurrent','failed/mismatched checkpoint cell')
            q.require(not record['CheckpointError'] and record['Checkpoint']['Refusal'] is None and record['Checkpoint']['PhysicalSafe'] is True,'failed checkpoint retained')
            q.require(type(record['CheckpointHoldNS']) is int and record['CheckpointHoldNS']>0,'missing checkpoint hold')
            count+=1
            q.require(count<=ATTEMPTS,'excess checkpoint samples')
    q.require(count==ATTEMPTS,'incomplete checkpoint population')
    return dict(records=count,case=case,qualification_verdict=None,reason='SCREENING component only; raw maintenance p99 is not a normal-writer/1M gate')


def profile_properties(profile):
    q.require(profile in ('standard', 'legacy8'), 'invalid profile')
    return [f'MemoryMax={"infinity" if profile == "standard" else "8589934592"}',
            f'MemorySwapMax={"infinity" if profile == "standard" else "0"}',
            f'CPUQuota={"400%" if profile == "standard" else "200%"}',
            'CPUQuotaPeriodSec=100ms', 'AllowedCPUs=0-3', 'CPUAccounting=yes',
            'MemoryAccounting=yes', 'IOAccounting=yes', 'NoNewPrivileges=yes',
            'RuntimeMaxSec=80min', 'KillMode=control-group']


def verify_producer(receipt, run, cfg):
    q.require(receipt.get('status') == 'complete' and receipt.get('inputs', {}).get('purpose') == 'qualification', 'producer receipt incomplete/preview')
    q.require(str(receipt.get('run_id')) == str(cfg['producer_run'])
              and str(receipt.get('run_attempt')) == str(cfg['producer_attempt'])
              and receipt.get('manifest_sha256') == cfg['manifest'], 'producer receipt identity mismatch')
    q.require(type(run.get('id')) is int and run['id'] == cfg['producer_run']
              and type(run.get('run_attempt')) is int and run['run_attempt'] == cfg['producer_attempt']
              and run.get('status') == 'completed' and run.get('conclusion') == 'success'
              and run.get('path') == '.github/workflows/store-qualification-fixture.yml'
              and run.get('head_branch') == 'main' and run.get('event') == 'workflow_dispatch'
              and run.get('head_sha') == receipt.get('tooling_commit'), 'successful main producer attempt not proven')


def prepare(source, artifact, output, work, producer_run):
    cfg = inputs(os.environ)
    q.require(all(p.is_absolute() and p == p.resolve() for p in (source, artifact, output, work)), 'canonical absolute paths required')
    q.require(not output.exists() and not work.exists() and source.is_dir() and artifact.is_dir(), 'fresh own output/work required')
    paths = (source, artifact, output, work)
    q.require(not any(a.is_relative_to(b) for a in paths for b in paths if a is not b), 'disjoint task paths required')
    q.require((cfg['purpose']=='screening' or os.environ.get('GITHUB_REF') == 'refs/heads/main')
              and os.environ.get('GITHUB_ACTIONS') == 'true'
              and os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted', 'qualification requires main tooling; all runs require standard runner')
    output.mkdir(mode=0o700)
    work.mkdir(mode=0o700)
    report = output
    receipt = dict(version='portico-canonical-class-execution-v1', status='failed', qualification_verdict=None,
                   declared=cfg, execution_purpose=cfg['purpose'], sign_off_eligible=cfg['purpose']=='qualification', attempts_requested=ATTEMPTS, ordinary_refusal_budget=0,
                   run_url=f"https://github.com/{os.environ.get('GITHUB_REPOSITORY', '')}/actions/runs/{os.environ.get('GITHUB_RUN_ID', '')}")
    q.atomic_json(report / 'receipt.json', receipt)
    env = q.clean_env(work)
    def cmd(argv, cwd, name, timeout=300):
        return q.command(argv, cwd, env, report, name, timeout)
    try:
        cmd(['git', 'fetch', '--no-tags', 'origin', cfg['source']], source, 'fetch-source')
        cmd(['git', 'checkout', '--detach', cfg['source']], source, 'checkout')
        actual = cmd(['git', 'rev-parse', 'HEAD'], source, 'actual-source').read_text().strip()
        q.require(actual == cfg['source'], 'source checkout mismatch')
        cmd(['git', 'fetch', '--no-tags', 'origin', 'refs/heads/main'], source, 'fetch-main')
        # SCREENING may use an unmerged draft; only qualification requires a
        # merged ancestor. The receipt records the exact SHA and purpose.
        if cfg['purpose'] != 'screening':
            cmd(['git', 'merge-base', '--is-ancestor', actual, 'FETCH_HEAD'], source, 'merged-source')
        module = source / 'server'
        q.require(re.findall(r'^go\s+(\S+)\s*$', (module / 'go.mod').read_text(), re.M) == ['1.26.10'], 'Go pin drift')
        q.require(cmd(['go', 'version'], module, 'go-version').read_text().strip() == 'go version go1.26.10 linux/amd64', 'toolchain drift')
        schema = q.file_hash(module / 'internal/store/schema/schema.sql')
        q.require(re.findall(r'const SHA256 = "([0-9a-f]{64})"', (module / 'internal/store/schema/schema.go').read_text()) == [schema], 'embedded schema fingerprint mismatch')
        q.regular(artifact / 'report/receipt.json', 65536)
        producer_receipt = json.loads((artifact / 'report/receipt.json').read_text(), object_pairs_hook=q.no_duplicates)
        run_receipt = json.loads(producer_run.read_text(), object_pairs_hook=q.no_duplicates)
        verify_producer(producer_receipt, run_receipt, cfg)
        receipt['producer_attempt_api'] = {key: run_receipt[key] for key in ('id', 'run_attempt', 'status', 'conclusion', 'path', 'head_branch', 'event', 'head_sha')}
        q.regular(artifact / 'population.json', 65536)
        q.require(q.file_hash(artifact / 'population.json') == cfg['manifest'], 'producer manifest pin mismatch')
        m = json.loads((artifact / 'population.json').read_text(), object_pairs_hook=q.no_duplicates)
        producer = m['source_commit']
        q.require(producer_receipt.get('source_commit') == producer, 'producer source differs from manifest')
        population, _ = q.validate_artifact(artifact, source=producer, schema=schema, seed=m['facts']['seed'],
                                          residents=1000000, manifest_sha=cfg['manifest'])
        cmd(['git', 'merge-base', '--is-ancestor', producer, actual], source, 'producer-ancestry')
        cmd(['git', 'diff', '--quiet', producer, actual, '--', *COMPATIBILITY_PATHS], source, 'population-compatibility')
        archive = work / 'source.tar'
        cmd(['git', 'archive', '--format=tar', '-o', str(archive), actual], source, 'source-archive')
        receipt.update(source_commit=actual, archive_sha256=q.file_hash(archive), population=population,
                       producer_manifest_sha256=cfg['manifest'], compatibility_paths=COMPATIBILITY_PATHS,
                       timing_stratum='source-owned; raw warmup/connection evidence required, never inferred by dispatcher',
                       fixture_cache_state='download/hash/private-copy setup may warm OS cache; not OS-cold',
                       profile_properties=profile_properties(cfg['profile']))
        archive.unlink()
        binary, host = work / 'writer.test', work / 'host'
        cmd(['go', 'test', '-p', '1', '-c', '-o', str(binary), './internal/store/writer'], module, 'build-target', 1200)
        cmd(['go', 'build', '-p', '1', '-o', str(host), './internal/store/qual/cmd/host'], module, 'build-host', 600)
        metadata = cmd(['go', 'version', '-m', str(binary)], module, 'binary-build-info').read_text()
        template = '{{if eq .ImportPath "modernc.org/sqlite/lib"}}{{.ImportPath}} {{.Module.Version}} {{if .Module.Replace}}REPLACED{{else}}ORIGINAL{{end}}{{end}}{{if eq .ImportPath "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"}}{{.ImportPath}} BUNDLED{{end}}{{if eq .ImportPath "modernc.org/sqlite"}}{{.ImportPath}} ROOT_DRIVER{{end}}'
        selected = cmd(['go', 'list', '-deps', '-test', '-f', template, './internal/store/writer'], module, 'sqlite-dependencies').read_text()
        receipt.update(binary_sha256=q.file_hash(binary), host_binary_sha256=q.file_hash(host),
                       binary_build_info=metadata, sqlite_dependencies=q.validate_sqlite_build_identity(metadata, selected))
        q.require(os.getuid() > 0 and os.getgid() > 0, 'target must retain unprivileged runner identity')
        env['GOMAXPROCS'] = '4' if cfg['profile'] == 'standard' else '2'
        launch = dict(cfg=cfg, source=str(source), artifact=str(artifact), output=str(output), work=str(work),
                      env=env, uid=os.getuid(), gid=os.getgid(), binary_sha256=receipt['binary_sha256'],
                      host_sha256=receipt['host_binary_sha256'], archive_sha256=receipt['archive_sha256'])
        q.atomic_json(work / 'launch.json', launch)
        receipt['status'] = 'prepared-only'
    except BaseException as error:
        receipt['error'] = f'{type(error).__name__}: {error}'
        raise
    finally:
        q.atomic_json(report / 'receipt.json', receipt)
        print(receipt['run_url'], flush=True)


def own_cgroup_members():
    paths = [line.split(':', 2)[2] for line in Path('/proc/self/cgroup').read_text().splitlines() if line.startswith('0::')]
    q.require(len(paths) == 1 and paths[0].startswith('/') and paths[0] != '/'
              and '..' not in Path(paths[0]).parts, 'own transient cgroup path unproven')
    values = (Path('/sys/fs/cgroup') / paths[0].lstrip('/') / 'cgroup.procs').read_text().split()
    q.require(all(re.fullmatch(r'[1-9][0-9]*', value) for value in values), 'cgroup process evidence malformed')
    return sorted(int(value) for value in values)


def run(launch_path):
    q.require(os.geteuid() == 0, 'root supervisor required for readable real-root host evidence')
    launch = json.loads(launch_path.read_text(), object_pairs_hook=q.no_duplicates)
    output, work = Path(launch['output']), Path(launch['work'])
    cfg = launch['cfg']
    q.require(type(launch.get('uid')) is int and launch['uid'] > 0
              and type(launch.get('gid')) is int and launch['gid'] > 0, 'unprivileged target UID/GID required')
    env = launch['env']
    binary, host = work / 'writer.test', work / 'host'
    q.require(q.file_hash(binary) == launch['binary_sha256'] and q.file_hash(host) == launch['host_sha256'], 'binary changed before execution')
    receipt = json.loads((output / 'receipt.json').read_text())
    boot = Path('/proc/sys/kernel/random/boot_id').read_text().strip()
    q.require(re.fullmatch(r'[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}', boot), 'actual boot identity missing')
    c = dict(run_id=f"gh-{cfg['workflow_run']}-{cfg['workflow_attempt']}-{cfg['name']}-{cfg['profile']}-{cfg['replicate']}",
             host_epoch=f"{boot}-k{os.uname().release}", source_commit=cfg['source'],
             output_path=str(work.parent / 'qual-class-memory' / 'raw.sock'), **{'class': cfg['name']}, profile=cfg['profile'],
             attempts=ATTEMPTS, artifact_directory=launch['artifact'], manifest_sha256=cfg['manifest'])
    q.atomic_json(work / 'config.json', c)
    env.update(PORTICO_CANONICAL_HELD_STREAM='unix-gzip-v1', PORTICO_CANONICAL_HELD='1', PORTICO_CANONICAL_HELD_CONFIG=str(work / 'config.json'))
    receipt.update(actual_config=c, status='failed', target_uid=launch['uid'], target_gid=launch['gid'],
                   disk_before_target=shutil.disk_usage(work)._asdict(),
                   host_scope='same cgroup/namespace as target; collector root, target unprivileged',
                   qualification_verdict=None)
    receipt['raw_spool'] = memory_evidence(work.parent / 'qual-class-memory', work)
    raw = Path(c['output_path'])
    checkpoint=cfg['name'] in CHECKPOINT_CASES
    if checkpoint:
        env.update(PORTICO_GV_NATIVE_TIMING='1',PORTICO_SANDBOX_IO_BYTES_PER_SEC='unrestricted')
        receipt.update(execution_purpose='screening',sign_off_eligible=False,raw_format='GV_KEEPER_CONCURRENT_V1 verbose records',measured_population_scope='existing GV small component, NOT canonical1M; downloaded seed is unused')
        q.atomic_json(raw.parent/'engine-path.json',dict(path=spool.cgroup()))
        os.chown(raw.parent/'engine-path.json',launch['uid'],launch['gid'])
    host_args = [str(host), '-source', cfg['source'], '-archive-sha256', launch['archive_sha256'],
                 '-profile', cfg['profile'], '-replicate', str(cfg['replicate'])]
    try:
        before = q.command(host_args, Path(launch['source']), env, output, 'host-before', 180)
        receipt['host_before'] = json.loads(before.read_text())
        argv = [str(binary), '-test.run=^' + TARGETS[cfg['name']] + '$', '-test.count=1',
                '-test.timeout=75m', '-test.v', '-canonical-held=true']
        if checkpoint:
            argv=[str(binary),'-test.run=^'+TARGETS[cfg['name']]+'$','-test.count='+str(ATTEMPTS),'-test.failfast','-test.timeout=75m','-test.v']
        q.atomic_json(output / 'target.argv.json', dict(argv=argv, uid=launch['uid'], gid=launch['gid']))
        with (output / 'target.log').open('wb') as log:
            receipt['launch_before_monotonic_ns'] = time.monotonic_ns()
            channel = socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) if checkpoint else None
            if channel is not None: channel.connect(str(raw))
            try:
                process = subprocess.Popen(argv, cwd=Path(launch['source']) / 'server/internal/store/writer', env=env,
                                       stdout=channel if checkpoint else log, stderr=subprocess.STDOUT, start_new_session=True,
                                       user=launch['uid'], group=launch['gid'], extra_groups=[])
            finally:
                if channel is not None: channel.close() # child alone owns EOF
            code = None
            try:
                code = process.wait(timeout=75*60+30)
            except BaseException:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait()
                raise
            finally:
                receipt['process_joined_monotonic_ns'] = time.monotonic_ns()
                receipt['target_exit_code'] = code
                receipt['engine_memory_peak_bytes'] = spool.engine_memory_peak()
                receipt['raw_capture'] = spool.wait_capture(raw.parent)
        receipt['target_exit_code'] = code
        q.require(code == 0, 'owner target failed; retain every refusal/error/partial record')
        receipt.update(wire=verify_checkpoint_stream(raw.parent/'raw.ndjson.gz',CHECKPOINT_CASES[cfg['name']]) if checkpoint else verify_stream(raw.parent/'raw.ndjson.gz', c, compressed=True), status='owner-execution-complete')
    except BaseException as error:
        receipt.update(status='failed', error=f'{type(error).__name__}: {error}')
        raise
    finally:
        try:
            after = q.command(host_args, Path(launch['source']), env, output, 'host-after', 180)
            receipt['host_after'] = json.loads(after.read_text())
            receipt['remaining_cgroup_pids'] = own_cgroup_members()
            q.require(receipt['remaining_cgroup_pids'] == [os.getpid()], 'unjoined descendant process remains')
        except BaseException as error:
            receipt.update(status='failed', host_after_error=f'{type(error).__name__}: {error}')
        receipt['disk_after_target'] = shutil.disk_usage(work)._asdict()
        q.atomic_json(output / 'receipt.json', receipt)
        q.require(receipt['status'] == 'owner-execution-complete', 'cell nonpassing; see retained receipt/raw')


def plan():
    cfg = inputs(os.environ)
    path = os.environ['GITHUB_OUTPUT']
    with open(path, 'a') as f:
        f.write(f"producer_run={cfg['producer_run']}\n")
        f.write(f"producer_attempt={cfg['producer_attempt']}\n")
        f.write(f"producer_artifact=qual-population-{cfg['producer_run']}-{cfg['producer_attempt']}\n")


def pack_raw(output, receipt, raw=None):
    raw = output/'raw.ndjson' if raw is None else raw
    stored = output/'raw.ndjson.gz'
    if not raw.exists():
        return
    q.regular(raw, MAX_STREAM_BYTES)
    q.require(not stored.exists(), 'compressed raw path already exists; preserve originals')
    started = time.monotonic_ns()
    digest, size = hashlib.sha256(), 0
    with raw.open('rb') as source, stored.open('xb') as target:
        with gzip.GzipFile(filename='', mode='wb', fileobj=target, mtime=0, compresslevel=1) as compressed:
            for block in iter(lambda: source.read(1 << 20), b''):
                digest.update(block); size += len(block); compressed.write(block)
    actual = digest.hexdigest()
    wire = receipt.get('wire')
    if receipt.get('status') == 'owner-execution-complete':
        q.require(wire and wire.get('raw_sha256') == actual, 'verified raw digest changed before compression')
    restored, restored_size = hashlib.sha256(), 0
    with gzip.open(stored, 'rb') as source:
        for block in iter(lambda: source.read(1 << 20), b''):
            restored.update(block); restored_size += len(block)
    q.require(restored_size == size and restored.hexdigest() == actual, 'compressed raw round-trip mismatch')
    q.atomic_json(output/'raw-storage.json', dict(version=1, original_path=raw.name,
                  original_bytes=size, original_sha256=actual, stored_path=stored.name,
                  stored_bytes=stored.stat().st_size, stored_sha256=q.file_hash(stored),
                  verified_roundtrip=True, wire_verified=bool(wire),
                  compression_started_monotonic_ns=started,
                  compression_returned_monotonic_ns=time.monotonic_ns(),
                  disk_before_original_removal=shutil.disk_usage(output)._asdict()))
    raw.unlink()  # only after independent round-trip and stored metadata


def memory_evidence(root, database):
    q.require(root.is_absolute() and root == root.resolve() and root.name == 'qual-class-memory', 'fixed canonical memory spool required')
    result = subprocess.run(['findmnt', '--json', '--mountpoint', str(root), '--output', 'TARGET,FSTYPE,SOURCE,OPTIONS'],
                            check=True, capture_output=True, text=True, timeout=30)
    mounts = json.loads(result.stdout, object_pairs_hook=q.no_duplicates)['filesystems']
    q.require(len(mounts)==1 and mounts[0]['target']==str(root) and mounts[0]['fstype']=='tmpfs'
              and mounts[0]['source']=='portico-class-raw'
              and {'noswap','nosuid','nodev','noexec'}.issubset(mounts[0].get('options','').split(',')), 'own tmpfs mount not proven')
    stat = os.statvfs(root)
    q.require(stat.f_blocks*stat.f_frsize==RAW_MEMORY_BYTES and root.stat().st_dev!=database.stat().st_dev,
              'memory bound/database-filesystem separation not proven')
    return dict(path=str(root), filesystem=mounts[0], limit_bytes=RAW_MEMORY_BYTES,
                device=root.stat().st_dev, database_device=database.stat().st_dev,
                accounting='external gzip observer and shmem outside engine cgroup; excluded from EC5 accounting')


def memory_setup(root, uid, gid):
    q.require(os.geteuid()==0 and uid>0 and gid>0 and root.is_absolute() and root==root.resolve()
              and root.name=='qual-class-memory' and not root.exists() and not (root.parent/'qual-class-memory-owner.json').exists(), 'fresh own root/runner identity required')
    root.mkdir(mode=0o700)
    q.atomic_json(root.parent/'qual-class-memory-owner.json', dict(path=str(root), source='portico-class-raw', uid=uid, gid=gid))
    subprocess.run(['mount', '-t', 'tmpfs', '-o', f'size={RAW_MEMORY_BYTES},noswap,nosuid,nodev,noexec,mode=0700,uid={uid},gid={gid}',
                    'portico-class-raw', str(root)], check=True, timeout=30)
    memory_evidence(root, root.parent)


def persist_memory(root, output):
    retired_unit() # observer verification already completed before engine retirement
    output.mkdir(parents=True, exist_ok=True)
    memory_evidence(root, output)
    receipt_path=output/'receipt.json'
    receipt=json.loads(receipt_path.read_text(), object_pairs_hook=q.no_duplicates) if receipt_path.exists() else dict(status='failed', qualification_verdict=None)
    raw=root/'raw.ndjson.gz'
    try:
        if (root/'capture.json').exists():
            capture=json.loads((root/'capture.json').read_text(), object_pairs_hook=q.no_duplicates)
            q.atomic_json(output/'raw-capture.json', capture)
        else:
            capture=dict(complete=False, error='gzip observer did not return a receipt')
        q.require(raw.exists(), 'observer gzip missing')
        if raw.exists():
            q.regular(raw, spool.COMPRESSED_BYTES)
            capture_bound=(raw.stat().st_size==capture.get('stored_bytes') and q.file_hash(raw)==capture.get('stored_sha256'))
            complete=capture.get('complete') is True and capture_bound
            target=output/'raw.ndjson.gz'
            q.require(not target.exists(), 'stored gzip already exists; preserve originals')
            shutil.copyfile(raw, target)
            q.require(raw.stat().st_size==target.stat().st_size and q.file_hash(raw)==q.file_hash(target), 'gzip persistence mismatch')
            if complete:
                q.require(spool.decoded_digest(target)==(capture['original_bytes'],capture['original_sha256']), 'persisted gzip round-trip mismatch')
            else:
                receipt.update(status='failed', observer_error=capture.get('error', 'capture digest/length mismatch'), qualification_verdict=None)
            q.atomic_json(output/'raw-storage.json', dict(capture, complete=complete, capture_digest_matches=capture_bound, stored_path=target.name,
                          stored_bytes=target.stat().st_size, stored_sha256=q.file_hash(target)))
            raw.unlink() # only the byte-verified compressed original is removed
        if (root/'capture.json').exists(): (root/'capture.json').unlink()
        q.require(complete, capture.get('error', 'gzip observer incomplete or digest changed'))
    except (OSError, ValueError, EOFError) as error:
        receipt.update(status='failed', compression_error=str(error), qualification_verdict=None)
        raise
    finally:
        q.atomic_json(receipt_path, receipt)


def observe_launch(launch_path):
    launch=json.loads(launch_path.read_text(), object_pairs_hook=q.no_duplicates)
    root=Path(launch['work']).parent/'qual-class-memory'
    memory_evidence(root, Path(launch['work']))
    argv=['sudo','systemd-run','--unit=portico-canonical-class','--wait','--pipe','--collect']
    for prop in profile_properties(launch['cfg']['profile']): argv.append('--property='+prop)
    argv += ['/usr/bin/python3','-B',str(Path(__file__).resolve()),'run','--launch',str(launch_path)]
    stop=threading.Event()
    sampler=threading.Thread(target=spool.timeline,args=(root,stop),daemon=True) if launch['cfg']['name'] in CHECKPOINT_CASES else None
    if sampler: sampler.start()
    try:
        code=spool.observe(root, lambda: subprocess.run(argv, check=False, timeout=82*60).returncode)
    finally:
        if sampler:
            stop.set();sampler.join(timeout=30)
            q.require(not sampler.is_alive(),'timeline observer unjoined')
            retired_unit()
            for name in ('timeline.jsonl.gz','timeline.json','engine-path.json'):
                source=root/name
                if source.exists():
                    target=Path(launch['output'])/name
                    shutil.copyfile(source,target)
                    q.require(q.file_hash(source)==q.file_hash(target),'timeline persistence mismatch')
                    source.unlink()
    if sampler:
        timeline=json.loads((Path(launch['output'])/'timeline.json').read_text())
        if not timeline.get('complete'):
            receipt=Path(launch['output'])/'receipt.json'
            r=json.loads(receipt.read_text());r.update(status='failed',timeline_error=timeline.get('error'),qualification_verdict=None);q.atomic_json(receipt,r)
        q.require(timeline.get('complete') is True,'checkpoint timeline incomplete; prefix retained')
    q.require(code==0, 'engine cell failed; compressed evidence retained')


def memory_cleanup(root, output):
    retired_unit()
    q.require(os.geteuid()==0 and root.is_absolute() and root==root.resolve() and root.name=='qual-class-memory', 'own root required')
    marker=root.parent/'qual-class-memory-owner.json'
    if root.exists():
        q.require(marker.is_file() and json.loads(marker.read_text()).get('path')==str(root), 'observer root ownership unproven')
        if os.path.ismount(root):
            memory_evidence(root, output)
            q.require(not any(root.iterdir()), 'unpersisted memory-spool data remains')
            subprocess.run(['umount', str(root)], check=True, timeout=30)
        q.require(not os.path.ismount(root), 'memory mount remains')
        root.rmdir()
    if marker.exists(): marker.unlink()
    output.mkdir(parents=True, exist_ok=True)
    q.atomic_json(output/'raw-memory-cleanup.json', dict(path=str(root), mounted=False, absent=not root.exists()))


def finalize(output):
    # Called after the unit/target has stopped, before artifact upload. Never
    # claim success just because an early failure omitted its normal receipt.
    output.mkdir(parents=True, exist_ok=True)
    if not (output / 'receipt.json').exists():
        q.atomic_json(output / 'receipt.json', dict(status='failed', qualification_verdict=None,
                      error='Preparation never produced a cell receipt; inspect job logs'))
    r = json.loads((output / 'receipt.json').read_text(), object_pairs_hook=q.no_duplicates)
    if r.get('status') == 'prepared-only':
        r.update(status='failed', error='Prepared target never completed execution')
        q.atomic_json(output / 'receipt.json', r)
    try:
        pack_raw(output, r)
    except (OSError, ValueError, EOFError) as error:
        r.update(status='failed', compression_error=str(error), qualification_verdict=None)
        q.atomic_json(output / 'receipt.json', r)
        raise
    files = []
    for path in sorted(output.iterdir()):
        if path.name == 'files.json':
            continue
        q.require(path.is_file() and not path.is_symlink(), 'unexpected output path')
        files.append(dict(path=path.name, bytes=path.stat().st_size, sha256=q.file_hash(path)))
    q.atomic_json(output / 'files.json', files)


def retired_unit():
    result = subprocess.run(['systemctl', 'show', 'portico-canonical-class.service',
                             '-p', 'LoadState', '-p', 'ActiveState', '-p', 'ControlGroup'],
                            capture_output=True, text=True, timeout=30)
    fields = {}
    for line in result.stdout.splitlines():
        key, sep, value = line.partition('=')
        q.require(sep and key not in fields, 'malformed own unit state')
        fields[key] = value
    q.require(set(fields) == {'LoadState', 'ActiveState', 'ControlGroup'}
              and result.returncode == 0 and fields.get('LoadState') in ('loaded', 'not-found')
              and fields.get('ActiveState') in ('inactive', 'failed'), 'own unit not proven stopped')
    group = fields.get('ControlGroup', '')
    if group:
        q.require(group.startswith('/') and group != '/' and '..' not in Path(group).parts, 'invalid own unit cgroup')
        events = Path('/sys/fs/cgroup') / group.lstrip('/') / 'cgroup.events'
        try:
            raw = events.read_text()
        except FileNotFoundError:
            fields['cgroup_absent'] = True
        else:
            values = {}
            for line in raw.splitlines():
                pair = line.split()
                q.require(len(pair) == 2 and pair[0] not in values and pair[1].isdigit(), 'malformed cgroup drain evidence')
                values[pair[0]] = pair[1]
            q.require(values.get('populated') == '0', 'own cgroup descendants remain')
            fields['cgroup_events'] = values
    return fields


def cleanup(keep_output=False):
    # All names derive from the runner's own fixed task paths, never dispatch
    # input. Only directories need write permission to unlink Go module files.
    temporary = Path(os.environ['RUNNER_TEMP']).resolve()
    workspace = Path(os.environ['GITHUB_WORKSPACE']).resolve()
    output = temporary / 'qual-class-out'
    output.mkdir(parents=True, exist_ok=True)
    paths = [temporary / 'qual-class-work', workspace / 'population']
    proof = dict(version=1, errors=[], paths=[])
    try:
        proof['unit'] = retired_unit()
    except (OSError, ValueError, subprocess.TimeoutExpired) as error:
        proof['errors'].append('Unit drain: ' + str(error))
    for root in ([] if proof['errors'] else paths):
        try:
            if root.is_symlink():
                root.unlink()  # never traverse a link to another task
            elif root.exists():
                for current, _, _ in os.walk(root, followlinks=False):
                    os.chmod(current, 0o700)
                shutil.rmtree(root)
            q.require(not os.path.lexists(root), 'own scratch remains')
            proof['paths'].append(dict(path=str(root), absent=True))
        except (OSError, ValueError) as error:
            proof['errors'].append(f'{root}: {error}')
    q.atomic_json(output / 'cleanup.json', proof)
    if proof['errors']:
        receipt = output / 'receipt.json'
        r = json.loads(receipt.read_text()) if receipt.exists() else {}
        r.update(status='failed', cleanup_errors=proof['errors'], qualification_verdict=None)
        q.atomic_json(receipt, r)
    q.require(not proof['errors'], 'own scratch cleanup failed; proof retained')
    if not keep_output:
        for current, _, _ in os.walk(output, followlinks=False):
            os.chmod(current, 0o700)
        shutil.rmtree(output)


def summarize(artifacts, output):
    output.mkdir(parents=True, exist_ok=True)
    cells = []
    errors = []
    seen = set()
    hosts = {'standard': set(), 'legacy8': set()}
    for receipt in sorted(artifacts.glob('canonical-class-*/receipt.json')):
        try:
            r = json.loads(receipt.read_text(), object_pairs_hook=q.no_duplicates)
            entries = json.loads((receipt.parent / 'files.json').read_text())
            q.require(len({e['path'] for e in entries}) == len(entries)
                      and {e['path'] for e in entries} == {p.name for p in receipt.parent.iterdir() if p.name != 'files.json'}, 'retained file inventory incomplete')
            for entry in entries:
                q.require(Path(entry['path']).name == entry['path'], 'invalid retained path')
                path = receipt.parent / entry['path']
                q.require(path.is_file() and not path.is_symlink() and path.stat().st_size == entry['bytes']
                          and q.file_hash(path) == entry['sha256'], 'retained artifact hash/length mismatch')
            cfg = r['declared']
            key = (cfg['profile'], cfg['replicate'])
            q.require(key not in seen and key[0] in hosts and key[1] in (1, 2, 3), 'duplicate/invalid cell')
            seen.add(key)
            host = r.get('host_before', {}).get('host', {}).get('files', {}).get('proc/sys/kernel/random/boot_id', '').strip()
            q.require(host and host not in hosts[key[0]], 'missing/duplicate VM boot identity')
            hosts[key[0]].add(host)
            q.require(r.get('status') == 'owner-execution-complete', 'cell did not complete; retain failure')
            cells.append(dict(profile=key[0], replicate=key[1], boot_id=host, status=r['status'],
                              source=cfg['source'], class_name=cfg['name'], manifest=cfg['manifest'],
                              producer_run=cfg['producer_run'], producer_attempt=cfg['producer_attempt']))
        except (ValueError, KeyError, OSError, TypeError) as error:
            errors.append(str(receipt.parent.name) + ': ' + str(error))
    if seen != {(p, n) for p in hosts for n in (1, 2, 3)}:
        errors.append('Need every standard/legacy8 VM1..3 receipt; missing cells never disappear')
    if len({(c['source'], c['class_name'], c['manifest'], c['producer_run'], c['producer_attempt']) for c in cells}) != 1:
        errors.append('Source/class pins differ or complete cells absent')
    q.atomic_json(output / 'summary.json', dict(cells=cells, protocol_errors=errors,
                  qualification_verdict=None, reason='Execution/transport summary only; reduce each actual recipe separately'))
    q.require(not errors, '; '.join(errors))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='mode', required=True)
    sub.add_parser('plan')
    ms=sub.add_parser('memory-setup'); ms.add_argument('--root', required=True, type=Path); ms.add_argument('--uid', required=True, type=int); ms.add_argument('--gid', required=True, type=int)
    for name in ('persist-memory', 'memory-cleanup'):
        m=sub.add_parser(name); m.add_argument('--root', required=True, type=Path); m.add_argument('--output', required=True, type=Path)
    cleanup_args = sub.add_parser('cleanup'); cleanup_args.add_argument('--keep-output', action='store_true')
    f = sub.add_parser('finalize'); f.add_argument('--output', required=True, type=Path)
    a = sub.add_parser('summarize'); a.add_argument('--artifacts', required=True, type=Path); a.add_argument('--output', required=True, type=Path)
    p = sub.add_parser('prepare')
    for name in ('source', 'artifact', 'output', 'work', 'producer-run'):
        p.add_argument('--' + name, required=True, type=Path)
    for name in ('run','observe'):
        r = sub.add_parser(name)
        r.add_argument('--launch', required=True, type=Path)
    args = parser.parse_args()
    if args.mode == 'plan':
        plan()
    elif args.mode == 'observe':
        observe_launch(args.launch)
    elif args.mode == 'memory-setup':
        memory_setup(args.root, args.uid, args.gid)
    elif args.mode == 'persist-memory':
        persist_memory(args.root, args.output)
    elif args.mode == 'memory-cleanup':
        memory_cleanup(args.root, args.output)
    elif args.mode == 'cleanup':
        cleanup(args.keep_output)
    elif args.mode == 'finalize':
        finalize(args.output)
    elif args.mode == 'summarize':
        summarize(args.artifacts, args.output)
    elif args.mode == 'prepare':
        prepare(args.source, args.artifact, args.output, args.work, args.producer_run)
    else:
        run(args.launch)


if __name__ == '__main__':
    main()
