"""Tiny system-SQLite VM control; not bundled latency qualification."""
import sqlite3,json,sys
c=sqlite3.connect(':memory:')
c.executescript('CREATE TABLE e(id INTEGER PRIMARY KEY,l INTEGER,k INTEGER,s TEXT COLLATE NOCASE,y INTEGER,r INTEGER); CREATE INDEX iy ON e(l,k,y,id);CREATE INDEX ix ON e(l,k,s,id);')
c.executemany("INSERT INTO e VALUES(?,1,1,'worda',0,0)",[(i,) for i in range(1,2001)])
queries=["SELECT id FROM e WHERE l=1 AND k=1 AND (y,id)>(SELECT y,id FROM e WHERE id=1500) ORDER BY y,id LIMIT 40", "SELECT id FROM e WHERE l=1 AND k=1 AND (y,id)>(0,1500) ORDER BY y,id LIMIT 40", "SELECT id FROM e WHERE l=1 AND k=1 AND y=0 AND id>1500 ORDER BY y,id LIMIT 40", "SELECT id FROM e WHERE l=1 AND k=1 AND (s COLLATE NOCASE,id)>('worda',1500) ORDER BY s COLLATE NOCASE,id LIMIT 40"]
out=[]
for q in queries:
 n=[0]
 def progress():n[0]+=1
 c.set_progress_handler(progress,1);rows=c.execute(q).fetchall();c.set_progress_handler(None,0)
 assert rows==[(i,) for i in range(1501,1541)]
 out.append({'sql':q,'vm_ops':n[0],'plan':c.execute('EXPLAIN QUERY PLAN '+q).fetchall(),'seek_opcodes':[r for r in c.execute('EXPLAIN '+q) if r[1] in ['SeekGT','SeekGE','Next','IdxGT']]})
json.dump({'sqlite_version':sqlite3.sqlite_version,'rows':2000,'boundary':'system-SQLite tiny VM/plan diagnosis only; no full-schema or bundled performance claim','queries':out},sys.stdout,indent=2);print()
