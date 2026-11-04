"""Exact diagnostic ownership; the consolidated nightly builds once."""
import argparse
import json
import os
import subprocess
from pathlib import Path
from plan import ROOT, balance
from run import go, ensure_ffmpeg

def select(plan, lane):
    tiers=json.loads((ROOT/'ci/tiers.json').read_text())
    wanted={(t['package'],t['name']) for t in tiers['nightly_lanes'][lane]}
    selected=[t for t in plan['all_inventory'] if (t['package'],t['name']) in wanted]
    if {(t['package'],t['name']) for t in selected} != wanted: raise ValueError('missing diagnostic parent')
    return selected

def configure_environment(lane):
    # This parent otherwise skips. Only its dedicated job gets the smoke tier;
    # the original pressure cohort and explicit release tiers stay unchanged.
    if lane == 'mixed-load':
        os.environ.setdefault('PORTICO_PERFORMANCE_TIER', 'smoke')

def run_receiver_contracts(lane):
    if lane != 'golden':
        return
    tests = sorted((ROOT/'server/receiver/cast/test').glob('*.test.cjs'))
    required = {'receiver.test.cjs', 'text.test.cjs'}
    if not required.issubset({test.name for test in tests}):
        raise ValueError('missing Cast receiver compatibility suite')
    subprocess.run(['node', '--test', '--test-concurrency=2', *map(str, tests)],
                   cwd=ROOT, check=True)

if __name__=='__main__':
    ap=argparse.ArgumentParser(); ap.add_argument('lane'); a=ap.parse_args()
    plan=json.loads(Path('ci-plan.json').read_text())
    configure_environment(a.lane)
    plan['shards'],_=balance(select(plan,a.lane),{},1)
    ensure_ffmpeg(plan)
    go(plan,0)
    run_receiver_contracts(a.lane)
