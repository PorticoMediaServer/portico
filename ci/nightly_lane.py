"""Exact diagnostic ownership; the consolidated nightly builds once."""
import argparse
import json
from pathlib import Path
from plan import ROOT, balance
from run import go, ensure_ffmpeg

def select(plan, lane):
    tiers=json.loads((ROOT/'ci/tiers.json').read_text())
    wanted={(t['package'],t['name']) for t in tiers['nightly_lanes'][lane]}
    selected=[t for t in plan['all_inventory'] if (t['package'],t['name']) in wanted]
    if {(t['package'],t['name']) for t in selected} != wanted: raise ValueError('missing diagnostic parent')
    return selected

if __name__=='__main__':
    ap=argparse.ArgumentParser(); ap.add_argument('lane'); a=ap.parse_args()
    plan=json.loads(Path('ci-plan.json').read_text())
    plan['shards'],_=balance(select(plan,a.lane),{},1)
    ensure_ffmpeg(plan)
    go(plan,0)
