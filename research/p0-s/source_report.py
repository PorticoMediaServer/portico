import csv, gzip, hashlib, io, json, pathlib

E = pathlib.Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0/S-evidence')
SHA = 'ef4b74ad542e0951c23760d15a3e60f295210d3bf0a1c29a9f9b944f1a4b722c'
def load(p):
    p = pathlib.Path(p)
    return json.loads(p.read_bytes() if p.exists() else gzip.decompress(p.with_suffix(p.suffix+'.gz').read_bytes()))
def spread(a):
    return f'{min(a):.3f}–{max(a):.3f}'
def field(h, key):
    return next((v for k,v in h['files'].items() if k.endswith('/'+key)), '')
def kv(h, key):
    return dict(x.split(maxsplit=1) for x in field(h,key).splitlines() if len(x.split()) >= 2)
def mem(h):
    return {'current_bytes': int(field(h,'memory.current') or 0), 'peak_bytes': int(field(h,'memory.peak') or 0), 'events':kv(h,'memory.events'), 'stat':kv(h,'memory.stat'), 'cpu':kv(h,'cpu.stat'), 'io':kv(h,'io.stat'), 'io_psi':field(h,'pressure/io'), 'memory_psi':field(h,'pressure/memory')}

groups = [('standard-1M','41927562561',[f'source-ef-standard-rep{n}' for n in [1,2,3]]),('legacy8-1M-STRESS','41927562561',[f'source-ef-legacy8-rep{n}' for n in [1,2,3]]),('legacy8-400k','41931143812',[f'source-ef-legacy8-400k-{n}' for n in [1,2,3]])]
lines = ['# Decoupled source acceptance, exact ef4b74 — UNBOUNDED WAL NEGATIVE', '', '**Invalid for qualification:** legacy8-1M lacks boundedWAL/reset admission. All standard/400k old-recipe numerical comparisons are exploratory and superseded by the pending common bounded-WAL recipe.', '', f'DDL SHA256 `{SHA}`. Bundled modernc SQLite3.53.4; FK ON; WAL + synchronous=NORMAL; writer autocheckpoint0; separate PASSIVE checkpointer every100ms. Three independent VMs/profile;1,000holds/size/VM. Phase0 accepted protocol: per-VM one-sided95%p99 upper (rank996) ≤10ms; hosted maximum is directional. No slow holds removed. Legacy8-1M is an unbounded-WAL negative; gate disposition follows repaired recipe. Synthetic fixture is standalone Basic movies, not the mixed21-path corpus.', '', 'Source acceptance includes subject, library-scoped identity, entity, asset/link, ready search_publish_jobs witness, generation counter, age count, library revisions, event, receipt and COMMIT. No FTS/search-publication writes; witness audit matches every accepted source row. Old full-seed.json shape prose incorrectly mentions FTS; decoupled_search=true, exact SQL and the source-witness audit establish the actual recipe. The opaque recipe hash is a fixture witness, not publisher correctness qualification.', '']
phase_names=['subject','identity-key','entity-indexes','asset-indexes','link-indexes','search-input-job','search-generation','age-counter','library-revision','event','receipt','commit']
buf=io.StringIO();cw=csv.writer(buf);cw.writerow(['ddl_sha256','run','profile','vm','control','batch','sample','start_ns','hold_ms']+[x+'_ms' for x in phase_names]);tails=0
summary=[]
for profile,run,dirs in groups:
    cells=[]
    for n,dirname in enumerate(dirs,1):
        d=load(E/dirname/'writer-attribution.json');assert d['ddl_sha256']==SHA and d['fts_excluded']
        clean={k:v for k,v in d.items() if k!='results'};clean['results']=[]
        for r in d['results']:
            clean['results'].append({k:v for k,v in r.items() if k not in ['samples','holds_over25_ms']})
            for s in r['holds_over25_ms']:
                phases={x['phase']:x['ms'] for x in s['phases']};cw.writerow([SHA,run,profile,n,r['control'],r['batch'],s['rep'],s['start_ns'],s['hold_ms']]+[phases.get(x,0) for x in phase_names]);tails+=1
        (E/dirname/'writer-summary.json').write_text(json.dumps(clean,indent=2)+'\n')
        cells.append(clean['results'])
        del d
    lines += [f'## {profile}, run{run}', '', '|Batch|p50 spread ms|p99 spread ms|p99 upper95 spread ms|max spread ms|items/s spread|Median ratio|', '|---:|---:|---:|---:|---:|---:|---:|']
    for batch in [1,2,4,8,16,32,64,128]:
        rr=[next(x for x in c if x['batch']==batch and x['control']=='normal-passive') for c in cells]
        ratio=max(x['p50_ms'] for x in rr)/min(x['p50_ms'] for x in rr)
        lines.append(f'|{batch}|'+ '|'.join(spread([x[k] for x in rr]) for k in ['p50_ms','p99_ms','p99_upper_95_ms','max_ms','entities_per_s'])+f'|{ratio:.2f}×|')
        summary.append({'profile':profile,'run':run,'batch':batch,'median_ratio':ratio,'split_4x':ratio>=4,'all_vm_p99_upper_pass':all(x['hosted_p99_upper_95_pass'] for x in rr),'vms':rr})
    rr=[next(x for x in c if x['batch']==1 and x['control']=='normal-passive') for c in cells]
    lines+=['', 'Batch1 COMMIT p50/p99/max spreads: '+ ' / '.join(spread([x['phase_summary']['commit'][k] for x in rr]) for k in ['p50_ms','p99_ms','max_ms'])+'ms. Entity/index statement max spread: '+spread([x['phase_summary']['entity-indexes']['max_ms'] for x in rr])+'ms.', '']
raw=buf.getvalue().encode();packed=gzip.compress(raw,mtime=0)
(E/'S-SOURCE-EF-HOLDS-OVER25.csv.gz').write_bytes(packed)
(E/'S-SOURCE-EF-HOLDS-OVER25.manifest.json').write_text(json.dumps({'ddl_sha256':SHA,'rows':tails,'raw_sha256':hashlib.sha256(raw).hexdigest(),'gzip_sha256':hashlib.sha256(packed).hexdigest()},indent=2)+'\n')
(E/'S-SOURCE-EF-summary.json').write_text(json.dumps({'ddl_sha256':SHA,'cells':summary},indent=2)+'\n')
lines+=['## Decision and boundary', '', 'Standard1M: largest tested batch with all3per-VM p99 upper bounds ≤10ms is16. Historical numerical legacy400k batch and the legacy1M stress distribution are shown in the tables above. No cohort exceeds the predeclared4×between-VM median split threshold. All tails, including COMMIT and every statement phase, remain in S-SOURCE-EF-HOLDS-OVER25.csv.gz with hashes. Small local source-only control: initial1k, final33,769,128holds/size; insufficient N for95%p99 bound. Largest observed10/25 batch32, not a full1M reference maximum qualification.', '', 'Each successive batch cohort grows the fixture:1M→1,256,001,400k→656,001 including checkpoint-paused diagnostic and one footprint item. Per-VM JSON summaries retain every reported statistic. Throughput includes host telemetry/SQL construction and is conservative relative to writer-only service rate. Checkpoint-paused diagnostic runs later and cannot establish a causal checkpoint comparison.', '', 'Per Basic ef4b74 footprint:42new ordinary secondary-index entries and11logical row effects (8inserts plus library/generation/age updates). No FTS shadows; no provider metadata, membership facets or scanner IO. Minimal Basic measurement is not complete enrichment, authority, staging or product acceptance proof.', '']
(E/'S-SOURCE-EF.md').write_text('\n'.join(lines))

resources=[]
for dirname in ['resource-ef-legacy8-1m']+[f'source-ef-legacy8-400k-{n}' for n in [1,2,3]]:
    p=E/dirname;ready=load(p/'fixture-ready.json');start=load(p/'host-run-start.json')['host'];end=load(p/'host-run-end.json')['host']
    resources.append({'directory':dirname,'target':ready['target_residents'],'ready_disk':ready['disk'],'pragmas':ready['pragmas'],'start':mem(start),'ready':mem(ready['host']),'end':mem(end)})
    rr=p/'writer-attribution.json'
    if rr.exists():
        d=load(rr)
        for cell in d['results']:
            if cell['control']!='normal-passive':continue
            a,b=cell['samples'][0]['host_before'],cell['samples'][-1]['host_after']
            resources[-1].setdefault('cohorts',[]).append({'batch':cell['batch'],'start':mem(a),'end':mem(b),'start_disk':a['disk'],'end_disk':b['disk']})
(E/'S-RESOURCE-EF-summary.json').write_text(json.dumps({'ddl_sha256':SHA,'runs':['41931140186','41931143812'],'resources':resources},indent=2)+'\n')
print('tails',tails)
for x in summary:
    if x['profile']=='legacy8-400k':print('400k',x['batch'],'upper',spread([v['p99_upper_95_ms'] for v in x['vms']]),'max',spread([v['max_ms'] for v in x['vms']]),'pass',x['all_vm_p99_upper_pass'])
for r in resources:
    print(r['directory'],r['ready_disk'], {k:r['ready']['stat'].get(k) for k in ['anon','file','pgscan_direct','pgsteal_direct','workingset_refault_file','pgmajfault']},'events',r['ready']['events'])
