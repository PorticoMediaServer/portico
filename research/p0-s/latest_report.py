from pathlib import Path
import json,gzip,collections
ROOT=Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0');E=ROOT/'S-evidence'
def load(p):
 if p.exists():return json.loads(p.read_text())
 return json.loads(gzip.decompress(p.with_suffix(p.suffix+'.gz').read_bytes()))
def spread(v,places=3):return f'{min(v):.{places}f}–{max(v):.{places}f}'
lines=['# Latest full-schema receipts','', 'Exact bundled SQLite3.53.4, physical schema-dependent opens including PRAGMAs and first40-row read;3fresh processes ×100opens per profile. Memory0/1/8/16/32physical connections with statement cache empty, fixed16MiB pager configuration,40row warm workload. No complete production statement cache or other-domain command state. These are measured primitives, not HTTP/product qualification.','', '|SHA /run|Profile|Native schema MiB/connection|Open p50 ms spread|Open p95 ms spread|RSS MiB/connection|','|---|---|---:|---:|---:|---:|']
for sha,run in [('426','41917663083'),('dabf','41919045287')]:
 for profile in ['standard','legacy8']:
  d=E/f'full-{sha}-{profile}';man=load(d/'full-manifest.json');m=load(d/'bundled-memory.json')['pools'];opens=[load(f) for f in d.glob('bundled-opens-*.json')]
  lines.append(f'|{man["ddl_sha256"]} /{run}|{profile}|{m[-1]["native"][0]["schema"]/1048576:.6f}|{spread([x["p50_ms"] for x in opens])}|{spread([x["p95_ms"] for x in opens])}|{(m[-1]["rss_bytes"]-m[0]["rss_bytes"])/32/1048576:.3f}|')
lines+=['', '**Gate decisions:** exact-schema native heap/open/RSS measurements are closed for bothSHAs/profiles. Median loaded schema misses2MiB (schema alone); p95schema remains within3MiB. RSS slope fits6MiB under the stated idle workload. On dabf44, fresh usable15/25ms p50/p95:standard passes;legacy p50misses15ms,p95passes25ms. Additional warm physical-open10/20ms:p50misses both,p95passes both. No causal profile comparison from one VM each. Native schema delta426→dabf is+2,155bytes/connection; open timing variants ran on different VMs, so not a paired latency delta. Full typed command cache and broader RSS remain OPEN.','']
(E/'S-FULL-LATEST.md').write_text('\n'.join(lines))
lines=['# NORMAL writer sweep: exact42605a76','', 'Run41917658664, SHA42605a7641e8e99bb359739fae6a380e1306665b72a46e1d7c8341dc747a5411. Complete standard3VMs, legacy2VMs (rep2failed without receipts); missing repetition is not a pass.128transactions/size. NORMAL/PASSIVE, exact subject+age count deltas charged. No phase timers in this run; see separate attributed run.','', '|Profile /availableVMs|Batch|p50 ms|p99 ms|max ms|items/s|','|---|---:|---:|---:|---:|---:|']
for profile in ['standard','legacy8']:
 dirs=sorted(E.glob(f'normal-426-{profile}-rep*'));rr=[load(d/'full-write-sweep.json') for d in dirs]
 for size in [16,32,64,128,256,1000]:
  rs=[next(x for x in r if x['batch']==size) for r in rr];lines.append(f'|{profile} /{len(rs)}|{size}|'+ '|'.join(spread([x[k] for x in rs],2) for k in ['p50_ms','p99_ms','max_ms','entities_per_s'])+'|')
lines+=['','No omitted/failed VM is used to infer consistent compliance. New dabf44 attributed batch1–1k run41919041482 will supersede this for the identity-key recipe.','']
(E/'S-NORMAL-426.md').write_text('\n'.join(lines))
# Full source-frame cases historical09, finalcaps and stress distinguished.
lines=['# Source-frame replacement: finalcaps, historical09','', 'Run41914343163, SHA09a97242b681fe51077bb109fe2c1d28a0eb2cdd7fccfed0a661c3d9115782b6.3VMs/profile. NORMAL/PASSIVE, four reusable typed prepared insert statements, sealed row arguments before writer acquisition; source header checks/revisions/receipt/COMMIT included. Current live bounded sealed-set materialization and later naming are separate commits. This attempted one-commit target is not an assertion about today. Later canonicalDDL requires another qualification; these are final512chapter/16version controls and labeled1024/32stress.','', '|Profile|Case|Child effects|p99 hold ms spread|max hold ms spread|','|---|---|---:|---:|---:|']
for profile in ['standard','legacy8']:
 rr=[load(E/f'frames-09-{profile}-rep{n}'/'source-frame-results.json')['results'] for n in [1,2,3]]
 for i,r in enumerate(rr[0]):
  z=[v[i] for v in rr];lines.append(f'|{profile}|{r["case"]}|{r["expected_child_mutations"]}|{spread([x["p99_ms"] for x in z])}|{spread([x["max_ms"] for x in z])}|')
(E/'S-FRAMES.md').write_text('\n'.join(lines)+'\n')
