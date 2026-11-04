#!/usr/bin/env python3
"""Temporary SQL-cost research harness. No production modules imported or changed."""
import argparse, ctypes as C, ctypes.util, hashlib, json, math, multiprocessing as mp, os, pathlib, platform, random, resource, sqlite3, statistics, subprocess, sys, threading, time
ROOT=pathlib.Path(__file__).parent
LIB=C.CDLL(ctypes.util.find_library('sqlite3'))
def api(name, restype, *args):
 f=getattr(LIB,name); f.restype=restype; f.argtypes=args; return f
ptr=C.c_void_p; i=C.c_int; char=C.c_char_p
openv=api('sqlite3_open_v2',i,char,C.POINTER(ptr),i,char)
execv=api('sqlite3_exec',i,ptr,char,ptr,ptr,C.POINTER(char))
prepare=api('sqlite3_prepare_v2',i,ptr,char,i,C.POINTER(ptr),ptr)
step=api('sqlite3_step',i,ptr); finalize=api('sqlite3_finalize',i,ptr)
errmsg=api('sqlite3_errmsg',char,ptr); close=api('sqlite3_close',i,ptr)
colcount=api('sqlite3_column_count',i,ptr); coltype=api('sqlite3_column_type',i,ptr,i)
colint=api('sqlite3_column_int64',C.c_longlong,ptr,i); colfloat=api('sqlite3_column_double',C.c_double,ptr,i); coltext=api('sqlite3_column_text',char,ptr,i)
stmtstatus=api('sqlite3_stmt_status',i,ptr,i,i)
dbstatus=api('sqlite3_db_status',i,ptr,i,C.POINTER(i),C.POINTER(i),i)
Callback=C.CFUNCTYPE(i,ptr); progress=api('sqlite3_progress_handler',None,ptr,i,Callback,ptr)
class DB:
 def __init__(self,path,ro=False):
  self.db=ptr(); rc=openv(str(path).encode(),C.byref(self.db),1 if ro else 6,None)
  if rc: raise RuntimeError('open '+str(rc))
  self.exec('PRAGMA busy_timeout=1000; PRAGMA cache_size=-16384; PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL; PRAGMA temp_store=MEMORY;')
  if ro:self.exec('PRAGMA query_only=ON;')
  self.deadline=0; self.cb=Callback(lambda _:int(self.deadline!=0 and time.monotonic()>self.deadline))
  progress(self.db,10000,self.cb,None)
 def exec(self,s):
  rc=execv(self.db,s.encode(),None,None,None)
  if rc:raise RuntimeError(f'{errmsg(self.db).decode()}: {s[:200]}')
 def query(self,s,timeout=12):
  st=ptr(); t=time.perf_counter(); self.deadline=time.monotonic()+timeout
  rc=prepare(self.db,s.encode(),-1,C.byref(st),None)
  if rc:raise RuntimeError(f'{errmsg(self.db).decode()}: {s}')
  rows=[]
  try:
   while True:
    rc=step(st)
    if rc!=100:break
    row=[]
    for c in range(colcount(st)):
     typ=coltype(st,c)
     if typ==1:v=colint(st,c)
     elif typ==2:v=colfloat(st,c)
     elif typ==5:v=None
     else:v=(coltext(st,c) or b'').decode()
     row.append(v)
    rows.append(row)
   ms=(time.perf_counter()-t)*1000
   stats={'ms':ms,'vm':stmtstatus(st,4,0),'fullscan_steps':stmtstatus(st,1,0),'sorts':stmtstatus(st,2,0),'rows':len(rows),'rc':rc}
   if rc not in (101,9):raise RuntimeError(errmsg(self.db).decode())
   return rows,stats
  finally:self.deadline=0;finalize(st)
 def mem(self):
  out={}
  for n,op in [('schema',2),('statement',3),('pager',1)]:
   cur=i();hi=i();dbstatus(self.db,op,C.byref(cur),C.byref(hi),0);out[n]=cur.value
  return out
 def close(self):close(self.db)
def scalar(db,s):return db.query(s,120)[0][0][0]
def atom(path,obj):
 p=pathlib.Path(path);p.parent.mkdir(parents=True,exist_ok=True);tmp=p.with_suffix('.tmp');tmp.write_text(json.dumps(obj,indent=2));tmp.replace(p)
def percentile(xs,q):return sorted(xs)[max(0,math.ceil(len(xs)*q)-1)] if xs else None
def summarize(xs):
 ok=[x for x in xs if x['rc']==101];ms=[x['ms'] for x in ok]
 return dict(n=len(xs),completed=len(ok),p50_ms=percentile(ms,.5),p95_ms=percentile(ms,.95),p99_ms=percentile(ms,.99),max_vm=max((x.get('vm',0) for x in xs),default=0),max_fullscan=max((x.get('fullscan_steps',0) for x in xs),default=0),max_sorts=max((x.get('sorts',0) for x in xs),default=0))
KINDS=['movie','show','season','episode','artist','album','song','author','book','audiobook_file','collection','ebook','comic']
COMMON=KINDS[:11];PLAYED=[x for x in COMMON if x!='author'];ASSET=[x for x in PLAYED]
SORTS={
 'title':(KINDS,'e.sort_key'), 'added':(KINDS,'e.added_at'), 'year':([x for x in KINDS if x not in ['artist','author','collection']],'e.year'),
 'duration':(ASSET,'e.duration_key'), 'communityRating':(COMMON,'e.community_rating_key'), 'personalRating':(PLAYED,'personal_rating'),
 'forYou':(['movie','show','album','book'],'recommendation'), 'aired':(['episode'],'e.release_key'), 'latestAired':(['show'],'e.latest_aired_key'),
 'lastPlayed':(PLAYED,'last_played'), 'releaseDate':(['movie','episode','show','season','album','book'],'e.release_key'),
 'criticRating':(['movie','episode','show','season','collection'],'e.critic_rating_key'), 'episodeCount':(['show','season'],'e.episode_count'),
 'album':(['song'],'album_key'), 'artist':(['song','album'],'artist_key'), 'trackNumber':(['song'],'e.track_key'),
 'fileSize':(ASSET,'e.file_size_key'), 'resolution':(['movie','episode','show','season','collection'],'e.resolution_key'),
 'random':(COMMON,'e.random_key'), 'groupOrder':(COMMON,'group_order'), 'playCount':(PLAYED,'play_count')}
CONTAINERS=['show','season','artist','album','author','book','collection']

def create(db):
 db.exec((ROOT/('schema-t1-entity.sql' if os.getenv('TARGET_ENTITY') else 'schema.sql')).read_text())

def seed(db,start,end,evict=None):
 if end>10000 and not os.getenv("GITHUB_RUN_ID"):raise RuntimeError("Local smoke hard limit: 10k IDs; scale runs require hosted run ID")
 # Each 1000 entities: 150 movies; 10 shows, 40 seasons, 400 episodes;
 # 10 artists, 30 albums, 250 songs; 4 authors, 20 books, 60 audio parts;
 # 10 collections, 10 ebooks, 6 comics. Mix sums to 1000; same per small library.
 n="WITH RECURSIVE n(id) AS (VALUES(%d) UNION ALL SELECT id+1 FROM n WHERE id<%d) "%(start,end)
 g='((id-1)/1000)*1000'; o='((id-1)%1000)'
 kind=f"CASE WHEN {o}<150 THEN 1 WHEN {o}<160 THEN 2 WHEN {o}<200 THEN 3 WHEN {o}<600 THEN 4 WHEN {o}<610 THEN 5 WHEN {o}<640 THEN 6 WHEN {o}<890 THEN 7 WHEN {o}<894 THEN 8 WHEN {o}<914 THEN 9 WHEN {o}<974 THEN 10 WHEN {o}<984 THEN 11 WHEN {o}<994 THEN 12 ELSE 13 END"
 parent=f"CASE WHEN {o} BETWEEN 160 AND 199 THEN {g}+151+({o}-160)/4 WHEN {o} BETWEEN 200 AND 599 THEN {g}+161+({o}-200)/10 WHEN {o} BETWEEN 610 AND 639 THEN {g}+601+({o}-610)/3 WHEN {o} BETWEEN 640 AND 889 THEN {g}+611+min(29,({o}-640)/9) WHEN {o} BETWEEN 894 AND 913 THEN {g}+891+({o}-894)/5 WHEN {o} BETWEEN 914 AND 973 THEN {g}+895+({o}-914)/3 END"
 # Set library split by complete 1000-entity group, leaving 2% in small library.
 db.exec('BEGIN IMMEDIATE')
 t=time.perf_counter();before=scalar(db,'SELECT total_changes()')
 if evict:
  lo,hi=evict
  db.exec(f"INSERT INTO search_own_prefix(search_own_prefix,rowid,title_fold) SELECT 'delete',entity_id,title_fold FROM search_own WHERE entity_id BETWEEN {lo} AND {hi}")
  for table,key in [('catalog_collection_members','collection_id'),('recommendation_candidates','entity_id'),('catalog_credits','entity_id'),('catalog_entity_terms','entity_id'),('personal_items','item_id'),('catalog_asset_links','entity_id'),('search_own','entity_id'),('catalog_assets','id')]:
   db.exec(f'DELETE FROM {table} WHERE {key} BETWEEN {lo} AND {hi}')
  db.exec(f'DELETE FROM catalog_entities WHERE id BETWEEN {lo} AND {hi}')
 db.exec(n+f"""INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_key,sort_key_keep_articles,random_key,year,added_at,last_content_added_at,release_key,latest_aired_key,duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,episode_count,track_key,content_rating)
 SELECT id,unhex(printf('%032x',id)),CASE WHEN ((id-1)/1000)%50=49 THEN 2 ELSE 1 END,{kind},{parent},
 printf('The Word %08d',id),printf('word %08d',id),printf('the word %08d',id),(id*2654435761)%4294967296,
 1950+(id*37)%76,printf('2020-09-%02dT00:00:00.%09dZ',1+id%28,id%1000000000),printf('2020-09-%02dT00:00:00.%09dZ',1+id%28,id%1000000000),printf('%04d-%02d-%02d',1950+id%76,1+id%12,1+id%28),printf('%04d-%02d-%02d',1950+id%76,1+id%12,1+id%28),
 60+(id*97)%10000,CAST((id*17)%101 AS REAL)/10,(id*19)%101,1000000+id*104729,CASE id%4 WHEN 0 THEN 480 WHEN 1 THEN 720 WHEN 2 THEN 1080 ELSE 2160 END,0,(id%3)*1000000+id%25,CASE id%5 WHEN 0 THEN 'G' WHEN 1 THEN 'PG' WHEN 2 THEN 'PG-13' WHEN 3 THEN 'R' ELSE 'NR' END FROM n""".replace("unhex(printf('%032x',id))","CAST(printf('%016d',id) AS BLOB)"))
 db.exec(f"INSERT INTO catalog_assets SELECT id,printf('asset-%d',id),printf('/synthetic/%d',id),file_size_key,1,'mkv','h264','aac',1920,resolution_key,duration_key,1 FROM catalog_entities WHERE id BETWEEN {start} AND {end} AND kind IN(1,4,7,10,12,13)")
 db.exec(f"INSERT INTO catalog_asset_links SELECT id,id,0,1 FROM catalog_entities WHERE id BETWEEN {start} AND {end} AND kind IN(1,4,7,10,12,13)")
 source_ms=(time.perf_counter()-t)*1000
 # Genre edges and credits normalized; six credits and three distinct terms/entity.
 db.exec(f"INSERT INTO catalog_entity_terms SELECT e.id,1+((e.id*7+v.column1)%30) FROM catalog_entities e CROSS JOIN (VALUES(0),(1),(2)) v WHERE e.id BETWEEN {start} AND {end}")
 db.exec(f"INSERT INTO catalog_credits SELECT e.id,v.column1,1+((e.id*13+v.column1*17)%2000),1 FROM catalog_entities e CROSS JOIN (VALUES(0),(1),(2),(3),(4),(5)) v WHERE e.id BETWEEN {start} AND {end}")
 db.exec(f"INSERT INTO personal_items SELECT 'viewer',id,id%11,printf('2026-10-%02d',1+id%28),id%19,1 FROM catalog_entities WHERE id BETWEEN {start} AND {end} AND id%10=0")
 db.exec(f"INSERT INTO catalog_collection_members SELECT ((id-1)/1000)*1000+975,id,printf('%010d',id) FROM catalog_entities WHERE id BETWEEN {start} AND {end} AND id%1000<>975")
 db.exec(f"INSERT INTO search_own SELECT id,library_id,kind,sort_key FROM catalog_entities WHERE id BETWEEN {start} AND {end}")
 db.exec(f"INSERT INTO search_own_prefix(rowid,title_fold) SELECT entity_id,title_fold FROM search_own WHERE entity_id BETWEEN {start} AND {end}")
 # Aggregate global sort facts only over the newly committed bounded 1000 group.
 for container_kind in [3,2,6,5,9,8,11]:
  extra=" OR x.id IN(SELECT item_id FROM catalog_collection_members WHERE collection_id=e.id)" if container_kind==11 else ''
  db.exec(f"UPDATE catalog_entities AS e SET (duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,episode_count,latest_aired_key)=(SELECT COALESCE(max(x.duration_key),0),COALESCE(max(x.community_rating_key),0),COALESCE(max(x.critic_rating_key),0),COALESCE(max(x.file_size_key),0),COALESCE(max(x.resolution_key),0),COALESCE(sum(CASE WHEN x.kind=4 THEN 1 ELSE x.episode_count END),0),COALESCE(max(x.release_key),'') FROM catalog_entities x WHERE x.parent_id=e.id{extra}) WHERE e.id BETWEEN {start} AND {end} AND e.kind={container_kind}")
 db.exec(f"INSERT INTO recommendation_candidates SELECT 'viewer',id,1000-id FROM catalog_entities WHERE id BETWEEN {start} AND {min(end,1000)} AND kind IN(1,2,6,9)")
 db.exec("UPDATE library_revisions SET revision=revision+1")
 changes=scalar(db,'SELECT total_changes()')-before
 db.exec('COMMIT');return dict(items=end-start+1,ms=(time.perf_counter()-t)*1000,changes=changes,source_entity_asset_ms=source_ms)

# Descendants computed from authoritative parent/member facts, no stored browse closure.
def descendants(alias='e'):
 return f"WITH RECURSIVE d(id) AS (SELECT {alias}.id UNION ALL SELECT x.id FROM catalog_entities x JOIN d ON x.parent_id=d.id) SELECT id FROM d UNION SELECT item_id FROM catalog_collection_members WHERE collection_id={alias}.id"
def visible(alias):return f"({alias}.content_rating IN ('G','PG'))"
def make_query(sort,kind,direction='asc',restricted=False,small=False,continuation=False):
 kid=KINDS.index(kind)+1;lib=2 if small else 1;col=SORTS[sort][1];params={};where=f'e.library_id={lib} AND e.kind={kid} AND e.retired=0';joins=''
 if sort in ('personalRating','lastPlayed','playCount'):
  metric={'personalRating':'rating','lastPlayed':'last_played_at','playCount':'plays'}[sort];default="''" if sort=='lastPlayed' else '0'
  if kind in CONTAINERS:
   filt=" AND x.content_rating IN ('G','PG')" if restricted else ''
   col=f"COALESCE((SELECT max(p.{metric}) FROM catalog_entities x CROSS JOIN personal_items p ON p.item_id=x.id AND p.profile_id='viewer' WHERE x.id IN ({descendants()}){filt}),{default})"
  else:
   joins=" LEFT JOIN personal_items p ON p.profile_id='viewer' AND p.item_id=e.id";col=f'COALESCE(p.{metric},{default})'
 elif sort=='forYou':
  # Finite bounded recommendation cohort; remaining title tail retained.
  joins=" LEFT JOIN recommendation_candidates r ON r.entity_id=e.id AND r.profile_id='viewer'";col='COALESCE(r.score,-1)'
 elif sort=='album':
  joins=' JOIN catalog_entities a ON a.id=e.parent_id';col='a.sort_key'
 elif sort=='artist':
  if kind=='song':joins=' JOIN catalog_entities a ON a.id=e.parent_id JOIN catalog_entities ar ON ar.id=a.parent_id'
  else:joins=' JOIN catalog_entities ar ON ar.id=e.parent_id'
  col='ar.sort_key'
 elif sort=='groupOrder':
  # Explicit current group; no global-library order claim.
  joins=' JOIN catalog_collection_members cm ON cm.item_id=e.id';where+=' AND cm.collection_id=975';col='cm.order_key'
 elif sort=='title' and kind=='season':
  joins=' JOIN catalog_entities sh ON sh.id=e.parent_id';col="sh.sort_key||' · Season '||CAST(e.track_key%1000000 AS TEXT)"
 if restricted:
  where+=' AND '+visible('e')
  if kind in CONTAINERS:
   where+=f" AND EXISTS(SELECT 1 FROM catalog_entities vl WHERE vl.id IN ({descendants()}) AND vl.kind IN(1,4,7,10,12,13) AND vl.content_rating IN ('G','PG'))"
   if sort in ('duration','communityRating','criticRating','fileSize','resolution','latestAired','episodeCount'):
    mc={'duration':'duration_key','communityRating':'community_rating_key','criticRating':'critic_rating_key','fileSize':'file_size_key','resolution':'resolution_key','latestAired':'release_key','episodeCount':'id'}[sort]
    agg='count(*)' if sort=='episodeCount' else f'max(v.{mc})'
    col=f"(SELECT {agg} FROM catalog_entities v WHERE v.id IN ({descendants()}) AND v.kind IN(1,4,7,10,12,13) AND v.content_rating IN ('G','PG'))"
 if continuation:
  # A tuple seek only for stored scalars; C-expressions use exact midpoint value.
  scalarcol=SORTS[sort][1]
  if scalarcol.startswith('e.') and col==scalarcol:
   op='>' if direction=='asc' else '<';where+=f' AND ({col},e.id){op}(SELECT {scalarcol},e.id FROM catalog_entities e WHERE e.library_id={lib} AND e.kind={kid} ORDER BY e.id LIMIT 1 OFFSET 100)'
 sql=f'SELECT e.id,{col} AS sort_value FROM catalog_entities e{joins} WHERE {where} ORDER BY {col} {direction},e.id {direction} LIMIT 40'
 if sort=='forYou':sql=sql[:sql.index(' ORDER BY ')]+' ORDER BY COALESCE(r.score,-1) DESC,e.sort_key ASC,e.id ASC LIMIT 40'
 return sql

def queries():
 out=[]
 for sort,(kinds,_) in SORTS.items():
  for kind in kinds:
   for direction in (['desc'] if sort=='forYou' else ['asc','desc']):
    for restricted in (False,True):
     out.append((f'{sort}/{kind}/{direction}/'+('restricted' if restricted else 'open'),make_query(sort,kind,direction,restricted)))
 # Multi-library sparse-source control and far tuple control.
 for sort in ['title','added','duration','communityRating','criticRating','fileSize','resolution','releaseDate']:
  out.append((f'small-library/{sort}',make_query(sort,'movie','desc',False,True)))
  out.append((f'continuation/{sort}',make_query(sort,'movie','desc',False,False,True)))
 for restricted in (False,True):
  suffix='restricted' if restricted else 'open';w=''
  if restricted:
   w=" AND e.content_rating IN ('G','PG') AND (e.kind NOT IN (2,3,5,6,8,9,11) OR EXISTS(SELECT 1 FROM catalog_entities vl WHERE vl.id IN ("+descendants()+") AND vl.kind IN(1,4,7,10,12,13) AND vl.content_rating IN ('G','PG')))"
  out.append((f'totals/{suffix}',f'SELECT kind,count(*) FROM catalog_entities e WHERE library_id=1 AND retired=0{w} GROUP BY kind'))
  out.append((f'facets/genre/{suffix}',f'SELECT t.term_id,count(*) FROM catalog_entities e JOIN catalog_entity_terms t ON t.entity_id=e.id WHERE e.library_id=1 AND e.retired=0{w} GROUP BY t.term_id'))
  out.append((f'facets/year/{suffix}',f'SELECT year,count(*) FROM catalog_entities e WHERE library_id=1 AND retired=0{w} GROUP BY year'))
  for word in ['word*','zzmissing*','000000*']:
   out.append((f'search/{word}/{suffix}',f"SELECT e.id,e.sort_key FROM search_own_prefix f JOIN catalog_entities e ON e.id=f.rowid WHERE search_own_prefix MATCH '{word}' AND e.library_id=1 AND e.retired=0{w} ORDER BY e.sort_key,e.id LIMIT 40"))
 return out

def writer(path,stop,ready,counter,output,start,growth=False):
 db=DB(path);xs=[];nextid=start;previous=None
 try:
  with pathlib.Path(output).with_suffix('.jsonl').open('w') as log:
   while not stop.is_set():
    began=time.time_ns();rec=seed(db,nextid,nextid+999,previous);ended=time.time_ns()
    rec.update(start_ns=began,end_ns=ended,first_id=nextid,evicted=1000 if previous else 0)
    xs.append(rec);log.write(json.dumps(rec)+'\n');log.flush()
    with counter.get_lock():counter.value+=1000
    ready.set();previous=None if growth else (nextid,nextid+999);nextid+=1000
    if growth and nextid-start>2_000_000:break
 finally:db.close();atom(output,{'batches':xs,'last_id':nextid-1,'mode':('pure append new ingestion, no sleeps; 2M new-item safety cap' if growth else 'continuous new ingestion with previous synthetic tail batch retired, no sleeps')})

def probe(args):
 path=pathlib.Path(args.db);out=pathlib.Path(args.out);out.mkdir(parents=True,exist_ok=True)
 if args.open_only:
  xs=[]
  for _ in range(100):
   t=time.perf_counter();db=DB(path,True);db.query('SELECT id,title FROM catalog_entities LIMIT 1');xs.append({'ms':(time.perf_counter()-t)*1000,'rc':101});db.close()
  atom(out/f'opens-{os.getpid()}.json',{'samples':xs,'summary':summarize(xs)});return
 if not path.exists():
  db=DB(path);create(db);batches=[]
  for start in range(1,args.size+1,1000):
   batches.append(seed(db,start,min(args.size,start+999)))
   if start%100000==1:print('seed',start,flush=True)
  db.exec('ANALYZE; PRAGMA wal_checkpoint(TRUNCATE);');db.close();atom(out/'seed.json',{'size':args.size,'batches':batches})
 from chronology import compare
 if not (out/'chronology-ab.json').exists():compare(path,out)
 db=DB(path,True)
 atom(out/'manifest.json',{'sqlite_version':scalar(db,'SELECT sqlite_version()'),'sqlite_options':db.query('PRAGMA compile_options')[0],'python':sys.version,'platform':platform.platform(),'profile':args.profile,'size':args.size,'sha':os.getenv('GITHUB_SHA'),'run_id':os.getenv('GITHUB_RUN_ID'),'ddl_sha256':hashlib.sha256((ROOT/'schema.sql').read_bytes()).hexdigest(),'entity_count':scalar(db,'SELECT count(*) FROM catalog_entities'),'foreign_key_violations':db.query('PRAGMA foreign_key_check',120)[0],'kind_count':db.query('SELECT kind,count(*) FROM catalog_entities GROUP BY kind')[0],'memory':db.mem()})
 if args.seed_only:return
 qs=queries();random.Random(1104).shuffle(qs)
 for phase in (['growth'] if args.growth else ['rest','ingest']):
  stop=mp.Event();ready=mp.Event();counter=mp.Value('q',0);p=None
  if phase in ['ingest','growth']:
   p=mp.Process(target=writer,args=(path,stop,ready,counter,out/(phase+'-writer.json'),args.size+1,args.growth));p.start()
   if not ready.wait(30):raise RuntimeError('writer did not publish initial progress')
  try:
   with (out/f'queries-{phase}.jsonl').open('w') as f:
    for j,(name,sql) in enumerate(qs):
     plan=db.query('EXPLAIN QUERY PLAN '+sql,120)[0]
     rows,warm=db.query(sql);xs=[];begin_ns=time.time_ns();begin_progress=counter.value
     while len(xs)<args.samples or (p and counter.value==begin_progress and time.time_ns()-begin_ns<2_000_000_000):
      rows,stats=db.query(sql);xs.append(stats)
      if stats['rc']!=101 or (len(xs)>=5 and stats['ms']>2000):break
     end_ns=time.time_ns();end_progress=counter.value
     overlap={'read_start_ns':begin_ns,'read_end_ns':end_ns,'accepted_start':begin_progress,'accepted_end':end_progress,'committed_during_window':end_progress-begin_progress,'label':('during-ingest' if end_progress>begin_progress else 'idle') if p else 'at-rest'}
     rec={'overlap':overlap,'name':name,'phase':phase,'sql':sql,'plan':plan,'warmup':warm,'summary':summarize(xs),'samples':xs,'row_hash':hashlib.sha256(json.dumps(rows).encode()).hexdigest(),'returned_rows':rows,'memory':db.mem()}
     f.write(json.dumps(rec)+'\n');f.flush()
     if j%40==0:print(phase,j,len(qs),name,rec['summary'],flush=True)
  finally:
   if p:stop.set();p.join(30)
   if p and p.is_alive():p.terminate();p.join();raise RuntimeError('writer did not exit')
  if p and p.exitcode:raise RuntimeError('writer failed')
 # Physical accounting: index cells are not entries on table-interior pages.
 objects=db.query("SELECT type,name FROM sqlite_schema WHERE type IN('table','index') AND name NOT LIKE 'sqlite_stat%' ORDER BY type,name",120)[0];sizes=[]
 for typ,name in objects:
  rows=scalar(db,'SELECT count(*) FROM "'+name+'"') if typ=='table' else None
  alloc=db.query("SELECT sum(pgsize),sum(payload),sum(CASE WHEN pagetype='leaf' THEN ncell ELSE 0 END),sum(CASE WHEN pagetype='internal' THEN ncell ELSE 0 END) FROM dbstat WHERE name='"+name+"'",120)[0]
  sizes.append({'type':typ,'name':name,'rows':rows,'dbstat':alloc})
 atom(out/'storage.json',{'objects':sizes,'db_bytes':path.stat().st_size,'wal_bytes':path.with_name(path.name+'-wal').stat().st_size if path.with_name(path.name+'-wal').exists() else 0,'entity_count':scalar(db,'SELECT count(*) FROM catalog_entities'),'rss_kb':resource.getrusage(resource.RUSAGE_SELF).ru_maxrss,'memory':db.mem()})
 db.close()
 # Fresh-process open cohorts (not a lazy pool borrow).
 for _ in range(3):subprocess.run([sys.executable,__file__,'--db',str(path),'--out',str(out),'--open-only'],check=True)
 pools=[];held=[]
 for count in [0,1,8,16,32]:
  while len(held)<count:
   x=DB(path,True);x.query('SELECT id,title FROM catalog_entities LIMIT 40');held.append(x)
  pools.append({'connections':count,'rss_kb':int(pathlib.Path('/proc/self/status').read_text().split('VmRSS:')[1].split()[0]),'per_connection':[x.mem() for x in held]})
 for x in held:x.close()
 atom(out/'memory-pools.json',pools)

if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('--db',required=True);ap.add_argument('--out',required=True);ap.add_argument('--size',type=int,default=10000);ap.add_argument('--samples',type=int,default=21);ap.add_argument('--profile',default='smoke');ap.add_argument('--open-only',action='store_true');ap.add_argument('--seed-only',action='store_true');ap.add_argument('--growth',action='store_true');probe(ap.parse_args())
