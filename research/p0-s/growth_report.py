from pathlib import Path
import json,gzip,re,hashlib,shutil
E=Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0/S-evidence')
lines=['# Pure growing first-scan control: partial evidence','', 'Run41903990946, source3f1a466ad3f68f34856d4fa45f61d88e3cbaf810; reduced native schema/systemSQLite3.45.1.100k initial residents per profile. Continuous runnable writer appends1000items/batch with no sleeps or evictions, up to the disclosed2Mnew-item safety cap. Actual resident count grows throughout the window. This is not latest full-schema or a fixed100k corpus.400k/1M initial-size jobs canceled; no pass inferred.','', '|Profile|Total read cohorts|Proven positive committed overlap|Idle cohorts after/no overlap|Initial→maximum residents|','|---|---:|---:|---:|---:|']
manifest={}
for profile in ['standard','legacy8']:
 d=E/f'growth-100000-{profile}';src=d/'queries-growth.jsonl';reader=src.open() if src.exists() else gzip.open(src.with_suffix('.jsonl.gz'),'rt');counts={'during-ingest':0,'idle':0};maxAccepted=0;n=0;receipts=[]
 with reader:
  for line in reader:
   overlap,_=json.JSONDecoder().raw_decode(line[len('{"overlap": '):])
   assert overlap['label']=='during-ingest' if overlap['committed_during_window']>0 else overlap['label']=='idle'
   counts[overlap['label']]+=1;n+=1;maxAccepted=max(maxAccepted,overlap['accepted_end']);receipts.append(overlap)
 lines.append(f'|{profile}|{n}|{counts["during-ingest"]}|{counts["idle"]}|100,000→{100000+maxAccepted:,}|')
 manifest[profile]={'run_id':'41903990946','source':'3f1a466ad3f68f34856d4fa45f61d88e3cbaf810','initial_residents':100000,'max_residents':100000+maxAccepted,'cohort_counts':counts,'receipts':receipts}
 # Stream compression, retaining raw and compressed hashes; no full artifact in RAM.
 if src.exists():
  rawhash=hashlib.sha256();target=src.with_suffix('.jsonl.gz')
  with src.open('rb') as inp,target.open('wb') as out,gzip.GzipFile(fileobj=out,mode='wb',mtime=0) as gz:
   while data:=inp.read(1048576):rawhash.update(data);gz.write(data)
  cmp=hashlib.sha256(target.read_bytes()).hexdigest();manifest[profile]['compression']={'raw_sha256':rawhash.hexdigest(),'raw_bytes':src.stat().st_size,'compressed_sha256':cmp,'compressed_bytes':target.stat().st_size};src.unlink()
lines+=['', 'Only positive-overlap cohorts are during-ingest evidence. Safety-cap-ended cohorts are explicitly idle and cannot qualify continuous initial scanning. Exact read intervals/progress witnesses are retained in S-GROWTH-RECEIPTS.json and full compressed per-query SQL/plans/samples/returned-row/hash artifacts. The growing matrix does not close the full-schema first-scan gate or the all-size continuous-read gate.','']
(E/'S-GROWTH-PARTIAL.md').write_text('\n'.join(lines));(E/'S-GROWTH-RECEIPTS.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('\n'.join(lines[:9]))
