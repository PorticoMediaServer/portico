#!/usr/bin/env python3
"""Generate observation-only overlays; no repository server file is modified."""
import argparse,hashlib,json,pathlib,subprocess
p=argparse.ArgumentParser();p.add_argument('--output',type=pathlib.Path,required=True);a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[2];out=a.output.resolve();out.mkdir(parents=True,exist_ok=True)
files={}; witnesses={}
def change(name,transform):
 path=root/name;source=path.read_text();result=transform(source)
 assert result!=source,name
 # Only the generated copy gains measurement imports and hooks.
 result=result.replace('import (','import (\n evidence "portico-scanner-evidence"',1)
 target=out/(name.replace('/','_'));target.write_text(result)
 subprocess.run(['gofmt','-w',str(target)],check=True)
 files[str(path)]=str(target)
 witnesses[name]={'sourceSha256':hashlib.sha256(source.encode()).hexdigest(),'overlaySha256':hashlib.sha256(target.read_bytes()).hexdigest()}
def add_span(source,signature,name):
 assert source.count(signature)==1,signature
 return source.replace(signature,signature+'\n defer evidence.Begin("'+name+'")()',1)
change('server/cmd/server/main.go',lambda s:s+'\nfunc init(){ evidence.Serve() }\n')
def service(s):
 s=add_span(s,'func (s *Service) processQuantum(ctx context.Context, id, library string) bool {','quantum')
 for call,label in [('more = dbwork.PaceBackground(work, started)','pacing'),('more = compactcatalog.AwaitTitles(work, s.db, titleCatchUp)','title_wait')]:
  assert s.count(call)==1
  s=s.replace(call,'endEvidence:=evidence.Begin("'+label+'")\n'+call+'\nendEvidence()',1)
 return s
change('server/internal/ingestion/service.go',service)
def analysis(s):
 for sig,name in [('func (s *Service) analyzeOne(parent context.Context, job string, source catalog.LibrarySource) error {','analysis'),('func (s *Service) finishBasicAnalysis(parent, ctx context.Context, job, object, revision string, queuedPolicy int64, policy catalog.ScanPolicy, source catalog.LibrarySource, completed []string, descriptor, deep, textPending bool) error {','finish')]:s=add_span(s,sig,name)
 line='accepted := acceptedKey{job: job, object: object, revision: revision, policy: policy.Revision}'
 assert s.count(line)==1
 s=s.replace(line,line+'\n evidence.ResumeReceipt(job,object,revision,policy.Revision)',1)
 return s
change('server/internal/ingestion/analysis.go',analysis)
# Locate signatures, then fail closed if the source no longer matches.
for name,prefix,label in [('server/internal/assets/probe.go','func (p Probe) InspectScan(','probe'),('server/internal/catalog/inventory_analysis.go','func (s *Service) AcceptInventoryAnalysis(','accept')]:
 path=root/name
 if not path.exists():
  matches=[x for x in (root/'server/internal').rglob('*.go') if prefix in x.read_text() and not x.name.endswith('_test.go')]
  assert len(matches)==1,(prefix,matches);path=matches[0];name=str(path.relative_to(root))
 def wrap(s,prefix=prefix,label=label):
  start=s.index(prefix);brace=s.index('{',start);sig=s[start:brace+1]
  s=add_span(s,sig,label)
  if label=='accept':
   assert '(compactcatalog.InfoAcceptReceipt, error)' in sig
   named=sig.replace('(compactcatalog.InfoAcceptReceipt, error)','(out compactcatalog.InfoAcceptReceipt, retErr error)')
   hook='\n defer func(){if retErr==nil && out.State==compactcatalog.InfoInputAcceptedPending {var token string;if e:=dbwork.QueryRow(ctx,s.db,`SELECT asset_id FROM inventory_objects WHERE id=? AND revision=?`,object,revision).Scan(&token);e==nil{evidence.PendingReceipt(job,object,revision,policyRevision,token,out.ExpectedEpoch)}}}()\n'
   s=s.replace(sig,named+hook,1).replace('import (','import (\n "github.com/PorticoMediaServer/portico/server/internal/dbwork"',1)
  return s
 change(name,wrap)
def publication(s):
 needle='_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=4 WHERE id=?`, c.id)'
 assert s.count(needle)==1
 s=s.replace(needle,needle+'\n if err==nil { token,epoch:=c.token,c.expected;_ = dbwork.AfterCommit(tx,func(){evidence.ReceiptApplied(token,epoch)}) }',1)
 return s.replace('import (','import (\n "github.com/PorticoMediaServer/portico/server/internal/dbwork"',1)
change('server/internal/compactcatalog/info_publication.go',publication)
change('server/internal/dbwork/write.go',lambda s:add_span(s,'func Begin(ctx context.Context, db *sql.DB, class Class) (*Write, error) {','writer_begin'))
def worker(s):
 sig='func RunWith(ctx context.Context, name string, urgent, shared *Signal, step Step) {'
 assert s.count(sig)==1
 s=s.replace(sig,sig+'\n endWait:=func(){}\n defer func(){endWait()}()',1)
 s=s.replace('lastPass = time.Now()','endWait(); endWait=func(){}\n lastPass = time.Now()',1)
 needle='wait := step(ctx)';assert s.count(needle)==1
 s=s.replace(needle,needle+'\n if name=="ingestion.scan" { endWait=evidence.Begin("scheduler_wait") }',1)
 return s
change('server/internal/worker/worker.go',worker)
def driver(s):
 sig='func (c *conn) step(pstmt uintptr) (int, error) {';assert s.count(sig)==1
 s=s.replace(sig,'func (c *conn) scannerEvidenceStep(pstmt uintptr) (int, error) {',1)
 return s+'\nfunc (c *conn) step(pstmt uintptr) (int,error) { return evidence.SQL(libc.GoString(sqlite3.Xsqlite3_sql(c.tls,pstmt)),func()(int,error){return c.scannerEvidenceStep(pstmt)}) }\n'
change('server/internal/thirdparty/sqlite/conn.go',driver)
(out/'overlay.json').write_text(json.dumps({'Replace':files},indent=2)+'\n')
(out/'overlay-witness.json').write_text(json.dumps(witnesses,indent=2)+'\n')
mod=(root/'server/go.mod').read_text()+'\nrequire portico-scanner-evidence v0.0.0\nreplace portico-scanner-evidence => '+str(root/'scripts/scanner-evidence')+'\n'
mod+='\nreplace github.com/PorticoMediaServer/portico/apikit => '+str(root/'apikit')+'\n'
(out/'evidence.mod').write_text(mod);(out/'evidence.sum').write_bytes((root/'server/go.sum').read_bytes())
print('Observation overlay generated:',len(files),'source witnesses')
