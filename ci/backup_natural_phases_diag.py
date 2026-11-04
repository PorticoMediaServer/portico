from pathlib import Path
import json,os,subprocess
from plan import ROOT
from run import go
plan=json.loads(Path(os.environ['RECORDED_PLAN']).read_text())
plan['shards']=[plan['shards'][2]]
results=[]
for variant,ref in [('baseline','88a1c5c2'),('fixed','f898e50f')]:
 text=subprocess.check_output(['git','show',ref+':server/internal/backup/backup_test.go'],text=True)
 needle='func TestBackupDuringConcurrentWrites(t *testing.T) {'
 assert needle in text
 wrapper='func TestBackupDuringConcurrentWrites(t *testing.T) { for i:=0;i<20;i++ {t.Run(fmt.Sprintf("phase-%02d",i),backupConcurrentWritesDiagnostic)} }\n\nfunc backupConcurrentWritesDiagnostic(t *testing.T) {'
 text=text.replace(needle,wrapper,1)
 (ROOT/'server/internal/backup/backup_test.go').write_text(text)
 code=0
 try: go(plan,0)
 except SystemExit as e: code=e.code
 Path('ci-results').rename('backup-natural-'+variant)
 results.append((variant,code))
print('Natural phase sampling results',results,flush=True)
