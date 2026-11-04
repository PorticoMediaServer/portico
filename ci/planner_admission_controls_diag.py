from pathlib import Path
import json,os,subprocess
from plan import ROOT
from run import go
plan=json.loads(Path(os.environ['RECORDED_PLAN']).read_text())
target=next(t for t in plan['shards'][7] if t['name']=='TestPlannerStatisticsNeverHoldTheWriteGateAcrossATable')
plan['shards']=[[target]]
fixed=(ROOT/'server/internal/persistence/planner_stats_test.go').read_text()
original=subprocess.check_output(['git','show','bf3143de:server/internal/persistence/planner_stats_test.go'],text=True)
gate=(ROOT/'server/internal/dbwork/gate.go').read_text()
planner=(ROOT/'server/internal/persistence/planner_stats.go').read_text()
anchor='\t\t\t\t})\n\t\t\t}, nil'
assert anchor in gate
late=gate.replace(anchor,'\t\t\t\t})\n if t.owner=="planner-admission-control" && !plannerLateReturnOnce.Swap(true) {time.Sleep(300*time.Millisecond)}\n\t\t\t}, nil',1)
(ROOT/'server/internal/dbwork/zz_planner_late_control.go').write_text('package dbwork; import "sync/atomic"; var plannerLateReturnOnce atomic.Bool\n')
results=[]
for case,text,late_return,held in [('old-late-return',original,True,False),('fixed-late-return',fixed,True,False),('fixed-held-table',fixed,False,True)]:
 start=text.index('func TestPlannerStatisticsNeverHoldTheWriteGateAcrossATable(')
 end=text.index('\nfunc Test',start+1)
 body=text[start:end]
 if late_return:
  needle='ctx, cancel := context.WithCancel(context.Background())'
  assert needle in body
  body=body.replace(needle,needle+'\n ctx=dbwork.WithOwner(ctx,"planner-admission-control")',1)
 text=text[:start]+body+text[end:]
 (ROOT/'server/internal/persistence/planner_stats_test.go').write_text(text)
 (ROOT/'server/internal/dbwork/gate.go').write_text(late if late_return else gate)
 source=planner
 if held:
  needle='exact := func() (int64, error) {\n'
  assert needle in source
  source=source.replace(needle,needle+'release,holdErr:=dbwork.WriteGate().Acquire(ctx,dbwork.ClassMaintenance)\n if holdErr!=nil{return 0,holdErr}\n defer release()\n',1)
 (ROOT/'server/internal/persistence/planner_stats.go').write_text(source)
 try: go(plan,0)
 except SystemExit: pass
 Path('ci-results').rename('planner-control-'+case)
 p=next(Path('planner-control-'+case).glob('*_persistence.jsonl'))
 events=[json.loads(l) for l in p.read_text().splitlines()]
 terminal=[e['Action'] for e in events if e.get('Test')==target['name'] and e['Action'] in ('pass','fail','skip')]
 results.append((case,terminal))
 output=''.join(e.get('Output','') for e in events)
 if case=='old-late-return': assert 'a sign-in-like write waited' in output and '250ms' in output
 if case=='fixed-held-table': assert 'the write gate was free for' in output
print(results,flush=True)
assert results==[('old-late-return',['fail']),('fixed-late-return',['pass']),('fixed-held-table',['fail'])],results
