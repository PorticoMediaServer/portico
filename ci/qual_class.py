#!/usr/bin/env python3
"""Canonical per-class execution transport; never a qualification verdict."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import time

import qual_fixture as q

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
COMPATIBILITY_PATHS = [
    'server/go.mod', 'server/go.sum', 'server/internal/store/schema/schema.sql',
    'server/internal/store/writer/qualification_population_seed_test.go',
    'server/internal/store/writer/scan_metadata.go', 'server/internal/sorttext',
]
ATTEMPTS = 10000


def identity(value, name, maximum=20):
    q.require(isinstance(value, str) and re.fullmatch(r'[1-9][0-9]{0,' + str(maximum-1) + '}', value), 'invalid ' + name)
    return int(value)


def inputs(env):
    source = env.get('INPUT_SOURCE_COMMIT', '')
    manifest = env.get('INPUT_MANIFEST_SHA256', '')
    name = env.get('INPUT_CLASS', '')
    profile = env.get('PROFILE', '')
    q.require(re.fullmatch(q.COMMIT, source) and re.fullmatch(q.SHA, manifest), 'full source/manifest pins required')
    q.require(name in TARGETS and profile in ('standard', 'legacy8'), 'closed class/profile required')
    return dict(source=source, manifest=manifest, name=name, profile=profile,
                replicate=q.uint(env.get('REPLICATE', ''), 1, 3, 'replicate'),
                producer_run=identity(env.get('INPUT_PRODUCER_RUN', ''), 'producer run'),
                producer_attempt=identity(env.get('INPUT_PRODUCER_ATTEMPT', '1'), 'producer attempt', 6),
                workflow_run=identity(env.get('GITHUB_RUN_ID', ''), 'workflow run'),
                workflow_attempt=identity(env.get('GITHUB_RUN_ATTEMPT', ''), 'workflow attempt', 6))


def verify_stream(path, cfg):
    """Only wire/pin/completion verification. No quantiles or accepted-count inference."""
    q.regular(path, 4 << 30)
    first = last = None
    count = 0
    with path.open('rb') as f:
        while line := f.readline((2 << 20) + 2):
            q.require(len(line) <= (2 << 20) + 1 and line.endswith(b'\n'), 'incomplete/oversized raw record')
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
    return dict(records=count, raw_sha256=q.file_hash(path), owner_final=last,
                qualification_verdict=None, verdict_reason='Owner raw evidence still needs per-recipe reduction and protocol gates')


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
    q.require(os.environ.get('GITHUB_REF') == 'refs/heads/main'
              and os.environ.get('GITHUB_ACTIONS') == 'true'
              and os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted', 'reviewed main tooling on standard runner required')
    output.mkdir(mode=0o700)
    work.mkdir(mode=0o700)
    report = output
    receipt = dict(version='portico-canonical-class-execution-v1', status='failed', qualification_verdict=None,
                   declared=cfg, attempts_requested=ATTEMPTS, ordinary_refusal_budget=0,
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
    env = launch['env']
    binary, host = work / 'writer.test', work / 'host'
    q.require(q.file_hash(binary) == launch['binary_sha256'] and q.file_hash(host) == launch['host_sha256'], 'binary changed before execution')
    receipt = json.loads((output / 'receipt.json').read_text())
    boot = Path('/proc/sys/kernel/random/boot_id').read_text().strip()
    q.require(re.fullmatch(r'[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}', boot), 'actual boot identity missing')
    c = dict(run_id=f"gh-{cfg['workflow_run']}-{cfg['workflow_attempt']}-{cfg['name']}-{cfg['profile']}-{cfg['replicate']}",
             host_epoch=f"{boot}-k{os.uname().release}", source_commit=cfg['source'],
             output_path=str(output / 'raw.ndjson'), **{'class': cfg['name']}, profile=cfg['profile'],
             attempts=ATTEMPTS, artifact_directory=launch['artifact'], manifest_sha256=cfg['manifest'])
    q.atomic_json(work / 'config.json', c)
    env.update(PORTICO_CANONICAL_HELD='1', PORTICO_CANONICAL_HELD_CONFIG=str(work / 'config.json'))
    receipt.update(actual_config=c, status='failed', target_uid=launch['uid'], target_gid=launch['gid'],
                   host_scope='same cgroup/namespace as target; collector root, target unprivileged',
                   qualification_verdict=None)
    host_args = [str(host), '-source', cfg['source'], '-archive-sha256', launch['archive_sha256'],
                 '-profile', cfg['profile'], '-replicate', str(cfg['replicate'])]
    try:
        before = q.command(host_args, Path(launch['source']), env, output, 'host-before', 180)
        receipt['host_before'] = json.loads(before.read_text())
        argv = [str(binary), '-test.run=^' + TARGETS[cfg['name']] + '$', '-test.count=1',
                '-test.timeout=75m', '-test.v', '-canonical-held=true']
        q.atomic_json(output / 'target.argv.json', dict(argv=argv, uid=launch['uid'], gid=launch['gid']))
        with (output / 'target.log').open('wb') as log:
            receipt['launch_before_monotonic_ns'] = time.monotonic_ns()
            process = subprocess.Popen(argv, cwd=Path(launch['source']) / 'server/internal/store/writer', env=env,
                                       stdout=log, stderr=subprocess.STDOUT, start_new_session=True,
                                       user=launch['uid'], group=launch['gid'], extra_groups=[])
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
        q.require(code == 0, 'owner target failed; retain every refusal/error/partial record')
        receipt.update(wire=verify_stream(output / 'raw.ndjson', c), status='owner-execution-complete')
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
        q.atomic_json(output / 'receipt.json', receipt)
        q.require(receipt['status'] == 'owner-execution-complete', 'cell nonpassing; see retained receipt/raw')


def plan():
    cfg = inputs(os.environ)
    path = os.environ['GITHUB_OUTPUT']
    with open(path, 'a') as f:
        f.write(f"producer_run={cfg['producer_run']}\n")
        f.write(f"producer_attempt={cfg['producer_attempt']}\n")
        f.write(f"producer_artifact=qual-population-{cfg['producer_run']}-{cfg['producer_attempt']}\n")


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
    files = []
    for path in sorted(output.iterdir()):
        if path.name == 'files.json':
            continue
        q.require(path.is_file() and not path.is_symlink(), 'unexpected output path')
        files.append(dict(path=path.name, bytes=path.stat().st_size, sha256=q.file_hash(path)))
    q.atomic_json(output / 'files.json', files)


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
    f = sub.add_parser('finalize'); f.add_argument('--output', required=True, type=Path)
    a = sub.add_parser('summarize'); a.add_argument('--artifacts', required=True, type=Path); a.add_argument('--output', required=True, type=Path)
    p = sub.add_parser('prepare')
    for name in ('source', 'artifact', 'output', 'work', 'producer-run'):
        p.add_argument('--' + name, required=True, type=Path)
    r = sub.add_parser('run')
    r.add_argument('--launch', required=True, type=Path)
    args = parser.parse_args()
    if args.mode == 'plan':
        plan()
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
