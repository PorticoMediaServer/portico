#!/usr/bin/env python3
"""Preserve native payload/fragment controls and exact-page overflow receipts."""
import argparse,collections,gzip,hashlib,json
from pathlib import Path
ap=argparse.ArgumentParser();ap.add_argument('input',type=Path);ap.add_argument('output',type=Path);ap.add_argument('--prefix',required=True);a=ap.parse_args();a.output.mkdir(parents=True,exist_ok=True);out=[]
for p in sorted(a.input.glob('*/result.jsonl')):
 ev=collections.defaultdict(list)
 for l in p.read_text().splitlines():
  try:e=json.loads(l)
  except ValueError:continue
  if isinstance(e,dict) and 'event'in e:ev[e['event']].append(e['data'])
 if not ev['environment']:continue
 env=ev['environment'][0];assert env['sqlite']=='3.53.4' and env['driver_version']=='v1.58.0'
 q=[]
 for d in ev['first_bounded']:
  d=dict(d);pages=d.pop('per_page',[]);d.pop('page_raw_ms',None);d.pop('raw_ms',None);d.pop('first_ids',None);d.pop('candidates',None);d['ordinal_page_p95_ms']=[x['p95_ms'] for x in pages];d['worst_page_p95_ms']=max(d['ordinal_page_p95_ms'],default=0);q.append(d)
 keep=['maximum_body_scope','maximum_body','chunk_capacity','chunk_controls','generation_flip','generation_reclaim','wal_summary','publisher_integrity']
 r={'artifact':p.parent.name,'environment':env,'queries':q,'short':[{k:v for k,v in d.items() if k not in ['raw_ms','ids']} for d in ev['policy_short']],'special':{k:ev[k] for k in keep if ev[k]},'operation':{d['tag']:d['latency'] for d in ev['operation']},'completed_events':[k for k in ev if k.endswith('_completed')],'stderr':p.with_suffix('.stderr').read_text() if p.with_suffix('.stderr').exists() else ''};out.append(r)
 for f in p.parent.iterdir():
  if f.is_file() and not f.name.startswith('search.sqlite'):
   dst=a.output/(a.prefix+'-'+p.parent.name+'-'+f.name+('' if f.suffix=='.gz' else '.gz'))
   if not dst.exists():dst.write_bytes(f.read_bytes() if f.suffix=='.gz' else gzip.compress(f.read_bytes(),mtime=0))
(a.output/(a.prefix+'-summary.json')).write_text(json.dumps(out,ensure_ascii=False,separators=(',',':'))+'\n')
manifest={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(a.output.iterdir()) if f.is_file() and f.name!='SHA256.json'};(a.output/'SHA256.json').write_text(json.dumps(manifest,indent=2)+'\n')
print(a.prefix,'receipts',len(out),'completed',sum(bool(r['completed_events']) for r in out))
