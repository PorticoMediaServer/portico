from latest_report import E,load,spread
import json,math,sys
PREFIX=sys.argv[1] if len(sys.argv)>1 else "facets-dabf"
OUTPUT=sys.argv[2] if len(sys.argv)>2 else "S-RESTRICTED-LATEST.md"
RUN=sys.argv[3] if len(sys.argv)>3 else "41919049298"
lines=['# Restricted age fast path and exact label fallback','', 'Canonicaldabf442e6aab647b693e59ebc509f703050e742f96eb5aac344e4e39ea45224f, run'+RUN+'. BundledSQLite3.53.4; Basic movie-only fixture, not the complete mixed-kind/HTTP authority route.500age-only samples/case; label direct/browse21samples/case; query-local bounded preparation3trials. Each fixture has3genres/item and5overlapping label/tag values with frequencies1/11,1/23,1/47,1/97,1/193. Source profile exclusions use field2value_key; member exclusions fields2/3lower(source_value). Lists1/3/5include both label/tag strings. Age0/8are sparse0.2%combined; about4.9%unknown; remaining ages13/17/18. Both BlockUnrated states and age ceilings8/17. Containers measured separately as propagation control.','', 'Source diagnosis: the old restricted genre aggregation traverses candidate items and genre edges and evaluates eligibility per item, then groups. With container membership it may repeat descendant checks. A count per term without eligibility cannot answer restrictions. Fixed global age-class counters replace that scan for age-only policy. Arbitrary excluded-label intersections still require exact source evaluation; no per-user/profile totals or stored permitted IDs are built.','']
for size in [400000,1000000]:
 for profile in ['standard','legacy8']:
  d=E/f'{PREFIX}-{profile}-{size}';f=d/'band-facet-results.json'
  if not f.exists() and not f.with_suffix('.json.gz').exists():lines += [f'{size}/{profile}: pending completed receipt.',''];continue
  v=load(f);rs=v['results'];lines += [f'## {size} /{profile}','','|Primitive|Maximum case p95 ms|Refused attempts|','|---|---:|---:|']
  for name,tier in [('facet',None),('browse','exact-canonical'),('browse','canonical-plus16candidate-prefixes'),('label-browse',None),('label-facet',None)]:
   group=[x for x in rs if x['case']==name and (tier is None or x.get('schema_tier')==tier)];ready=[];refused=0
   for x in group:
    r=x.get('candidate',x.get('result'));refused+=r['refused']
    if r['completed']:ready.append(r['p95_ms'])
   lines.append(f'|{name} {tier or ""}|{max(ready,default=0):.3f}|{refused}|')
  lines += ['','|Policy /labels /age ceiling /BlockUnrated|Direct facet p95 ms (completed only)|Direct refusals /21|Bounded exact cold max ms /completed of3|Step max ms|','|---|---:|---:|---|---:|']
  for x in [x for x in rs if x['case']=='label-facet']:
   match=next(y for y in rs if y['case']=='label-facet-quantized' and all(y[k]==x[k] for k in ['scope','labels','ceiling','mode']));r=x['result'];runs=match['runs'];good=[z['completion_ms'] for z in runs if not z['error']]
   cold=f'{max(good):.1f} /{len(good)}' if good else 'refused /0'
   lines.append(f'|{x["scope"]} /{x["labels"].count(",")+1} /{x["ceiling"]} /{x["mode"]}|{r.get("p95_ms",0):.3f}|{r["refused"]}|{cold}|{max(z.get("quantum_max_ms",0) for z in runs):.3f}|')
  alloc=v['allocation'];extra=sum(a[1] for a in alloc if a[0].startswith('restricted_'));canonical=sum(a[1] for a in alloc if not a[0].startswith('restricted_'))
  age=load(d/'age-propagation.json')
  lines += ['',f'Allocation:16extra prefix indexes{extra:,}bytes ({extra/size:.2f}B/item); canonical4age-prefix+counter/index objects{canonical:,}bytes. Counter-before-index total DB allocation{v["counter_before_index_bytes"]:,}bytes. These are dbstat allocated pages, not file-size subtraction or schema heap.',f'Random single-item class change with both mode item/facet deltas and all20age indexes: p99{v["write_p99_ms"]:.3f}ms/max{v["write_max_ms"]:.3f}ms;501samples. Child-min propagation to season/show/one authored collection: p99{age["p99_ms"]:.3f}ms/max{age["max_ms"]:.3f}ms;101samples, source revisions/library receipt/COMMIT included. Full row-effect arrays retained.','']
lines += ['Age-only facet and index-served browse SQL must fit the150ms ready browse budget. A completed/refused exact-label facet is not an approximate count. Direct labels use100ms physical statement deadlines; failed attempts are excluded from ready percentiles and counted explicitly. The128raw-item keyset fallback aggregates only query-local30genre counters in one source snapshot, each step100ms and cold total10s. It returns no partial counts on refusal. Multi-second cold preparation is a ready-budget miss even when every physical step is bounded; this is the accepted slower unusual-policy path, not a ready pass. Three cold trials do not estimate a robust p95; maxima are reported.','', 'The canonical4age indexes cover title/added for both global BlockUnrated frontiers.16extra hot-sort indexes are a hashed explicit candidate overlay. Parent propagation uses canonical admission_age nonNULL extrema index and flattened leaf membership per ancestor, while own container age21is ignored; empty frontiers=-1, unknown-only nonempty allow=-1/knownNULL. No per-profile maintenance fanout. V still must prove all actual source membership/availability/empty/unknown/blockset combinations.','']
(E/OUTPUT).write_text('\n'.join(lines))
