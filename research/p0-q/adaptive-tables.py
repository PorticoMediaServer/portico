#!/usr/bin/env python3
"""Dispatch report from completed bounded-reducer receipts."""
import argparse, collections, json
from pathlib import Path
ap=argparse.ArgumentParser();ap.add_argument('evidence',type=Path);a=ap.parse_args();p=a.evidence
rs=json.loads((p/'4158-adaptive-summary.json').read_text());rs=[r for r in rs if r['environment']['items']>=100000]
lines=['# Script-aware dispatch: adaptive exact context reducer','',
 'Exact4158892e55858944b84beee38a8d95704a7d2d3f95fcefded4291240dcff7224/code98f1e2f623c6adc1673b2442e993654012b7312c/run41945030570. Bundled modernc1.58.0/SQLite3.53.4, upstream driver. All13receipts preserved:10complete including smoke, threecancelled1Mcells at the120minute job limit (standard5%, old8g5%/30%). Completed per-query receipts and all short probes retain their scope; missing later traversals do not qualify. These P0 SQL/native read controls are not a W-owned command or full HTTP/authority route qualification.','',
 'Ordinary titles-only Latin1–2/CJK1 is top20by catalogue insertion/entityID DESC, with256raw candidate ceiling. Full search and explicit short context trial at most512entity candidates before exact source-frontier fallback, preserve exact tier/entityID order and complete membership. First-ready targetp95<=50ms, formal1Msearchp95<=200ms and600msgroup remain unchanged. Whole-keyset-page times include work between separately closed SQL chunks.128first-page samples, threefull ordered traversals per query; ordinal-pagep95withN3is observed max, not tail certification.','',
 'The debounce350ms and per-session supersede/cancel/generation check remain build rules. HTTP/client scheduling, exact multiword rarest lead, real authority/caps/provenance, arbitrary mixed/longCJK and maximum metadata remain separate. No totals or full-population ranking precedes the first page.','',
 '## Ordinary short input','',
 'Each table cell is the largest p95/p99 and largest actual snapshot over bothCJKshares and the named query group; these are not pooled quantiles.','',
 '|Profile/items|Latin1 a p95/p99 ms|Latin2 al/jo p95/p99 ms|CJK1 東/王 p95/p99 ms|Largest actual snapshot ms|',
 '|---|---:|---:|---:|---:|']
groups=collections.defaultdict(list)
for r in rs:groups[(r['environment']['profile'],r['environment']['items'])].append(r)
for (profile,n),rr in sorted(groups.items()):
 vals=[]
 for queries in [['a'],['al','jo'],['東','王']]:
  ds=[d for r in rr for d in r['short'] if d['query'] in queries];vals.append(f"{max(d['latency']['p95_ms'] for d in ds):.3f}/{max(d['latency']['p99_ms'] for d in ds):.3f}" if ds else 'not emitted')
 snaps=[d['snapshot_maxima']['max_ms'] for r in rr for d in r['short']];snap=f'{max(snaps):.3f}' if snaps else 'unknown'
 lines.append(f'|{profile}/{n:,}|'+ '|'.join(vals)+f'|{snap}|')
lines+=['','## Full search and explicit short-context pages','','|Profile/items/CJK|Query|First p95/p99 ms|Worst ordinal-page p95 ms|Largest actual SQL snapshot ms|All result IDs|','|---|---|---:|---:|---:|---:|']
for r in sorted(rs,key=lambda r:(r['environment']['profile'],r['environment']['items'],r['environment']['cjk_share_percent'])):
 e=r['environment']
 for d in r['queries']:
  snap=max(d['snapshots']['max_ms'],d['all_page_snapshots']['max_ms'])
  lines.append(f"|{e['profile']}/{e['items']:,}/{e['cjk_share_percent']}%|{d['query']}|{d['latency']['p95_ms']:.3f}/{d['latency']['p99_ms']:.3f}|{d['worst_page_p95_ms']:.3f}|{snap:.3f}|{d['result_count']}|")
lines+=['','Every emitted per-query receipt follows its complete ordered-ID oracle/digest check. Standard1M5% and old8g1M5% have no completed jo receipt; old8g1M30% has neither al nor jo. All short probes were emitted before those traversals. These cells remain incomplete; no missing query is converted into a fast empty page. Earlier97dc bounded-spill controls independently complete every jo688822hit on both profiles. A page-budget miss remains a miss even if all its SQL snapshots are short. The fixed512trial can add overhead to rare context, so it is tunable dispatch evidence rather than a schema change. The original al653/jo710/harbor-ad306 witnesses remain in their small complete legacy controls; this large-corpus traversal is single-token only. Full per-query/page/snapshot/spill statistics and every raw receipt remain in4158-adaptive-summary.json and adjacent gzip files.']
(p/'4158-adaptive-tables.md').write_text('\n'.join(lines)+'\n')
print('scale receipts',len(rs),'complete',sum(bool(r['completed_events']) for r in rs),'short maximum p95',max(d['latency']['p95_ms'] for r in rs for d in r['short']),
 'full largest first p95',max(d['latency']['p95_ms'] for r in rs for d in r['queries']),
 'page largest p95',max(d['worst_page_p95_ms'] for r in rs for d in r['queries']))
