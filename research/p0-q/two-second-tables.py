#!/usr/bin/env python3
"""Last pre-launched P0 snapshot-cap control, separate provenance/populations."""
import argparse,json,math
from pathlib import Path
ap=argparse.ArgumentParser();ap.add_argument('evidence',type=Path);ap.add_argument('input',type=Path);a=ap.parse_args();p=a.evidence
rs=json.loads((p/'4158-two-second-summary.json').read_text());audits=[]
def stats(xs):
 xs=sorted(xs);return {'n':len(xs),**({k:xs[math.ceil(len(xs)*q)-1] for k,q in [('p95',.95),('p99',.99),('max',1)]} if xs else {})}
lines=['# Last pre-launched two-second reader baseline','',
 'Exact4158892e55858944b84beee38a8d95704a7d2d3f95fcefded4291240dcff7224/code49d118d430bed0323d72fb12a06fbcd2b2197013/run41953539117. Bundled modernc1.58.0/SQLite3.53.4, upstream driver. Both1M30%CJKprofiles complete. This P0run was launched before the owned-native gate; it is a diagnostic beyond the adopted<=1s reader rule, not a new native qualification. No RESTART/TRUNCATE.','',
 'The executed benchmark package, including writer/reader/checkpointer code, is byte-identical to6fadce332e3d14ddc830e13ded32aae3dbc86941. The research-tree difference is only an unexecuted nested semantic-control program. Retain49d118d4as executed SHA. Source640byte ordinary overviews and synthetic adapters remain scoped. Security mean60s, progress mean1s; no full auth/state route is implied.','',
 '|Profile/1M/nominal2s/CP50|Source rate /s; elapsed seconds|Actual snapshot p95/p99/max ms|CP p95/p99/max ms|Reset/min|Peak physical/active/uncheckpointed MiB|Read p95 ms|Visibility lag median/p99 ms|Source arrival admission p99/max ms|',
 '|---|---:|---:|---:|---:|---:|---:|---:|---:|']
for r in rs:
 assert r['completed'];fw=r['flow_wal'];f=r['flow'][0];sn=fw['snapshot_actual_ms'];cp=fw['checkpoint_ms'];series=fw['series_5s'];lag=f['lag_seconds']
 triple=lambda x:'/'.join(f'{x[k]:.3f}' for k in ['p95','p99','max'])
 peaks='/'.join(f'{max(s[k] for s in series)/2**20:.2f}' for k in ['physical_bytes','active_bytes','uncheckpointed_bytes'])
 admission=f['source_admission_ms']
 lines.append(f"|{r['environment']['profile']}|{f['actual_docs_per_s']:.2f};{f['elapsed_s']:.3f}|{triple(sn)}|{triple(cp)}|{fw['resets_per_minute']:.1f}|{peaks}|{fw['foreground_read_ms']['p95']:.3f}|{lag['p50']*1000:.3f}/{lag['p99']*1000:.3f}|{admission['p99_ms']:.3f}/{admission['max_ms']:.3f}|")
 start,end=r['flow_window_epoch'];fg=[]
 for line in (a.input/r['artifact']/'result.jsonl').open():
  try:e=json.loads(line)
  except ValueError:continue
  if isinstance(e,dict) and e.get('event')=='foreground_write':fg.append(e['data'])
 classes={}
 for cls in ['security_FULL','progress_NORMAL']:
  served=[d for d in fg if start<=d['start_unix_ns']/1e9<=end and d['class']==cls];arrived=[d for d in served if d['arrival_unix_ns']/1e9>=start]
  classes[cls]={'served':len(served),'actual_flow_arrivals':len(arrived),'carry_in':len(served)-len(arrived),'actual_queue_ms':stats([d['queue_ms'] for d in arrived])}
 audits.append({'artifact':r['artifact'],'classes':classes})
lines+=['','Standard67.53updates/s misses its100/s target; old8g39.79/s is near40/s. A planned600s source-arrival horizon can take longer to drain under WAL admission; actual elapsed and arrival-to-source-commit delay are included rather than subtracting backpressure. Visibility lag separately starts at source COMMIT. The same header does not imply equal elapsed/auth sample counts.','', 'Every reset count and five-second WAL observation remains in4158-two-second-summary.json. Sparse CP-attached WAL observations are not per-TX pre/post readings or an all-reader global guarantee. The nominal2s caps have scheduler/cleanup overrun; this is not a strict2s or1s pass.','','|Profile|Actual auth arrivals / served COMMIT N|Auth COMMIT p95/p99/max ms|Maximum known background prefix MiB|Progress COMMIT p99/max ms|Actual progress arrival N; queue p95/p99/max ms; carry-in|','|---|---:|---:|---:|---:|---:|']
for r in rs:
 op=r['operations'];fw=r['flow_wal'];auth=op['foreground.security_FULL.commit'];pr=op['foreground.progress_NORMAL.commit'];ac=next(x for x in audits if x['artifact']==r['artifact'])['classes'];q=ac['progress_NORMAL']['actual_queue_ms'];triple=lambda x:'/'.join(f'{x[k]:.3f}' for k in ['p95','p99','max'])
 lines.append(f"|{r['environment']['profile']}|{ac['security_FULL']['actual_flow_arrivals']}/{auth['n']}|{auth['p95_ms']:.3f}/{auth['p99_ms']:.3f}/{auth['max_ms']:.3f}|{fw['foreground']['security_FULL']['background_wal_bytes']['max']/2**20:.2f}|{pr['p99_ms']:.3f}/{pr['max_ms']:.3f}|{ac['progress_NORMAL']['actual_flow_arrivals']};{triple(q)};{ac['progress_NORMAL']['carry_in']}|")
lines+=['','Auth N is rare real-rate evidence, not statisticalp99 certification. Background-prefix maxima are independently maximized, not paired with the slowest COMMIT. Queue actual arrivals exclude fixture-adjacent setup carry-in; all served native COMMITs remain a separate population.','','|Profile|NORMAL flow recipe|N|p99/max hold ms|','|---|---|---:|---:|']
for r in rs:
 for tag,s in sorted(r['operations'].items()):
  if tag.startswith('flow.') and '.tx' in tag and not any(x in tag for x in ['thread_cpu','sched_wait','checkpoint_']):
   lines.append(f"|{r['environment']['profile']}|{tag}|{s['n']}|{s['p99_ms']:.3f}/{s['max_ms']:.3f}|")
lines+=['','R-09 remains RED wherever max exceeds25ms. The mechanism permits WAL reset with released2s readers in these controls; it does not recommend2s readers, solve COMMIT waits or qualify the W-owned publisher. All pre-launched P0cohorts are now terminal; cancelled/failed/missing phases stay explicit.']
(p/'4158-two-second-tables.md').write_text('\n'.join(lines)+'\n');(p/'4158-two-second-arrival-audit.json').write_text(json.dumps(audits,indent=2)+'\n');print('two-second completed profiles',len(rs))
