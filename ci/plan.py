#!/usr/bin/env python3
"""Conservative reset planner. No product tests or builds are executed here."""
import argparse
import json
import os
import math
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
COMPONENTS = ['web', 'packages', 'apps/native', 'apps/vega', 'apps/desktop', 'website']

def command(args, cwd=ROOT):
    return subprocess.check_output(args, cwd=cwd, text=True, timeout=180)

def decode_stream(raw):
    decoder = json.JSONDecoder(); result = []
    while raw.strip():
        obj, end = decoder.raw_decode(raw.lstrip()); result.append(obj); raw = raw.lstrip()[end:]
    return result

def base_head(event, env):
    head = command(['git', 'rev-parse', 'HEAD']).strip()
    if env.get('GITHUB_EVENT_NAME') == 'pull_request':
        target = event['pull_request']['base']['sha']
        base = command(['git', 'merge-base', head, target]).strip()
    elif env.get('GITHUB_EVENT_NAME') == 'merge_group':
        checkout=head
        base=event['merge_group']['base_sha']
        head=event['merge_group']['head_sha']
        if checkout != head: raise ValueError('checkout does not match merge group head')
        for ref in [base,head]:
            if not re.fullmatch('[0-9a-f]{40}',ref): raise ValueError('invalid merge group SHA')
            command(['git','cat-file','-e',ref+'^{commit}'])
        command(['git','merge-base','--is-ancestor',base,head])
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

def balance(tests, durations, count=None):
    if count is None: count = min(10, max(1, math.ceil(sum(durations.get(t['package']+'.'+t['name'],1) for t in tests)/150)))
    bins = [[] for _ in range(count)]; costs = [0.0]*count
    # Keep small packages together; split only packages whose measured sum exceeds 240s.
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

def components(paths, full=False):
    if full: return sorted(COMPONENTS), True
    chosen=set(); go=False
    for path in paths:
        if path.endswith(('.md','.png','.jpg','.jpeg','.gif','.webp','.svg','.ico')) and ((ROOT/path).is_file() or path.startswith(('server/','apikit/'))): continue
        if path.startswith(('server/', 'apikit/')): go=True
        elif path.startswith('packages/'):
            if path.startswith('packages/contracts/'): go=True
            chosen.update(COMPONENTS[:-1])
        elif any(path.startswith(c+'/') for c in COMPONENTS):
            chosen.update(c for c in COMPONENTS if path.startswith(c+'/'))
        else: chosen.update(COMPONENTS); go=True
    if 'web' in chosen: chosen.add('packages')
    return sorted(chosen), go

def android_selected(paths, full=False):
    return full or any(p.startswith(('apps/native/android/','apps/native/modules/','scripts/run-android-harnesses')) or
        (p.startswith('apps/native/') and (p.endswith(('.kt','.kts','.gradle')) or 'AndroidManifest' in p or 'transport' in p.lower())) for p in paths)

def go_seeds(packages, paths, full=False, diagnostics=None):
    all_packages={p['ImportPath'] for p in packages}
    if full: return all_packages
    seeds=set()
    for path in paths:
        if not path.startswith(('server/','apikit/')): continue
        if path.endswith(('.md','.png','.jpg','.jpeg','.gif','.webp','.svg','.ico')): continue
        if Path(path).name in ('go.mod','go.sum'):
            if diagnostics is not None: diagnostics.append(path+': Go manifest changed')
            return all_packages
        owners=[p for p in packages if path.startswith(str(Path(p['Dir']).relative_to(ROOT))+'/')]
        if not owners:
            if diagnostics is not None: diagnostics.append(path+': no build-aware owning Go package')
            return all_packages
        owner=max(owners,key=lambda p:len(p['Dir']))
        relative=str(Path(path).relative_to(Path(owner['Dir']).relative_to(ROOT)))
        embedded=owner.get('EmbedFiles',[])+owner.get('TestEmbedFiles',[])+owner.get('XTestEmbedFiles',[])
        if not (path.endswith('.go') or relative.startswith('testdata/') or relative in embedded):
            if diagnostics is not None: diagnostics.append(path+': unrecognized non-Go package input')
            return all_packages
        seeds.add(owner['ImportPath'])
    return seeds

def guarded_costs(packages, durations):
    # Read actual early guards, so the parent's R8 edits are the cost authority.
    adjusted=dict(durations)
    for p in packages:
        for file in p.get('TestGoFiles',[])+p.get('XTestGoFiles',[]):
            source=(Path(p['Dir'])/file).read_text()
            for match in re.finditer(r'^func\s+((?:Test|Fuzz)\w+)\([^\n]*\)\s*{(.*?)(?=^func |\Z)',source,re.M|re.S):
                if re.search(r'if testing.Short\(\)\s*{\s*\w+\.Skip',match[2][:700]):
                    adjusted[p['ImportPath']+'.'+match[1]]=0
    return adjusted

def make_plan(paths, full=False, tier="short"):
    selected, go = components(paths,full); go_fallbacks=[]; inventory=[]; exemptions=[]; packages=[]; modules=[]
    tags=command(['python3','scripts/release-build-tags.py','--capability']).strip()
    if go:
        for module in ['server','apikit']:
            raw=command(['go','list','-json','-tags',tags,'./...'], ROOT/module)
            listed=decode_stream(raw)
            for p in listed: p['module']=module
            packages.extend(listed); modules.append(module)
        probe_seeds={p['ImportPath'] for p in packages if any(re.search(r'ffmpeg|ffprobe', (Path(p['Dir'])/f).read_text(),re.I) for field in ['GoFiles','TestGoFiles','XTestGoFiles'] for f in p.get(field,[]))}
        probe_packages=closure(packages,probe_seeds)
        if full: go_fallbacks.append('explicit full inventory')
        elif not any(x.startswith(('server/','apikit/')) for x in paths): go_fallbacks.append('cross-component/root or contracts change without Go-owned paths')
        seeds = go_seeds(packages, paths, full or not any(x.startswith(('server/','apikit/')) for x in paths),diagnostics=go_fallbacks)
        chosen=closure(packages,seeds)
        if tier != 'short':
            chosen.update(p['ImportPath'] for p in packages if any(s in p['ImportPath'] for s in ['/httpapi','/identity','/auth','/session','/privacy']))
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
    tiers=json.loads((ROOT/'ci/tiers.json').read_text())
    all_inventory=list(inventory)
    diagnostics=[t for lane in tiers['nightly_lanes'].values() for t in lane]
    exclusions=diagnostics if tier=='nightly' else tiers['nightly_only']
    if tier=='short': durations=guarded_costs(packages,durations)
    excluded={(t['package'],t['name']) for t in exclusions}
    inventory=[t for t in inventory if (t['package'],t['name']) not in excluded]
    cost=sum(durations.get(t['package']+'.'+t['name'],1) for t in inventory)
    bins,costs=balance(inventory,durations)
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
    bins,costs=balance(inventory,durations)
    from explain import selection_reasons
    why=selection_reasons(paths,full,selected,go,components,js_reasons,go_fallbacks)
    return {'selection_diagnostics':why,'ffmpeg_packages':sorted(probe_packages) if go else [],'android':android_selected(paths,full),'all_inventory':all_inventory,'nightly_diagnostics':diagnostics,'js_owned_producers':assigned,'tier':tier,'paths':paths,'tags':tags,'packages':[{'module':p['module'],'package':p['ImportPath']} for p in packages], 'inventory':inventory,'shards':bins,'predicted_seconds':costs,'js':js,'tier_exclusions':exclusions, 'compile_only_examples':exemptions, 'unmeasured_parents':[t for t in inventory if t['package']+'.'+t['name'] not in durations]}

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

def vps_trusted(event, env):
    repo=env.get('GITHUB_REPOSITORY','')
    if repo.lower()!='porticomediaserver/portico': return False
    name=env.get('GITHUB_EVENT_NAME','')
    if name=='pull_request':
        pr=event.get('pull_request') or {}
        head=pr.get('head') or {}
        head_repo=head.get('repo') or {}
        full_name=head_repo.get('full_name')
        return isinstance(full_name,str) and full_name.lower()==repo.lower()
    if name=='push': return env.get('GITHUB_REF')=='refs/heads/main'
    return name in ('merge_group','schedule','workflow_dispatch')


def go_matrix(plan,event,env):
    trusted=vps_trusted(event,env) and env.get('CI_HOSTED_ONLY','').lower()!='true'
    # One heavy G12 slot; other Go shards stay hosted without changing selection.
    return {'include':[{'index':i,'runner':'portico-vps-go' if trusted and i<1 else 'ubuntu-24.04'}
                       for i in range(len(plan['shards']))]}


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
        paths=command(['git','diff','--name-only','-z',base,head]).split('\0'); paths=[p for p in paths if p]
    plan=make_plan(paths,a.full,'merge' if a.full and a.tier=='short' else a.tier); plan.update(base=base,head=head)
    Path(a.output).write_text(json.dumps(plan,indent=2)+'\n')
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        from explain import render_summary
        with open(os.environ['GITHUB_STEP_SUMMARY'],'a') as f: f.write(render_summary(plan))
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'],'a') as f:
            f.write('go_matrix='+json.dumps(go_matrix(plan,event,os.environ))+'\n')
            f.write('android='+str(plan['android']).lower()+'\n')
            f.write('go='+str(bool(plan['packages'])).lower()+'\n')
            f.write('js='+str(any(x['shards'] for x in plan['js'])).lower()+'\n')
            f.write('js_static='+str(bool(plan['js'])).lower()+'\n')
            f.write('js_static_matrix='+json.dumps({'include':[{'component':x['component']} for x in plan['js']]},separators=(',',':'))+'\n')
            f.write('js_matrix='+json.dumps({'include':[{'component':x['component'],'index':i} for x in plan['js'] for i in range(len(x['shards']))]},separators=(',',':'))+'\n')
if __name__=='__main__': main()
