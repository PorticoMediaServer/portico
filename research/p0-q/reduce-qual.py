#!/usr/bin/env python3
"""Preserve schema-pinned bundled-engine publisher/People/WAL receipts."""
import argparse,collections,datetime,gzip,hashlib,json,math
from pathlib import Path
SHAS={'97dc083d437e4da0aaab9b277bdc9c8f5dace1450a2a5e754579867916352393','4158892e55858944b84beee38a8d95704a7d2d3f95fcefded4291240dcff7224'}
def stats(v):
 v=sorted(v)
 return {'n':len(v),**({k:v[min(len(v)-1,max(0,math.ceil(len(v)*p)-1))] for k,p in [('p50',.5),('p95',.95),('p99',.99),('max',1)]} if v else {})}
def ts(e):return datetime.datetime.fromisoformat(e['at'].replace('Z','+00:00')).timestamp()
def trim(v):
 if isinstance(v,dict):return {k:trim(x) for k,x in v.items() if k not in ['raw_ms','lag_seconds_raw','allocated','objects','ids']}
 if isinstance(v,list):return [trim(x) for x in v]
 return v
def run(base,out,prefix):
 out.mkdir(parents=True,exist_ok=True);results=[]
 for p in sorted(base.glob('*/result.jsonl')):
  events=collections.defaultdict(list);raw=[]
  for l in p.read_text().splitlines():
   try:e=json.loads(l)
   except ValueError:continue
   if isinstance(e,dict) and 'event'in e:events[e['event']].append(e);raw.append(e)
  if not events['environment']:continue
  env=events['environment'][0]['data'];assert env['candidate_sha256'] in SHAS;assert env['sqlite']=='3.53.4' and env['driver_version']=='v1.58.0'
  flow=(events['people_flow'] or events['publisher_flow']);window=None
  if flow:
   d=flow[-1]['data'];elapsed=d.get('elapsed_s',d.get('accepted',0)/d.get('actual_people_per_s',1));window=(ts(flow[-1])-elapsed,ts(flow[-1]))
  def within(e):return not window or window[0]<=ts(e)<=window[1]
  cp=[e['data'] for e in events['passive_checkpoint'] if within(e)];fg=[e['data'] for e in events['foreground_write'] if within(e)]
  grants=[e['data'] for e in events['passive_checkpoint']];intervals=[x['since_previous_start_ms'] for x in grants if x.get('since_previous_start_ms',0)>0]
  series={}
  for e in events['passive_checkpoint']:
   if not within(e):continue
   d=e['data'];w=d['wal'];sec=5*int((ts(e)-window[0])/5) if window else 0
   z=series.setdefault(sec,{'second':sec,'physical_bytes':0,'active_bytes':0,'uncheckpointed_bytes':0,'reset_count':0})
   for dst,src in [('physical_bytes','Physical'),('active_bytes','ActiveBytes'),('uncheckpointed_bytes','Uncheckpointed')]:z[dst]=max(z[dst],w[src])
  resets=[e for e in events['wal_reset'] if within(e)]
  for e in resets:
   sec=5*int((ts(e)-window[0])/5) if window else 0
   if sec in series:series[sec]['reset_count']+=1
  fgstats={}
  for kind in sorted(set(x['class'] for x in fg)):
   xs=[x for x in fg if x['class']==kind];fgstats[kind]={key:stats([x[key] for x in xs]) for key in ['queue_ms','transaction_ms','response_ms']}
   fgstats[kind]['background_wal_bytes']=stats([x['known_durability_debt']['background_bytes'] for x in xs]);fgstats[kind]['events']=[{k:x[k] for k in ['transaction_ms','queue_ms','response_ms','known_durability_debt','checkpoint_running']} for x in xs] if kind=='security_FULL' else []
  ops={e['data']['tag']:e['data']['latency'] for e in events['operation']}
  r={'artifact':p.parent.name,'environment':env,'capacity':trim([e['data'] for e in events['publisher_capacity']+events['people_capacity']]),'choice':[e['data'] for e in events['publisher_choice']+events['people_choice']],'flow':trim([e['data'] for e in flow]),'queries':trim([e['data'] for e in events['people_query']]),'population':[e['data'] for e in events['people_population']],'short':trim([e['data'] for e in events['policy_short']]),'controls':trim([e['data'] for e in events['publisher_controls']+events['people_controls']]),'integrity':trim([e['data'] for e in events['publisher_integrity']]),'generation_flip':trim([e['data'] for e in events['generation_flip']]),'generation_reclaim':trim([e['data'] for e in events['generation_reclaim']]),'operations':ops,'wal':[e['data'] for e in events['wal_summary']],'flow_window_epoch':window,'flow_wal':{'checkpoint_ms':stats([x['ms'] for x in cp]),'checkpoint_busy':sum(x['busy']!=0 for x in cp),'checkpoint_errors':sum('error'in x for x in cp),'writer_commits_during_checkpoint':sum(x['writer_commits_during'] for x in cp),'reader_queries_during_checkpoint':sum(x['reader_queries_during'] for x in cp),'all_grants_min_start_spacing_ms':min(intervals,default=0),'all_grants_spacing_violations':sum(x.get('since_previous_start_ms',0)>0 and x['since_previous_start_ms']+.002<x['minimum_grant_interval_ms'] for x in grants),'resets':len(resets),'resets_per_minute':len(resets)*60/(window[1]-window[0]) if window else None,'series_5s':list(series.values()),'snapshot_actual_ms':stats([e['data']['actual_ms'] for e in events['snapshot_lifetime'] if within(e)]),'foreground_read_ms':stats([e['data']['ms'] for e in events['concurrent_tune'] if within(e)]),'read_checkpoint_overlap_ms':stats([e['data']['ms'] for e in events['concurrent_tune'] if within(e) and e['data'].get('checkpoint_overlap')]),'read_checkpoint_idle_ms':stats([e['data']['ms'] for e in events['concurrent_tune'] if within(e) and not e['data'].get('checkpoint_overlap')]),'checkpoints_with_writer_progress':sum(x['writer_commits_during']>0 for x in cp),'checkpoints_with_reader_progress':sum(x['reader_queries_during']>0 for x in cp),'foreground':fgstats},'completed':[e['event'] for e in raw if e['event'].endswith('_completed')],'stderr':p.with_suffix('.stderr').read_text() if p.with_suffix('.stderr').exists() else ''}
  results.append(r)
  for f in p.parent.iterdir():
   if not f.is_file():continue
   dest=out/(prefix+'-'+p.parent.name+'-'+f.name+('' if f.suffix=='.gz' else '.gz'))
   if not dest.exists():dest.write_bytes(f.read_bytes() if f.suffix=='.gz' else gzip.compress(f.read_bytes(),mtime=0))
 (out/(prefix+'-summary.json')).write_text(json.dumps(results,indent=2,ensure_ascii=False)+'\n')
 print(json.dumps({'receipts':len(results),'complete':sum(bool(r['completed']) for r in results),'publisher':sum(bool(r['capacity']) for r in results),'wal':sum(bool(r['flow_wal']['foreground']) for r in results),'schemas':dict(collections.Counter(r['environment']['candidate_sha256'] for r in results))}))
if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('input',type=Path);ap.add_argument('output',type=Path);ap.add_argument('--prefix',required=True);a=ap.parse_args();run(a.input,a.output,a.prefix)
