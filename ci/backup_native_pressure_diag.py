from pathlib import Path
import json,os,subprocess
from plan import ROOT
from run import go
plan=json.loads(Path(os.environ['RECORDED_PLAN']).read_text())
target=next(t.copy() for t in plan['shards'][2] if t['name']=='TestBackupDuringConcurrentWrites')
target['name']='TestBackupNativeWALPressureDiagnostic'
plan['shards']=[[target]]
for variant,ref in [('baseline','88a1c5c2'),('fixed','f898e50f')]:
 text=subprocess.check_output(['git','show',ref+':server/internal/backup/backup_test.go'],text=True)
 text=text.replace('func TestBackupDuringConcurrentWrites(t *testing.T) {','func TestBackupNativeWALPressureDiagnostic(t *testing.T) {',1)
 start=text.index('func TestBackupNativeWALPressureDiagnostic')
 end=text.index('\n// Backups list newest',start)
 body=text[start:end]
 body=body.replace('id, err := svc.Create(',"""checksBefore:=dbwork.Checkpoints().Attempts
 svc.SetCopyScrubber(func(wait context.Context,_ *dbwork.Candidate)error{
  // Model sustained real WAL growth until the actual owner services it or
  // the real admission envelope is reached. No ledger mutation or sleeps
  // that extend the production no-progress/copy limits.
  for dbwork.WALReservations(db).AdmissionBytes<dbwork.CheckpointHardFileBytes && dbwork.Checkpoints().Attempts==checksBefore {
   if wait.Err()!=nil{return wait.Err()}
   time.Sleep(time.Millisecond)
  }
  t.Logf("NATIVE-PRESSURE before publication ledger=%+v checkpoints=%+v",dbwork.WALReservations(db),dbwork.Checkpoints())
  return nil
 })
 id, err := svc.Create(""",1)
 left=body.index('\tif max := time.Duration(maxWait.Load());')
 right=body.index('\tcheckOK(',left)
 body=body[:left]+'t.Logf("NATIVE-PRESSURE user-write latency under deliberate WAL pressure=%s",time.Duration(maxWait.Load()))\n'+body[right:]
 text=text[:start]+body+text[end:]
 (ROOT/'server/internal/backup/backup_test.go').write_text(text)
 try: go(plan,0)
 except SystemExit: pass
 Path('ci-results').rename('backup-native-'+variant)
for variant,expected in [('baseline','fail'),('fixed','pass')]:
 p=next(Path('backup-native-'+variant).glob('*_backup.jsonl'))
 events=[json.loads(l) for l in p.read_text().splitlines()]
 terminals=[e['Action'] for e in events if e.get('Test')==target['name'] and e['Action'] in ('pass','fail','skip')]
 assert terminals==[expected],(variant,terminals)
 if variant=='baseline':
  output=''.join(e.get('Output','') for e in events)
  assert 'reason=wal-admission-limit' in output and 'phase=advance-files' in output and 'backup could not finish without holding up the server' in output
print('Native WAL pressure baseline fails, production-owner fixture completes; healthy-copy 250ms assertion remains in the normal qualification.',flush=True)
