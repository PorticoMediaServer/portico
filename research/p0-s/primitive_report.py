import pathlib,json,gzip,collections,csv,io,hashlib
E=pathlib.Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0/S-evidence');SHA='97dc083d437e4da0aaab9b277bdc9c8f5dace1450a2a5e754579867916352393'
def load(p):
 p=pathlib.Path(p);return json.loads(p.read_bytes() if p.exists() else gzip.decompress(p.with_suffix(p.suffix+'.gz').read_bytes()))
def spread(a):return f'{min(a):.3f}–{max(a):.3f}'
groups=collections.defaultdict(list);summaries=[];buf=io.StringIO();cw=csv.writer(buf);cw.writerow(['ddl_sha256','case','profile','vm','sample','hold_ms','phase','phase_ms','start_ns']);tails=0
for prefix,run in [('scalar-97-','41935016042'),('cue-97-','pending')]:
 for directory in sorted(E.glob(prefix+'*')):
  profile='legacy8' if 'legacy8' in directory.name else 'standard';vm=directory.name.split('-rep')[1].split('-')[0]
  names=['scalar-remove-extreme','scalar-add','scalar-update','scalar-reverse-10','scalar-reverse-100'] if prefix.startswith('scalar') else ['cue255-change-corrected','cue255-change-old-full-bound','cue255-delete','cue255-size-collapsed100','episode8-change-corrected','episode8-delete']
  wal=load(directory/'bounded-wal.json');invalid=[]
  if wal['checkpoint_ms']!=50 or wal['peak_physical_bytes']>192*2**20 or not wal['resets']:invalid.append('wal-envelope')
  if any(x.get('error') for x in wal['checkpoints']):invalid.append('checkpoint-error')
  for name in names:
   p=directory/(name+'.json')
   if not p.exists() and not p.with_suffix('.json.gz').exists():continue
   d=load(p);assert d['ddl_sha256']==SHA
   if d['n']<1000:invalid.append('sample-count')
   summary={k:v for k,v in d.items() if k not in ['samples','rows_changed']};summary.update(profile=profile,vm=vm,run=run,case=name,invalid=invalid.copy(),rows_min=min(d['rows_changed']),rows_max=max(d['rows_changed']),wal_peak_mib=wal['peak_physical_bytes']/2**20,wal_resets=wal['resets'])
   commits=[next(x['ms'] for x in s['phases'] if x['phase']=='commit') for s in d['samples']];summary['commit_p99_ms']=sorted(commits)[int(len(commits)*.99)-1];summary['commit_max_ms']=max(commits)
   for s in d['samples']:
    if s['hold_ms']>25:
     tails+=1
     for phase in s['phases']:cw.writerow([SHA,name,profile,vm,s['sample'],s['hold_ms'],phase['phase'],phase['ms'],phase['start_ns']])
   groups[name,profile].append(summary);summaries.append(summary)
   (directory/(name+'-summary.json')).write_text(json.dumps(summary,indent=2)+'\n')
   del d
lines=['# Bundled contribution and source-cap primitives,97dc','',f'Exact DDL SHA256 `{SHA}`, bundledmoderncSQLite3.53.4,1Mresident Basic source entities onbothprofiles; FKON,NORMAL/autockpt0,50msminimumgovernedPASSIVE/64–192MiBWALadmission and20Hzclosedtitle40readers.3VMs/profile×1,000holds/case. Scalar run41935016042; CUE run recordedinindividualsource receipt. AllSQLphases+revision/event/receipt+COMMIT included; setup/restore/telemetry outside writer hold. Millionmemberfixture built in1kboundedsetupquanta. Source membershipadd/remove includes assetauthored-membership revision andreadysearchinputjob delta. No FTS.','', 'Base-inline aggregate branch, aggregate_publication_id=NULL. Five exact partial scalar extrema seeks plus per(parent,asset)size seek; no member scan to find next maximum. No active publication overlay debt, private typed authority/independent oracle, HTTP/fullproductpopulation or physical reference-machine qualification. Per-VMp99upper95<=10ms; max<=25ms remainsquiet/reference requirement, hostedmaxdirectional. No slow-tail exclusions.','', '|Case|Profile|VMs|p50 spread ms|p99 spread ms|p99upper95 spread ms|max spread ms|Rows touched min–max|COMMITp99/max spreads ms|Median ratio|Result|','|---|---|---:|---:|---:|---:|---:|---|---|---:|---|']
for (name,profile),z in sorted(groups.items()):
 ratio=max(x['p50_ms'] for x in z)/min(x['p50_ms'] for x in z)
 result='pending3VM' if len(z)<3 else 'INVALID' if any(x['invalid'] for x in z) else 'INCONCLUSIVE4xsplit' if ratio>=4 else 'p99upperPASS' if all(x['hosted_p99_upper_95_pass'] for x in z) else 'p99upperFAIL'
 if 'old-full' in name:result='semanticnegativebound'
 lines.append(f'|{name}|{profile}|{len(z)}/3|'+ '|'.join(spread([x[k] for x in z]) for k in ['p50_ms','p99_ms','p99_upper_95_ms','hosted_max_directional_ms'])+f'|{min(x["rows_min"] for x in z)}–{max(x["rows_max"] for x in z)}|{spread([x["commit_p99_ms"] for x in z])}/{spread([x["commit_max_ms"] for x in z])}|{ratio:.2f}×|{result}|')
 print(name,profile,result,'upper',spread([x['p99_upper_95_ms'] for x in z]),'max',spread([x['hosted_max_directional_ms'] for x in z]),'rows',z[0]['rows_max'])
lines+=['', 'CUE uses3source categories perchild:album3,albumartist4,andexplicitartist5; artist/album parent updates coalesce to2distinctparents. Episodes use1/2(show/season). ClosedCUEduration staysend-start, openlast followsasset-start, audioresolutionNULL. Oldfull propagation deliberately assignswhole-file durationtoalltracks: semanticnegative upperbound only. Physicalasset delete removeslinks/asset andzeroesmetrics while retainingmetadata/subjects/hierarchy; entityretirement andpinnedpreparedcustody are separate authority gates. Size-onlyCAPSc leaves25,500per-child authoredscalar tuples untouched, updating102per(parent,asset)size rows(100sharedauthored+2auto),plus256owner/childkeys and102parentkeys; authored membership witnesschecked separately. Synthetic unparentedmovieowner deliberately charges oneextraownerrow; not a musicidentity oracle.', '', 'Canceled CUE41935579224 usedincorrectsource8forautomaticparents and is protocol-invalid. Corrected rerun must supplysources3/4/5beforequalification. Local2-holdfunctionalcontrols are not distributions orquiet-machine maximumqualification.', '']
raw=buf.getvalue().encode();packed=gzip.compress(raw,mtime=0);(E/'S-PRIMITIVES-97-HOLDS-OVER25.csv.gz').write_bytes(packed);(E/'S-PRIMITIVES-97-HOLDS-OVER25.manifest.json').write_text(json.dumps({'ddl_sha256':SHA,'holds':tails,'raw_sha256':hashlib.sha256(raw).hexdigest(),'gzip_sha256':hashlib.sha256(packed).hexdigest()},indent=2)+'\n')
(E/'S-PRIMITIVES-97-summary.json').write_text(json.dumps({'ddl_sha256':SHA,'runs':summaries},indent=2)+'\n');(E/'S-PRIMITIVES-97.md').write_text('\n'.join(lines))
