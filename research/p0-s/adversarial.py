#!/usr/bin/env python3
"""Append-unit sweep and uncapped scalar reverse-fanout controls on core scalar DDL."""
import argparse,json,os,pathlib,time
from bench import DB,create,seed,scalar,atom,summarize

def timed(db,change,compute=''):
 wait=time.perf_counter();db.exec('BEGIN IMMEDIATE');beg=time.perf_counter();before=scalar(db,'SELECT total_changes()')
 db.exec(change)
 stats={}
 if compute:
  _,stats=db.query(compute,120)
 db.exec('COMMIT');end=time.perf_counter()
 return {'ms':(end-beg)*1000,'admission_ms':(beg-wait)*1000,'changes':scalar(db,'SELECT total_changes()')-before,'rc':101,'vm':stats.get('vm',0),'fullscan_steps':stats.get('fullscan_steps',0),'sorts':stats.get('sorts',0)}

def append_unit(db,start,count):
 end=start+count-1
 n=f'WITH RECURSIVE n(id) AS(VALUES({start}) UNION ALL SELECT id+1 FROM n WHERE id<{end}) '
 # Parent references exist before acceptance. 18% movies, 46% episodes,
 # 29% songs, 7% audio-book files; all are new playable leaf entities.
 expr="CASE WHEN id%100<18 THEN 1 WHEN id%100<64 THEN 4 WHEN id%100<93 THEN 7 ELSE 10 END"
 parent="CASE WHEN id%100<18 THEN NULL WHEN id%100<64 THEN 161 WHEN id%100<93 THEN 611 ELSE 895 END"
 sql=n+f"""INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_key,sort_key_keep_articles,random_key,year,added_at,last_content_added_at,release_key,latest_aired_key,duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,episode_count,track_key,content_rating)
 SELECT id,CAST(printf('%016d',id) AS BLOB),1,{expr},{parent},printf('Word %08d',id),printf('word %08d',id),printf('word %08d',id),(id*2654435761)%4294967296,2000+id%26,printf('2026-11-04T00:00:00.%09dZ',id),NULL,printf('%04d-01-01',2000+id%26),'',60+id%10000,CAST(id%101 AS REAL)/10,id%101,1000000+id*97,1080,0,id%25,'G' FROM n;
 INSERT INTO catalog_assets SELECT id,printf('asset-%d',id),printf('/sweep/%d',id),file_size_key,1,'mkv','h264','aac',1920,1080,duration_key,1 FROM catalog_entities WHERE id BETWEEN {start} AND {end};
 INSERT INTO catalog_asset_links SELECT id,id,0,1 FROM catalog_entities WHERE id BETWEEN {start} AND {end};
 INSERT INTO catalog_entity_terms SELECT e.id,1+((e.id*7+v.column1)%30) FROM catalog_entities e CROSS JOIN (VALUES(0),(1),(2)) v WHERE e.id BETWEEN {start} AND {end};
 INSERT INTO catalog_credits SELECT e.id,v.column1,1+((e.id*13+v.column1*17)%2000),1 FROM catalog_entities e CROSS JOIN (VALUES(0),(1),(2),(3),(4),(5)) v WHERE e.id BETWEEN {start} AND {end};
 INSERT INTO personal_items SELECT 'viewer',id,id%11,printf('2026-10-%02d',1+id%28),id%19,1 FROM catalog_entities WHERE id BETWEEN {start} AND {end} AND id%10=0;
 INSERT INTO catalog_collection_members SELECT 975,id,printf('%010d',id) FROM catalog_entities WHERE id BETWEEN {start} AND {end};
 INSERT INTO search_own SELECT id,library_id,kind,sort_key FROM catalog_entities WHERE id BETWEEN {start} AND {end};
 INSERT INTO search_own_prefix(rowid,title_fold) SELECT entity_id,title_fold FROM search_own WHERE entity_id BETWEEN {start} AND {end};"""
 # Coalesce append extrema to bounded known ancestors. No rereading old members.
 for pid,kinds in [(161,'4'),(151,'4'),(611,'7'),(601,'7'),(895,'10'),(891,'10'),(975,'1,4,7,10')]:
  sql+=f"""UPDATE catalog_entities SET duration_key=max(duration_key,(SELECT COALESCE(max(duration_key),0) FROM catalog_entities WHERE id BETWEEN {start} AND {end} AND kind IN({kinds}))),file_size_key=max(file_size_key,(SELECT COALESCE(max(file_size_key),0) FROM catalog_entities WHERE id BETWEEN {start} AND {end} AND kind IN({kinds}))),episode_count=episode_count+(SELECT count(*) FROM catalog_entities WHERE id BETWEEN {start} AND {end} AND kind=4),revision=revision+1 WHERE id={pid};"""
 sql+='UPDATE library_revisions SET revision=revision+1 WHERE library_id=1;'
 return timed(db,sql)

def sweep(out,total):
 allrows=[]
 for size in [16,32,64,128,256,1000]:
  path=out/f'sweep-{size}.db';db=DB(path);create(db);seed(db,1,1000);xs=[];start=1001;t=time.perf_counter()
  while start<1001+total:
   xs.append(append_unit(db,start,min(size,1001+total-start)));start+=size
  elapsed=time.perf_counter()-t;summary=summarize(xs);summary.update(batch=size,entities=total,elapsed_s=elapsed,entities_per_s=total/elapsed,strict=summary['p99_ms']<=10 and max(x['ms'] for x in xs)<=25,max50=max(x['ms'] for x in xs)<=50,max_ms=max(x['ms'] for x in xs))
  atom(out/f'sweep-{size}.json',{'summary':summary,'samples':xs});print('sweep',summary,flush=True);allrows.append(summary);db.close()
  for suffix in ['', '-wal','-shm']:pathlib.Path(str(path)+suffix).unlink(missing_ok=True)
 atom(out/'sweep-summary.json',allrows)

def populate_members(db,n):
 # Core physical entity row/index cost, not complete ingest seeding.
 lo=2_000_001;hi=2_000_000+n
 db.exec(f"""BEGIN; WITH RECURSIVE n(id) AS(VALUES({lo}) UNION ALL SELECT id+1 FROM n WHERE id<{hi}) INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_key,sort_key_keep_articles,random_key,year,added_at,last_content_added_at,release_key,latest_aired_key,duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,episode_count,track_key,content_rating)
 SELECT id,CAST(printf('%016d',id) AS BLOB),1,1,NULL,printf('Member %d',id),printf('member %d',id),printf('member %d',id),id,2000,'2026-11-04T00:00:00.000000000Z',NULL,printf('%010d',id),'',id,id%101,id%101,id,1080,0,0,'G' FROM n;
 INSERT INTO catalog_collection_members SELECT 975,id,printf('%010d',id) FROM catalog_entities WHERE id BETWEEN {lo} AND {hi}; COMMIT;""")
 return lo,hi

def fanout(out):
 results=[]
 for n in [10000,100000,1000000]:
  p=out/'fanout.db';db=DB(p);create(db);seed(db,1,1000)
  db.exec('DELETE FROM catalog_collection_members WHERE collection_id=975');lo,hi=populate_members(db,n+1)
  db.exec(f'DELETE FROM catalog_collection_members WHERE item_id={hi}')
  expr="UPDATE catalog_entities SET (duration_key,file_size_key,community_rating_key,critic_rating_key,latest_aired_key,episode_count)=(SELECT COALESCE(max(e.duration_key),0),COALESCE(max(e.file_size_key),0),COALESCE(max(e.community_rating_key),0),COALESCE(max(e.critic_rating_key),0),COALESCE(max(e.release_key),''),count(*) FROM catalog_collection_members m CROSS JOIN catalog_entities e ON e.id=m.item_id WHERE m.collection_id=975),revision=revision+1 WHERE id=975 RETURNING id"
  for case in ['add-delta','add-rescan','update-member','delete-member','delete-extreme']:
   xs=[]
   for rep in range(5):
    if case.startswith('add'):
     change=f"INSERT INTO catalog_collection_members VALUES(975,{hi},'{hi}');"
     compute=expr
     if case=='add-delta':compute=f'UPDATE catalog_entities SET duration_key=max(duration_key,{hi}),file_size_key=max(file_size_key,{hi}),revision=revision+1 WHERE id=975 RETURNING id'
     rec=timed(db,change,compute);db.exec(f'DELETE FROM catalog_collection_members WHERE item_id={hi}')
    elif case=='update-member':
     rec=timed(db,f'UPDATE catalog_entities SET duration_key={hi+rep+1} WHERE id={lo}',expr)
     db.exec(f'UPDATE catalog_entities SET duration_key=id WHERE id={lo}')
    else:
     victim=hi-1 if case=='delete-extreme' else lo+100
     rec=timed(db,f'DELETE FROM catalog_collection_members WHERE item_id={victim}',expr)
     db.exec(f"INSERT INTO catalog_collection_members VALUES(975,{victim},'{victim}')")
    rec['rows_read_lower_bound']=0 if case=='add-delta' else n-1;xs.append(rec)
   summary=summarize(xs);summary.update(case=case,members=n,max_ms=max(x['ms'] for x in xs),max_changes=max(x['changes'] for x in xs),strict=summary['p99_ms']<=10 and max(x['ms'] for x in xs)<=25,max50=max(x['ms'] for x in xs)<=50)
   results.append({'summary':summary,'samples':xs});atom(out/'collection-results.json',results);print('collection',summary,flush=True)
  db.close()
  for suffix in ['', '-wal','-shm']:pathlib.Path(str(p)+suffix).unlink(missing_ok=True)
 shared=[]
 for n in [10000,100000]:
  p=out/'shared.db';db=DB(p);create(db);seed(db,1,1000);lo,hi=populate_members(db,n)
  db.exec(f"INSERT INTO catalog_assets VALUES(10000000,'shared','/shared',1,1,'mkv','h264','aac',1920,1080,123,1); INSERT INTO catalog_asset_links SELECT id,10000000,0,1 FROM catalog_entities WHERE id BETWEEN {lo} AND {hi};")
  for case in ['asset-change','asset-delete']:
   xs=[]
   for rep in range(5):
    if case=='asset-change':
     change='UPDATE catalog_assets SET duration=456 WHERE id=10000000;'
     compute='UPDATE catalog_entities SET duration_key=(SELECT COALESCE(max(a.duration),0) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=catalog_entities.id AND l.available=1 AND a.available=1),revision=revision+1 WHERE id IN(SELECT entity_id FROM catalog_asset_links WHERE asset_id=10000000) RETURNING id'
    else:
     change='DELETE FROM catalog_asset_links WHERE asset_id=10000000; DELETE FROM catalog_assets WHERE id=10000000;'
     compute=f'UPDATE catalog_entities SET duration_key=0,revision=revision+1 WHERE id BETWEEN {lo} AND {hi} RETURNING id'
    rec=timed(db,change,compute);rec['rows_read_lower_bound']=n;xs.append(rec)
    if case=='asset-delete':db.exec(f"INSERT INTO catalog_assets VALUES(10000000,'shared','/shared',1,1,'mkv','h264','aac',1920,1080,123,1); INSERT INTO catalog_asset_links SELECT id,10000000,0,1 FROM catalog_entities WHERE id BETWEEN {lo} AND {hi};")
   summary=summarize(xs);summary.update(case=case,parents=n,max_ms=max(x['ms'] for x in xs),max_changes=max(x['changes'] for x in xs),strict=summary['p99_ms']<=10 and max(x['ms'] for x in xs)<=25,max50=max(x['ms'] for x in xs)<=50)
   shared.append({'summary':summary,'samples':xs});atom(out/'shared-results.json',shared);print('shared',summary,flush=True)
  # A chunked point-update witness demonstrates the publication problem.
  db.exec(f'BEGIN; UPDATE catalog_assets SET duration=789 WHERE id=10000000; UPDATE catalog_entities SET duration_key=789 WHERE id={lo}; COMMIT;')
  witness=db.query(f'SELECT a.duration,e.duration_key FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.id={hi}')[0]
  atom(out/f'chunk-consistency-{n}.json',{'after_first_chunk_unchanged_parent':witness,'expected_equal':False,'conclusion':'source changed; unprocessed parent scalar stale after first committed chunk'})
  db.close()
  for suffix in ['', '-wal','-shm']:pathlib.Path(str(p)+suffix).unlink(missing_ok=True)

if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('--out',required=True);ap.add_argument('--mode',choices=['sweep','fanout'],required=True);ap.add_argument('--total',type=int,default=128000);args=ap.parse_args();out=pathlib.Path(args.out);out.mkdir(parents=True,exist_ok=True)
 if not os.getenv('GITHUB_RUN_ID') and (args.mode=='fanout' or args.total>5000):raise SystemExit('hosted-only adversarial scale run')
 if args.mode=='sweep':sweep(out,args.total)
 else:fanout(out)
