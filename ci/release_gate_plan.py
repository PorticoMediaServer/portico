#!/usr/bin/env python3
"""The release gate's plan: the merge tier's full Go selection, balanced into N shards for -race.

Merge CI never runs -race, so the release gate is Portico's only race run. It uses ci/plan.py's
own selection (the merge tier, full inventory: the same test parents, exclusions and examples)
and its duration-balanced test-level split, with more shards because -race is several times
slower. The Go producer tests the merge tier hands to JS jobs run here too: a release runs no JS
shard. Each shard is then run by `ci/run.py go` with PORTICO_CI_GO_RACE=1.

  release_gate_plan.py --shards 30 --output gate-plan.json   (writes shards=N to $GITHUB_OUTPUT too)
"""
import argparse
import json
import os
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / 'ci'))
import plan as merge_plan  # noqa: E402


def durations():
    raw = json.loads((ROOT / 'ci/durations.json').read_text())
    return {k.replace('portico.local/server', 'github.com/PorticoMediaServer/portico/server'): v for k, v in raw.items()}


def make(count):
    plan = merge_plan.make_plan([], True, 'merge')
    tests = plan['inventory'] + plan['js_owned_producers']
    if not tests:
        raise SystemExit('release gate: empty Go selection')
    bins, costs = merge_plan.balance(tests, durations(), count)
    if any(not b for b in bins):
        raise SystemExit(f'release gate: {count} shards leave some empty; use fewer')
    plan.update(shards=bins, predicted_seconds=costs, gate={'race': True, 'shards': count,
                'note': 'predicted_seconds are measured without -race'})
    return plan


def main(argv=None):
    p = argparse.ArgumentParser()
    p.add_argument('--shards', type=int, default=30)
    p.add_argument('--output', type=pathlib.Path, required=True)
    a = p.parse_args(argv)
    plan = make(a.shards)
    a.output.write_text(json.dumps(plan, indent=2) + '\n')
    print(f'{a.shards} shards, predicted without -race: max {max(plan["predicted_seconds"]):.0f}s, '
          f'total {sum(plan["predicted_seconds"]):.0f}s, {sum(len(b) for b in plan["shards"])} test parents')
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a') as f:
            f.write('matrix=' + json.dumps({'index': list(range(a.shards))}, separators=(',', ':')) + '\n')


if __name__ == '__main__':
    main()
