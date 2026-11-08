#!/usr/bin/env python3
"""Source-bound population artifact transport; never a qualification verdict."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess

VERSION = 'portico-qualification-population-v1'
SHAPE = 'canonical-movies-v1'
SEARCH_SHAPE = 'canonical-movies-v1-search'
GO = 'go1.26.10'
ENGINE = '3.53.4'
MAX_IMAGE = 8 << 30
SHA = r'[0-9a-f]{64}'
COMMIT = r'[0-9a-f]{40}'
MANIFEST_KEYS = set(('version source_commit schema_sha256 engine engine_source '
                     'go_version actual_policy dqs_ddl dqs_dml database_sha256 '
                     'database_bytes facts').split())
FACT_KEYS = set(('version seed residents library_id library_key first_id last_id '
                 'counts logical_sha256').split())
SEARCH_FACT_KEYS = FACT_KEYS | set(('search_bank statistics search_bank_integrity '
                                    'search_bank_foreign_keys').split())
SEARCH_BANK_KEYS = set(('version profile entities people credits search_publications '
                        'search_publish_jobs search_fragments prepared_bytes '
                        'search_own_prefix_docsize search_image_seals logical_sha256 '
                        'statistics').split())


def require(ok, message):
    if not ok:
        raise ValueError(message)


def uint(value, low, high, name):
    require(isinstance(value, str) and re.fullmatch(r'[0-9]{1,10}', value)
            and low <= int(value) <= high, f'{name} must be {low}..{high}')
    return int(value)


def bounded_int(value, low, high):
    return type(value) is int and low <= value <= high


def search_bank(value):
    require(value in ('true', 'false', ''), 'search_bank must be true or false')
    return value == 'true'


def source_ref(value):
    if re.fullmatch(r'[0-9a-fA-F]{40}', value):
        return value.lower()
    require(not value.startswith('refs/') or value.startswith('refs/heads/'),
            'only branch refs or full SHAs are supported')
    branch = value.removeprefix('refs/heads/')
    require(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._/-]{0,199}', branch)
            and '..' not in branch and '//' not in branch
            and all(not x.startswith('.') and not x.endswith(('.', '.lock'))
                    for x in branch.split('/')) and not branch.endswith('/'),
            'source_ref must be a pushed branch or full SHA')
    return 'refs/heads/' + branch


def inputs(env):
    purpose = env.get('INPUT_PURPOSE', 'preview')
    require(purpose in ('preview', 'qualification'), 'invalid purpose')
    return dict(fetch_ref=source_ref(env.get('INPUT_SOURCE_REF', '')),
                purpose=purpose, seed=uint(env.get('INPUT_SEED', '0'), 0, 2**32-1, 'seed'),
                residents=uint(env.get('INPUT_RESIDENTS', '1000000'), 1, 1000000, 'residents'),
                timeout_minutes=uint(env.get('INPUT_TIMEOUT_MINUTES', '60'), 1, 90, 'timeout'),
                search_bank=search_bank(env.get('INPUT_SEARCH_BANK', 'false')))


def file_hash(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def atomic_json(path, value):
    temporary = path.with_name(path.name + '.tmp')
    temporary.write_text(json.dumps(value, indent=2, allow_nan=False) + '\n')
    temporary.replace(path)


def closed_object(value, keys, label):
    require(type(value) is dict and set(value) == keys, f'invalid {label} fields')


def no_duplicates(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate JSON field')
        result[key] = value
    return result


def regular(path, maximum):
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= maximum,
            f'invalid file type/length: {path.name}')
    return info.st_size


def validate_artifact(directory, *, source, schema, seed, residents, manifest_sha=None, search=False):
    """Validate bytes without opening SQLite or changing caller authority.

    The v1 envelope and facts are closed. counts is a source-owned table-name
    map; policy retains the source Settings representation, including omitted
    zero DQS fields. A new envelope/shape needs an explicit producer update.
    """
    manifest = directory / 'population.json'
    regular(manifest, 65536)
    actual_sha = file_hash(manifest)
    if manifest_sha is not None:
        require(re.fullmatch(SHA, manifest_sha) and actual_sha == manifest_sha,
                'manifest digest mismatch')
    m = json.loads(manifest.read_text(), object_pairs_hook=no_duplicates,
                   parse_constant=lambda _: require(False, 'nonfinite JSON'))
    closed_object(m, MANIFEST_KEYS, 'manifest')
    require(m['version'] == VERSION and m['source_commit'] == source
            and re.fullmatch(COMMIT, source) and m['schema_sha256'] == schema
            and re.fullmatch(SHA, schema) and m['engine'] == ENGINE
            and m['go_version'] == GO, 'source/schema/toolchain mismatch')
    require(isinstance(m['engine_source'], str) and 1 <= len(m['engine_source']) <= 512,
            'missing engine source identity')
    require(type(m['dqs_ddl']) is int and m['dqs_ddl'] == 0
            and type(m['dqs_dml']) is int and m['dqs_dml'] == 0, 'DQS must be explicitly off')
    p = m['actual_policy']
    required = set(('Engine JournalMode ForeignKeys Synchronous AutoCheckpoint '
                    'CacheSize BusyTimeout MmapSize QueryOnly TempStore').split())
    require(type(p) is dict and required <= set(p)
            and not set(p) - required - {'DQSDDL', 'DQSDML', 'ReadLimits'}, 'invalid actual_policy fields')
    require(p['Engine'] == ENGINE and p['JournalMode'] == 'wal', 'invalid actual engine/journal')
    require(all(type(p[k]) is int for k in required - {'Engine', 'JournalMode'}),
            'invalid policy numeric values')
    require(p['ForeignKeys'] == 1 and p['Synchronous'] == 2
            and p['AutoCheckpoint'] == 0 and p['CacheSize'] == -1024
            and p['BusyTimeout'] == p['MmapSize'] == p['QueryOnly'] == 0
            and p['TempStore'] == 1, 'invalid canonical writer policy')
    require(all(type(p.get(k, 0)) is int and p.get(k, 0) == 0 for k in ('DQSDDL', 'DQSDML')),
            'policy DQS mismatch')
    if 'ReadLimits' in p:
        closed_object(p['ReadLimits'], {'LengthBytes', 'Columns'}, 'ReadLimits')
        require(all(type(x) is int and x == 0 for x in p['ReadLimits'].values()),
                'unexpected writer read limits')
    f = m['facts']
    closed_object(f, SEARCH_FACT_KEYS if search else FACT_KEYS, 'facts')
    require(f['version'] == (SEARCH_SHAPE if search else SHAPE) and bounded_int(f['seed'], 0, 2**32-1)
            and f['seed'] == seed and bounded_int(f['residents'], 1, 1000000)
            and f['residents'] == residents and type(f['library_id']) is int
            and f['library_id'] == 7 and f['library_key'] == 'qualification-movies'
            and isinstance(f['logical_sha256'], str) and re.fullmatch(SHA, f['logical_sha256']),
            'population facts mismatch')
    require(bounded_int(f['first_id'], 1, 2**63-1)
            and bounded_int(f['last_id'], f['first_id'], 2**63-1)
            and f['first_id'] == 1000000001
            and f['last_id'] == 1000000000 + residents, 'population ID bounds mismatch')
    require(type(f['counts']) is dict and 1 <= len(f['counts']) <= 256
            and all(isinstance(k, str) and re.fullmatch(r'[A-Za-z][A-Za-z0-9_]{0,127}', k)
                    and bounded_int(v, 0, 100000000) for k, v in f['counts'].items()),
            'invalid population counts')
    for name in ('catalog_subjects', 'catalog_entities', 'catalog_assets', 'catalog_asset_links',
                 'catalog_scalar_contributions', 'catalog_asset_contributions', 'current_movies'):
        require(f['counts'].get(name) == residents, 'population resident count mismatch: ' + name)
    for name in ('libraries', 'catalog_libraries', 'catalog_kinds', 'catalog_membership_clock', 'catalog_age_item_counts'):
        require(f['counts'].get(name) == 1, 'population singleton count mismatch: ' + name)
    if search:
        b = f['search_bank']
        closed_object(b, SEARCH_BANK_KEYS, 'search_bank')
        require(b['version'] == 'canonical-search-v1' and type(b['profile']) is dict
                and bounded_int(b['entities'], 1, 1000000) and b['entities'] == residents
                and all(bounded_int(b[k], 1, 100000000) for k in
                        ('people', 'credits', 'search_publications', 'search_publish_jobs',
                         'search_fragments', 'prepared_bytes', 'search_image_seals'))
                and b['search_own_prefix_docsize'] == residents
                and isinstance(b['logical_sha256'], str) and re.fullmatch(SHA, b['logical_sha256']),
                'invalid search bank facts')
        require(isinstance(f['statistics'], str) and 1 <= len(f['statistics']) <= 256
                and f['search_bank_integrity'] == 'ok'
                and f['search_bank_foreign_keys'] == 'empty', 'invalid search bank verification facts')

    require(bounded_int(m['database_bytes'], 1, MAX_IMAGE)
            and isinstance(m['database_sha256'], str) and re.fullmatch(SHA, m['database_sha256']),
            'invalid image bounds/hash')
    for suffix in ('-wal', '-shm'):
        require(not os.path.lexists(directory / ('population.sqlite' + suffix)), 'image sidecar present')
    image = directory / 'population.sqlite'
    require(regular(image, MAX_IMAGE) == m['database_bytes'], 'image length mismatch')
    require(file_hash(image) == m['database_sha256'], 'image digest mismatch')
    return m, actual_sha


def clean_env(work):
    # Keep only the native tool path and OS basics. No credentials, dispatch,
    # inherited Go flags or Actions file-command channels reach source code.
    env = {k: os.environ[k] for k in ('PATH', 'LANG', 'LC_ALL', 'TZ') if k in os.environ}
    for name in ('home', 'tmp', 'gocache', 'gopath'):
        (work / name).mkdir()
    env.update(HOME=str(work / 'home'), TMPDIR=str(work / 'tmp'),
               GOCACHE=str(work / 'gocache'), GOPATH=str(work / 'gopath'),
               GOTOOLCHAIN='local', GOWORK='off', GOFLAGS='-mod=readonly',
               GOMAXPROCS='2', CGO_ENABLED='0', GITHUB_ACTIONS='true')
    return env


def command(argv, cwd, env, report, name, timeout):
    atomic_json(report / (name + '.argv.json'), dict(argv=argv, cwd=str(cwd), timeout_seconds=timeout))
    log_path = report / (name + '.log')
    with log_path.open('wb') as log:
        process = subprocess.Popen(argv, cwd=cwd, env=env, stdout=log,
                                   stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = process.wait(timeout=timeout)
        except BaseException:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()
            raise
    require(code == 0, f'{name} failed ({code}); see {log_path.name}')
    # Only small metadata commands are read back; build/export logs stay on disk.
    return log_path


def validate_sqlite_build_identity(metadata, selected):
    # Go test binaries omit dependency records from debug/buildinfo. Retain
    # that actual metadata, and prove the build-selected dependency separately
    # with go list under the SAME source/env/flags as the test binary build.
    lines = metadata.splitlines()
    require(lines and lines[0].endswith(': ' + GO)
            and '\tpath\tgithub.com/PorticoMediaServer/portico/server/internal/store/writer.test' in metadata,
            'test binary toolchain/package identity mismatch')
    dependencies = [line.strip() for line in selected.splitlines() if line.strip()]
    expected = {'modernc.org/sqlite/lib v1.58.0 ORIGINAL',
                'github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite BUNDLED'}
    require(len(dependencies) == len(expected) and set(dependencies) == expected,
            'build-selected bundled SQLite dependency identity mismatch/replacement')
    return sorted(dependencies)


def publish(staging, output, receipt, **expected):
    m, manifest_sha = validate_artifact(staging, **expected)
    for name in ('population.sqlite', 'population.json'):
        require(not os.path.lexists(output / name), 'publication already exists')
    # Same runner-temp filesystem: rename, never another multi-GB Python copy.
    for name in ('population.sqlite', 'population.json'):
        (staging / name).rename(output / name)
    receipt.update(status='complete', manifest_sha256=manifest_sha,
                   database_sha256=m['database_sha256'], database_bytes=m['database_bytes'],
                   logical_sha256=m['facts']['logical_sha256'], actual_policy=m['actual_policy'],
                   dqs_ddl=m['dqs_ddl'], dqs_dml=m['dqs_dml'], engine_source=m['engine_source'])


def produce(source, output, work):
    require(all(p.is_absolute() and p == p.resolve() for p in (source, output, work)),
            'source/output/work must be absolute canonical paths')
    paths = (source, output, work)
    require(source.is_dir() and not work.exists()
            and not any(a.is_relative_to(b) for a in paths for b in paths if a is not b),
            'source/output/work must be disjoint')
    output.mkdir(mode=0o700)  # refuse reuse, including a previous complete image
    report = output / 'report'
    report.mkdir()
    run = os.environ.get('GITHUB_RUN_ID', '')
    attempt = os.environ.get('GITHUB_RUN_ATTEMPT', '')
    repo = os.environ.get('GITHUB_REPOSITORY', '')
    url = f'https://github.com/{repo}/actions/runs/{run}'
    receipt = dict(version='portico-qualification-population-receipt-v1', status='failed',
                   qualification='NOT qualification', run_id=run, run_attempt=attempt,
                   run_url=url, artifact_name=f'qual-population-{run}-{attempt}')
    atomic_json(report / 'receipt.json', receipt)
    print(url, flush=True)
    owns_work = False
    try:
        cfg = inputs(os.environ)
        receipt['inputs'] = cfg
        require(os.environ.get('GITHUB_ACTIONS') == 'true'
                and os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted'
                and os.environ.get('RUNNER_OS') == 'Linux', 'producer requires GitHub-hosted Linux')
        require(re.fullmatch(r'[1-9][0-9]{0,19}', run) and re.fullmatch(r'[1-9][0-9]{0,5}', attempt)
                and re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repo), 'invalid run identity')
        work.mkdir(mode=0o700)
        owns_work = True
        env = clean_env(work)
        def run_command(argv, cwd, name, timeout=180):
            return command(argv, cwd, env, report, name, timeout)
        run_command(['git', 'fetch', '--no-tags', 'origin', cfg['fetch_ref']], source, 'fetch')
        pin = run_command(['git', 'rev-parse', '--verify', 'FETCH_HEAD^{commit}'], source, 'source-sha').read_text().strip()
        require(re.fullmatch(COMMIT, pin), 'invalid actual checkout SHA')
        if re.fullmatch(COMMIT, cfg['fetch_ref']):
            require(pin == cfg['fetch_ref'], 'full SHA checkout mismatch')
        run_command(['git', 'checkout', '--detach', pin], source, 'checkout')
        actual = run_command(['git', 'rev-parse', 'HEAD'], source, 'actual-head').read_text().strip()
        require(actual == pin, 'actual HEAD mismatch')
        receipt['source_commit'] = actual
        archive = work / 'source.tar'
        run_command(['git', 'archive', '--format=tar', '-o', str(archive), actual], source, 'source-archive', 300)
        receipt['source_archive_sha256'] = file_hash(archive)
        archive.unlink()  # retain the hash and argv, never a source-tree evidence copy

        if cfg['purpose'] == 'qualification':
            require(os.environ.get('GITHUB_REF') == 'refs/heads/main',
                    'qualification producer tooling must also run from main')
            run_command(['git', 'fetch', '--no-tags', 'origin', 'refs/heads/main'], source, 'fetch-main')
            main = run_command(['git', 'rev-parse', 'FETCH_HEAD^{commit}'], source, 'main-sha').read_text().strip()
            run_command(['git', 'merge-base', '--is-ancestor', actual, main], source, 'merged-main')
            receipt.update(merged_main_commit=main,
                           qualification='merged source; review prerequisite remains operator-owned; no timing verdict')
        else:
            receipt['qualification'] = 'NOT qualification: preview source, never auto-adopt'
        module = source / 'server'
        go_mod = (module / 'go.mod').read_text()
        require(re.findall(r'^go\s+(\S+)\s*$', go_mod, re.M) == ['1.26.10'], 'go.mod must pin exactly 1.26.10')
        require(re.search(r'^\s*modernc.org/sqlite v1\.58\.0\s*$', go_mod, re.M), 'unexpected modernc module pin')
        version = run_command(['go', 'version'], module, 'go-version').read_text().strip()
        require(version == 'go version go1.26.10 linux/amd64', 'exact Go1.26.10 linux/amd64 required')
        schema_dir = module / 'internal/store/schema'
        schema = file_hash(schema_dir / 'schema.sql')
        require(re.findall(r'const SHA256 = "([0-9a-f]{64})"', (schema_dir / 'schema.go').read_text()) == [schema],
                'embedded schema fingerprint mismatch')
        receipt.update(schema_sha256=schema, go_version=GO, engine=ENGINE,
                       tooling_commit=os.environ.get('GITHUB_SHA'),
                       workflow_ref=os.environ.get('GITHUB_WORKFLOW_REF'),
                       runner=dict(os='ubuntu-24.04', image=os.environ.get('ImageVersion')),
                       go_mod_sha256=file_hash(module / 'go.mod'),
                       go_sum_sha256=file_hash(module / 'go.sum'))
        identity = dict(source_commit=actual, schema_sha256=schema,
                        version=SEARCH_SHAPE if cfg['search_bank'] else SHAPE,
                        seed=cfg['seed'], residents=cfg['residents'],
                        search_bank=cfg['search_bank'])
        receipt['cache_identity'] = identity
        receipt['cache_key'] = hashlib.sha256(json.dumps(identity, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
        binary = work / 'writer.test'
        run_command(['go', 'test', '-p', '1', '-c', '-o', str(binary), './internal/store/writer'], module, 'build', 1200)
        receipt['binary_sha256'] = file_hash(binary)
        metadata = run_command(['go', 'version', '-m', str(binary)], module, 'binary-build-info').read_text()
        template = '{{if eq .ImportPath "modernc.org/sqlite/lib"}}{{.ImportPath}} {{.Module.Version}} {{if .Module.Replace}}REPLACED{{else}}ORIGINAL{{end}}{{end}}{{if eq .ImportPath "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"}}{{.ImportPath}} BUNDLED{{end}}{{if eq .ImportPath "modernc.org/sqlite"}}{{.ImportPath}} ROOT_DRIVER{{end}}'
        selected = run_command(['go', 'list', '-deps', '-test', '-f', template, './internal/store/writer'],
                               module, 'sqlite-build-dependency', 300).read_text()
        dependencies = validate_sqlite_build_identity(metadata, selected)
        receipt['binary_build_info'] = metadata
        receipt['sqlite_build_dependencies'] = dependencies
        staging = work / 'export'
        config_path = work / 'export-config.json'
        atomic_json(config_path, dict(output_dir=str(staging), source_commit=actual,
                                      seed=cfg['seed'], residents=cfg['residents'],
                                      search_bank=cfg['search_bank']))
        env.update(PORTICO_QUAL_FIXTURE='1', PORTICO_QUAL_FIXTURE_HOSTED='1',
                   PORTICO_QUAL_FIXTURE_CONFIG=str(config_path))
        minutes = cfg['timeout_minutes']
        run_command([str(binary), '-test.run=^TestExportQualificationPopulation$', '-test.count=1',
                     f'-test.timeout={minutes}m', '-test.v'], module / 'internal/store/writer',
                    'export', minutes * 60 + 30)
        publish(staging, output, receipt, source=actual, schema=schema,
                seed=cfg['seed'], residents=cfg['residents'],
                search=cfg['search_bank'])
    except BaseException as error:
        receipt.update(status='failed', error=f'{type(error).__name__}: {error}')
        for name in ('population.sqlite', 'population.json'):
            (output / name).unlink(missing_ok=True)
        raise
    finally:
        atomic_json(report / 'receipt.json', receipt)
        print(json.dumps(receipt, indent=2), flush=True)
        # Only this invocation's private scratch; never touch source/shared caches.
        if owns_work and work.is_dir():
            for current, _, _ in os.walk(work, followlinks=False):
                os.chmod(current, 0o700)
            shutil.rmtree(work)


def failure_receipt(output):
    """Retain a fail-closed receipt if setup/controls failed before produce."""
    report = output / 'report'
    report.mkdir(parents=True, exist_ok=True)
    if not (report / 'receipt.json').exists():
        run = os.environ.get('GITHUB_RUN_ID', '')
        repo = os.environ.get('GITHUB_REPOSITORY', '')
        url = f'https://github.com/{repo}/actions/runs/{run}'
        atomic_json(report / 'receipt.json', dict(status='failed', qualification='NOT qualification',
                    error='Producer did not finish initialization; inspect GitHub job logs', run_url=url))
        print(url, flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='mode', required=True)
    p = sub.add_parser('produce')
    for key in ('source', 'output', 'work'):
        p.add_argument('--' + key, required=True, type=Path)
    f = sub.add_parser('failure-receipt')
    f.add_argument('--output', required=True, type=Path)
    v = sub.add_parser('verify')
    v.add_argument('--artifact', required=True, type=Path)
    for key in ('source', 'schema', 'manifest-sha'):
        v.add_argument('--' + key, required=True)
    v.add_argument('--seed', required=True)
    v.add_argument('--residents', required=True)
    v.add_argument('--search-bank', action='store_true')
    args = parser.parse_args()
    if args.mode == 'produce':
        produce(args.source, args.output, args.work)
    elif args.mode == 'failure-receipt':
        failure_receipt(args.output)
    else:
        _, digest = validate_artifact(args.artifact, source=args.source, schema=args.schema,
                                     seed=uint(args.seed, 0, 2**32-1, 'seed'),
                                     residents=uint(args.residents, 1, 1000000, 'residents'),
                                     manifest_sha=args.manifest_sha, search=args.search_bank)
        print('Verified population manifest ' + digest)


if __name__ == '__main__':
    main()
