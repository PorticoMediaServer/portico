#!/usr/bin/env python3
"""Preserve exact ef4 bundled-engine receipts and compact phase/page summaries."""
import argparse,json,gzip,hashlib,collections
from pathlib import Path
SHA='ef4b74ad542e0951c23760d15a3e60f295210d3bf0a1c29a9f9b944f1a4b722c'
def run(base,out,prefix):
 result=[];out.mkdir(parents=True,exist_ok=True)
 for p in sorted(base.glob('*/result.jsonl')):
  events=collections.defaultdict(list)
  for l in p.read_text().splitlines():
   try:e=json.loads(l)
   except ValueError:continue
   if isinstance(e,dict) and 'event'in e:events[e['event']].append(e['data'])
  if not events['environment']:continue
  env=events['environment'][0];assert env['candidate_sha256']==SHA;assert env['sqlite']=='3.53.4'
  build=(p.parent/'build.txt').read_text() if (p.parent/'build.txt').exists() else ''
  if build:assert 'modernc.org/sqlite\tv1.58.0' in build
  ops={o['tag']:o['latency'] for o in events['operation']}
  q=[]
  for d in events['first_bounded']:
   x={k:v for k,v in d.items() if k not in ['page_raw_ms','per_page','first_ids','candidates','raw_ms']};x['page_count']=len(d['per_page']);x['per_page_p95_ms']=[v['p95_ms'] for v in d['per_page']];x['worst_page_p95_ms']=max(x['per_page_p95_ms'],default=0);q.append(x)
  allocation=events['allocation_base'][0] if events['allocation_base'] else {};objects=allocation.pop('objects',{})
  family=sum(v['allocated'] for k,v in objects.items() if k.startswith('search_') or k.startswith('sqlite_autoindex_search_'))
  r={'artifact':p.parent.name,'environment':env,'queries':q,'short':events['policy_short'],'diagnosis':events['first_diagnosis'],'allocation':allocation,'search_family_allocated_bytes':family,'source_accept':ops.get('accept.tx'),'publisher_capacity':events['publisher_capacity'],'publisher_flow':[{k:v for k,v in f.items() if k!='lag_seconds_raw'} for f in events['publisher_flow']],'publisher_choice':events['publisher_choice'],'controls':events['publisher_controls'],'integrity':events['publisher_integrity'],'generation_flip':events['generation_flip'],'generation_reclaim':[{k:v for k,v in g.items() if k!='allocated'} for g in events['generation_reclaim']],'operations':ops,'wal':events['wal_summary'],'completed':events['first_completed']+events['publisher_completed']+events['seed_completed'],'stderr':p.with_suffix('.stderr').read_text() if p.with_suffix('.stderr').exists() else ''}
  result.append(r)
  for f in p.parent.iterdir():
   if f.is_file():
    data=f.read_bytes();(out/(prefix+'-'+p.parent.name+'-'+f.name+('.gz' if f.suffix!='.gz' else ''))).write_bytes(gzip.compress(data,mtime=0) if f.suffix!='.gz' else data)
 (out/(prefix+'-summary.json')).write_text(json.dumps(result,indent=2,ensure_ascii=False)+'\n')
 pairs=collections.defaultdict(dict)
 for r in result:
  e=r['environment'];pairs[(e['profile'],e['items'],e['cjk_share_percent'])][e['old_cjk_recipe']]=r
 lines=['# Exact ef4 / bundled modernc3.53.4 bounded-tier search','', '128first-page samples;3complete ordered traversals per query. Every ordinal page p95 is retained in summary; with3samples p95=max. Warm DB. Known single-token lead; no full population count. Working first-ready budget50ms p95; formal1MSearch200ms/600msgroup timeout unchanged.','', '|Profile/items/CJK |東京p95/p99 |王菲p95/p99 |李华contextp95/p99 |Worst CJKpagep95 |Largest CJKsnapshot |Old/new FTSMiB |Search-familyMiB |','|---|---:|---:|---:|---:|---:|---:|---:|']
 for key,pair in sorted(pairs.items()):
  r=pair.get(False)
  if not r:continue
  qs={q['query']:q for q in r['queries']};cjk=[qs[q] for q in ['東京','王菲','李华'] if q in qs]
  def val(q):
   s=qs.get(q,{}).get('latency',{});return f"{s.get('p95_ms',0):.3f}/{s.get('p99_ms',0):.3f}"
  e=r['environment'];old=pair.get(True);ob=old['allocation'].get('fts_bytes',0)/2**20 if old else 0;nb=r['allocation'].get('fts_bytes',0)/2**20
  snap=max([max(q['snapshots'].get('max_ms',0),q['all_page_snapshots'].get('max_ms',0)) for q in cjk],default=0)
  lines.append(f"|{key[0]}/{key[1]:,}/{key[2]}% |{val('東京')} |{val('王菲')} |{val('李华')} |{max([q['worst_page_p95_ms'] for q in cjk],default=0):.3f} |{snap:.3f} |{ob:.2f}/{nb:.2f} |{r['search_family_allocated_bytes']/2**20:.2f} |")
 (out/(prefix+'-tables.md')).write_text('\n'.join(lines)+'\n')
 manifest={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(out.iterdir()) if f.is_file() and f.name!='SHA256.json'};(out/'SHA256.json').write_text(json.dumps(manifest,indent=2)+'\n')
 print('preserved',len(result),'receipts')
 print('\n'.join(lines))
if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('input',type=Path);ap.add_argument('output',type=Path);ap.add_argument('--prefix',default='ef4-first');a=ap.parse_args();run(a.input,a.output,a.prefix)
