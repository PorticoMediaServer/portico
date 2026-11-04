#!/usr/bin/env python3
"""Coverage allocation, NOT a claim that locating a test qualifies a route."""
import argparse,collections,csv,hashlib,json,pathlib,re
p=argparse.ArgumentParser();p.add_argument('usage');p.add_argument('destination');a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[2];dest=pathlib.Path(a.destination);dest.mkdir(parents=True,exist_ok=True)
source=pathlib.Path(a.usage);rows=list(csv.DictReader(source.open()));assert len(rows)==967
keys={(r['id'],r['method'],r['path']) for r in rows};assert len(keys)==967
api=json.loads((root/'server/api/portico.openapi.json').read_text());ops={(o['operationId'],m.upper(),path) for path,item in api['paths'].items() for m,o in item.items() if m in ['get','post','put','patch','delete','head','options']};assert keys==ops,(keys-ops,ops-keys)
# Golden paths are fixture requests; method and whole path must both match.
gold=json.loads((root/'server/internal/httpapi/testdata/readpath_golden.json').read_text())
tests=[]
for f in (root/'server/internal').rglob('*_test.go'):
    s=f.read_text();matches=list(re.finditer(r'^func (Test\w+)\(t \*testing.T\)',s,re.M))
    for i,m in enumerate(matches):
        body=s[m.start():matches[i+1].start() if i+1<len(matches) else len(s)]
        if '"/v1/' in body or '"/Users' in body or '"/Items' in body or '"/mediabrowser' in body:
            tests.append((str(f.relative_to(root)),m.group(1),s[:m.start()].count('\n')+1,re.findall(r'"([^"\n]+)"',body)))

def domain(r):
    path=r['path'];name=r['id'];low=path.lower()
    if name.startswith('compat_'): return 'COMPAT'
    if not path.startswith('/v1/') and ('mediabrowser' in low or path.startswith(('/Users','/Items','/System','/Library','/Sessions','/Videos','/Audio','/Shows','/Genres','/Artists','/Persons','/PlaybackInfo','/ScheduledTasks'))):return 'COMPAT'
    if any(s in low for s in ['/playback','/queues','/receivers','/handoffs','/media/','/cast','/airplay','/groups/']):return 'PLAYBACK'
    if any(s in low for s in ['/downloads','/trickplay','/subtitles']):return 'OFFLINE'
    if any(s in low for s in ['/prepared-versions','/optimization-jobs','/subtitle-fonts','/lyrics']):return 'OFFLINE'
    if '/reading' in low:return 'READING'
    if any(s in low for s in ['/live','/guide','/dvr','/library-channels']):return 'LIVE'
    if any(s in low for s in ['/notifications','/events','/changes']):return 'EVENTS'
    if any(s in low for s in ['/auth','/direct','/networking','/devices','/setup','/me/','/admin/access','/admin/sign-in','/admin/sessions','/admin/api-keys','/integrations/app-passwords']):return 'AUTH'
    if any(s in low for s in ['/storage','/ingestion','/metadata','/scan','/admin/libraries']):return 'INGEST'
    if any(s in low for s in ['/playlists','/collections','/saved','/personal','/mutations','/progress','/history']):return 'PERSONAL'
    if any(s in low for s in ['/access/invitations','/quick-connect','/sessions/current','/integrations/keys','/integrations/users','/integrations/sessions','/topshelf/token']) or path=='/v1/me':return 'AUTH'
    if any(s in low for s in ['/browse','/facet-queries','/genre-queries','/file-info-queries','/selection-actions','/topshelf','/rating-systems']):return 'CATALOG'
    if any(s in low for s in ['/preferences','/user-mutation','/music/mixes']):return 'PERSONAL'
    if path=='/v1/groups':return 'PLAYBACK'
    if '/integrations/refresh' in low:return 'INGEST'
    if any(s in low for s in ['/jobs','/app-reports','/diagnostics/client-logs']):return 'ADMIN'
    if path.startswith('/v1/admin') or any(s in low for s in ['/backups','/console','/logs','/settings','/telemetry','/feedback','/operations']):return 'ADMIN'
    if any(s in low for s in ['/libraries','/items','/shows','/seasons','/albums','/artists','/books','/people','/search','/home','/titles','/recommend']):return 'CATALOG'
    return 'PLATFORM'

out=[]
for r in rows:
    pattern='^'+re.sub(r'\{[^}]+\}',r'[^/?]+',r['path'])+'$';rx=re.compile(pattern)
    gs=[g['name'] for g in gold if g['method']==r['method'] and rx.fullmatch(g['path'].split('?')[0])]
    witnesses=[]
    for f,n,line,literals in tests:
        if any(rx.fullmatch(s.split('?')[0]) for s in literals):witnesses.append(f'{f}:{line}#{n}')
    d=domain(r)
    row=dict(r,oracleKind='golden' if gs else 'domain',oracleId='READPATH-V1' if gs else 'D-'+d,
      qualification='recorded-baseline-not-rerun' if gs else 'assigned-build-gate',
      goldenCases=';'.join(gs),routeLiteralWitnesses=';'.join(witnesses),
      witnessLimit='literal located, method/execution/assertion coverage not certified' if witnesses else 'no exact route literal located; composed paths may exist',
      waiverReason='',acceptance='same logical history on baseline/new store; full response + exact authority/replay/side-effect assertions; see V-ORACLE domain contract',
      gate='must qualify before affected domain merges; all routes before cutover')
    # No automatic waivers: byte/health/compat routes still have authority and wire contracts.
    out.append(row)
cols=list(out[0]);csvpath=dest/'V-ROUTE-COVERAGE.csv'
with csvpath.open('w') as f:
    w=csv.DictWriter(f,fieldnames=cols);w.writeheader();w.writerows(out)
summary={'usageSHA256':hashlib.sha256(source.read_bytes()).hexdigest(),'usageSourceCommit':'4be642b62157dd6b593c683df3d51d65e4c7d858','currentOpenAPI':len(ops),'uniqueRoutes':len(keys),'oracleKinds':dict(collections.Counter(x['oracleKind'] for x in out)),'domains':dict(collections.Counter(x['oracleId'] for x in out)),'exactRouteLiteralWitness':sum(bool(x['routeLiteralWitnesses']) for x in out),'waivers':0,'qualification':'allocation only; route-literal witnesses are not coverage proof'}
(dest/'V-ROUTE-SUMMARY.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps(summary,indent=2))
