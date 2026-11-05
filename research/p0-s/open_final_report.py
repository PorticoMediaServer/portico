import pathlib,json,math,re,statistics
E=pathlib.Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0/S-evidence')
SHAS={'415889':'4158892e55858944b84beee38a8d95704a7d2d3f95fcefded4291240dcff7224','291ab3':'291ab3ac4b78338227923e090174b562726d3f7cddb36669e94cb14e05747fdd'}
def spread(a):return f'{min(a):.3f}–{max(a):.3f}'
def pct(a,p):return sorted(a)[math.ceil(len(a)*p)-1]
results=[];paired=[];groups={}
for profile in ['standard','legacy8']:
 for schema,sha in SHAS.items():
  vms=[]
  for vm in [1,2,3]:
   root=E/f'open-291-{profile}-rep{vm}-1000000-cp100';p=root/f'full-{schema}'
   o=[json.load(open(x)) for x in p.glob('bundled-opens-*.json')];assert len(o)==3 and all(x['ddl_sha256']==sha for x in o)
   samples=[t for x in o for t in x['samples_ms']];assert len(samples)==300
   m=json.load(open(p/'bundled-memory.json'));assert m['ddl_sha256']==sha;z=m['pools'];native=z[-1]['native'];assert len({x['schema'] for x in native})==1
   model=re.search(r'Model name:\s*(.*)',(root/'host-openpair.txt').read_text()).group(1)
   r={'profile':profile,'vm':vm,'schema':schema,'ddl_sha256':sha,'cpu_model':model,'processes':o,'pool_curves':z,'schema_bytes_per_connection':native[0]['schema'],'rss_mib_per_connection':(z[-1]['rss_bytes']-z[0]['rss_bytes'])/32/2**20,'pooled_p50_ms':pct(samples,.5),'pooled_p95_ms':pct(samples,.95),'max_process_p95_ms':max(x['p95_ms'] for x in o),'mean_process_p95_ms':statistics.mean(x['p95_ms'] for x in o)}
   vms.append(r);results.append(r)
  groups[profile,schema]=vms
 for vm in [1,2,3]:
  a=next(x for x in groups[profile,'415889'] if x['vm']==vm);b=next(x for x in groups[profile,'291ab3'] if x['vm']==vm)
  paired.append({'profile':profile,'vm':vm,'cpu_model':b['cpu_model'],'p95_delta_ms':b['mean_process_p95_ms']-a['mean_process_p95_ms'],'p95_percent_delta':100*(b['mean_process_p95_ms']/a['mean_process_p95_ms']-1)})
lines=['# Final usable-open and memory result: 291a versus 4158','', '**Schema heap passes the accepted 5 MiB budget. Usable-open p95 misses the 21 ms standard and 26 ms legacy budgets in this cohort.** No slow process or VM was excluded. The paired 4158 baseline is also slower on these VMs than in the previous cohort; the eight table changes do not produce a large paired regression. This does not convert the absolute budget miss into a pass.','', 'Run **41939826702**, research pin `c745ef1d67f66636b3b26cc55d32183bc964c992`, bundled modernc SQLite 3.53.4. Three independent VMs per profile. On each VM: 4158 then 291a, three fresh processes per schema, 100 fresh physical connections per process. Each timed open applies required PRAGMAs and completes a real 40-row read. Native connection/schema caches are fresh; the OS file cache is warm. The database has 1,000 Basic items and the complete candidate DDL. Legacy8 uses an 8 GiB cgroup including page cache, swap 0, and a 200% CPU quota across CPUs 0–3. It is a memory/reduced-CPU emulation, not a calibrated 2015 IPC or HDD database benchmark; scratch is on runner SSD.','']
for schema,sha in SHAS.items():lines.append(f'- {schema}: `{sha}`')
lines+=['', '| Profile | Schema | Process p50 spread, ms | Process p95 spread, ms | Schema MiB/connection | RSS MiB/connection |','|---|---|---:|---:|---:|---:|']
for profile in ['standard','legacy8']:
 for schema in SHAS:
  z=groups[profile,schema];o=[p for x in z for p in x['processes']]
  lines.append(f'| {profile} | {schema} | {spread([x["p50_ms"] for x in o])} | {spread([x["p95_ms"] for x in o])} | {spread([x["schema_bytes_per_connection"]/2**20 for x in z])} | {spread([x["rss_mib_per_connection"] for x in z])} |')
lines+=['', 'The 291a native schema allocation is **4,790,694 bytes / 4.568762 MiB per connection**, identical across all six VMs. It increases by 66,510 bytes from 4158 and remains below 5 MiB. RSS is the slope from 0 to 32 open connections, including engine, allocator, pager and Go effects. Statement memory is zero in this idle workload. Actual first-read pager allocation is reported in each pool curve; the configured cache limit is 16 MiB. Full production statement-cache and loaded-server RSS are phase-1 work.','', '| Profile | VM | CPU reported by runner | 4158 pooled p95, ms | 291a pooled p95, ms |','|---|---:|---|---:|---:|']
for profile in ['standard','legacy8']:
 for vm in [1,2,3]:
  a=next(x for x in groups[profile,'415889'] if x['vm']==vm);b=next(x for x in groups[profile,'291ab3'] if x['vm']==vm)
  lines.append(f'| {profile} | {vm} | {b["cpu_model"]} | {a["pooled_p95_ms"]:.3f} | {b["pooled_p95_ms"]:.3f} |')
lines+=['', 'The previous paired run 41937512010 measured 4158 p95 of 15.519–20.867 ms standard and 19.921–26.010 ms legacy. Its standard VMs used EPYC 9V45/9V74; this standard cohort used EPYC 7763 and Xeon 8573C. Hardware differences are recorded, not used to discard runs. No between-VM median ratio reaches the predeclared 4× split threshold. The paired baseline and source/engine pin are retained so the cohort difference can be assessed without blaming the 291a DDL alone.','', '| Current phase-0 gate | Result |','|---|---|','| Standard usable open p95 ≤21 ms | **MISS:** worst process p95 29.661 ms; all three pooled VM p95 values also miss |','| Legacy usable open p95 ≤26 ms | **MISS:** worst process p95 26.798 ms; one pooled VM p95 also misses |','| Schema heap ≤5 MiB/connection | **PASS:** 4.568762 MiB on every VM |','| RSS | Measured: 9.251–9.269 MiB/connection under the stated idle workload; reported separately |','', 'These are the revised budgets accepted by lead/R in INT-OPEN-DECISION.json. The old 15 ms fresh-open and 2/3 MiB heap lines are historical. All raw samples, exact DDL hashes, source archive hashes, native status values, pool curves and host configuration remain in the run artifacts. This is not a complete product cold-start or populated 1M statement-cache measurement.','']
# Assert the literal byte figure in prose against the actual receipts.
assert {x['schema_bytes_per_connection'] for x in results if x['schema']=='291ab3'}=={4790694}
(E/'S-OPEN-291.md').write_text('\n'.join(lines));(E/'S-OPEN-291-summary.json').write_text(json.dumps({'run':'41939826702','schemas':SHAS,'results':results,'paired':paired},indent=2)+'\n');print('\n'.join(lines[:16]))
