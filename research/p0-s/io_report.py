import json,pathlib,re,gzip,hashlib,sys
E=pathlib.Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0/S-evidence')
def load(p):
 p=pathlib.Path(p);return json.loads(p.read_bytes() if p.exists() else gzip.decompress(p.with_suffix(p.suffix+'.gz').read_bytes()))
lines=['# WAL/cache diagnostic — unbounded negative control','', 'Exact ef4b74ad542e0951c23760d15a3e60f295210d3bf0a1c29a9f9b944f1a4b722c, bundled3.53.4, legacy8. Runs41932182527(1M)/41932186450(400k). One VM/size, fresh fixture per variant, sequential default16MiB,2MiBpager,256MiBmmap request, then separately traced default. All used the broken unbounded WAL envelope: **directional only**, not qualification or capacity evidence. Writer NORMAL/autockpt0, old separate100ms PASSIVE; no FTS. TaggedCPU sampled, strace perturbs wall latency.','', '|Residents|Variant|Actual cacheKiB /mmapbytes|Ready DB/WAL GiB|memory.max /directreclaimed pages|Batch1p99/max ms|Batch16p99/max ms|','|---:|---|---|---|---|---|---|']
traces=[]
for size,folder in [(400000,'io-ef-legacy8-400k'),(1000000,'io-ef-legacy8-1m')]:
 for variant in ['default','small-cache','mmap','tagged-io']:
  p=E/folder/variant
  if not (p/'fixture-ready.json').exists():continue
  d=load(p/'fixture-ready.json');pr=d['pragmas'];f=d['host']['files'];stat=dict(x.split() for k,v in f.items() if k.endswith('/memory.stat') for x in v.splitlines());events=dict(x.split() for k,v in f.items() if k.endswith('/memory.events') for x in v.splitlines());r=load(p/'writer-attribution.json')['results'];rr={x['batch']:x for x in r};dis=d['disk']
  lines.append(f'|{size}|{variant}|{pr["cache_size"]} /{pr["mmap_size"]}|{dis["full.db"]["size"]/2**30:.3f}/{dis["full.db-wal"]["size"]/2**30:.3f}|{events["max"]}/{stat["pgsteal_direct"]}|'+ '|'.join(f'{rr[b]["p99_ms"]:.3f}/{rr[b]["max_ms"]:.3f}' for b in [1,16])+'|')
  if variant=='tagged-io':
   status=load(p/'io-trace-status.json');assert status.get('attached'),status
   spans=[]
   # strace -ttt epoch seconds. Finished calls only; resumed lines carry their
   # completion timestamp, so duration reconstructs the start conservatively.
   trace=p/'wal-io.strace';raw=trace.read_text() if trace.exists() else gzip.decompress(trace.with_suffix('.strace.gz').read_bytes()).decode()
   pending={}
   for line in raw.splitlines():
    m=re.match(r'\s*(\d+)\s+(\d+\.\d+)\s+(.*)',line)
    if not m:continue
    tid,ts,s=m.groups();ts=float(ts)
    if '<unfinished ...>' in s:pending[tid]=(ts,s);continue
    dur=re.search(r'<([\d.]+)>\s*$',s)
    if not dur:continue
    duration=float(dur.group(1));beg=ts
    if 'resumed>' in s:
     if tid in pending:beg,previous=pending.pop(tid);s=previous+s
     else:beg=ts-duration
    if '-wal' not in s:continue
    kind=next((x for x in ['pwrite64','fdatasync','fsync'] if x in s),'other')
    spans.append((beg,beg+duration,kind))
   count=0;overlap=0;commitwall=0;tails=[]
   for cell in r:
    for sample in cell['samples']:
     phase=next(x for x in sample['phases'] if x['phase']=='commit');a=phase['start_ns']/1e9;b=a+phase['ms']/1000;commitwall+=phase['ms']
     matches=[(max(a,x),min(b,y),kind) for x,y,kind in spans if y>=a and x<=b]
     # Union avoids double counting overlapping threads' syscall wall spans.
     intervals=sorted((x,y) for x,y,_ in matches if y>x);total=0;end=0
     for x,y in intervals:total+=max(0,y-max(x,end));end=max(end,y)
     overlap+=total*1000;count+=len(matches)
     if sample['hold_ms']>25:tails.append({'batch':cell['batch'],'sample':sample['rep'],'start_ns':sample['start_ns'],'hold_ms':sample['hold_ms'],'commit_ms':phase['ms'],'wal_syscall_union_overlap_ms':total*1000,'wal_syscalls':len(matches)})
   traces.append({'size':size,'attached':True,'commit_wall_sum_ms':commitwall,'wal_syscall_union_overlap_sum_ms':overlap,'matching_calls':count,'holds_over25':tails,'interpretation':'Union wall intersection includes concurrent checkpointer WAL syscalls; not exact per-writer syscall or CPU attribution. Readable fd labels exclude nonWAL DB writes. Strace wall distributions are diagnostic only.'})
lines+=['', 'Reducing the pager cache does not cap the OS page cache for a growing WAL. mmap affects database reads and cannot remove unbounded WAL debt. These one-VM sequential controls cannot establish a causal profile speed comparison. The repaired Q-style192MiB WAL envelope supersedes them. Actual PRAGMA values, direct memory/fault counters and individual strace/operation intervals are retained.', '']
(E/'S-WAL-IO-NEGATIVE.md').write_text('\n'.join(lines))
(E/'S-WAL-IO-NEGATIVE-summary.json').write_text(json.dumps({'ddl_sha256':'ef4b74ad542e0951c23760d15a3e60f295210d3bf0a1c29a9f9b944f1a4b722c','traces':traces},indent=2)+'\n')
print('\n'.join(lines))
