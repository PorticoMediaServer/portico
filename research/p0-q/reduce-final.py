#!/usr/bin/env python3
"""Reduce immutable hosted Q receipts; no database engine is used here."""
import argparse, collections, gzip, hashlib, json, math
from pathlib import Path

def stat(xs):
    s = sorted(xs)
    return {"n": len(s), **({k: s[math.ceil(q*len(s))-1] for k,q in [("p50_ms",.5),("p95_ms",.95),("p99_ms",.99),("max_ms",1)]} if s else {})}

def reduce(path):
    events=[]
    for line in path.read_text().splitlines():
        try: e=json.loads(line)
        except ValueError: continue
        if isinstance(e,dict) and 'event' in e: events.append(e)
    by=collections.defaultdict(list)
    for e in events: by[e['event']].append(e)
    def data(name): return [e['data'] for e in by[name]]
    def one(name): return data(name)[0] if by[name] else None
    env=one('environment')
    if not env: return None
    assert env['sqlite']=='3.53.4', (path,env)
    assert env['candidate_sha256']=='dabf442e6aab647b693e59ebc509f703050e742f96eb5aac344e4e39ea45224f'
    build=(path.parent/'build.txt').read_text() if (path.parent/'build.txt').exists() else ''
    assert 'modernc.org/sqlite\tv1.58.0' in build, path
    ops={o['tag']:o['latency'] for o in data('operation')}
    cp=data('passive_checkpoint'); fg=data('foreground_write'); reads=data('concurrent_tune')
    allocations={name:{k:v for k,v in (one(name) or {}).items() if k!='objects'} for name in ['allocation_base','allocation_churn']}
    snapshots=data('snapshot_lifetime'); short=data('policy_short'); full=data('policy_full')
    for q in full:
        q['page_count']=len(q.get('per_page',[]))
        q['worst_subsequent_page_p95_ms']=max([p.get('p95_ms',0) for p in q.get('per_page',[])[1:]],default=0)
        # Per-ordinal samples/stats are retained, including page1 preparation.
    completed=data('completed');duration=None
    if len(events)>1:
        from datetime import datetime
        duration=(datetime.fromisoformat(events[-1]['at'])-datetime.fromisoformat(events[0]['at'])).total_seconds()
    def fgclass(cl):
        a=[f for f in fg if f['class']==cl]
        return {'n':len(a),'commit':ops.get('foreground.'+cl+'.commit'),'transaction':stat([x['transaction_ms'] for x in a]),'response':stat([x['response_ms'] for x in a]),'queue':stat([x['queue_ms'] for x in a]),'max_background_debt_bytes':max([x.get('known_durability_debt',{}).get('background_bytes',0) for x in a],default=0),'writes':a if cl=='security_FULL' else []}
    wal_series=[{'at':e['at'],'physical':e['data']['wal'].get('Physical'),'active':e['data']['wal'].get('ActiveBytes'),'uncheckpointed':e['data']['wal'].get('Uncheckpointed'),'frames':e['data']['log_frames'],'backfilled':e['data']['backfilled_frames'],'busy':e['data']['busy']} for e in by['passive_checkpoint']]
    return {'artifact':path.parent.name,'environment':env,'engine_provenance':'bundled modernc.org/sqlite v1.58.0 / SQLite3.53.4','elapsed_s':duration,'complete':bool(completed and 'churn_operations' in completed[-1]),'completed':completed,'build':one('build_done'),'allocations':allocations,'operations':ops,'policy_short':short,'policy_full':full,'controls':data('controls'),'wal':one('wal_summary'),'wal_failure':one('wal_hard_limit'),'snapshot_lifetimes':stat([x['actual_ms'] for x in snapshots]),'snapshot_lifetime_raw':snapshots,'mixed':one('mixed_summary'),'checkpoints':{'duration':stat([c['ms'] for c in cp]),'busy':sum(c['busy']!=0 for c in cp),'writer_progress_intervals':sum(c['writer_commits_during']>0 for c in cp),'reader_progress_intervals':sum(c['reader_queries_during']>0 for c in cp),'resets':len(data('wal_reset')),'resets_per_minute':len(data('wal_reset'))*60/duration if duration else None},'wal_series':wal_series,'reads':{'latency':stat([r['ms'] for r in reads]),'checkpoint_overlap':stat([r['ms'] for r in reads if r.get('checkpoint_overlap')]),'no_checkpoint_overlap':stat([r['ms'] for r in reads if not r.get('checkpoint_overlap')]),'errors':data('concurrent_read_error')},'foreground':{cl:fgclass(cl) for cl in ['security_FULL','progress_NORMAL']},'slow_engine_calls':data('native_cpu_sample'),'stderr':path.with_suffix('.stderr').read_text() if path.with_suffix('.stderr').exists() else ''}

def main():
    ap=argparse.ArgumentParser();ap.add_argument('input',type=Path);ap.add_argument('output',type=Path);args=ap.parse_args();args.output.mkdir(parents=True,exist_ok=True)
    records=[]
    for p in sorted(args.input.glob('*/result.jsonl')):
        r=reduce(p)
        if r is None: continue
        records.append(r)
        for f in p.parent.iterdir():
            if f.is_file():
                raw=f.read_bytes();(args.output/('dabf-'+p.parent.name+'-'+f.name+('.gz' if f.suffix!='.gz' else ''))).write_bytes(gzip.compress(raw,mtime=0) if f.suffix!='.gz' else raw)
    (args.output/'dabf-final-summary.json').write_text(json.dumps(records,indent=2,ensure_ascii=False)+'\n')
    pairs=collections.defaultdict(dict)
    for r in records:
        e=r['environment']
        if e['mode'] in ['policy','recipe']:pairs[(e['profile'],e['items'],e['cjk_share_percent'])][e['old_cjk_recipe']]=r
    lines=['# Bundled modernc3.53.4 / dabf442e final-policy receipts','', 'Eight samples per static query: p95/p99 equal observed max; not a reliability qualification. Every ordinal show-more page and raw sample is in dabf-final-summary.json. Full first-page includes preparation. Corpus is static accepted state; production freshness remains a separate gate.','', '| Profile | Items | CJK share | Old/new FTS MiB | Delta | a / al / jo p95 ms | 東 / 王 title p95 ms | 東京 / 王菲 full p95 ms | Worst read snapshot ms |','|---|---:|---:|---:|---:|---:|---:|---:|---:|']
    for (profile,n,share),pair in sorted(pairs.items()):
        new=pair.get(False); old=pair.get(True)
        if not new:continue
        a=new['allocations']['allocation_base'].get('fts_bytes',0);b=old['allocations']['allocation_base'].get('fts_bytes') if old else None
        short={q['query']:q for q in new['policy_short']};full={q['query']:q for q in new['policy_full']}
        vals=lambda qs,m,k:' / '.join(f"{m[q][k].get('p95_ms',0):.3f}" if q in m else 'pending' for q in qs)
        snaps=[q['snapshot_maxima'].get('max_ms',0) for q in short.values()]+[q[k].get('max_ms',0) for q in full.values() for k in ['prepare_snapshots','page_snapshots']]
        lines.append(f"| {profile} | {n:,} | {share}% | {b/2**20 if b else 0:.2f}/{a/2**20:.2f} | {(a-b)*100/b if b else 0:.2f}% | {vals(['a','al','jo'],short,'latency')} | {vals(['東','王'],short,'latency')} | {vals(['東京','王菲'],full,'first_ready')} | {max(snaps,default=0):.3f} |")
    lines+=['','WAL/source holds below are the coupled pre-publisher contract. FAIL remains a failure; timeout/partial receipts are retained. Requested snapshot cap is not assumed actual. PASSIVE overlaps show progress, not proof of zero interference.','', '| Profile/items | Requested / observed snapshot max ms | Cadence ms | Source churn p99/max ms | Read p95 ms | CP p99/max ms | Resets/min | Peak physical WAL MiB | Complete |','|---|---:|---:|---:|---:|---:|---:|---:|---|']
    for r in records:
        e=r['environment']
        if e['mode']!='tune':continue
        w=r['wal'] or {};o=r['operations'].get('churn.tx',{});c=r['checkpoints']['duration'];s=r['snapshot_lifetimes'];read=r['reads']['latency']
        lines.append(f"| {e['profile']}/{e['items']:,} | {w.get('snapshot_cap_ms','?')} / {s.get('max_ms',0):.2f} | {w.get('checkpointer_cadence_ms','?')} | {o.get('p99_ms',0):.2f}/{o.get('max_ms',0):.2f} | {read.get('p95_ms',0):.2f} | {c.get('p99_ms',0):.2f}/{c.get('max_ms',0):.2f} | {r['checkpoints']['resets_per_minute'] or 0:.1f} | {w.get('peak_physical_bytes',0)/2**20:.2f} | {r['complete']} |")
    (args.output/'dabf-final-tables.md').write_text('\n'.join(lines)+'\n')
    manifest={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(args.output.iterdir()) if f.is_file() and f.name!='SHA256.json'};(args.output/'SHA256.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print('reduced',len(records),'receipts, complete',sum(r['complete'] for r in records))
    print('\n'.join(lines))
if __name__=='__main__':main()
