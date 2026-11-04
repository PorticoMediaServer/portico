import json,pathlib,time
from bench import DB,atom,scalar,summarize

def compare(source,out):
 out=pathlib.Path(out);p=out/'chronology.db';results=[]
 db=DB(p)
 try:
  db.exec("ATTACH DATABASE '"+str(source).replace("'","''")+"' AS src")
  for typ,expr in [('INTEGER',"CAST(strftime('%s',added_at) AS INTEGER)*1000000000+CAST(substr(added_at,21,9) AS INTEGER)"),('TEXT','added_at')]:
   db.exec(f'CREATE TABLE chronology(id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL,kind INTEGER NOT NULL,added_at {typ}) STRICT; CREATE INDEX chronological ON chronology(library_id,kind,added_at,id);')
   t=time.perf_counter();db.exec(f'INSERT INTO chronology SELECT id,library_id,kind,{expr} FROM src.catalog_entities');build=(time.perf_counter()-t)*1000
   qs=[]
   for direction in ['ASC','DESC']:
    sql=f'SELECT id,added_at FROM chronology WHERE library_id=1 AND kind=1 ORDER BY added_at {direction},id {direction} LIMIT 40';xs=[]
    db.query(sql)
    for _ in range(101):xs.append(db.query(sql)[1])
    qs.append({'direction':direction,'summary':summarize(xs)})
   results.append({'type':typ,'rows':scalar(db,'SELECT count(*) FROM chronology'),'build_ms':build,'allocation':db.query("SELECT name,sum(pgsize),sum(payload) FROM dbstat WHERE name IN('chronology','chronological') GROUP BY name",120)[0],'queries':qs})
   db.exec('DROP TABLE chronology')
 finally:db.close()
 atom(out/'chronology-ab.json',results);p.unlink(missing_ok=True)
if __name__=='__main__':
 import sys
 compare(sys.argv[1],sys.argv[2])
