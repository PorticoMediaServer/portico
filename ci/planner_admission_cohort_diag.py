from pathlib import Path
import json,os
from run import go
plan=json.loads(Path(os.environ['RECORDED_PLAN']).read_text())
p=Path('server/internal/persistence/planner_stats_test.go')
s=p.read_text().replace('func TestPlannerStatisticsNeverHoldTheWriteGateAcrossATable(t *testing.T) {','func TestPlannerStatisticsNeverHoldTheWriteGateAcrossATable(t *testing.T) {for i:=0;i<10;i++{t.Run(strconv.Itoa(i),plannerAdmissionDiagnostic)}}\n\nfunc plannerAdmissionDiagnostic(t *testing.T) {',1)
p.write_text(s)
go(plan,7)
