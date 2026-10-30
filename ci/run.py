#!/usr/bin/env python3
"""Execute a recorded selection, preserving exact terminal events and diagnostics."""
import argparse
import datetime
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from plan import ROOT

def policy(data, today=None):
    today=today or datetime.date.today(); entries={}
    for e in data['entries']:
        if not all(e.get(k) for k in ['package','test','owner','expires','reason']): raise ValueError('incomplete quarantine')
        if datetime.date.fromisoformat(e['expires']) < today: raise ValueError('expired quarantine')
        key=(e['package'],e['test'])
        if key in entries: raise ValueError('duplicate quarantine')
        entries[key]=e
    if entries and data.get('status')!='approved': raise ValueError('quarantine not approved')
    return entries

def classify(events, expected, returncode, allowed):
    terminals={}; failures=set(); started=set(); package_terminal=None; dangerous=False
    for e in events:
        output=e.get('Output','')
        if re.match(r'^(?:panic: test timed out after|WARNING: DATA RACE)',output) or e.get('Action')=='build-fail': dangerous=True
        if re.match(r'^(?:panic: |fatal error:)',output):
            name=e.get('Test')
            terminal=next((x.get('Action') for x in reversed(events) if x.get('Test')==name and x.get('Action') in ['pass','fail','skip']),None)
            if terminal not in ['pass','skip']: dangerous=True
        name=e.get('Test'); action=e.get('Action')
        if name and action=='run': started.add(name)
        if not name and action in ['pass','fail','skip']: package_terminal=action
        if name and action in ['pass','fail','skip']: terminals[name]=action
        if name and action=='fail': failures.add(name.split('/')[0])
    if dangerous or package_terminal is None or any(t not in terminals for t in set(expected)|started): return 'blocked'
    if failures:
        if returncode!=1 or package_terminal!='fail' or not failures.issubset(allowed): return 'blocked'
        return 'quarantined'
    return 'pass' if returncode==0 and package_terminal in ['pass','skip'] else 'blocked'

def execute(args,cwd=ROOT):
    subprocess.run(args,cwd=cwd,check=True,timeout=900)

def go(plan,index):
    validate_tags(plan)
    helper=ROOT/'ci-tools/portico-server'
    if not helper.is_file(): build_helper(plan)
    helper.chmod(0o755)
    os.environ['PORTICO_PLAYBACK_TEST_HELPER']=str(helper)
    selected=plan['shards'][index]
    allowed=policy(json.loads((ROOT/'ci/quarantine.json').read_text())); blocked=False
    out=ROOT/'ci-results'; out.mkdir(exist_ok=True)
    for module, groups, whole in batches(plan, selected):
        args=['go','test','-json','-count=1','-p=4','-timeout=780s','-tags',plan['tags']]
        if plan.get('tier')=='short': args+=['-short']
        if not whole: args+=['-run','^(?:'+'|'.join(re.escape(t['name']) for t in groups[0][1])+')$']
        args += [package for package,tests in groups]
        result=subprocess.run(args,cwd=ROOT/module,capture_output=True,text=True,timeout=900)
        try: events=[json.loads(line) for line in result.stdout.splitlines()]
        except ValueError: events=[]
        requested={package for package,tests in groups}
        if any(e.get('Package') not in requested for e in events): blocked=True
        if result.returncode==1 and not any(not e.get('Test') and e.get('Action')=='fail' for e in events): blocked=True
        for package,tests in groups:
            own=[e for e in events if e.get('Package')==package]
            stem=package.replace('/','_')
            (out/(stem+'.jsonl')).write_text(''.join(json.dumps(e)+'\n' for e in own))
            (out/(stem+'.stderr')).write_text(result.stderr)
            permitted={test for (pkg,test) in allowed if pkg==package}
            # A sibling may fail a batched command: use each package's terminal,
            # but never hide a tool/infrastructure exit outside Go's 0/1 contract.
            terminal=next((e['Action'] for e in reversed(own) if not e.get('Test') and e.get('Action') in ['pass','fail','skip']),None)
            code=(0 if terminal in ['pass','skip'] else 1) if result.returncode in [0,1] else result.returncode
            status=classify(own,[t['name'] for t in tests],code,permitted)
            print(package,status,flush=True); blocked |= status=='blocked'
    if blocked: raise SystemExit(1)

def batches(plan, selected):
    grouped={}
    for t in selected: grouped.setdefault((t['module'],t['package']),[]).append(t)
    all_tests={}
    for t in plan['all_inventory']: all_tests.setdefault(t['package'],set()).add(t['name'])
    complete={}; partial=[]
    for (module,package),tests in sorted(grouped.items()):
        if {t['name'] for t in tests} == all_tests[package]: complete.setdefault(module,[]).append((package,tests))
        else: partial.append((module,[(package,tests)],False))
    return [(m,groups,True) for m,groups in sorted(complete.items())]+partial

def build_helper(plan):
    (ROOT/'ci-tools').mkdir(exist_ok=True)
    execute(['go','build','-tags',plan['tags'],'-o',str(ROOT/'ci-tools/portico-server'),'./cmd/server'],ROOT/'server')

def download():
    for module in ['server','apikit']: execute(['go','mod','download'],ROOT/module)

def ensure_ffmpeg(plan, index=0, component=None):
    import shutil
    if component:
        files=next(x['files'] for x in plan['js'] if x['component']==component)
        needed=any(re.search(r'ffmpeg|ffprobe|go[\s\S]{0,80}test', (ROOT/f).read_text(),re.I) for f in files)
    else:
        packages={t['package'] for t in plan['shards'][index]}
        needed=bool(packages.intersection(plan.get('ffmpeg_packages',[])))
        for p in plan['packages']:
            if p['package'] not in packages: continue
            suffix=p['package'].split('/portico/'+p['module'],1)[1].lstrip('/')
            needed |= any(re.search(r'ffmpeg|ffprobe',f.read_text(),re.I) for f in (ROOT/p['module']/suffix).glob('*.go'))
    if needed and not (shutil.which('ffmpeg') and shutil.which('ffprobe')):
        execute(['sudo','apt-get','update']); execute(['sudo','apt-get','install','-y','ffmpeg'])

def fixture_producers(files, directory):
    producers = [
        ('home-reading-wire.test.ts', 'PORTICO_READING_HOME_FIXTURE_DIR', 'TestHomeReadingSavedDeferredHTTP'),
        ('concurrency-memory-limit.test.ts', 'PORTICO_CONCURRENCY_DIAGNOSTICS_FIXTURE_DIR', 'TestConcurrencyDiagnosticsMemoryLimitWire'),
        ('console-registry-null-defaults.test.ts', 'PORTICO_CONSOLE_REGISTRY_FIXTURE_DIR', 'TestConsoleRegistryProducerRecordsNullableDefaults'),
    ]
    for suffix, variable, test in producers:
        if any(f.endswith(suffix) for f in files):
            target = Path(directory)/test; target.mkdir()
            os.environ[variable] = str(target)
            # Recording must run full even when feedback source guards skip under -short.
            execute(['go','test','-count=1','-run','^'+test+'$','./internal/httpapi'],ROOT/'server')
            if not list(target.glob('*.json')): raise ValueError('producer did not record fixtures: '+test)

def js(plan,component):
    with tempfile.TemporaryDirectory(prefix='portico-ci-fixtures-') as directory:
        saved = {k:os.environ.get(k) for k in ['PORTICO_READING_HOME_FIXTURE_DIR','PORTICO_CONCURRENCY_DIAGNOSTICS_FIXTURE_DIR','PORTICO_CONSOLE_REGISTRY_FIXTURE_DIR']}
        try:
            fixture_producers(next(x for x in plan['js'] if x['component']==component)['files'],directory)
            js_tests(plan,component)
        finally:
            for k,v in saved.items():
                if v is None: os.environ.pop(k,None)
                else: os.environ[k]=v

def js_tests(plan,component):
    item=next(x for x in plan['js'] if x['component']==component)
    if component=='website': execute(['npm','run','check'],ROOT/component); return
    if not item['files']: raise ValueError('refusing empty node test list')
    execute(['npm','run','build'],ROOT/'packages/contracts')
    roots={}
    for file in item['files']:
        parts=Path(file).parts; group='/'.join(parts[:2]) if parts[0] in ['apps','packages'] else parts[0]
        roots.setdefault(group,[]).append(str(Path(file).relative_to(group)))
    for group,files in roots.items():
        args=['node','--experimental-transform-types']
        if group=='apps/native': args+=['--experimental-vm-modules']
        if (ROOT/group/'test/register.mjs').exists(): args+=['--import','./test/register.mjs']
        execute(args+['--test-timeout=120000','--test-concurrency=4','--test',*files],ROOT/group)
        pkg=json.loads((ROOT/group/'package.json').read_text()); scripts=pkg.get('scripts',{})
        if (ROOT/group/'tsconfig.json').exists(): execute(['npx','--no-install','tsc','--noEmit'],ROOT/group)
        for name in ['lint:boundary','lint:hooks','lint:strings','lint:design','lint:routes','tokens']:
            if name in scripts: execute(['npm','run',name],ROOT/group)
    if component=='apps/desktop': execute(['npm','run','test:rust'],ROOT/component)

def aggregate(plan,needs):
    expected={'planner':True,'hygiene':True,'go_build':bool(plan['packages']),'go_tests':bool(plan['packages']),'js':bool(plan['js']), 'android':plan.get('android',False)}
    for job,required in expected.items():
        actual=needs.get(job,{}).get('result')
        if actual != ('success' if required else 'skipped'): raise ValueError(f'{job}: unexpected {actual}')

def validate_tags(plan):
    execute(['python3','scripts/release-build-tags.py','--capability','--check',plan['tags']])

def build(plan):
    validate_tags(plan)
    execute(['go','run','-tags',plan['tags'],'./cmd/apigen','-root','..'],ROOT/'server')
    execute(['git','diff','--exit-code'])
    for module in ['server','apikit']:
        for tags in ['',plan['tags']]:
            execute(['go','build','-p=2','-tags',tags,'./...'],ROOT/module)
            execute(['sh',str(ROOT/'scripts/vet-go.sh'),'--tags',tags],ROOT/module)
    build_helper(plan)

if __name__=='__main__':
    ap=argparse.ArgumentParser(); ap.add_argument('kind',choices=['go','build','js','aggregate','download','helper','ffmpeg']); ap.add_argument('--plan',default='ci-plan.json'); ap.add_argument('--index',type=int,default=0); ap.add_argument('--component'); ap.add_argument('--needs'); a=ap.parse_args(); plan=json.loads(Path(a.plan).read_text())
    if a.kind=='download': download()
    elif a.kind=='helper': build_helper(plan)
    elif a.kind=='ffmpeg': ensure_ffmpeg(plan,a.index,a.component)
    elif a.kind=='go': go(plan,a.index)
    elif a.kind=='js': js(plan,a.component)
    elif a.kind=='aggregate': aggregate(plan,json.loads(a.needs))
    else: build(plan)
