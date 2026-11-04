import json,pathlib,gzip,re,collections,csv,io,hashlib
E=pathlib.Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0/S-evidence');SHA='97dc083d437e4da0aaab9b277bdc9c8f5dace1450a2a5e754579867916352393'
def load(p):
 p=pathlib.Path(p);return json.loads(p.read_bytes() if p.exists() else gzip.decompress(p.with_suffix(p.suffix+'.gz').read_bytes()))
def spread(a):return f'{min(a):.3f}–{max(a):.3f}' if a else 'pending'
groups=collections.defaultdict(list);runs=[];buf=io.StringIO();cw=csv.writer(buf);phases=['subject','identity-key','entity-indexes','asset-indexes','link-indexes','search-input-job','search-generation','age-counter','library-revision','event','receipt','commit'];cw.writerow(['ddl_sha256','profile','vm','residents','cadence_ms','batch','sample','start_ns','hold_ms']+[x+'_ms' for x in phases]);tails=0
for p in sorted(E.glob('bounded-97-*')):
 m=re.fullmatch(r'bounded-97-(standard|legacy8)-rep(\d)-(400000|1000000)-cp(50|250)',p.name)
 if not m or not(p/'bounded-wal.json').exists() and not(p/'bounded-wal.json.gz').exists():continue
 profile,vm,size,cadence=m.groups();vm,size,cadence=map(int,[vm,size,cadence]);w=load(p/'bounded-wal.json');d=load(p/'writer-attribution.json');audit=load(p/'source-witness-audit.json')
 assert d['ddl_sha256']==SHA and w['ddl_sha256']==SHA and audit['ddl_sha256']==SHA and d['fts_excluded']
 cp=w['checkpoints'];readers=w['readers'];intervals=[x['start_interval_ms'] for x in cp if x['start_interval_ms']];invalid=[]
 if w['peak_physical_bytes']>192*2**20 or w['peak_active_bytes']>192*2**20:invalid.append('hardcap')
 if w['checkpoint_ms']!=cadence:invalid.append('wrong-cadence-setting')
 if any(x<cadence for x in intervals):invalid.append('minimum-spacing')
 if not w['resets']:invalid.append('no-reset-witness')
 if any(x.get('error') for x in cp):invalid.append('checkpoint-error')
 if audit.get('asset_admission_witnesses')!=audit['entities']:invalid.append('asset-witness')
 rr=[{k:v for k,v in r.items() if k not in ['samples','holds_over25_ms']} for r in d['results']];(p/'writer-summary.json').write_text(json.dumps({'ddl_sha256':SHA,'invalid':invalid,'results':rr},indent=2)+'\n')
 for r in d['results']:
  for s in r['holds_over25_ms']:
   phase={x['phase']:x['ms'] for x in s['phases']};cw.writerow([SHA,profile,vm,size,cadence,r['batch'],s['rep'],s['start_ns'],s['hold_ms']]+[phase.get(x,0) for x in phases]);tails+=1
 del d
 def values(name,key):return [int(dict(y.split(maxsplit=1) for y in x['memory'].get(name,'').splitlines() if len(y.split())>=2).get(key,0)) for x in cp]
 stats={'profile':profile,'vm':vm,'size':size,'cadence_ms':cadence,'invalid':invalid,'peak_physical_mib':w['peak_physical_bytes']/2**20,'peak_active_mib':w['peak_active_bytes']/2**20,'resets':w['resets'],'checkpoints':len(cp),'min_interval_ms':min(intervals) if intervals else 0,'checkpoint_max_ms':max(x['ms'] for x in cp),'admission_count':len(w['admission_pauses']),'admission_max_ms':max([x['ms'] for x in w['admission_pauses']] or [0]),'reader_count':len(readers),'reader_max_ms':max([x['ms'] for x in readers] or [0]),'reader_refusals':sum('error'in x for x in readers),'max_file_mib':max(values('memory.stat','file'))/2**20,'max_anon_mib':max(values('memory.stat','anon'))/2**20,'memory_max_events':max(values('memory.events','max')),'direct_reclaimed_pages':max(values('memory.stat','pgsteal_direct')),'major_faults':max(values('memory.stat','pgmajfault')),'results':rr}
 groups[profile,size,cadence].append(stats);runs.append(stats)
lines=['# Governed bounded-WAL source acceptance,97dc','',f'Exact DDL SHA256 `{SHA}`; researcha4ecff24904cbd8cc6b6e5bc616003362dd1c04e; run41933849234. BundledmoderncSQLite3.53.4,FKON,NORMAL,writerautockpt0. Source+subject+identity+newassetadmissionwitness+readysearchjob/generation+age/library/event/receipt; NOFTS.1,000holds/size/VM; threeVMs required percell. Each cohort grows the fixture; batch128finishes~255k aboveinitial400k/1M. Partial report updates as artifacts arrive.','', 'Q659a7258 mechanism:64MiBsoftdebt,96MiBpressureprefix,176MiBpre-admissionstopwith16MiBreservebelow192MiBhardcap. SeparatePASSIVEphysicalconnection/busy_timeout0. Minimum start spacing50/250ms with pressure/ticker trigger and actual intervals. Admission outside writer custody, foreground20Hz40titleprobes continue; requested250msdeadline, actual lifetimes recorded. NoRESTART/TRUNCATE; resets detectedfromSHMsalt/mxFrame, physicalWALretainsits bounded highwater.','', '|Profile|Initial|CPms|VM receipts|Peak physicalWAL MiB spread|Resets spread|Peak file/anon MiB spread|memory.max events|Max reader ms|','|---|---:|---:|---:|---:|---:|---|---:|---:|']
for key in sorted(groups):
 z=groups[key];profile,size,cadence=key
 lines.append(f'|{profile}|{size}|{cadence}|{len(z)}/3|{spread([x["peak_physical_mib"] for x in z])}|{spread([x["resets"] for x in z])}|{spread([x["max_file_mib"] for x in z])}/{spread([x["max_anon_mib"] for x in z])}|{max(x["memory_max_events"] for x in z)}|{max(x["reader_max_ms"] for x in z):.3f}|')
for key in sorted(groups):
 z=groups[key];profile,size,cadence=key
 lines+=['',f'## {profile}, initial{size}, minimumCPspacing{cadence}ms ({len(z)}/3VMs)', '', '|Batch|p50 spread ms|p99 spread ms|p99 upper95 spread ms|max spread ms|items/s spread|Median ratio|Protocol result|','|---:|---:|---:|---:|---:|---:|---:|---|']
 for batch in [1,2,4,8,16,32,64,128]:
  rr=[next(r for r in x['results'] if r['batch']==batch) for x in z];ratio=max(x['p50_ms'] for x in rr)/min(x['p50_ms'] for x in rr)
  result='pending3VM' if len(z)<3 else 'INVALID' if any(x['invalid'] for x in z) else 'INCONCLUSIVE4xsplit' if ratio>=4 else 'p99-upperPASS' if all(r['hosted_p99_upper_95_pass'] for r in rr) else 'p99-upperFAIL'
  lines.append(f'|{batch}|'+ '|'.join(spread([x[k] for x in rr]) for k in ['p50_ms','p99_ms','p99_upper_95_ms','max_ms','entities_per_s'])+f'|{ratio:.2f}×|{result}|')
  if batch==1:print(key,'b1',result,'upper',spread([r['p99_upper_95_ms'] for r in rr]),'max',spread([r['max_ms'] for r in rr]))
 lines+=['','Per-VM byte/memory/reset/checkpoint/admission/read statistics and all scalar phase summaries are in S-BOUNDED-97-summary.json. Every hold>25ms is retained with statement/COMMIT phases in S-BOUNDED-97-HOLDS-OVER25.csv.gz; hosted maximum stays directional. No slow-tail exclusions. Incomplete groups cannot close a gate. Reader cancellation is a refusal, never a partial page. This is a minimal movie Basic acceptance SQL-cost prototype, not full typed/private authority, enrichment, publisher or independentV oracle qualification.','']
raw=buf.getvalue().encode();packed=gzip.compress(raw,mtime=0);(E/'S-BOUNDED-97-HOLDS-OVER25.csv.gz').write_bytes(packed)
(E/'S-BOUNDED-97-HOLDS-OVER25.manifest.json').write_text(json.dumps({'ddl_sha256':SHA,'rows':tails,'raw_sha256':hashlib.sha256(raw).hexdigest(),'compressed_sha256':hashlib.sha256(packed).hexdigest()},indent=2)+'\n')
(E/'S-BOUNDED-97-summary.json').write_text(json.dumps({'ddl_sha256':SHA,'run':'41933849234','runs':runs},indent=2)+'\n');(E/'S-BOUNDED-97.md').write_text('\n'.join(lines));print('receipts',len(runs),'tails',tails)
