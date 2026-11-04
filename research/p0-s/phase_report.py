from latest_report import E,load,spread
from pathlib import Path
import json,csv,io,gzip,hashlib,collections
configs=[('09a972','41918231507','attribution-09-426'),('42605a','41918231507','attribution-09-426'),('dabf44','41919041482','attribution-dabf')]
lines=['# Writer phases and single-item decisions','', 'NORMAL/PASSIVE, writer autocheckpoint0, separate100ms PASSIVE connection; one benchmark job per hosted VM,3independent VMs/profile,128transactions/size. Ranges are per-VM statistic minima–maxima. All cases start at1Mresident Basic movies, then grow. Unlike the earlier multi-statement Exec sweep, each statement has a separate ExecContext and timer; parse/driver work is charged. Hold begins after BEGIN grant and ends after COMMIT. Standalone item includes same-transaction own-title FTS, and canonical subject/age/identity facts where present. No provider/fragment/full typed-command claim.','']
phases=['subject','identity-key','entity-indexes','asset-indexes','link-indexes','fts','library-revision','event','age-counter','receipt','commit']
csvout=io.StringIO();writer=csv.writer(csvout);writer.writerow(['ddl_sha256','run','profile','vm_rep','control','batch','sample','start_ns','hold_ms']+[x+'_ms' for x in phases]);tailcount=0
for schema,run,prefix in configs:
 lines+=['## '+schema+' /run'+run,'','|Profile|Batch|p50 ms|p99 ms|max ms|items/s|','|---|---:|---:|---:|---:|---:|']
 for profile in ['standard','legacy8']:
  vv=[load(E/f'{prefix}-{profile}-rep{n}'/f'full-{schema}'/'writer-attribution.json') for n in [1,2,3]]
  rr=[v['results'] for v in vv]
  for size in [1,2,4,8,16,32,64,128,256,1000]:
   rs=[next(x for x in r if x['batch']==size and x['control']=='normal-passive') for r in rr]
   lines.append(f'|{profile}|{size}|'+ '|'.join(spread([x[k] for x in rs],3) for k in ['p50_ms','p99_ms','max_ms','entities_per_s'])+'|')
  for n,v in enumerate(vv,1):
   for r in v['results']:
    for tail in r['holds_over25_ms']:
     values={x['phase']:x['ms'] for x in tail['phases']}
     writer.writerow([v['ddl_sha256'],run,profile,n,r['control'],r['batch'],tail['rep'],tail['start_ns'],tail['hold_ms']]+[values.get(x,0) for x in phases]);tailcount+=1
 lines+=['']
lines+=['No tested size1–1k passes strict p99<=10/max<=25 across all3VMs in both profiles in these FTS-coupled recipes. Standard single-item max exceeds25on multiple VMs. The lower legacy maxima are runner-specific observations, not evidence that throttling improves SQLite.','', 'dabf44single-item phase evidence: standard COMMIT p99 is22.403–32.786ms/max26.436–34.789ms, whereas entity+index statement max1.344–2.814ms. Age-counter max<=0.156ms and receipt max<=0.052ms. The long single-item holds occur mainly in COMMIT; index-entry count alone does not explain them. DeferredFTS flush/merge and WAL remain inside COMMIT. The local FTS exclusion pair supports a substantial FTS component; no direct syscall/internalmerge decomposition or causal host-noise proportion is measured.','', 'The post-sweep checkpoint-paused control runs at a later source/WAL/FTS state. It is retained as diagnostic evidence and cannot isolate checkpointer causally from state/segment-layout changes.','', 'At50k versus1M, entity and title index root-to-leaf depth remains2edges (3levels); added_at index grows2→3edges. The50klocal dataset is not an equivalent1Mmemory/page/FTS state even where depthmatches.','']
# requested local/hosted side-by-side same426 sizes
lines+=['## Local quiet50k versus hosted1M, exact426','', '|Batch|Local p50/p99/max ms spreads|Hosted standard p50/p99/max ms spreads|','|---:|---|---|']
local=[load(E/f'local-426-quiet/rep{n}'/'writer-attribution.json')['results'] for n in [1,2,3]]
host=[load(E/f'attribution-09-426-standard-rep{n}'/'full-42605a'/'writer-attribution.json')['results'] for n in [1,2,3]]
for size in [1,2,4,8,16,32,64,128]:
 cells=[]
 for group in [local,host]:
  rs=[next(x for x in r if x['batch']==size and x['control']=='normal-passive') for r in group]
  cells.append(' / '.join(spread([x[k] for x in rs]) for k in ['p50_ms','p99_ms','max_ms']))
 lines.append(f'|{size}|'+ '|'.join(cells)+'|')
lines+=['','## dabf44 per-Basic footprint','']
x=load(E/'attribution-dabf-standard-rep1/full-dabf44/basic-index-footprint.json');groups=collections.Counter()
for i in x['indexes']:groups[i['table']]+=i['entry_delta']
lines+=['Measured47new ordinary secondary-index records: '+', '.join(f'{k}={v}' for k,v in groups.items())+'. Seven ordinary new rows plus library and age-count updates; compressed FTS adds segment/shadow work. Native total_changes=15is not15logical product rows. This supersedes45entries/6new rows on426. No scalar membership or genre facet rows in this minimal Basic.','', '|Largest entity index allocation at~1.19M|Bytes|','|---|---:|']
for i in sorted([i for i in x['indexes'] if i['table']=='catalog_entities'],key=lambda i:i['bytes_after'],reverse=True)[:8]:lines.append(f'|{i["name"]}|{i["bytes_after"]}|')
raw=csvout.getvalue().encode();gz=gzip.compress(raw,mtime=0);(E/'S-ALL-HOLDS-OVER25.csv.gz').write_bytes(gz)
(E/'S-ALL-HOLDS-OVER25.manifest.json').write_text(json.dumps({'rows':tailcount,'raw_sha256':hashlib.sha256(raw).hexdigest(),'compressed_sha256':hashlib.sha256(gz).hexdigest(),'columns':['all statement phases','commit'],'description':'Every hold>25ms in full09/426/dabf attributed runs, all6VMs/hash including checkpoint-paused control.'},indent=2)+'\n')
lines+=['',f'All{tailcount}holds over25ms, including every statement phase and COMMIT, are in S-ALL-HOLDS-OVER25.csv.gz with raw/compressed SHA manifest. Raw per-sample phase timestamps are also in each run artifact.','']
(E/'S-WRITER-PHASES.md').write_text('\n'.join(lines))
# CAPS focused scope
lines=['# CAPS-PRODUCT: shared asset size contributions','', 'Exactdabf442e6aab647b693e59ebc509f703050e742f96eb5aac344e4e39ea45224f; run41920207301;3VMs/profile. One owner+255CUE children, all255in the SAME100authored collections. Canonical inline control: one complete update/VM, not a measured p99. Candidate:101transactions/VM. NORMAL/PASSIVE, FKson. Asset/child+parent entity keys, source header validation, library revision/event/receipt and COMMIT charged.','', '|Profile|Canonical inline single-hold spread ms|Canonical rows changed|Candidate p99 spread ms|Candidate max spread ms|Candidate rows changed|','|---|---:|---:|---:|---:|---:|']
for profile in ['standard','legacy8']:
 z=[load(E/f'caps-dabf-{profile}-rep{n}'/'caps-product.json') for n in [1,2,3]]
 lines.append(f'|{profile}|{spread([v["baseline_single_inline"]["max_ms"] for v in z])}|25,860|{spread([v["candidate"]["p99_ms"] for v in z])}|{spread([v["candidate"]["max_ms"] for v in z])}|460|')
lines+=['', 'The narrow candidate relation stores size once per(collection,asset), with(parent,sizeDESC,asset)extreme and(asset,parent)reverse indexes and same-library FKs. It collapses25,500size contributions to100. Other per-child scalar metrics remain per-child. This candidate fits observed10/25ms on all6VMs; INT/lead accepts CAPS-PRODUCT(c)as closed. Exact overlay and hash are retained in each receipt.','', 'Conditions: collections must share the asset for this collapse;255children with disjoint collection sets do not collapse. Candidate does not advance25,500otherwise-unchanged generic contributor_revision witnesses; INTmust define field-scoped/source-size revisions, or a broad revision rewrite would erase the gain. Focused collection case does not charge album/artist ancestor publication. This is an explicit candidate overlay, not silent canonical DDL or full typed acceptance proof. Per lead, no inline-budget sweep, chunking or local control was run.','']
(E/'S-CAPS-PRODUCT.md').write_text('\n'.join(lines))
print('published',tailcount,'phase tails')
