#!/usr/bin/env python3
"""Conservative reset planner. No product tests or builds are executed here."""
import argparse
import fnmatch
import json
import os
import math
from pathlib import Path
import re
import subprocess

import source_readers

ROOT = Path(__file__).resolve().parents[1]
COMPONENTS = ['web', 'packages', 'apps/native', 'apps/vega', 'apps/desktop', 'website']
# Full (merge/nightly) Go shards are planned from measured parent weights so no
# single `go test` batch approaches its -timeout. A shard runs one complete-package
# command (-p=GO_PACKAGE_WORKERS) and then its split packages in a worker pool.
GO_TEST_DEADLINE = 780.0   # ci/run.py `go test -timeout`
GO_TEST_BUDGET = GO_TEST_DEADLINE * 0.6   # regression ceiling for any batch
GO_TEST_TARGET = GO_TEST_DEADLINE * 0.5   # planner's aim for every batch
GO_PACKAGE_WORKERS = 4     # PORTICO_CI_GO_PACKAGE_JOBS default
GO_PACKAGE_SPLIT_SECONDS = 150
UNMEASURED_PARENT_SECONDS = 20.0  # above the measured p99 unless named in the table

def command(args, cwd=ROOT):
    return subprocess.check_output(args, cwd=cwd, text=True, timeout=180)

def decode_stream(raw):
    decoder = json.JSONDecoder(); result = []
    while raw.strip():
        obj, end = decoder.raw_decode(raw.lstrip()); result.append(obj); raw = raw.lstrip()[end:]
    return result

def target_tip(group):
    """The commit the queue's target branch points at, from the planner's full-history checkout."""
    match=re.fullmatch(r'refs/heads/([A-Za-z0-9._/-]+)',group.get('base_ref',''))
    if not match: raise ValueError('merge group has no target branch ref')
    local='refs/remotes/origin/'+match[1]
    try: return command(['git','rev-parse','--verify','--quiet',local+'^{commit}']).strip()
    except subprocess.CalledProcessError:
        command(['git','fetch','--no-tags','origin','+refs/heads/'+match[1]+':'+local])
        return command(['git','rev-parse','--verify',local+'^{commit}']).strip()

def base_head(event, env):
    head = command(['git', 'rev-parse', 'HEAD']).strip()
    if env.get('GITHUB_EVENT_NAME') == 'pull_request':
        target = event['pull_request']['base']['sha']
        base = command(['git', 'merge-base', head, target]).strip()
    elif env.get('GITHUB_EVENT_NAME') == 'merge_group':
        checkout=head
        group=event['merge_group']
        head=group['head_sha']
        if checkout != head: raise ValueError('checkout does not match merge group head')
        for ref in [group['base_sha'],head]:
            if not re.fullmatch('[0-9a-f]{40}',ref): raise ValueError('invalid merge group SHA')
            command(['git','cat-file','-e',ref+'^{commit}'])
        command(['git','merge-base','--is-ancestor',group['base_sha'],head])
        # A group's own base_sha is the previous group's head, so a group whose diff held only web
        # files planned no Go tests even though the groups ahead of it changed Go and nothing had
        # merged yet. Plan against the target branch's tip: each group tests everything it carries
        # beyond main.
        base=command(['git','merge-base',head,target_tip(group)]).strip()
    elif env.get('GITHUB_EVENT_NAME') == 'push':
        base = event['before']
        if not re.fullmatch('[0-9a-f]{40}', base) or set(base) == {'0'}:
            raise ValueError('initial/deleted push needs explicit full plan')
        command(['git', 'cat-file', '-e', base + '^{commit}'])
    else:
        target = env.get('CI_BASE')
        if not target: raise ValueError('manual plan requires CI_BASE or --full')
        base = command(['git', 'merge-base', head, target]).strip()
    return base, head

def closure(packages, seeds):
    selected = set(seeds)
    while True:
        expanded = selected | {p['ImportPath'] for p in packages if selected.intersection(p.get('Imports', []) + p.get('TestImports', []) + p.get('XTestImports', []))}
        if expanded == selected: return selected
        selected = expanded

def full_go_packages(packages, paths):
    """PR full tests: changed package owners and one reverse-import hop, not the closure."""
    if any(Path(p).name in ('go.mod', 'go.sum', 'go.work', 'go.work.sum') for p in paths):
        return {p['ImportPath'] for p in packages}
    seeds = go_seeds(packages, paths)
    return seeds | {p['ImportPath'] for p in packages if seeds.intersection(
        p.get('Imports', []) + p.get('TestImports', []) + p.get('XTestImports', []))}

def balance(tests, durations, count=None, max_count=10):
    if count is None: count = min(max_count, max(1, math.ceil(sum(durations.get(t['package']+'.'+t['name'],1) for t in tests)/150)))
    bins = [[] for _ in range(count)]; costs = [0.0]*count
    # Keep small packages together; split only packages whose measured sum exceeds 150s.
    grouped = {}
    for t in sorted(tests,key=lambda t:(t['package'],t['name'])): grouped.setdefault(t['package'], []).append(t)
    units = []
    for group in grouped.values():
        cost = sum(durations.get(t['package']+'.'+t['name'], 1.0) for t in group)
        units.extend([[t] for t in group] if cost > 150 else [group])
    units.sort(key=lambda u: (-sum(durations.get(t['package']+'.'+t['name'], 1.0) for t in u), u[0]['package'], u[0]['name']))
    for unit in units:
        i = min(range(count), key=lambda i:(costs[i],i)); bins[i].extend(unit)
        costs[i] += sum(durations.get(t['package']+'.'+t['name'], 1.0) for t in unit)
    assert sorted((t['package'],t['name']) for b in bins for t in b) == sorted((t['package'],t['name']) for t in tests)
    return bins, costs

def observed_durations(durations, tier):
    """Merge/nightly price parents from the measured table over the short baseline."""
    if tier == 'short':
        return dict(durations)
    table = ROOT/'ci/merge_durations.json'
    if not table.is_file():
        return dict(durations)
    measured = dict(durations)
    measured.update({str(k): float(v) for k, v in json.loads(table.read_text()).items()})
    return measured


def parent_seconds(test, durations):
    return durations.get(test['package']+'.'+test['name'], UNMEASURED_PARENT_SECONDS)


def merge_units(tests, durations):
    """Per-test units for a heavy package, whole groups otherwise (deterministic)."""
    grouped = {}
    for t in sorted(tests, key=lambda t: (t['package'], t['name'])):
        grouped.setdefault(t['package'], []).append(t)
    units = []
    for group in grouped.values():
        if sum(parent_seconds(t, durations) for t in group) > GO_PACKAGE_SPLIT_SECONDS:
            units.extend([[t] for t in group])
        else:
            units.append(group)
    return units


def go_batch_model(bins, all_tests, durations, workers=GO_PACKAGE_WORKERS):
    """Model the commands ci/run.py runs per shard: one complete batch, then partials.

    Complete packages share one -p=workers command, so it costs the larger of its
    largest package and its summed work divided by the worker count. Each split
    package is its own command in a workers-wide pool.
    """
    rows = []
    for shard in bins:
        grouped = {}
        for t in shard:
            grouped.setdefault(t['package'], []).append(t)
        complete = []
        partial = []
        for package, tests in grouped.items():
            seconds = sum(parent_seconds(t, durations) for t in tests)
            if {t['name'] for t in tests} == all_tests.get(package, {t['name'] for t in tests}):
                complete.append((package, seconds, len(tests)))
            else:
                partial.append((package, seconds, len(tests)))
        complete_seconds = (max([sum(s for _, s, _ in complete)/workers]
                                + [s for _, s, _ in complete]) if complete else 0.0)
        pool_seconds = (max([max(s for _, s, _ in partial), sum(s for _, s, _ in partial)/workers])
                        if partial else 0.0)
        commands = ([{'kind': 'complete', 'packages': sorted(p for p, _, _ in complete),
                      'tests': sum(n for _, _, n in complete), 'seconds': round(complete_seconds, 1)}]
                    if complete else [])
        commands += [{'kind': 'partial', 'package': package, 'tests': count, 'seconds': round(seconds, 1)}
                     for package, seconds, count in sorted(partial)]
        rows.append({'seconds': round(complete_seconds+pool_seconds, 1),
                     'complete_seconds': round(complete_seconds, 1),
                     'partial_pool_seconds': round(pool_seconds, 1),
                     'commands': commands})
    return rows


def oversized_single_parents(bins, all_tests, durations):
    """Parents that alone exceed the budget; the planner cannot split a test by name."""
    found = []
    for row in go_batch_model(bins, all_tests, durations):
        for command in row['commands']:
            if (command['kind'] == 'partial' and command['tests'] == 1
                    and command['seconds'] > GO_TEST_BUDGET):
                found.append({'package': command['package'], 'seconds': command['seconds']})
    return sorted(found, key=lambda x: (-x['seconds'], x['package']))


def assert_merge_shard_budget(bins, tests, all_tests, durations):
    """Fail closed if a full-inventory shard plans a batch over budget.

    A single parent that alone exceeds the budget is reported, not failed: it
    cannot be split by test name. It must still fit the 780 s deadline itself.
    """
    flattened = sorted((t['package'], t['name']) for shard in bins for t in shard)
    if len(set(flattened)) != len(flattened):
        raise ValueError('go shard assignment duplicates a parent')
    if flattened != sorted((t['package'], t['name']) for t in tests):
        raise ValueError('go shard assignment does not cover every parent exactly once')
    for index, row in enumerate(go_batch_model(bins, all_tests, durations)):
        for command in row['commands']:
            limit = GO_TEST_DEADLINE if command['kind'] == 'partial' and command['tests'] == 1 else GO_TEST_BUDGET
            if command['seconds'] > limit:
                label = command.get('package') or ','.join(command.get('packages', []))
                raise ValueError(f'go shard {index} {command["kind"]} batch {label} '
                                 f'is {command["seconds"]}s, over its {limit:.0f}s limit')
    return go_batch_model(bins, all_tests, durations)


def balance_merge(tests, durations, all_tests, target=GO_TEST_TARGET, max_count=128, shard_target=None):
    """Deterministic weighted open-shop packing for full Go shards.

    Parents whose measured weight alone exceeds the per-command target get their
    own shard (two of them could never share a batch). Everything else longest-
    processing-time packs into the fewest shards whose modeled commands fit.
    The ceiling is 128: GitHub matrices allow 256 jobs, and the imported trains
    keep growing the full inventory past the old 64-shard limit.
    shard_target also caps a shard's serialized wall clock (complete batch then
    partial pool); the short tier uses it so its predicted seconds stay
    truthful. A lone parent over the target remains the documented exception.
    """
    units = [(sum(parent_seconds(t, durations) for t in unit), unit) for unit in merge_units(tests, durations)]
    heavy = sorted((unit for weight, unit in units if weight > target),
                   key=lambda u: (-sum(parent_seconds(t, durations) for t in u), u[0]['package'], u[0]['name']))
    rest = sorted((unit for weight, unit in units if weight <= target),
                  key=lambda u: (-sum(parent_seconds(t, durations) for t in u), u[0]['package'], u[0]['name']))
    for count in range(1, max_count+1):
        # A parent longer than the target cannot share a batch with anything, so
        # its shard is closed before the remaining units are packed.
        bins = [[t] for unit in heavy for t in unit] + [[] for _ in range(count)]
        costs = [sum(parent_seconds(t, durations) for t in shard) for shard in bins]
        for unit in rest:
            index = min(range(len(heavy), len(bins)), key=lambda i: (costs[i], i))
            bins[index].extend(unit)
            costs[index] += sum(parent_seconds(t, durations) for t in unit)
        fits = True
        rows = go_batch_model(bins, all_tests, durations)
        for row in rows:
            for command in row['commands']:
                single = command['kind'] == 'partial' and command['tests'] == 1
                if command['seconds'] > (GO_TEST_DEADLINE if single else target):
                    fits = False
            lone_heavy = (len(row['commands']) == 1 and row['commands'][0]['kind'] == 'partial'
                          and row['commands'][0]['tests'] == 1)
            if shard_target is not None and row['seconds'] > shard_target and not lone_heavy:
                fits = False
        if fits:
            return bins, costs
    raise ValueError(f'the full Go inventory needs more than {max_count} shards to fit the batch budget')


SHORT_CENSUSES = {
    ('github.com/PorticoMediaServer/portico/server/internal/persistence', 'TestProductionStatementsPrepareAgainstTheBaseline'),
    ('github.com/PorticoMediaServer/portico/server/internal/persistence', 'TestNoInsertOrIgnoreUpdateTriggerOnAnUpsertedTable'),
}

def balance_short_censuses(tests, durations, max_count=10):
    census = [t for t in tests if (t['package'], t['name']) in SHORT_CENSUSES]
    if not census: return balance(tests, durations, max_count=max_count)
    # Preserve the previous short layout, where these guards cost zero, then
    # move only the censuses to their own VM. Their added CPU must neither
    # contend with interactive fixtures nor reshuffle those fixtures together.
    layout_costs = dict(durations)
    layout_costs.update({t['package']+'.'+t['name']: 0 for t in census})
    bins, costs = balance(tests, layout_costs, max_count=max_count)
    ordinary = [([t for t in shard if (t['package'],t['name']) not in SHORT_CENSUSES], cost)
                for shard,cost in zip(bins,costs)]
    ordinary = [(shard,cost) for shard,cost in ordinary if shard]
    return ([shard for shard,cost in ordinary] + [census],
            [cost for shard,cost in ordinary] + [sum(durations.get(t['package']+'.'+t['name'],1) for t in census)])


def split_short_censuses(tests):
    census = [t for t in tests if (t['package'], t['name']) in SHORT_CENSUSES]
    ordinary = [t for t in tests if (t['package'], t['name']) not in SHORT_CENSUSES]
    return ordinary, census


def balance_short_large(tests, durations, all_tests, max_count=128):
    """Size a large short-tier inventory by the merge budget, not a shard cap.

    Promoted PR work gets more VMs, not longer deadlines. The 10-shard LPT
    layout (kept for ordinary PRs) stacks every inferred full inventory into
    ~760s batches, leaving no room for compile, link or run-to-run variance
    under the 780s `go test -timeout`. Here the short tier reuses the merge
    tier's weighted open-shop packing at its own guarded durations: a parent
    over the target gets a shard alone, everything else packs into the fewest
    shards whose modeled commands fit the target. The 128 ceiling is only a
    guard against an inventory that cannot be planned. Censuses keep the same
    isolation the small-tier layout gives them.
    """
    ordinary, census = split_short_censuses(tests)
    bins = (balance_merge(ordinary, durations, all_tests, max_count=max_count,
                          shard_target=GO_TEST_TARGET)[0] if ordinary else [])
    if census: bins.append(census)
    # Fail closed exactly like the merge tier: a modeled command over budget (or
    # a single parent over the deadline) cannot reach the runner.
    rows = assert_merge_shard_budget(bins, tests, all_tests, durations)
    # predicted_seconds is the modeled wall clock for the shard, which is what
    # the deadline applies to; a single parent keeps its own seconds.
    return bins, [row['seconds'] for row in rows]


def balance_short(tests, durations, all_tests, max_count=10, large_durations=None):
    """Short-tier dispatch: ordinary PRs keep the LPT layout and its 10-shard cap.

    Once the inventory is promoted/full or the LPT layout would model a shard
    over GO_TEST_TARGET, the budget (not the cap) sizes the shards. A promoted
    inventory is priced by the caller's measured full table: ci/durations.json
    only covers the parents ordinary short PRs run, and the 20s unmeasured
    default for the rest would plan hundreds of shards.
    """
    bins, costs = balance_short_censuses(tests, durations, max_count=max_count)
    rows = go_batch_model(bins, all_tests, durations)
    if max(costs, default=0.0) <= GO_TEST_TARGET and all(row['seconds'] <= GO_TEST_TARGET for row in rows):
        return bins, costs
    return balance_short_large(tests, large_durations if large_durations is not None else durations,
                               all_tests)

def components(paths, full=False):
    if full: return sorted(COMPONENTS), True
    chosen=set(); go=False
    for path in paths:
        # Go metadata must distinguish documentation from fixtures/embedded
        # inputs before their suffix can suppress package discovery.
        if path.startswith(('server/', 'apikit/')): go=True
        elif path.endswith(('.md','.png','.jpg','.jpeg','.gif','.webp','.svg','.ico')) and (ROOT/path).is_file(): continue
        elif path.startswith('packages/'):
            if path.startswith('packages/contracts/'): go=True
            chosen.update(COMPONENTS[:-1])
        elif any(path.startswith(c+'/') for c in COMPONENTS):
            chosen.update(c for c in COMPONENTS if path.startswith(c+'/'))
        else: chosen.update(COMPONENTS); go=True
    if 'web' in chosen: chosen.add('packages')
    return sorted(chosen), go

def android_selected(paths, full=False):
    return full or any(p in ('.github/workflows/android-tests.yml', 'ci/robolectric_sdk.py',
        'ci/robolectric-sdk.json', 'ci/robolectric-offline.gradle') or
        p.startswith(('apps/native/android/','apps/native/modules/','scripts/run-android-harnesses')) or
        (p.startswith('apps/native/') and (p.endswith(('.kt','.kts','.gradle')) or 'AndroidManifest' in p or 'transport' in p.lower())) for p in paths)

def embedded_input(owner, relative, deleted):
    if relative in owner.get('EmbedFiles',[])+owner.get('TestEmbedFiles',[])+owner.get('XTestEmbedFiles',[]):
        return True
    if not deleted: return False
    # Deleted inputs disappear from EmbedFiles. Directory prefixes preserve
    # recursive embeds; ambiguous Go pattern syntax conservatively keeps owner.
    prefixes=['/'.join(relative.split('/')[:i]) for i in range(1,len(relative.split('/'))+1)]
    patterns=owner.get('EmbedPatterns',[])+owner.get('TestEmbedPatterns',[])+owner.get('XTestEmbedPatterns',[])
    return any('[' in p or '\\' in p or any(fnmatch.fnmatchcase(prefix,p.removeprefix('all:')) for prefix in prefixes)
               for p in patterns)

def go_seeds(packages, paths, full=False, diagnostics=None):
    all_packages={p['ImportPath'] for p in packages}
    if full: return all_packages
    seeds=set()
    for path in paths:
        if not path.startswith(('server/','apikit/')): continue
        documentation=path.endswith(('.md','.png','.jpg','.jpeg','.gif','.webp','.svg','.ico'))
        if Path(path).name in ('go.mod','go.sum'):
            if diagnostics is not None: diagnostics.append(path+': Go manifest changed')
            return all_packages
        owners=[p for p in packages if path.startswith(str(Path(p['Dir']).relative_to(ROOT))+'/')]
        if not owners:
            if documentation and '/testdata/' not in path: continue
            if diagnostics is not None: diagnostics.append(path+': no build-aware owning Go package')
            return all_packages
        owner=max(owners,key=lambda p:len(p['Dir']))
        relative=str(Path(path).relative_to(Path(owner['Dir']).relative_to(ROOT)))
        if not (path.endswith('.go') or relative.startswith('testdata/') or embedded_input(owner,relative,not (ROOT/path).exists())):
            if documentation and '/testdata/' not in path: continue
            if diagnostics is not None: diagnostics.append(path+': unrecognized non-Go package input')
            return all_packages
        seeds.add(owner['ImportPath'])
    return seeds

def guarded_costs(packages, durations, full_packages=()):
    # Read actual early guards, so the parent's R8 edits are the cost authority.
    adjusted=dict(durations)
    for p in packages:
        if p['ImportPath'] in full_packages: continue
        for file in p.get('TestGoFiles',[])+p.get('XTestGoFiles',[]):
            source=(Path(p['Dir'])/file).read_text()
            for match in re.finditer(r'^func\s+((?:Test|Fuzz)\w+)\([^\n]*\)\s*{(.*?)(?=^func |\Z)',source,re.M|re.S):
                if re.search(r'if testing.Short\(\)\s*{\s*\w+\.Skip',match[2][:700]):
                    adjusted[p['ImportPath']+'.'+match[1]]=0
    return adjusted

def reader_packages(listed, hits):
    """{import path: {test: [changed path]}} for the declared source-reading tests the changed paths reach."""
    by_dir={str(Path(p['Dir']).relative_to(ROOT)):p for p in listed}; chosen={}
    for directory,tests in hits.items():
        if directory not in by_dir: raise ValueError('source reader package is not a Go package: '+directory)
        chosen[by_dir[directory]['ImportPath']]=tests
    return chosen

def make_plan(paths, full=False, tier="short"):
    selected, go = components(paths,full); go_fallbacks=[]; inventory=[]; exemptions=[]; packages=[]; modules=[]
    # Go-owned means the change itself reaches Go (server/ or apikit/, contracts, the root). A change that
    # only reaches a test reading its files (packages/i18n, web/public) selects just those tests.
    go_owned=go; readers={} if full else source_readers.select(paths); reader_tests={}; go=go or bool(readers)
    full_packages=set()
    tags=command(['python3','scripts/release-build-tags.py','--capability']).strip()
    if go:
        # Include the real HTTP/client boundary in build-aware test discovery.
        listing_tags = tags + ',reading_client_integration'
        for module in ['server','apikit']:
            raw=command(['go','list','-json','-tags',listing_tags,'./...'], ROOT/module)
            listed=[p for p in decode_stream(raw) if not re.search(r'/server/internal/store(?:/|$)', p['ImportPath'])]
            for p in listed: p['module']=module
            packages.extend(listed); modules.append(module)
        probe_seeds={p['ImportPath'] for p in packages if any(re.search(r'ffmpeg|ffprobe', (Path(p['Dir'])/f).read_text(),re.I) for field in ['GoFiles','TestGoFiles','XTestGoFiles'] for f in p.get(field,[]))}
        probe_packages=closure(packages,probe_seeds)
        far=go_owned and not any(x.startswith(('server/','apikit/')) for x in paths)
        if full: go_fallbacks.append('explicit full inventory')
        elif far: go_fallbacks.append('cross-component/root or contracts change without Go-owned paths')
        seeds = go_seeds(packages, paths, full or far,diagnostics=go_fallbacks)
        if not seeds and not full and not any(components([p])[1] for p in paths if not p.startswith(('server/','apikit/'))):
            go_owned=False  # ordinary Go-tree docs needed metadata, not tests
        if tier == 'short': full_packages = full_go_packages(packages, paths)
        chosen=closure(packages,seeds)
        if tier != 'short' and go_owned:
            chosen.update(p['ImportPath'] for p in packages if any(s in p['ImportPath'] for s in ['/httpapi','/identity','/auth','/session','/privacy']))
        # Tests that read other packages' files (source censuses and guards) are selected whenever those
        # files change, which the import graph cannot see. A package already chosen runs all its tests.
        reader_tests={k:v for k,v in reader_packages(packages,readers).items() if k not in chosen}
        chosen.update(reader_tests)
        go=bool(chosen)
        if go: tags=listing_tags
        packages=[p for p in packages if p['ImportPath'] in chosen]
        for p in packages:
            # go list's build-aware files; include examples and fuzz seed parents.
            for file in p.get('TestGoFiles',[])+p.get('XTestGoFiles',[]):
                source=(Path(p['Dir'])/file).read_text()
                for name in re.findall(r'^func\s+((?:Test|Fuzz)[A-Z0-9_]\w*|Example\w*)\s*\(',source,re.M):
                    if name=='TestMain':
                        exemptions.append({'package':p['ImportPath'],'name':name,'reason':'package lifecycle hook; not a runnable test parent'})
                        continue
                    # Go examples without Output are compile-only by Go's own contract.
                    if name.startswith('Example'):
                        tail=source[source.index('func '+name):]
                        tail=re.split(r'\nfunc ',tail,maxsplit=1)[0]
                        if not re.search(r'(?im)//\s*(?:unordered )?output:',tail):
                            exemptions.append({'package':p['ImportPath'],'name':name,'reason':'example without Output; compiled but not run by Go'})
                            continue
                    inventory.append({'module':p['module'],'package':p['ImportPath'],'name':name})
    inventory=list({(t['package'],t['name']):t for t in inventory}.values())
    durations=json.loads((ROOT/'ci/durations.json').read_text())
    durations={k.replace('portico.local/server','github.com/PorticoMediaServer/portico/server'):v for k,v in durations.items()}
    durations=observed_durations(durations,tier)
    tiers=json.loads((ROOT/'ci/tiers.json').read_text())
    all_inventory=list(inventory)
    # A package chosen only for its source-reading tests runs just those: the rest of its tests are unrelated.
    for package,names in reader_tests.items():
        missing=sorted(names.keys()-{t['name'] for t in inventory if t['package']==package})
        if missing: raise ValueError('source reader tests not in '+package+': '+', '.join(missing))
    inventory=[t for t in inventory if t['package'] not in reader_tests or t['name'] in reader_tests[t['package']]]
    diagnostics=[t for lane in tiers['nightly_lanes'].values() for t in lane]
    exclusions=diagnostics if tier=='nightly' else tiers['nightly_only']
    short_durations=guarded_costs(packages,durations) if tier=='short' else durations
    # A promoted short PR runs the whole Go inventory, but ci/durations.json
    # only measured the parents ordinary PRs exercise. Price that inventory
    # from ci/merge_durations.json (over the short baseline) before choosing
    # shards; the unmeasured default stays 20s for anything still absent.
    promoted_durations=(guarded_costs(packages,observed_durations(durations,'merge'),full_packages)
                        if tier=='short' else None)
    if tier=='short': durations=guarded_costs(packages,durations,full_packages)
    excluded={(t['package'],t['name']) for t in exclusions}
    inventory=[t for t in inventory if (t['package'],t['name']) not in excluded]
    all_test_names={}
    for t in all_inventory: all_test_names.setdefault(t['package'],set()).add(t['name'])
    def shards(tests):
        # Promoted PR work gets more VMs, not longer deadlines. Queue/nightly
        # size their shards from measured parent weights so every go test batch
        # stays under the deadline as the inventory grows.
        if tier=='short':
            if full_packages: return balance_short_large(tests,promoted_durations,all_test_names)
            return balance_short(tests,durations,all_test_names,large_durations=promoted_durations)
        return balance_merge(tests,durations,all_test_names)
    cost=sum(parent_seconds(t,durations) for t in inventory)
    js=[]; js_reasons={}
    from js_affected import select as affected_js
    for component in selected:
        roots=list((ROOT/'packages').iterdir()) if component=='packages' else [ROOT/component]
        files=sorted(str(f.relative_to(ROOT)) for root in roots for f in root.rglob('*') if f.is_file() and re.search(r'\.test\.(?:[cm]?js|tsx?)$',f.name) and not any(x in f.parts for x in ['node_modules','third_party','dist','build']))
        if tier=='short' and not full and component!='website':
            js_reasons[component]={}
            files=affected_js(ROOT,paths,files,diagnostics=js_reasons[component])
        else: js_reasons[component]={'mode':'full component inventory' if full or tier!='short' else 'website check'}
        if component!='website' and not files: raise ValueError('empty JS selection: '+component)
        js.append({'component':component,'files':files})
    from js_shards import extend_selection
    extend_selection(js, json.loads((ROOT/'ci/js-durations.json').read_text())['seconds'])
    producer_consumers = {
        'TestHomeReadingSavedDeferredHTTP':'home-reading-wire.test.ts',
        'TestConcurrencyDiagnosticsMemoryLimitWire':'concurrency-memory-limit.test.ts',
        'TestConsoleRegistryProducerRecordsNullableDefaults':'console-registry-null-defaults.test.ts',
        'TestInvitationAcceptanceProducerContract':'invitation-producer-contract.test.ts',
        'TestResetFileHTTPProducerContractsAndAuthority':'reset-producer-contract.test.ts',
    }
    js_files=[f for item in js for f in item['files']]
    assigned=[t for t in inventory if '/httpapi' in t['package'] and t['name'] in producer_consumers and any(f.endswith(producer_consumers[t['name']]) for f in js_files)]
    assigned_keys={(t['package'],t['name']) for t in assigned}
    inventory=[t for t in inventory if (t['package'],t['name']) not in assigned_keys]
    bins,costs=shards(inventory)
    from explain import selection_reasons
    why=selection_reasons(paths,full,selected,go_owned,components,js_reasons,go_fallbacks,source_readers=reader_tests)
    extra=sum(durations.get(t['package']+'.'+t['name'],1)-short_durations.get(t['package']+'.'+t['name'],1) for t in inventory)
    budget={}
    if tier!='short':
        rows=assert_merge_shard_budget(bins,inventory,all_test_names,durations)
        budget={'deadline_seconds':GO_TEST_DEADLINE,'budget_seconds':GO_TEST_BUDGET,'target_seconds':GO_TEST_TARGET,
                'workers':GO_PACKAGE_WORKERS,'commands_seconds':[row['seconds'] for row in rows],
                'oversized_single_tests':oversized_single_parents(bins,all_test_names,durations)}
    return {'full_go_packages':sorted(full_packages), 'full_go_extra_seconds':extra,'selection_diagnostics':why,'source_reader_tests':[{'package':k,'name':n,'changed':c} for k,v in sorted(reader_tests.items()) for n,c in sorted(v.items())],'ffmpeg_packages':sorted(probe_packages) if go else [],'android':android_selected(paths,full),'all_inventory':all_inventory,'nightly_diagnostics':diagnostics,'js_owned_producers':assigned,'tier':tier,'paths':paths,'tags':tags,'packages':[{'module':p['module'],'package':p['ImportPath']} for p in packages], 'inventory':inventory,'shards':bins,'predicted_seconds':costs,'shard_budget':budget,'js':js,'tier_exclusions':exclusions, 'compile_only_examples':exemptions, 'unmeasured_parents':[t for t in inventory if t['package']+'.'+t['name'] not in durations]}

def event_tier(event, tier, full):
    if event in ['pull_request','push']:
        if tier not in ['', 'short'] or full: raise ValueError('PR/push requires affected short tier')
        return 'short', False
    if event == 'merge_group':
        if tier not in ['', 'merge'] or full: raise ValueError('merge_group requires exact queue diff and deterministic merge tier')
        return 'merge', False
    if event == 'workflow_dispatch': return ('merge' if full and tier != 'nightly' else tier or 'short'), full
    if event == 'schedule' and tier == 'nightly': return tier, True
    if event: raise ValueError('unsupported CI event')
    return tier or 'short', full

def go_matrix(plan):
    return {'include':[dict(index=i, **({'reading_client': True} if any(
        t['name']=='TestReadingHTTPClientPublication' for t in shard) else {}))
                       for i,shard in enumerate(plan['shards'])]}


def main():
    ap=argparse.ArgumentParser(); ap.add_argument('--tier',choices=['short','merge','nightly'],default=''); ap.add_argument('--full',action='store_true'); ap.add_argument('--paths',nargs='*'); ap.add_argument('--output',default='ci-plan.json'); a=ap.parse_args()
    base=None; head=None
    event=json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text()) if os.environ.get('GITHUB_EVENT_PATH') else {}
    a.full = a.full or os.environ.get('CI_FULL_PLAN','').lower() == 'true'
    a.tier,a.full=event_tier(os.environ.get('GITHUB_EVENT_NAME',''),a.tier,a.full)
    head=command(['git','rev-parse','HEAD']).strip()
    if a.paths is not None: paths=a.paths
    elif a.full: paths=[]
    else:
        base,head=base_head(event,os.environ)
        # Treat renames as delete+add so both package owners (and source readers)
        # see their changed input, including a source missing from this checkout.
        paths=command(['git','diff','--no-renames','--name-only','-z',base,head]).split('\0'); paths=[p for p in paths if p]
    plan=make_plan(paths,a.full,'merge' if a.full and a.tier=='short' else a.tier); plan.update(base=base,head=head)
    Path(a.output).write_text(json.dumps(plan,indent=2)+'\n')
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        from explain import render_summary
        with open(os.environ['GITHUB_STEP_SUMMARY'],'a') as f: f.write(render_summary(plan))
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'],'a') as f:
            f.write('go_matrix='+json.dumps(go_matrix(plan))+'\n')
            f.write('android='+str(plan['android']).lower()+'\n')
            f.write('go='+str(bool(plan['packages'])).lower()+'\n')
            f.write('js='+str(any(x['shards'] for x in plan['js'])).lower()+'\n')
            f.write('js_static='+str(bool(plan['js'])).lower()+'\n')
            f.write('js_static_matrix='+json.dumps({'include':[{'component':x['component']} for x in plan['js']]},separators=(',',':'))+'\n')
            f.write('js_matrix='+json.dumps({'include':[{'component':x['component'],'index':i} for x in plan['js'] for i in range(len(x['shards']))]},separators=(',',':'))+'\n')
if __name__=='__main__': main()
