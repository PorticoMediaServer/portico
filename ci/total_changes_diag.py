"""Branch-only same-VM pressure comparison; never merge."""
import json,os,subprocess,sys
from pathlib import Path
root=Path(__file__).resolve().parents[1]
refs={'A':'96118d5a32e42a8af76dcebb89574e3c0882e92f','B':'b66db669c9e9bd29ad0de0381a986457ac630dce'}
paths=['server/internal/dbwork/instrument.go','server/internal/compactcatalog/source_asset_cost_test.go','server/internal/dbwork/plan.go','server/internal/compactcatalog/derive.go','server/internal/compactcatalog/total_changes_parity_test.go','server/internal/dbwork/write.go','server/internal/dbwork/total_changes.go','server/internal/dbwork/total_changes_test.go','server/internal/thirdparty/sqlite/total_changes.go','server/internal/thirdparty/sqlite/total_changes_test.go']
plan=json.loads((root/'ci-plan.json').read_text())
wanted=json.loads((root/'ci/tiers.json').read_text())['nightly_lanes']['pressure']
packages=sorted({entry['package'] for entry in wanted})
out=root/'counter-pressure-results';out.mkdir()
(out/'hardware.json').write_text(json.dumps({'cpus':os.cpu_count(),'package_jobs':os.environ.get('PORTICO_CI_GO_PACKAGE_JOBS','4'),'refs':refs,'cohort':wanted},indent=2)+'\n')

def source(variant):
    for name in paths:
        result=subprocess.run(['git','show',refs[variant]+':'+name],cwd=root,capture_output=True,text=True)
        p=root/name
        if result.returncode==0:p.write_text(result.stdout)
        elif variant=='A' and 'total_changes' in name:p.unlink(missing_ok=True)
        else:raise ValueError(result.stderr)

    p=root/'server/internal/compactcatalog/derive.go'
    s=p.read_text()
    anchor='func (w *Worker) Step(ctx context.Context, limit int) (int, error) {'
    assert s.count(anchor)==1
    s=s.replace(anchor,anchor+'\n if counterTraceEnabled && counterTraceMarked(ctx) {var end func();ctx,end=counterTraceStep(ctx);defer end()}')
    p.write_text(s)

# Warm both source variants, with test caching disabled in each real replay.
for variant in ['A','B']:
    source(variant)
    subprocess.run(['go','test','-p=4','-tags',plan['tags'],'-run','^$']+packages,cwd=root/'server',check=True)
subprocess.run([sys.executable,'ci/run.py','helper'],cwd=root,check=True)
records=[]
for label,variant,trace in [('pair1-A','A',False),('pair1-B','B',False),('pair2-A','A',False),('pair2-B','B',False),('statements-A','A',True),('statements-B','B',True)]:
    source(variant)
    env=dict(os.environ,PORTICO_COUNTER_TRACE='1' if trace else '0')
    result=subprocess.run([sys.executable,'ci/nightly_lane.py','pressure'],cwd=root,env=env,capture_output=True,text=True)
    (out/(label+'.stdout')).write_text(result.stdout)
    (out/(label+'.stderr')).write_text(result.stderr)
    if (root/'ci-results').exists():(root/'ci-results').rename(out/label)
    records.append({'label':label,'variant':variant,'trace':trace,'returncode':result.returncode})
    (out/'sequence.json').write_text(json.dumps(records,indent=2)+'\n')
    print(label,result.returncode,flush=True)
if any(r['returncode'] for r in records):raise SystemExit(1)
