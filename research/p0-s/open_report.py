import pathlib,json,statistics
E=pathlib.Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0/S-evidence')
SHAS={'97dc08':'97dc083d437e4da0aaab9b277bdc9c8f5dace1450a2a5e754579867916352393','415889':'4158892e55858944b84beee38a8d95704a7d2d3f95fcefded4291240dcff7224'}
def spread(a):return f'{min(a):.3f}–{max(a):.3f}'
results=[];paired=[]
lines=['# Paired full-schema usable open and connection memory','', 'Run41937512010, researche1e75a4e83c7bfd38657180dc7d4b1deecffc578, bundledmoderncSQLite3.53.4. Three independent VMs/profile; each schema uses3freshchildprocesses×100freshphysicalopens. Every open sets requiredPRAGMAs and completes a40-rowcatalogueread; not parser-only. Nativeconnection/schema caches are fresh; OSfilecache is warm. Fixed order97dc then4158 on eachVM. This is the same comparison method as prior open receipts, not the writerp99/1000-hold protocol. CPU/storage/limits in host-openpair.txt. Database has1kBasicfacts; whole exactDDL is loaded.','']
for name,sha in SHAS.items():lines.append(f'- {name}: `{sha}`')
lines+=['', '|Profile|Schema|Freshusable p50 spread ms|p95 spread ms|Native schema MiB/conn|Idle RSS slope MiB/conn|','|---|---|---:|---:|---:|---:|']
for profile in ['standard','legacy8']:
 vms={}
 for schema,sha in SHAS.items():
  opens=[];schemas=[];rss=[]
  for rep in [1,2,3]:
   p=E/f'open-415-{profile}-rep{rep}-1000000-cp100'/f'full-{schema}'
   o=[json.load(open(x)) for x in p.glob('bundled-opens-*.json')];assert len(o)==3
   for x in o:assert x['ddl_sha256']==sha
   m=json.load(open(p/'bundled-memory.json'));assert m['ddl_sha256']==sha
   z=m['pools'];native=[x['schema'] for x in z[-1]['native']];assert len(set(native))==1
   slope=(z[-1]['rss_bytes']-z[0]['rss_bytes'])/32/2**20
   opens+=o;schemas.append(native[0]/2**20);rss.append(slope)
   vms[rep,schema]={'ddl_sha256':sha,'profile':profile,'vm':rep,'opens':o,'schema_bytes_per_connection':native[0],'rss_mib_per_connection':slope,'mean_process_p50_ms':statistics.mean(x['p50_ms'] for x in o),'mean_process_p95_ms':statistics.mean(x['p95_ms'] for x in o)}
   results.append(vms[rep,schema])
  lines.append(f'|{profile}|{schema}|{spread([x["p50_ms"] for x in opens])}|{spread([x["p95_ms"] for x in opens])}|{spread(schemas)}|{spread(rss)}|')
 for rep in [1,2,3]:
  a,b=vms[rep,'97dc08'],vms[rep,'415889'];paired.append({'profile':profile,'vm':rep,'p50_delta_ms':b['mean_process_p50_ms']-a['mean_process_p50_ms'],'p95_delta_ms':b['mean_process_p95_ms']-a['mean_process_p95_ms'],'p95_percent_delta':100*(b['mean_process_p95_ms']/a['mean_process_p95_ms']-1)})
lines+=['', 'STRICT restoration reduces native schema heap6,837,476→4,724,184bytes/connection, a2,113,292byte(30.91%) saving, identical onallVMs. RSS32-connection slope falls~12.82–12.85→9.12–9.15MiB/connection. The native counter is schema only; RSS includes allocator/engine/pager/Go effects and is not assigned to a specific allocator without measurement. Statement memory is0inthis idle workload; configuredpager16MiB but actualfirst40rowpager~0.864MiBon4158. Fullproductionstatementcache, search/other-domainwarmstate and loadedserverRSS remainOPEN.', '', 'The comparison improves97dc but does not restore the historical~9.5/~17msp95cells. Laterdabf44receipts41919045287 hadstandard13.349–13.394/legacy17.877–18.155msp95, schema2.491MiB/RSS5.305–5.311MiB. Those ran different SHAs/VMs, so are historical references, not a paired causal delta. Paired97→415per-VM changes are in the JSON summary.', '', '|Gate|4158 outcome|','|---|---|','|Loaded schema median<=2MiB,p95<=3MiB|**FAIL**,4.505MiB deterministically beforestatementcache|','|Idle resident slope<=6MiB/connection|**FAIL**,9.125–9.150MiBat32connections|','|Fresh usable open p50<=15/p95<=25ms|Standardp50fails onsomeVMs;legacyp50failsall,p95oneprocess26.010msmiss; nofullprofilepass|','|Additional warm physical open p50<=10/p95<=20ms|p50failsbothprofiles;p95someprocessesmiss20ms|','|ExactDDLmeasurement scope|Measured bothprofiles/exactSHAs; failingbudgetlinesstayOPEN. No waiver implied|','', 'Open/memory are scoped primitive measurements, not completeproductcoldstart, socketlatency or a populated1Mprivatecommandcache proof. No slowprocess discarded. Historical0/1/8/16/32curves andall900samples/profile/schema retained.','']
(E/'S-OPEN-415.md').write_text('\n'.join(lines));(E/'S-OPEN-415-summary.json').write_text(json.dumps({'run':'41937512010','schemas':SHAS,'results':results,'paired':paired},indent=2)+'\n');print('\n'.join(lines[:17]))
