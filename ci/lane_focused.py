#!/usr/bin/env python3
"""Manual standard-runner tests/captures. No qualification verdicts or timing budgets."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys

TOOLS = Path(__file__).resolve().parent
CAPTURES = {
    'hn-canonical': ('stmt/plan', 'TestPlanCanonicalSchemaFKAndStatementContracts', 'record-plans', ['canonical.json', 'canonical-fk.json.gz']),
    'hn-issuer': ('stmt/plan', 'TestIssuerSourceOctetLengthsGolden', 'record-issuer-octets', ['issuer-source-octets.json']),
    'hn-issuer-library': ('stmt/plan', 'TestIssuerLibraryRootOctetLengthsGolden', 'record-issuer-library-octets', ['issuer-library-root-octets.json']),
    'hn-n1-preflights': ('writer', 'TestN1PreflightStatementPlans', 'record-n1-preflight-plans', ['n1-byte-preflights.json']),
    'hn-autoindexes': ('stmt/plan', 'TestPlanAutoindexGoldenAndNativeKeys', 'record-autoindex-plans', ['autoindexes.json']),
    'hn-scan': ('writer', 'TestScanInventoryStatementPlans', 'record-scan-plans', ['scan-inventory-accept.json', 'scan-inventory-accept-fk.json.gz']),
    'hn-scan-fresh': ('writer', 'TestScanFreshStatementPlans', 'record-scan-fresh-plans', ['scan-inventory-fresh.json', 'scan-inventory-fresh-fk.json.gz']),
    'hn-wc-progress': ('writer', 'TestWCProgressStatementPlans', 'record-wc-progress-plans', ['wc-progress-capture_start.json', 'wc-progress-pending_drain.json', 'wc-progress-fk.json.gz']),
    'hn-browse': ('read', 'TestBrowsePlans', 'record-browse-plans', ['browse-plans.json.gz']),
    'hn-foreground': ('read', 'TestForegroundPlans', 'record-foreground-plans', ['foreground-plans.json.gz']),
    'hn-autoindex-fences': ('stmt/plan', 'TestPlanAutoindexRealJoinedFences', 'record-autoindex-plans', ['autoindex-fences.json.gz']),
    'hn-autoindex-keysets': ('stmt/plan', 'TestPlanAutoindexPolicyChildKeysetGolden', 'record-autoindex-plans', ['autoindex-keysets.json.gz']),
    'hn-autoindex-inclusive': ('stmt/plan', 'TestPlanAutoindexInclusiveChannelKeysetsGolden', 'record-autoindex-plans', ['autoindex-inclusive.json.gz']),
    'hn-scan-object': ('writer', 'TestScanObjectStatementPlans', 'record-scan-object-plans', ['scan-inventory-object.json', 'scan-inventory-object-fk.json.gz']),
    'hn-disclosure': ('writer', 'TestInventoryDisclosurePlanGoldens', 'record-inventory-disclosure-plans', ['scan-inventory-disclosure.json']),
    'hn-fresh-library': ('writer', 'TestInventoryFreshLibraryPlanGolden', 'record-inventory-fresh-plan', ['scan-inventory-fresh-library.json']),
    'hn-lifecycle': ('writer', 'TestScanRunLifecycleStatementPlans', 'record-scan-run-lifecycle-plans', ['scan-run-lifecycle.json', 'scan-run-lifecycle-fk.json.gz']),
    'hn-lifecycle-history': ('writer', 'TestScanLifecycleHistoryStatementPlans', 'record-scan-lifecycle-history-plans', ['scan-run-lifecycle-history.json']),
    'hn-basic-stream': ('writer', 'TestBasicStreamStatementPlans', 'record-basic-stream-plans', ['scan-basic-stream-stage.json', 'scan-basic-stream-stage-fk.json.gz']),
    'hn-enrichment-stage': ('writer', 'TestEnrichmentStageStatementPlans', 'record-enrichment-stage-plans', ['enrichment-stage-010.json.gz']),
    'hn-search-input': ('writer', 'TestSearchInputStatementPlans', 'record-search-input-plans', ['search-input-intent.json', 'search-input-intent-fk.json.gz']),
    'hn-search-image': ('writer', 'TestSearchImageBeginStatementPlans', 'record-search-image-plans', ['search-image-begin.json', 'search-image-begin-fk.json.gz']),
    'hn-search-fragment': ('writer', 'TestSearchFragmentStatementPlans', 'record-search-fragment-plans', ['search-fragment-stage.json', 'search-fragment-stage-fk.json.gz']),
    'hn-wc-support': ('writer', 'TestWCProgressSupportStatementPlans', 'record-wc-progress-support-plans', ['wc-progress-support-source_ack_null_due.json', 'wc-progress-support-source_ack_existing_due.json', 'wc-progress-support-api_parent_full.json', 'wc-progress-support-normal-fk.json.gz', 'wc-progress-support-full-fk.json.gz']),
    'hn-wc-reclaim': ('writer', 'TestWCReclaimStatementPlans', 'record-wc-reclaim-plans', ['wc-reclaim.json', 'wc-reclaim-fk.json.gz']),
    'hn-wc-security': ('writer', 'TestWCSecurityStatementPlans', 'record-wc-security-plans', ['wc-security.json', 'wc-security-fk.json.gz']),
    'hn-owner-reverse': ('read', 'TestOwnerReversePlans', 'record-owner-plans', ['owner-reverse-plans.json.gz']),
    'hn-mp-prepared': ('read', 'TestMPPreparedPlans', 'record-mp-prepared-plans', ['mp-prepared-plans.json.gz']),
    'hn-mp-queue': ('read', 'TestMPQueuePlans', 'record-mp-queue-plans', ['mp-queue-plans.json.gz']),
    'hn-mp-session': ('read', 'TestMPSessionPlans', 'record-mp-session-plans', ['mp-session-plans.json.gz']),
    'hn-rs-collection': ('read', 'TestRSCollectionPlans', 'record-rs-collection-plans', ['rs-collection-plans.json.gz']),
    'hn-rs-detail': ('read', 'TestRSDetailPlans', 'record-rs-detail-plans', ['rs-detail-plans.json.gz']),
    'hn-rs-people': ('read', 'TestRSPeoplePlans', 'record-rs-people-plans', ['rs-people-plans.json.gz']),
    'hn-uo-personal': ('read', 'TestUOPersonalPlans', 'record-uo-personal-plans', ['uo-personal-plans.json.gz']),
    'hn-basic-control': ('writer', 'TestBasicControlStatementPlans', 'record-basic-control-plans', ['scan-basic-control.json', 'scan-basic-control-fk.json.gz']),
}
HARNESSES = {
    'ec5-memory': ('./internal/store/writer', '^TestEC5'),
    'connections': ('./internal/store/qual', '^TestConnection'),
    'wal-protocol': ('./internal/store/governor', '^Test(NativeEmptyWALFirstGovernedWrite|WAL)'),
    'n1-protocol': ('./internal/store/writer', '^TestN1(OwnerWindowBindingAndPartials|ProfileNoRegistryOrMAXIsNotSynthesized|InputProfileEscapingKeepsObservedZero)$'),
    'wc-progress': ('./internal/store/writer', '^TestWC(Capture|008Native|ProgressClosedGrammar)'),
}


def digest(data):
    return hashlib.sha256(data).hexdigest()


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + '\n')


def clean_env(output):
    # No dispatch/auth/artifact or workflow file-command channels reach tests.
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(('GITHUB_', 'ACTIONS_', 'RUNNER_', 'INPUT_'))
           and k not in ('GH_TOKEN', 'GITHUB_TOKEN', 'GOFLAGS', 'GOTESTFLAGS')}
    for name in ('tmp', 'home'):
        (output / name).mkdir(parents=True, exist_ok=True)
    env.update(PORTICO_LANE_EVIDENCE_DIR=str(output / 'evidence-custom'), TMPDIR=str(output / 'tmp'), HOME=str(output / 'home'), GOMAXPROCS='2', GOTOOLCHAIN='auto')
    return env


def remove_disposable(path):
    # Go's module cache contains read-only directories. Only change our own
    # scratch directories, never follow links into a source/shared cache.
    if path.is_symlink():
        path.unlink()
    elif path.exists():
        for current, _, _ in os.walk(path, followlinks=False):
            os.chmod(current, Path(current).stat().st_mode | 0o700)
        shutil.rmtree(path)


def command(args, cwd, timeout=180, env=None):
    try:
        return subprocess.check_output(args, cwd=cwd, env=env, text=True, stderr=subprocess.PIPE, timeout=timeout)
    except subprocess.CalledProcessError as error:
        raise RuntimeError(str(error) + '\n' + error.stderr[-65536:]) from error


def integer(value, low, high, label):
    if not re.fullmatch('[0-9]{1,4}', value) or not low <= int(value) <= high:
        raise ValueError(f'{label} must be {low}..{high}')
    return int(value)


def validated(env):
    get = lambda key, default: env.get('INPUT_' + key.upper(), default)
    ref = get('source_ref', '')
    if re.fullmatch('[0-9a-fA-F]{40}', ref):
        fetch = ref.lower()
    else:
        branch = ref.removeprefix('refs/heads/')
        if (not re.fullmatch('[A-Za-z0-9][A-Za-z0-9._/-]{0,199}', branch)
                or '..' in branch or '//' in branch or branch.endswith(('/', '.', '.lock'))
                or any(x.startswith('.') for x in branch.split('/'))):
            raise ValueError('source_ref must be a pushed branch or full commit SHA')
        fetch = 'refs/heads/' + branch
    module, runner, mode = get('module', 'server'), get('runner', 'ubuntu-24.04'), get('mode', 'tests')
    if module not in ('server', 'apikit') or runner not in ('ubuntu-24.04', 'macos-15') or mode not in ('tests', 'capture', 'harness'):
        raise ValueError('invalid module/runner/mode')
    packages = get('packages', './internal/store/qual').split()
    if not 1 <= len(packages) <= 32:
        raise ValueError('provide 1..32 module-relative Go package patterns')
    for p in packages:
        if not re.fullmatch(r'\./(?:[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*/?)?(?:\.\.\.)?', p) or p == './':
            raise ValueError('invalid module-relative Go package pattern')
    pattern = get('run', '.')
    if not pattern or len(pattern) > 4096 or any(ord(c) < 32 or ord(c) == 127 for c in pattern):
        raise ValueError('invalid -run filter')
    tests = get('tests', '').split()
    if len(tests) > 1024 or any(not t.isidentifier() or len(t) > 256 or not t.startswith(('Test', 'Example', 'Fuzz')) for t in tests):
        raise ValueError('invalid exact parent-test list')
    race = get('race', 'false')
    if race not in ('true', 'false'):
        raise ValueError('race must be true or false')
    count = integer(get('count', '1'), 1, 100, 'count')
    shards = get('shards', 'auto')
    if shards != 'auto':
        shards = integer(shards, 1, 64, 'shards')
    targets = get('targets', '').split()
    if mode == 'tests' and targets:
        raise ValueError('targets belongs to capture/harness mode')
    allowed = CAPTURES if mode == 'capture' else HARNESSES
    if mode != 'tests' and (not targets or len(targets) > len(allowed) or len(set(targets)) != len(targets) or any(t not in allowed for t in targets)):
        raise ValueError('select names from the fixed capture/harness allowlist')
    if mode != 'tests' and module != 'server':
        raise ValueError('fixed captures/harnesses belong to server')
    if mode == 'capture' and count != 1:
        raise ValueError('captures use count=1')
    return dict(source_ref=ref, fetch_ref=fetch, module=module, runner=runner, mode=mode,
                packages=packages, run=pattern, tests=tests, race=race == 'true', count=count,
                shards=shards, targets=targets)


def decode_stream(raw):
    decoder = json.JSONDecoder()
    while raw.strip():
        value, end = decoder.raw_decode(raw.lstrip())
        yield value
        raw = raw.lstrip()[end:]


def split_units(units, requested):
    # Intentionally simple: stable round-robin parents, 25 parents/runner by
    # default, capped at32. A large SINGLE package is split too.
    count = min(32, max(1, math.ceil(len(units) / 25))) if requested == 'auto' else requested
    count = min(count, len(units))
    return [units[i::count] for i in range(count)]


def plan(source, output):
    cfg = validated(os.environ)  # before fetch or executing source tooling
    env = clean_env(output)
    command(['git', 'fetch', '--no-tags', '--depth=1', 'origin', cfg['fetch_ref']], source)
    sha = command(['git', 'rev-parse', '--verify', 'FETCH_HEAD^{commit}'], source).strip()
    if not re.fullmatch('[0-9a-f]{40}', sha):
        raise ValueError('remote ref did not resolve to a commit')
    command(['git', 'checkout', '--detach', sha], source)
    module = source / cfg['module']
    if not (module / 'go.mod').is_file() or not module.resolve().is_relative_to(source.resolve()):
        raise ValueError('module is absent or escapes source')
    tags = command([sys.executable, str(TOOLS.parent / 'scripts/release-build-tags.py'), '--capability'], TOOLS.parent).strip()
    units, parents = [], {}
    if cfg['mode'] == 'capture':
        for target in cfg['targets']:
            package, test, flag, paths = CAPTURES[target]
            units.append(dict(Package='./internal/store/' + package, Name=test, target=target,
                              flags=['-' + flag], evidence=['server/internal/store/' + package + '/testdata/' + p for p in paths]))
    else:
        selections = [(cfg['packages'], cfg['run'], cfg['tests'])] if cfg['mode'] == 'tests' else [([HARNESSES[t][0]], HARNESSES[t][1], []) for t in cfg['targets']]
        for patterns, pattern, exact in selections:
            args = ['go', 'list', '-mod=readonly', '-json', '-tags=' + tags]
            if cfg['race']:
                args.append('-race')
            packages = list(decode_stream(command(args + patterns, module, env=env)))
            for p in packages:
                if not Path(p['Dir']).resolve().is_relative_to(module.resolve()):
                    raise ValueError('selected package escapes module')
            request = json.dumps(dict(Packages=packages, Run=pattern, Tests=exact))
            result = subprocess.run(['go', 'run', str(TOOLS / 'lane_inventory.go')], input=request,
                                    cwd=module, env=env, text=True, capture_output=True, timeout=180)
            if result.returncode:
                raise ValueError('test inventory: ' + result.stderr)
            found = json.loads(result.stdout)
            parents.update(found['Parents'])
            for unit in found['Units']:
                unit.update(run=pattern, exact_coverage='/' not in pattern, flags=[], evidence=[])
                units.append(unit)
    units = sorted({(u['Package'], u['Name']): u for u in units}.values(), key=lambda u: (u['Package'], u['Name']))
    if not units:
        raise ValueError('empty test/capture plan')
    bins = split_units(units, cfg['shards'])
    result = dict(config=cfg, source_sha=sha, tags=tags, parents=parents, shards=bins, qualification=False,
                  run_url=f"{os.environ['GITHUB_SERVER_URL']}/{os.environ['GITHUB_REPOSITORY']}/actions/runs/{os.environ['GITHUB_RUN_ID']}")
    write(output / 'plan.json', result)
    with open(os.environ['GITHUB_OUTPUT'], 'a') as stream:
        stream.write('sha=' + sha + '\nrunner=' + cfg['runner'] + '\n')
        stream.write('matrix=' + json.dumps({'include': [{'index': i} for i in range(len(bins))]}, separators=(',', ':')) + '\n')
    print('Run URL: ' + result['run_url'])
    print(f"{len(units)} parent tests/captures -> {len(bins)} standard-runner shards")


def collect_events(path, expected, strict, returncode):
    events = [json.loads(x) for x in path.read_text().splitlines() if x.strip()]
    terminals, started, package_end = {}, set(), []
    for e in events:
        name, action = e.get('Test'), e.get('Action')
        if name and '/' not in name:
            if action == 'run':
                started.add(name)
            if action in ('pass', 'fail', 'skip'):
                terminals.setdefault(name, []).append(action)
        elif not name and action in ('pass', 'fail', 'skip'):
            package_end.append(action)
    missing = sorted(set(expected) - set(terminals)) if strict else sorted(started - set(terminals))
    unexpected = sorted(started - set(expected))
    good = (returncode == 0 and (package_end == ['pass'] or (not expected and package_end == ['skip'])) and not missing and not unexpected
            and all(actions and all(a in ('pass', 'skip') for a in actions) for actions in terminals.values()))
    return dict(ok=good, returncode=returncode, expected=expected, missing=missing, unexpected=unexpected,
                terminals=terminals, root_events=sum(len(v) for v in terminals.values()),
                passed=sum(a == 'pass' for v in terminals.values() for a in v),
                skipped=sum(a == 'skip' for v in terminals.values() for a in v))


def test_command(p, package, units):
    cfg = p['config']
    names = sorted(u['Name'] for u in units if u['Name'])
    known = p.get('parents', {}).get(package, [u['Name'] for shard in p['shards'] for u in shard if u['Package'] == package])
    others = sorted(set(known) - set(names) - {''})
    pattern = units[0].get('run', '^(' + '|'.join(re.escape(n) for n in names) + ')$')
    if cfg.get('tests') and '/' not in pattern:
        pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$'
    # Slash filters stay untouched. The COMPLETE package inventory excludes
    # unselected parents too, including names removed by an exact test list.
    args = ['go', 'test', '-json', '-mod=readonly', '-tags=' + p['tags'], '-p=2', '-parallel=2',
            '-count=' + str(cfg['count']), '-timeout=25m', '-run=' + (pattern if names else '^$')]
    if others:
        args.append('-skip=^(' + '|'.join(re.escape(n) for n in others) + ')$')
    if cfg['race']:
        args.append('-race')
    return args + [package] + sorted({flag for u in units for flag in u['flags']})


def worker(source, output, plan_path, index):
    p = json.loads(plan_path.read_text())
    cfg = p['config']
    if index < 0 or index >= len(p['shards']) or command(['git', 'rev-parse', 'HEAD'], source).strip() != p['source_sha']:
        raise ValueError('shard/source binding mismatch')
    env = clean_env(output)
    if cfg['mode'] == 'harness' and 'ec5-memory' in cfg['targets']:
        env['PORTICO_EC5_FUNCTIONAL'] = '1'
    groups = {}
    for u in p['shards'][index]:
        groups.setdefault((u['Package'], u.get('run')), []).append(u)
    receipt = dict(index=index, source_sha=p['source_sha'], plan_sha256=digest(plan_path.read_bytes()), mode=cfg['mode'], qualification=False,
                   complete=False, packages=[], evidence=[], run_url=p['run_url'])
    report = output / 'result.json'
    write(report, receipt)
    try:
        for number, ((package, _), units) in enumerate(sorted(groups.items())):
            names = sorted(u['Name'] for u in units if u['Name'])
            args = test_command(p, package, units)
            paths = sorted({path for u in units for path in u['evidence']})
            baseline = {path: digest((source / path).read_bytes()) if (source / path).is_file() else None for path in paths}
            log, errlog = output / f'{number:03d}.jsonl', output / f'{number:03d}.stderr'
            with log.open('w') as stdout, errlog.open('w') as stderr:
                run = subprocess.Popen(args, cwd=source / cfg['module'], env=env, stdout=stdout, stderr=stderr, start_new_session=True)
                try:
                    run.wait(timeout=1800)
                except subprocess.TimeoutExpired:
                    os.killpg(run.pid, signal.SIGKILL)
                    run.wait()
                    raise
            result = collect_events(log, names, all(u.get('exact_coverage', True) for u in units), run.returncode)
            result.update(package=package, command=args, log=log.name, stderr=errlog.name)
            receipt['packages'].append(result)
            for path in paths:
                f = source / path
                if not f.is_file() or f.is_symlink() or not f.resolve().is_relative_to(source.resolve()):
                    raise ValueError('capture artifact missing/unsafe: ' + path)
                data = f.read_bytes(); target = output / 'evidence' / path
                target.parent.mkdir(parents=True, exist_ok=True); target.write_bytes(data)
                receipt['evidence'].append(dict(path=path, sha256=digest(data), bytes=len(data),
                                                baseline_sha256=baseline[path], byte_identical_to_baseline=digest(data) == baseline[path]))
            write(report, receipt)
            print(package + ': ' + ('PASS' if result['ok'] else 'FAIL') + f" ({result['passed']} pass, {result['skipped']} skip)")
        receipt['complete'] = True
    except BaseException as error:
        receipt['error'] = str(error)
        raise
    finally:
        # Only owned, disposable tool caches; evidence/logs survive failures.
        for name in ('tmp', 'home'):
            remove_disposable(output / name)
        receipt['ok'] = receipt['complete'] and all(x['ok'] for x in receipt['packages'])
        write(report, receipt)
        manifest = [{'path': str(f.relative_to(output)), 'sha256': digest(f.read_bytes()), 'bytes': f.stat().st_size}
                    for f in sorted(output.rglob('*')) if f.is_file() and not any(x in f.relative_to(output).parts for x in ('tmp', 'home'))]
        write(output / 'manifest.json', dict(source_sha=p['source_sha'], qualification=False, files=manifest))
    return 0 if receipt['ok'] else 1


def summarize(output, plan_path, results):
    p = json.loads(plan_path.read_text()); reports = {}
    for path in results.rglob('result.json'):
        r = json.loads(path.read_text()); index = r.get('index')
        if type(index) is not int or index in reports or not 0 <= index < len(p['shards']) or r.get('source_sha') != p['source_sha'] or r.get('plan_sha256') != digest(plan_path.read_bytes()):
            raise ValueError('duplicate, invalid or mismatched shard receipt')
        reports[index] = r
    missing = sorted(set(range(len(p['shards']))) - set(reports))
    empty_match = not any(x['Name'] for shard in p['shards'] for x in shard) or any(pkg['root_events'] for r in reports.values() for pkg in r['packages'])
    good = not missing and bool(empty_match) and all(r['ok'] and r['complete'] for r in reports.values())
    rows = ['# Lane tests / captures — not qualification', '', 'Run: ' + p['run_url'], '',
            f"Source: `{p['source_sha']}` · mode `{p['config']['mode']}` · {len(p['shards'])} shards", '',
            '| Shard | Result | Pass events | Skip events |', '| --- | --- | ---: | ---: |']
    for i in range(len(p['shards'])):
        r = reports.get(i); passed = sum(x['passed'] for x in r['packages']) if r else 0; skipped = sum(x['skipped'] for x in r['packages']) if r else 0
        rows.append(f"| {i} | {'PASS' if r and r['ok'] else 'FAIL/MISSING'} | {passed} | {skipped} |")
    if not empty_match:
        rows += ['', '**FAIL: no requested tests actually matched.**']
    if missing:
        rows += ['', '**Missing shards:** ' + ', '.join(map(str, missing))]
    evidence = [e for r in reports.values() for e in r['evidence']]
    if evidence:
        rows += ['', f"Capture files: {len(evidence)}; byte-identical to checked-out baselines: {sum(e['byte_identical_to_baseline'] for e in evidence)}. Differences require owner review; nothing is adopted."]
    output.mkdir(parents=True, exist_ok=True)
    (output / 'summary.md').write_text('\n'.join(rows) + '\n')
    write(output / 'summary.json', dict(ok=good, source_sha=p['source_sha'], qualification=False, missing=missing, reports=reports))
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as f:
            f.write((output / 'summary.md').read_text())
    print('Run URL: ' + p['run_url'])
    return 0 if good else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['plan', 'worker', 'summary'])
    parser.add_argument('--source', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--plan', type=Path)
    parser.add_argument('--index', type=int)
    parser.add_argument('--results', type=Path)
    args = parser.parse_args()
    if args.action == 'plan':
        plan(args.source.resolve(), args.output.resolve()); return 0
    if args.action == 'worker':
        return worker(args.source.resolve(), args.output.resolve(), args.plan, args.index)
    return summarize(args.output, args.plan, args.results)


if __name__ == '__main__':
    raise SystemExit(main())
