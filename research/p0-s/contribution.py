import os,pathlib,time
from bench import DB,create,atom,scalar,summarize
from adversarial import reference_seed,populate_members,timed

def run(out,n=1000000):
 out=pathlib.Path(out);p=out/'contributions.db';db=DB(p);create(db);reference_seed(db);lo,hi=populate_members(db,n)
 metrics=[('duration','REAL','duration_key'),('size','INTEGER','file_size_key'),('rating','REAL','community_rating_key'),('critic','REAL','critic_rating_key'),('resolution','INTEGER','resolution_key'),('latest','TEXT','release_key')]
 t=time.perf_counter()
 for name,typ,col in metrics:
  db.exec(f'CREATE TABLE contribution_{name}(parent INTEGER NOT NULL,member INTEGER NOT NULL,value {typ} NOT NULL,PRIMARY KEY(parent,member)) WITHOUT ROWID,STRICT; CREATE INDEX contribution_{name}_extreme ON contribution_{name}(parent,value,member); INSERT INTO contribution_{name} SELECT 975,id,{col} FROM catalog_entities WHERE id BETWEEN {lo} AND {hi};')
 setup_ms=(time.perf_counter()-t)*1000;db.exec('PRAGMA wal_checkpoint(TRUNCATE)')
 indexes=db.query("SELECT name,sum(pgsize),sum(payload) FROM dbstat WHERE name LIKE 'contribution_%' GROUP BY name",120)[0]
 xs=[];plans=[]
 for rep in range(101):
  sql=f'DELETE FROM catalog_collection_members WHERE collection_id=975 AND item_id={hi};'
  sets=[]
  for name,typ,col in metrics:
   sql+=f'DELETE FROM contribution_{name} WHERE parent=975 AND member={hi};'
   expr=f"SELECT value FROM contribution_{name} WHERE parent=975 ORDER BY value DESC,member DESC LIMIT 1"
   if rep==0:plans.append({'metric':name,'plan':db.query('EXPLAIN QUERY PLAN '+expr)[0]})
   default="''" if typ=='TEXT' else '0';sets.append(f'{col}=COALESCE(({expr}),{default})')
  rec=timed(db,sql,'UPDATE catalog_entities SET '+','.join(sets)+',revision=revision+1 WHERE id=975 RETURNING id');rec['source_members_visited']=1;rec['extreme_seeks']=6;xs.append(rec)
  restore=f"INSERT INTO catalog_collection_members VALUES(975,{hi},'{hi}');"
  for name,typ,col in metrics:restore+=f'INSERT INTO contribution_{name} SELECT 975,id,{col} FROM catalog_entities WHERE id={hi};'
  db.exec('BEGIN;'+restore+'COMMIT;')
 summary=summarize(xs);summary.update(max_ms=max(x['ms'] for x in xs),max_changes=max(x['changes'] for x in xs),strict=summary['p99_ms']<=10 and max(x['ms'] for x in xs)<=25,max50=max(x['ms'] for x in xs)<=50)
 atom(out/'contribution-results.json',{'n_members':n,'contribution_rows':6*n,'setup_ms':setup_ms,'allocation':indexes,'plans':plans,'summary':summary,'samples':xs,'boundary':'Six typed contribution tables, each (parent,value,member) index; atomic member+6 contribution deletes+parent scalar updates. This removes extreme lookup scans; does not bound source asset->parent fanout.'})
 db.close()
 for suffix in ['', '-wal','-shm']:pathlib.Path(str(p)+suffix).unlink(missing_ok=True)
if __name__=='__main__':
 import sys
 if not os.getenv('GITHUB_RUN_ID'):raise SystemExit('hosted-only million-member contribution control')
 run(sys.argv[1])
