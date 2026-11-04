from pathlib import Path
import json,os,subprocess
from plan import ROOT
from run import go
original=subprocess.check_output(['git','show','88a1c5c2:server/internal/backup/backup_test.go'],text=True)
fixed=(ROOT/'server/internal/backup/backup_test.go').read_text()
plan=json.loads(Path(os.environ['RECORDED_PLAN']).read_text())
target=[t for t in plan['shards'][2] if t['name']=='TestBackupDuringConcurrentWrites']
assert len(target)==1
plan['shards']=[target]
failed=[]
for variant,text in [('baseline',original),('fixed',fixed)]:
 needle='id, err := svc.Create('
 index=text.index(needle,text.index('func TestBackupDuringConcurrentWrites'))
 text=text[:index]+'dbwork.DiagnosticStaleBackupFreeSpace(db)\n\t'+text[index:]
 (ROOT/'server/internal/backup/backup_test.go').write_text(text)
 try: go(plan,0)
 except SystemExit as e: failed.append((variant,str(e)))
 Path('ci-results').rename('backup-owner-'+variant)
print('Controlled no-owner failure is expected; fixed must pass. Results:',failed,flush=True)
for variant,expected in [('baseline','fail'),('fixed','pass')]:
 files=list(Path('backup-owner-'+variant).glob('*_backup.jsonl'))
 assert len(files)==1
 events=[json.loads(line) for line in files[0].read_text().splitlines()]
 terminals=[e['Action'] for e in events if e.get('Test')=='TestBackupDuringConcurrentWrites' and e['Action'] in ('pass','fail','skip')]
 assert terminals==[expected],(variant,terminals)
 if variant=='baseline':
  output=''.join(e.get('Output','') for e in events)
  assert 'reason=free-observation' in output and 'backup could not finish without holding up the server' in output
if any(v=='fixed' for v,_ in failed): raise SystemExit(1)
