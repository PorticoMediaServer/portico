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

def reference_seed(db):
 if not os.getenv('TARGET_ENTITY'):seed(db,1,1000);return
 rows=[(1,1,None),(151,2,None),(161,3,151),(601,5,None),(611,6,601),(891,13,None),(895,8,891),(975,10,None)]
 for eid,kind,parent in rows:
  extra={3:"151,1,NULL,NULL,NULL",6:"NULL,NULL,601,NULL,NULL"}.get(kind,"NULL,NULL,NULL,NULL,NULL")
  db.exec(f"INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,show_id,season_number,artist_id,album_id,book_id) VALUES({eid},CAST(printf('%016d',{eid}) AS BLOB),1,{kind},{parent if parent else 'NULL'},'Reference {eid}','reference {eid}','r','reference {eid}','r',{eid},zeroblob(16),zeroblob(16),zeroblob(16),1,{extra})")

def add_target_required(sql,leaf=False):
 if not os.getenv('TARGET_ENTITY'):return sql
 cols='sort_head,sort_head_keep_articles,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision'
 values="'w','w',zeroblob(16),zeroblob(16),zeroblob(16),1"
 if leaf:
  cols+=',show_id,season_id,numbering,episode_number,album_id,book_id'
  values+=",CASE WHEN id%100 BETWEEN 18 AND 63 THEN 151 END,CASE WHEN id%100 BETWEEN 18 AND 63 THEN 161 END,CASE WHEN id%100 BETWEEN 18 AND 63 THEN 'seasonal' END,CASE WHEN id%100 BETWEEN 18 AND 63 THEN id END,CASE WHEN id%100 BETWEEN 64 AND 92 THEN 611 END,CASE WHEN id%100>=93 THEN 895 END"
 # These two INSERT-SELECT controls have one final content-rating expression.
 sql=sql.replace('track_key,content_rating)','track_key,content_rating,'+cols+')',1)
 if leaf:sql=sql.replace(",id%25,'G' FROM n",",id%25,'G',"+values+' FROM n',1)
 else:sql=sql.replace(",0,0,'G' FROM n",",0,0,'G',"+values+' FROM n',1)
 return sql

def append_unit(db,start,count):
 end=start+count-1
 n=f'WITH RECURSIVE n(id) AS(VALUES({start}) UNION ALL SELECT id+1 FROM n WHERE id<{end}) '
 # Parent references exist before acceptance. 18% movies, 46% episodes,
 # 29% songs, 7% audio-book files; all are new playable leaf entities.
 expr=f"CASE WHEN id%100<18 THEN 1 WHEN id%100<64 THEN 4 WHEN id%100<93 THEN 7 ELSE {9 if os.getenv('TARGET_ENTITY') else 10} END"
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
 sql=add_target_required(sql,True)
 sql+='UPDATE library_revisions SET revision=revision+1 WHERE library_id=1;'
 return timed(db,sql)

def sweep(out,total):
 allrows=[]
 for size in [16,32,64,128,256,1000]:
  path=out/f'sweep-{size}.db';db=DB(path);create(db);reference_seed(db);xs=[];start=1001;t=time.perf_counter()
  while start<1001+total:
   xs.append(append_unit(db,start,min(size,1001+total-start)));start+=size
  elapsed=time.perf_counter()-t;summary=summarize(xs);summary.update(batch=size,entities=total,elapsed_s=elapsed,entities_per_s=total/elapsed,strict=summary['p99_ms']<=10 and max(x['ms'] for x in xs)<=25,max50=max(x['ms'] for x in xs)<=50,max_ms=max(x['ms'] for x in xs))
  atom(out/f'sweep-{size}.json',{'summary':summary,'samples':xs});print('sweep',summary,flush=True);allrows.append(summary);db.close()
  for suffix in ['', '-wal','-shm']:pathlib.Path(str(path)+suffix).unlink(missing_ok=True)
 atom(out/'sweep-summary.json',allrows)

def populate_members(db,n):
 # Core physical entity row/index cost, not complete ingest seeding.
 lo=2_000_001;hi=2_000_000+n
 sql=f"""BEGIN; WITH RECURSIVE n(id) AS(VALUES({lo}) UNION ALL SELECT id+1 FROM n WHERE id<{hi}) INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_key,sort_key_keep_articles,random_key,year,added_at,last_content_added_at,release_key,latest_aired_key,duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,episode_count,track_key,content_rating)
 SELECT id,CAST(printf('%016d',id) AS BLOB),1,1,NULL,printf('Member %d',id),printf('member %d',id),printf('member %d',id),id,2000,'2026-11-04T00:00:00.000000000Z',NULL,printf('%010d',id),'',id,id%101,id%101,id,1080,0,0,'G' FROM n;
 INSERT INTO catalog_collection_members SELECT 975,id,printf('%010d',id) FROM catalog_entities WHERE id BETWEEN {lo} AND {hi}; COMMIT;"""
 db.exec(add_target_required(sql))
 return lo,hi

def envelope_setup(db,n,lo,hi):
 for lib in [1,2,3,4]:
  db.exec(f"INSERT OR IGNORE INTO catalog_libraries(id,name) VALUES({lib},'envelope-{lib}')")
  artist=5000+lib*10;album=artist+1;show=artist+2
  for eid,kind in [(artist,5),(album,6),(show,2)]:
   extra=f',artist_id' if kind==6 else '';val=f',{artist}' if kind==6 else ''
   db.exec(f"INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision{extra}) VALUES({eid},CAST(printf('%016d',{eid}) AS BLOB),{lib},{kind},'Envelope parent','envelope','e','envelope','e',{eid},zeroblob(16),zeroblob(16),zeroblob(16),1{val})")
  if n==1024 or (n==777 and lib<=3):
   first=lo+(lib-1)*256;last=first+255
   db.exec(f'UPDATE catalog_entities SET library_id={lib},kind=7,parent_id={album},album_id={album} WHERE id BETWEEN {first} AND {last}')
  else:
   first=lo+((lib-1)*9 if n==36 else 768);last=first+8
   db.exec(f'UPDATE catalog_entities SET library_id={lib} WHERE id={first}')
   db.exec(f"UPDATE catalog_entities SET library_id={lib},kind=4,parent_id={show},show_id={show},season_id=NULL,numbering='seasonal',episode_number=id WHERE id BETWEEN {first+1} AND {last}")
 db.exec('PRAGMA foreign_key_check')

def fanout(out):
 results=[]
 for n in ([] if os.getenv("SUPPORTED") else [10000,100000,1000000]):
  p=out/'fanout.db';db=DB(p);create(db);reference_seed(db)
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
 for n in (([9,256,4097] if os.getenv("CUE_RANGE") else [36,777,1024]) if os.getenv("ENVELOPE") else [8,255,264,4096] if os.getenv("SUPPORTED") else [10000,100000]):
  p=out/'shared.db';db=DB(p);create(db);reference_seed(db);lo,hi=populate_members(db,n)
  db.exec(f"INSERT INTO catalog_assets VALUES(10000000,'shared','/shared',1,1,'mkv','h264','aac',1920,1080,123,1); INSERT INTO catalog_asset_links SELECT id,10000000,0,1 FROM catalog_entities WHERE id BETWEEN {lo} AND {hi};")
  if os.getenv('CUE_RANGE'):
   if n in [256,4097]:db.exec(f'UPDATE catalog_entities SET kind=7,parent_id=611,album_id=611 WHERE id BETWEEN {lo} AND {hi}')
   else:db.exec(f"UPDATE catalog_entities SET kind=4,parent_id=161,show_id=151,season_id=161,numbering='seasonal',episode_number=id WHERE id BETWEEN {lo+1} AND {hi}")
  elif os.getenv('ENVELOPE') and n in [36,777,1024]:
   envelope_setup(db,n,lo,hi)
  elif os.getenv('SUPPORTED'):
   # True kind/parent relationships on exact T1 entity DDL.
   if n>=255:db.exec(f'UPDATE catalog_entities SET kind=7,parent_id=611,album_id=611 WHERE id BETWEEN {lo} AND {lo+(4096 if n==4096 else 255)-1}')
   if n==8:db.exec(f"UPDATE catalog_entities SET kind=4,parent_id=161,show_id=151,season_id=161,numbering='seasonal',episode_number=id WHERE id BETWEEN {lo} AND {hi}")
   if n==264:
    db.exec("INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision) VALUES(152,CAST(printf('%016d',152) AS BLOB),2,2,'Cross-library show','cross','c','cross','c',152,zeroblob(16),zeroblob(16),zeroblob(16),1)")
    db.exec(f"UPDATE catalog_entities SET kind=4,parent_id=161,show_id=151,season_id=161,numbering='seasonal',episode_number=id WHERE id BETWEEN {lo+255} AND {hi-1}")
    db.exec(f"UPDATE catalog_entities SET library_id=2,kind=4,parent_id=152,show_id=152,season_id=NULL,numbering='seasonal',episode_number=id WHERE id>{hi-1}")
  if os.getenv('CUE_RANGE'):db.exec("CREATE TABLE admin_receipts(scope TEXT NOT NULL,operation_id TEXT NOT NULL,digest TEXT NOT NULL,result TEXT NOT NULL,created_ms INTEGER NOT NULL,PRIMARY KEY(scope,operation_id)) STRICT")
  cue=(f'((id-{lo})%256)>0' if n==1024 else f'id<{lo+768} AND ((id-{lo})%256)>0' if n==777 else '0' if n in [8,36] else 'kind=7')
  if os.getenv('CUE_RANGE'):cue=f'id>{lo}' if n in [256,4097] else '0'
  affected=(f'id={hi}' if n in [255,4096] else f'((id-{lo})%256) IN(0,255)' if n==1024 else f'(id<{lo+768} AND ((id-{lo})%256) IN(0,255)) OR id>={lo+768}' if n==777 else '1')
  if os.getenv('CUE_RANGE'):affected=f'id IN({lo},{hi})' if n in [256,4097] else '1'
  for case in ((['asset-change','asset-delete','cue-size-resolution-change','cue-size-resolution-delete','cue-range-change','cue-range-delete'] if os.getenv('CUE_RANGE') else ['asset-change','asset-delete','cue-size-resolution-change','cue-size-resolution-delete']) if os.getenv('SUPPORTED') else ['asset-change','asset-delete']):
   xs=[]
   for rep in range(101 if os.getenv('SUPPORTED') else 5):
    if os.getenv('CUE_RANGE'):
     base_duration='123' if case in ['asset-change','asset-delete'] else f'CASE WHEN ({cue}) THEN CASE WHEN ({affected}) THEN 3 ELSE 180 END ELSE 123 END'
     db.exec(f'BEGIN;UPDATE catalog_assets SET duration=123,size=1,height=1080 WHERE id=10000000;UPDATE catalog_entities SET duration_key={base_duration},file_size_key=1,resolution_key=1080 WHERE id BETWEEN {lo} AND {hi};COMMIT;')
    if case in ['asset-change','cue-size-resolution-change']:
     change='UPDATE catalog_assets SET duration=456 WHERE id=10000000;'
     compute='UPDATE catalog_entities SET duration_key=(SELECT COALESCE(max(a.duration),0) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=catalog_entities.id AND l.available=1 AND a.available=1),revision=revision+1 WHERE id IN(SELECT entity_id FROM catalog_asset_links WHERE asset_id=10000000) RETURNING id'
    else:
     change='DELETE FROM catalog_asset_links WHERE asset_id=10000000; DELETE FROM catalog_assets WHERE id=10000000;'
     compute=f'UPDATE catalog_entities SET duration_key=0,revision=revision+1 WHERE id BETWEEN {lo} AND {hi} RETURNING id'
    if os.getenv('SUPPORTED') and not case.startswith('cue-size-resolution'):
     if case=='asset-change':
      change='UPDATE catalog_assets SET duration=456,size=999999,height=2160 WHERE id=10000000;'
      compute='UPDATE catalog_entities SET duration_key=456,file_size_key=999999,resolution_key=2160,revision=revision+1 WHERE id IN(SELECT entity_id FROM catalog_asset_links WHERE asset_id=10000000) RETURNING id'
     else:compute=f'UPDATE catalog_entities SET duration_key=0,file_size_key=0,resolution_key=0,revision=revision+1 WHERE id BETWEEN {lo} AND {hi} RETURNING id'
    if case.startswith('cue-size-resolution'):
     newsize,newres,newdur=(999999,2160,456) if case.endswith('change') else (0,0,0)
     if case.endswith('change'):change='UPDATE catalog_assets SET duration=456,size=999999,height=2160 WHERE id=10000000;'
     change+=f'UPDATE catalog_entities SET file_size_key={newsize},resolution_key={newres},revision=revision+1 WHERE id BETWEEN {lo} AND {hi} AND ({cue});'
     compute=f'UPDATE catalog_entities SET duration_key={newdur},file_size_key={newsize},resolution_key={newres},revision=revision+1 WHERE id BETWEEN {lo} AND {hi} AND NOT ({cue}) RETURNING id'
    if case.startswith('cue-range'):
     newsize,newres,newdur=(999999,2160,336) if case.endswith('change') else (0,0,0)
     if os.getenv('CUE_RANGE'):affected=f'id IN({lo},{hi})' if n in [256,4097] else '1'
     elif n in [255,4096]:affected=f'id={hi}'
     elif n==1024:affected=f'((id-{lo})%256) IN(0,255)'
     elif n==777:affected=f'(id<{lo+768} AND ((id-{lo})%256) IN(0,255)) OR id>={lo+768}'
     else:affected='1'
     change=('UPDATE catalog_assets SET duration=456,size=999999,height=2160 WHERE id=10000000;' if case.endswith('change') else 'DELETE FROM catalog_asset_links WHERE asset_id=10000000;DELETE FROM catalog_assets WHERE id=10000000;')
     change+=f'UPDATE catalog_entities SET file_size_key={newsize},resolution_key={newres},revision=revision+1 WHERE id BETWEEN {lo} AND {hi};'
     durvalue=f'CASE WHEN ({cue}) THEN {newdur} ELSE {456 if case.endswith("change") else 0} END'
     compute=f'UPDATE catalog_entities SET duration_key={durvalue},revision=revision+1 WHERE id BETWEEN {lo} AND {hi} AND ({affected}) RETURNING id'
    if os.getenv('CUE_RANGE'):change+=f"INSERT INTO admin_receipts VALUES('asset','{n}-{case}-{rep}','{'0'*64}','applied',1);"
    rec=timed(db,change,compute);rec['rows_read_lower_bound']=n;xs.append(rec)
    if case.endswith('delete'):db.exec(f"INSERT INTO catalog_assets VALUES(10000000,'shared','/shared',1,1,'mkv','h264','aac',1920,1080,123,1); INSERT INTO catalog_asset_links SELECT id,10000000,0,1 FROM catalog_entities WHERE id BETWEEN {lo} AND {hi};")
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
