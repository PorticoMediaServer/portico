import hashlib,json,os,pathlib,time
from bench import DB,create,scalar,atom,summarize,ROOT
from adversarial import reference_seed,populate_members,timed
DDL=(ROOT/'selected-contributions.sql').read_text();SHA=hashlib.sha256(DDL.encode()).hexdigest()
FIELDS=[('duration_key','duration','duration IS NOT NULL'),('community_rating_key','community_rating','community_rating IS NOT NULL'),('critic_rating_key','critic_rating','1'),('file_size_key','file_size','1'),('resolution_key','resolution','1'),('latest_aired_key','latest_aired_key','1')]
def key_sql(where):
 sets=[]
 for target,source,pred in FIELDS:
  default="''" if source=='latest_aired_key' else '0'
  sets.append(f"{target}=COALESCE((SELECT {source} FROM catalog_scalar_contributions c WHERE c.parent_id=catalog_entities.id AND {pred} ORDER BY {source} DESC,contributor_id,source LIMIT 1),{default})")
 return 'UPDATE catalog_entities SET '+','.join(sets)+',revision=revision+1,collection_revision=collection_revision+1 WHERE '+where+' RETURNING id'
def from_entity(parent,where):
 return f'SELECT 1,{parent},id,0,revision,duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,release_key FROM catalog_entities WHERE {where}'
def receipt(db,key):
 return f"INSERT INTO admin_receipts(scope,operation_id,digest,result,created_ms) VALUES('research.scalar','{key}','{'0'*64}','applied',1);"
def init(path):
 db=DB(path);create(db);reference_seed(db);db.exec(DDL);db.exec("CREATE TABLE admin_receipts(scope TEXT NOT NULL,operation_id TEXT NOT NULL,digest TEXT NOT NULL,result TEXT NOT NULL,created_ms INTEGER NOT NULL,PRIMARY KEY(scope,operation_id)) STRICT;")
 return db
def record(out,name,xs,extra):
 s=summarize(xs);s.update(max_ms=max(x['ms'] for x in xs),max_rows_changed=max(x['changes'] for x in xs),strict=s['p99_ms']<=10 and max(x['ms'] for x in xs)<=25,max50=max(x['ms'] for x in xs)<=50)
 obj={'contribution_ddl_sha256':SHA,'entity_ddl_pin':json.loads((ROOT/'t1-entity-pin.json').read_text()),'summary':s,'samples':xs,**extra};atom(out/(name+'.json'),obj);print(name,s,flush=True)
def run(out,n=1000000,reps=101):
 out=pathlib.Path(out);out.mkdir(parents=True,exist_ok=True);p=out/'selected.db';db=init(p);lo,last=populate_members(db,n+1);hi=last-1;extra=last
 db.exec(f'DELETE FROM catalog_collection_members WHERE collection_id=975 AND item_id={extra}')
 t=time.perf_counter();db.exec('INSERT INTO catalog_scalar_contributions '+from_entity(975,f'id BETWEEN {lo} AND {hi}'));db.exec(key_sql('id=975').replace(' RETURNING id',''));setup=(time.perf_counter()-t)*1000
 allocation=db.query("SELECT name,sum(pgsize),sum(payload) FROM dbstat WHERE name='catalog_scalar_contributions' OR name LIKE 'catalog_scalar_%' GROUP BY name",120)[0]
 plans=[]
 for target,source,pred in FIELDS:plans.append({'metric':source,'plan':db.query(f'EXPLAIN QUERY PLAN SELECT {source} FROM catalog_scalar_contributions WHERE parent_id=975 AND {pred} ORDER BY {source} DESC,contributor_id,source LIMIT 1')[0]})
 for case in ['remove-extreme','add','update']:
  xs=[]
  for j in range(reps):
   if case=='remove-extreme':
    change=f'DELETE FROM catalog_collection_members WHERE collection_id=975 AND item_id={hi};DELETE FROM catalog_scalar_contributions WHERE parent_id=975 AND contributor_id={hi};'
    restore=f"INSERT INTO catalog_collection_members VALUES(975,{hi},'{hi}');INSERT INTO catalog_scalar_contributions "+from_entity(975,f'id={hi}')+';'
   elif case=='add':
    change=f"INSERT INTO catalog_collection_members VALUES(975,{extra},'{extra}');INSERT INTO catalog_scalar_contributions "+from_entity(975,f'id={extra}')+';'
    restore=f'DELETE FROM catalog_scalar_contributions WHERE parent_id=975 AND contributor_id={extra};DELETE FROM catalog_collection_members WHERE collection_id=975 AND item_id={extra};'
   else:
    change=f'UPDATE catalog_entities SET duration_key={hi+100},file_size_key={hi+100},revision=revision+1 WHERE id={lo};UPDATE catalog_scalar_contributions SET duration={hi+100},file_size={hi+100},contributor_revision=contributor_revision+1 WHERE parent_id=975 AND contributor_id={lo};'
    restore=f'UPDATE catalog_entities SET duration_key={lo},file_size_key={lo},revision=revision+1 WHERE id={lo};DELETE FROM catalog_scalar_contributions WHERE parent_id=975 AND contributor_id={lo};INSERT INTO catalog_scalar_contributions '+from_entity(975,f'id={lo}')+';'
   # Native hold includes complete delta, 6 seeks, entity/revision, receipt and FULL COMMIT.
   change+=receipt(db,f'{case}-{j}');xs.append(timed(db,change,key_sql('id=975')))
   db.exec('BEGIN;'+restore+key_sql('id=975').replace(' RETURNING id','')+';COMMIT;')
  record(out,'selected-million-'+case,xs,{'members':n,'setup_ms':setup,'allocation':allocation,'plans':plans,'receipt_shape':'exact admin_receipts DDL; research.scalar scope, not claim of a production private command'})
 db.close()
 for suffix in ['', '-wal','-shm']:pathlib.Path(str(p)+suffix).unlink(missing_ok=True)
 for degree in [10,100,1000]:
  p=out/'reverse.db';db=init(p);low,high=populate_members(db,degree)
  db.exec(f'UPDATE catalog_entities SET kind=10 WHERE id BETWEEN {low} AND {high};DELETE FROM catalog_collection_members;')
  db.exec(f"INSERT INTO catalog_collection_members SELECT id,1,'one' FROM catalog_entities WHERE id BETWEEN {low} AND {high};INSERT INTO catalog_scalar_contributions SELECT 1,id,1,0,1,100,5,60,1000,1080,'2026-11-04' FROM catalog_entities WHERE id BETWEEN {low} AND {high};")
  xs=[]
  for j in range(reps):
   value=100+j%2
   change=f'UPDATE catalog_entities SET duration_key={value},file_size_key={value*10},revision=revision+1 WHERE id=1;UPDATE catalog_scalar_contributions SET duration={value},file_size={value*10},contributor_revision=contributor_revision+1 WHERE contributor_id=1;'+receipt(db,f'reverse-{degree}-{j}')
   xs.append(timed(db,change,key_sql('id IN(SELECT parent_id FROM catalog_scalar_contributions WHERE contributor_id=1)')))
  record(out,f'selected-reverse-{degree}',xs,{'authored_collections':degree,'rows_changed_expected':degree*2+2,'boundary':'One accepted contributor changes, N contribution rows, six exact indexed extrema per parent, N entity key rows, receipt and COMMIT. No staged/source-versioned publication or permission work.'})
  db.close()
  for suffix in ['', '-wal','-shm']:pathlib.Path(str(p)+suffix).unlink(missing_ok=True)
if __name__=='__main__':
 import sys
 if not os.getenv('GITHUB_RUN_ID'):raise SystemExit('selected million-member measurement hosted only')
 run(sys.argv[1])
