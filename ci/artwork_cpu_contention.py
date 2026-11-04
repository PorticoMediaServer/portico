"""Controlled runner CPU contention, not an original-shard timing sample."""
import os,subprocess,sys
from pathlib import Path
available=sorted(os.sched_getaffinity(0))
assert len(available)>=4,available
selected=available[:2]
os.sched_setaffinity(0,selected)
assert os.environ['GOMAXPROCS']=='4'
print('ARTWORK-CPU-CONTROL original_affinity='+str(available)+' selected='+str(selected)+' GOMAXPROCS=4 package_jobs=4',flush=True)
plan=Path(os.environ['RUNNER_TEMP'])/'original-selection/ci-plan.json'
raise SystemExit(subprocess.run([sys.executable,'ci/run.py','go','--index','3','--plan',str(plan)]).returncode)
