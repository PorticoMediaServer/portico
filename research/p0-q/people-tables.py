#!/usr/bin/env python3
import json,collections,argparse,datetime
from pathlib import Path
ap=argparse.ArgumentParser();ap.add_argument('summary',type=Path);ap.add_argument('output',type=Path);a=ap.parse_args();rs=json.loads(a.summary.read_text());groups=collections.defaultdict(list)
for r in rs:
 e=r['environment']
 if e['mode']=='people' and e.get('people_divisor')==50 and e['items']>=100000:groups[(e['profile'],e['items'],e['cjk_share_percent'])].append(r)
lines=['# People bank on exact4158: received ordinary-density cells','', 'Updated '+datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%d %H:%M UTC')+'. Run41940905567/code2b0ce283; schema4158892e55858944b84beee38a8d95704a7d2d3f95fcefded4291240dcff7224; bundled modernc1.58.0/SQLite3.53.4. This table uses ordinary independent People count N/50 (2000/8000/20000), not 1M people. Each VM has128query samples and exact legacy top20ID/score parity. Cells below report worst VM p95/p99, not pooled quantiles. Full query vectors/first IDs and raw snapshots remain in4158-people-summary.json/raw receipts. No HTTP portrait/role/count hydration or exact total.','', 'Short Latin and CJK1 calls here are the explicit People action, not ordinary titles-only dispatch. Normal ranking remains -bm25 plus exact1000/name-prefix100, then source sort_name/personID. The synthetic restricted path includes preparing visible people from1%allowed items in bounded source chunks; it is not the production authority oracle.']
for restricted in [False,True]:
 lines+=['', '## '+('Restricted synthetic viewer' if restricted else 'Normal visible-credit viewer'),'','|Profile/items/CJK|VMs|a p95/p99 ms|jo p95/p99|ada p95/p99|ada lovelace p95/p99|王菲 p95/p99|Largest actual snapshot ms|','|---|---:|---:|---:|---:|---:|---:|---:|']
 for key,rr in sorted(groups.items()):
  cells=[];snap=0
  for q in ['a','jo','ada','ada lovelace','王菲']:
   ds=[d for r in rr for d in r['queries'] if d['query']==q and d['restricted']==restricted]
   if any(d['failures'] for d in ds):cells.append('FAILED / no intended-policy qualification');continue
   cells.append(f"{max(d['latency']['p95_ms'] for d in ds):.3f}/{max(d['latency']['p99_ms'] for d in ds):.3f}");snap=max(snap,max(d['snapshots']['max_ms'] for d in ds))
  lines.append(f"|{key[0]}/{key[1]:,}/{key[2]}%|{len(rr)}/3|"+'|'.join(cells)+f"|{snap:.3f}|")
lines+=['','## Dense negative and corrected connection control','','All four initial1M/200kPeople dense cells time out on a/jo and later fail custody before capacity. Those later reads have uncontrolled replacement-connection cache policy, so they are not a valid declared-cache comparison. No capacity is inferred. Q-CONNECTION-CONTROL.md and the per-artifact audit retain those negatives. Correct per-connection DSN run41947538788/codefc8193ae has passed local/hosted smoke and all six100k3VM controls; fourdense1M reruns are pending. Do not print zero-sample latency as0ms.','', 'Publisher capacities/flows remain per-VM in4158-people-summary.json. The 200/80People-updates/s stress means two independent changes per100/40item-updates/s, deliberately above shared-name dedup rates; it is not a claim that every accepted item changes two names. One standard100k30%VM falls behind at182.4/s with p99current-or-newer revision lag21.79s; peers keep up. All source revision coalescing counts and source-admission delay stay separate from search visibility lag.','', 'R-09, full source/authority/caps/provenance, sparse viewers, cancellation/whole-serving deadlines and maximum name/word ceilings remain first-slice qualification. These are SQL/native-image controls, not the complete People API.']
a.output.write_text('\n'.join(lines)+'\n');print('groups',len(groups),'ordinary VM receipts',sum(map(len,groups.values())))
