import argparse,collections,gzip,hashlib,json,pathlib
ap=argparse.ArgumentParser();ap.add_argument('root');args=ap.parse_args();root=pathlib.Path(args.root);parts=[];summary={}
for d in sorted([p for p in root.iterdir() if p.name.split('-')[0] in ['100000','400000','1000000']]):
 size,profile=d.name.split('-',1);phases={}
 for phase in ['rest','ingest']:
  p=d/f'queries-{phase}.jsonl'
  if p.exists():rs=[json.loads(x) for x in p.read_text().splitlines()]
  else:
   with gzip.open(str(p)+'.gz','rt') as f:rs=[json.loads(x) for x in f]
  phases[phase]=rs
 summary[d.name]={p:[{k:r[k] for k in ['name','summary','overlap']} for r in rs] for p,rs in phases.items()}
 parts+=['\n### '+size+' '+profile+' query matrix','\nWorst applicable kind/direction for each cell, selected by that cohort’s p95; shown as p50 / p95 ms. These are individual cohort percentiles, never percentiles of pooled kind maxima. Native SQL cost only.','\n| Sort | Rest open | Ingest open | Rest restricted | Ingest restricted |','|---|---:|---:|---:|---:|']
 sorts=[]
 for r in phases['rest']:
  sort=r['name'].split('/')[0]
  if len(r['name'].split('/'))==4 and sort not in sorts:sorts.append(sort)
 for sort in sorted(sorts):
  vals=[]
  for viewer in ['open','restricted']:
   for phase in ['rest','ingest']:
    rows=[r for r in phases[phase] if r['name'].startswith(sort+'/') and r['name'].endswith('/'+viewer)]
    worst=max(rows,key=lambda x:x['summary']['p95_ms'] or 0);s=worst['summary'];vals.append(f"{s['p50_ms']:.2f} / {s['p95_ms']:.2f}")
  parts.append('| '+sort+' | '+' | '.join(vals)+' |')
 parts+=['\n| Facet / total / lexical control | Rest p50 / p95 ms | Ingest p50 / p95 ms | Ingest VM max |','|---|---:|---:|---:|']
 for r in phases['rest']:
  if not r['name'].startswith(('facets','totals','search/word')):continue
  s=r['summary'];v=next(x['summary'] for x in phases['ingest'] if x['name']==r['name'])
  parts.append(f"| {r['name']} | {s['p50_ms']:.2f} / {s['p95_ms']:.2f} | {v['p50_ms']:.2f} / {v['p95_ms']:.2f} | {v['max_vm']:,} |")
 counts={p:{'cohorts':len(rs),'incomplete':sum(r['summary']['completed']!=r['summary']['n'] for r in rs),'idle':sum(r['overlap']['label']=='idle' for r in rs)} for p,rs in phases.items()}
 parts.append('\nCohort accounting: '+json.dumps(counts)+'. Interrupted or idle cohorts are not passes. The 21 published IDs are exercised over this model’s applicable kinds; source/wire compatibility remains an independent open gate.')
 if not (d/'storage.json').exists():continue
 st=json.loads((d/'storage.json').read_text());live=[x for x in st['objects'] if x['type']=='table' and not x['name'].startswith(('sqlite_','search_own_prefix'))];idx=[x for x in st['objects'] if x['type']=='index'];logical=sum(x['rows'] or 0 for x in live);entries=sum((x['dbstat'][0][2] or 0)+(x['dbstat'][0][3] or 0) for x in idx)
 parts.append(f"\nCaptured at {st['entity_count']:,} resident entities: {logical:,} logical rows excluding FTS virtual/shadow aliases ({logical/st['entity_count']:.2f}/entity); {entries:,} explicit/unique B-tree index records ({entries/st['entity_count']:.2f}/entity, excludes WITHOUT ROWID table PKs and FTS postings). Main DB {st['db_bytes']:,} bytes plus WAL {st['wal_bytes']:,} bytes. Native schema allocation {st['memory']['schema']:,} bytes, pager {st['memory']['pager']:,} bytes: **reduced-core diagnostics only**. Churn WAL is substantial and is not a steady-state or checkpoint-debt bound.")
 opens=[json.loads(p.read_text())['summary'] for p in d.glob('opens-*.json')]
 parts.append('\nCore usable-open cohorts (three fresh processes, 100 each): '+', '.join(f"{s['p50_ms']:.2f}/{s['p95_ms']:.2f} ms" for s in opens)+'. These include the post-churn WAL state; they exceed the nominal full-product 15/25ms line but cannot isolate schema versus WAL initialization. Full-DDL/bundled-engine opens remain OPEN.')
 vals={}
 for profile in ['standard','legacy8']:
  rows=collections.defaultdict(list)
  for d in root.glob('fanout-'+profile+'-*'):
   for file in ['collection-results.json','shared-results.json']:
    for r in json.loads((d/file).read_text()):
     s=r['summary'];rows[(s.get('members',s.get('parents')),s['case'])].append(s)
  if rows:vals[profile]=rows
if vals:
 parts+=['\n## Adversarial fanout: completed hosted run 41904765975','\nSource 884614b3264927dcb3fef7bfe9a494fb7bddcb88. Three independent VMs per profile; five timed mutations/case/size in each VM. Table shows minimum–maximum of each VM’s maximum transaction hold. Five samples make each cohort p99 equal its maximum; this is an exploratory extreme, not a calibrated tail probability. FULL commit included; source and scalar changes commit together. **Budget lines: p99≤10ms AND max≤25ms (binding), with max≤50ms separately reported as a relaxed comparison only.**','\n| Members / parents | Case | Standard max spread ms | legacy8 max spread ms | SQL rows changed | Indexed member rows read, lower bound |','|---:|---|---:|---:|---:|---:|']
 for key,rows in vals['standard'].items():
  alt=vals['legacy8'][key];n,case=key
  def spread(xs):return f"{min(x['max_ms'] for x in xs):.2f}–{max(x['max_ms'] for x in xs):.2f}"
  touched=max(x['max_changes'] for x in rows+alt);read=0 if case=='add-delta' else n-1 if 'asset-' not in case else n
  parts.append(f'| {n:,} | {case} | {spread(rows)} | {spread(alt)} | {touched:,} | ≥{read:,} |')
 parts+=['\nAdd-delta is deliberately a **two-extrema lower bound** (duration and file size), not a complete canonical collection acceptance command or a proof all scalar keys are maintained. Other collection cases recompute six scalar values and scan admitted member rows using the source membership key plus entity PK. Updating one member and collection only touches two logical rows, while it may still read one million rows. Shared asset change updates every linked parent; delete removes all source links and updates every parent. B-tree index churn and physical IO are additional to total_changes.','\nThe 100k-member rescan already exceeds the 25ms maximum across these VMs; 1M-member rescan and 100k-parent reverse updates exceed both budget lines. At 10k, full rescans usually fit the maximum, but standard’s 18.61ms add-rescan sample fails the strict p99≤10 line. Shared-asset 10k results have wide outliers; no blanket 10k fanout-safe claim follows.','\nChunk witness files show the semantic failure: after a source asset duration=789 and first parent’s scalar are committed, an untouched parent still exposes duration_key=0. A bounded cursor cannot fix this accepted-source/stale-key image by itself. **No consistent chunked publication primitive is implemented or qualified here.** INT must choose and test measured scalar-contribution indexing or staged/source-versioned atomic publication before freeze. The reduced schema already falsifies an unconditional bounded point-update claim; complete T1/INT DDL and bundled engine may add cost.']
(root/'S-measurements.json').write_text(json.dumps(summary,indent=2))
(root/'S-RESULT-TABLES.md').write_text('\n'.join(parts)+'\n')
# Preserve completed evidence compactly; hashes cover original and compressed bytes.
for d in [p for p in root.iterdir() if p.name.split('-')[0] in ['100000','400000','1000000']]:
 manifest=[]
 for p in d.glob('*.jsonl'):
  original=p.read_bytes();compressed=gzip.compress(original,mtime=0);q=p.with_suffix(p.suffix+'.gz');q.write_bytes(compressed);manifest.append({'file':p.name,'sha256':hashlib.sha256(original).hexdigest(),'compressed_file':q.name,'compressed_sha256':hashlib.sha256(compressed).hexdigest(),'bytes':len(original)});p.unlink()
 if manifest:(d/'compression-manifest.json').write_text(json.dumps(manifest,indent=2))
