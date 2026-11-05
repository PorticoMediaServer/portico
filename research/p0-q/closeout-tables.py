#!/usr/bin/env python3
"""Small final tables from preserved receipts; no SQLite work."""
import argparse, datetime, json, math
from pathlib import Path

ap=argparse.ArgumentParser();ap.add_argument('evidence',type=Path);ap.add_argument('steady',type=Path);a=ap.parse_args();p=a.evidence
def read(name):return json.loads((p/name).read_text())
def stat(xs):
    xs=sorted(xs)
    return {'n':len(xs),**({k:xs[math.ceil(len(xs)*q)-1] for k,q in [('p95',.95),('p99',.99),('max',1)]} if xs else {})}
def trio(s,scale=1):return '/'.join(f'{s[k]*scale:.3f}' for k in ['p95','p99','max']) if s.get('n') else 'no samples'
def triple(s):return '/'.join(f'{s[k]:.3f}' for k in ['p95_ms','p99_ms','max_ms']) if s.get('n') else 'no samples'
def key(r):
    e=r['environment'];return f"{e['profile']}/{e['items']:,}/{e['cjk_share_percent']}%"

# Actual arrival timestamps, rather than the start of fixture-adjacent setup.
audits=[]
for f in sorted(a.steady.glob('*/result.jsonl')):
    events=[];flow=None
    for line in f.open():
        try:e=json.loads(line)
        except ValueError:continue
        if not isinstance(e,dict):continue
        if e.get('event')=='foreground_write':events.append(e['data'])
        if e.get('event') in ('publisher_flow','people_flow'):flow=e
    if not flow:continue
    d=flow['data'];end=datetime.datetime.fromisoformat(flow['at'].replace('Z','+00:00')).timestamp();duration=d.get('elapsed_s',d.get('accepted',0)/d.get('actual_people_per_s',1));start=end-duration
    classes={}
    for cls in ['security_FULL','progress_NORMAL']:
        served=[e for e in events if e['class']==cls and start<=e['start_unix_ns']/1e9<=end]
        actual=[e for e in served if e['arrival_unix_ns']/1e9>=start]
        classes[cls]={'served':len(served),'carry_in':len(served)-len(actual),'flow_arrivals':len(actual),
            'queue_flow_arrivals_ms':stat([e['queue_ms'] for e in actual]),'transaction_flow_arrivals_ms':stat([e['transaction_ms'] for e in actual])}
    audits.append({'artifact':f.parent.name,'flow_start_epoch':start,'flow_end_epoch':end,'classes':classes})
(p/'4158-steady-arrival-audit.json').write_text(json.dumps(audits,indent=2)+'\n');audit={r['artifact']:r for r in audits}
rs=read('4158-steady-summary.json')
lines=['# Completed post-fixture mixed durability controls','',
 'Exact schema4158892e55858944b84beee38a8d95704a7d2d3f95fcefded4291240dcff7224; code6fadce332e3d14ddc830e13ded32aae3dbc86941/run41949339885; bundled modernc1.58.0/SQLite3.53.4, upstream driver. All15cells completed, including smoke. These P0 primitive rows do not qualify W-owned commands or full auth/state routes. Each scale flow is600seconds, security mean60seconds and progress mean1second. Eleven auth samples per cell make empirical p95/p99 equal max, with no statistical p99 certification.','',
 'Source/People flows start after fixture construction. The audited actual arrival timestamps exclude any progress request carried through the brief metadata/setup interval. All11auth arrivals in each scale cell are genuine flow arrivals. Inclusive served COMMIT populations and actual-arrival queue populations are explicitly distinct.','',
 '|Profile/items/path/cap ms/CP ms|Rate /s|Auth N; COMMIT p95/p99/max ms|BG prefix max MiB|Progress COMMIT N; p95/p99/max ms|Actual arrival N; queue p95/p99/max ms; carry-in|',
 '|---|---:|---|---:|---|---|']
for r in rs:
 e=r['environment']
 if e['items']<100000:continue
 fw=r['flow_wal'];fg=fw['foreground'];auth=fg.get('security_FULL',{});pr=fg.get('progress_NORMAL',{});ac=audit[r['artifact']]['classes']['progress_NORMAL'];op=r['operations'];flow=r['flow'][0];path='People' if 'people' in e['mode'] else 'catalogue';caps=fw['snapshot_actual_ms'];mode=e['config'];cad=250 if mode=='normal-passive-250' else 50
 # Nominal caps are explicit in artifact names; environment does not contain them.
 import re
 cap=re.search(r'-cap(\d+)-',r['artifact']).group(1)
 lines.append(f"|{e['profile']}/{e['items']:,}/{path}/{cap}/{cad}|{flow.get('actual_docs_per_s',flow.get('actual_people_per_s')):.2f}|{op.get('foreground.security_FULL.commit',{}).get('n',0)};{triple(op.get('foreground.security_FULL.commit',{}))}|{auth.get('background_wal_bytes',{}).get('max',0)/2**20:.2f}|{op.get('foreground.progress_NORMAL.commit',{}).get('n',0)};{triple(op.get('foreground.progress_NORMAL.commit',{}))}|{ac['flow_arrivals']};{trio(ac['queue_flow_arrivals_ms'])};{ac['carry_in']}|")
lines+=['','The background-byte maximum is independently maximized, not paired with the slowest COMMIT. Per-request prefix/queue/CP state remains in the JSON/raw receipts. It is a conservative frame ledger, not an exact VFS unsynced-byte oracle.','','|Profile/items/path/cap/CP|CP p95/p99/max ms|CPs with writer/reader progress|Read p95 overlap/idle ms|Resets/min|Peak physical WAL MiB|Actual snapshot max ms|Visibility lag median/p99 ms|','|---|---|---:|---:|---:|---:|---:|---:|']
for r in rs:
 e=r['environment']
 if e['items']<100000:continue
 fw=r['flow_wal'];flow=r['flow'][0];lag=flow['lag_seconds'];cap=re.search(r'-cap(\d+)-',r['artifact']).group(1);cad=250 if e['config']=='normal-passive-250' else 50;path='People' if 'people' in e['mode'] else 'catalogue'
 lines.append(f"|{e['profile']}/{e['items']:,}/{path}/{cap}/{cad}|{trio(fw['checkpoint_ms'])}|{fw['checkpoints_with_writer_progress']}/{fw['checkpoints_with_reader_progress']}|{fw['read_checkpoint_overlap_ms'].get('p95',0):.3f}/{fw['read_checkpoint_idle_ms'].get('p95',0):.3f}|{fw['resets_per_minute']:.1f}|{max((z['physical_bytes'] for z in fw['series_5s']),default=0)/2**20:.2f}|{fw['snapshot_actual_ms'].get('max',0):.3f}|{lag['p50']*1000:.3f}/{lag['p99']*1000:.3f}|")
lines+=['','Strict checkpoint start spacing has zero reported violations. A checkpoint allowing writer/read progress still causes individual waits/IO interference. Reset and bounded WAL are mechanism successes; they do not excuse auth or background max>25ms. Cap1000controls can exceed nominal1second slightly due scheduler/cleanup and are reported by actual lifetime. Five-second WAL trajectories, actual arrival audit, policy assertions and all negative holds remain in adjacent receipts. No RESTART/TRUNCATE or new native gate experiment is used.']
(p/'4158-steady-tables.md').write_text('\n'.join(lines)+'\n')

lines=['# Completed P0 follow-on receipt tables','',
 'All rows retain executed schema4158892e55858944b84beee38a8d95704a7d2d3f95fcefded4291240dcff7224 and bundled modernc1.58.0/SQLite3.53.4. These are temporary research SQL/native-image baselines using the upstream driver. They do not qualify W-owned phase1 commands, exact DDL adapters, authorities or complete routes.','','## Generation baseline: run41944655550/codee180c402','','|Profile/1M/30%CJK|Backfill-to-flip seconds|Global flip ms|7813preflight windows max ms|127750old-head p99/max ms|127750fragment reclaim p99/max ms|','|---|---:|---:|---:|---:|---:|']
for r in read('4158-generation-seek-summary.json'):
 if r['environment']['items']!=1000000:continue
 f=r['special']['generation_flip'][0];g=r['special']['generation_reclaim'][0]
 lines.append(f"|{r['environment']['profile']}|{f['elapsed_s']:.3f}|{f['flip']['max_ms']:.3f}|{f['preflight_snapshots']['max_ms']:.3f}|{g['native_old_head']['p99_ms']:.3f}/{g['native_old_head']['max_ms']:.3f}|{g['fragment_reclaim']['p99_ms']:.3f}/{g['fragment_reclaim']['max_ms']:.3f}|")
lines+=['','Head retirement includes old People/own native rows plus job-pointer clears and COMMIT; fragment/image reclaim follows separately. No active old reader leases, populated Reading, physical DROP or sustained100/40updates/s throughout the generation is claimed. Every maximum above25ms remains a failure. Detailed interval extraction:4158-generation-retirement-tails.json; missing old IDs/payload/native work remain unknown.','','## Maximum-body negative: run41943311980/code1647d5a3','','All13cells completed. Values below are worst over5%/30%CJK fixtures for each profile/size, not pooled quantiles. Early max rejection leaves small N, which does not qualify tail confidence. One physical fragment is a deliberate negative. Legal source widths remain unchanged.','','|Profile/items|Case|Min phase N stage/head/reclaim|Stage p99/max ms|Head p99/max ms|Reclaim p99/max ms|','|---|---|---:|---:|---:|---:|']
groups={}
for r in read('4158-maxbody-summary.json'):
 e=r['environment']
 if e['items']<100000:continue
 for d in r['special']['maximum_body']:groups.setdefault((e['profile'],e['items'],d['case']),[]).append(d)
for (profile,n,case),ds in sorted(groups.items()):
 if '65536' not in case and '64' not in case:continue
 cells=[f"{max(d[k]['p99_ms'] for d in ds):.3f}/{max(d[k]['max_ms'] for d in ds):.3f}" for k in ['stage','head','reclaim']];counts='/'.join(str(min(d[k]['n'] for d in ds)) for k in ['stage','head','reclaim']);lines.append(f'|{profile}/{n:,}|{case}|{counts}|'+ '|'.join(cells)+'|')
lines+=['','## Physical chunk controls: run41944437712/code2231bd99','','All13cells completed. Per-column packing targets1024UTF8bytes with a whole-token exception; normalized windows512+512overlap. This is physical-quantum evidence only: the retained 辛哫 counterexample proves same-row lexical/normalized verification can lose a hit. Logical-subject verification or aligned windows is required before semantic qualification.','','|Profile/items|Case|Fragment stage p99/max ms|Fragment reclaim p99/max ms|Whole-document rate /s|','|---|---|---:|---:|---:|']
groups={}
for r in read('4158-chunks-summary.json'):
 e=r['environment']
 if e['items']<100000:continue
 for d in r['special']['chunk_capacity']:groups.setdefault((e['profile'],e['items'],d['case']),[]).append(d)
for (profile,n,case),ds in sorted(groups.items()):
 cells=[f"{max(d[k]['p99_ms'] for d in ds):.3f}/{max(d[k]['max_ms'] for d in ds):.3f}" for k in ['fragment','fragment_reclaim']];lines.append(f'|{profile}/{n:,}|{case}|'+ '|'.join(cells)+f"|{min(d['documents_per_s'] for d in ds):.3f}–{max(d['documents_per_s'] for d in ds):.3f}|")
lines+=['','## Corrected People reconnect: run41947538788/codefc8193ae','','All11cells completed: smoke, six100kordinary-density controls and four1M/200kPeople dense controls. Forced2ms cancellation preserves asserted per-physical-connection FK/NORMAL/autockpt/cache policy. All empty-content/custody/rollback/generation controls finish. This fixes measurement policy; it does not make dense queries fast. Each dense normal a/jo still times out128/128 at250ms. A zero-success latency is FAILED, never0ms.','','|Profile/1M/CJK|Normal a/jo timeouts|Normal 王菲 p95/p99 ms|Restricted a p95/p99 ms|Restricted jo p95/p99 ms|Restricted 王菲 p95/p99 ms|Flow people/s|Visibility lag median/p99 ms|','|---|---:|---:|---:|---:|---:|---:|---:|']
for r in read('4158-reconnect-summary.json'):
 e=r['environment']
 if e['items']!=1000000:continue
 def q(query,restricted):return next(d for d in r['queries'] if d['query']==query and d['restricted']==restricted)
 def pair(d):return 'FAILED' if d['failures'] else f"{d['latency']['p95_ms']:.3f}/{d['latency']['p99_ms']:.3f}"
 f=r['flow'][0];lag=f['lag_seconds'];lines.append(f"|{key(r)}|{len(q('a',False)['failures'])}/{len(q('jo',False)['failures'])}|{pair(q('王菲',False))}|{pair(q('a',True))}|{pair(q('jo',True))}|{pair(q('王菲',True))}|{f['actual_people_per_s']:.2f}|{lag['p50']*1000:.3f}/{lag['p99']*1000:.3f}|")
lines+=['','No publisher capacity is inferred from failed read samples. Capacity/flow phases are separate populations in4158-reconnect-summary.json. Original four dense failures remain preserved and invalid for intended post-cancel cache/FK policy. The36ordinary-density VM query cells from41940905567 retain exact legacy top20ID/score parity; two final generation preflights fail separately, after those query/capacity phases. Full authority/field caps/HTTP hydration and maximum legal native name rows remain gates.','','[Completed mixed FULL/NORMAL and WAL tables](4158-steady-tables.md) retain all foreground failures, real arrival counts and reset/read evidence. Next owned/native controls await the lead-selected reviewed W helper and exact INT/R DDL; no LIMIT8 or blind CP-active refusal qualifies the path.']
(p/'4158-closeout-tables.md').write_text('\n'.join(lines)+'\n')
print('steady tables',len(rs),'arrival audits',len(audits),'follow-on closeout written')
