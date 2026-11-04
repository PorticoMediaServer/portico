#!/usr/bin/env python3
"""Split the unchanged nightly race inventory into bounded, single-package cohorts."""
import argparse
from collections import Counter, defaultdict
import hashlib
import json
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def digest(plan):
    return hashlib.sha256(json.dumps(plan, sort_keys=True).encode()).hexdigest()


def identity(test):
    return test['module'], test['package'], test['name']


def make_cohorts(plan, durations, budget=600, multiplier=10):
    if budget <= 0 or multiplier <= 0:
        raise ValueError('invalid race planning budget')
    durations = {k.replace('portico.local/server', 'github.com/PorticoMediaServer/portico/server'): v
                 for k, v in durations.items()}
    tests = plan['inventory']
    keys = [identity(t) for t in tests]
    if len(set(keys)) != len(keys):
        raise ValueError('duplicate race inventory parent')
    grouped = defaultdict(list)
    for test in tests:
        grouped[test['module'], test['package']].append(dict(test))
    cohorts = []
    for (module, package), own in sorted(grouped.items()):
        def cost(test):
            measured = durations.get(package + '.' + test['name'], 1)
            if not isinstance(measured, (int, float)) or measured < 0:
                raise ValueError('invalid duration for ' + package + '.' + test['name'])
            return max(.05, measured * multiplier)
        bins = []
        for test in sorted(own, key=lambda t: (-cost(t), t['name'])):
            weight = cost(test)
            fits = [c for c in bins if c['predicted_seconds'] + weight <= budget and len(c['tests']) < 128]
            if fits:
                cohort = max(fits, key=lambda c: c['predicted_seconds'])
            else:
                cohort = {'module': module, 'package': package, 'tests': [], 'predicted_seconds': 0}
                bins.append(cohort)
            cohort['tests'].append(test)
            cohort['predicted_seconds'] += weight
        for cohort in bins:
            cohort['predicted_seconds'] = round(cohort['predicted_seconds'], 3)
        cohorts.extend(bins)
    return {'schema': 1, 'source_plan_sha256': digest(plan), 'cohort_budget_seconds': budget,
            'bootstrap_cost_multiplier': multiplier, 'cohorts': cohorts}


def matrix(race):
    if len(race['cohorts']) > 256:
        raise ValueError('race cohorts exceed the hosted matrix limit; no inventory was dropped')
    return {'include': [dict(index=i, **({'reading_client': True} if any(
        t['name'] == 'TestReadingHTTPClientPublication' for t in cohort['tests']) else {}))
        for i, cohort in enumerate(race['cohorts'])]}


def select(plan, race, index):
    if race.get('source_plan_sha256') != digest(plan):
        raise ValueError('race plan does not match the exact source selection')
    expected = Counter(json.dumps(t, sort_keys=True) for t in plan['inventory'])
    actual = Counter(json.dumps(t, sort_keys=True) for c in race['cohorts'] for t in c['tests'])
    if actual != expected:
        raise ValueError('race cohorts changed the selected inventory')
    for cohort in race['cohorts']:
        if not cohort['tests'] or any((t['module'], t['package']) != (cohort['module'], cohort['package'])
                                      for t in cohort['tests']):
            raise ValueError('race cohort mixes packages or is empty')
    if not 0 <= index < len(race['cohorts']):
        raise ValueError('unknown race cohort')
    return race['cohorts'][index]['tests']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', default='ci-plan.json')
    parser.add_argument('--output', default='ci-race-plan.json')
    args = parser.parse_args()
    plan = json.loads(Path(args.plan).read_text())
    race = make_cohorts(plan, json.loads((ROOT / 'ci/durations.json').read_text()))
    assignment = matrix(race)
    target = Path(args.output)
    temporary = target.with_suffix(target.suffix + '.tmp')
    temporary.write_text(json.dumps(race, indent=2) + '\n')
    temporary.replace(target)
    print(f"Race observer: {len(plan['inventory'])} parents, {len(race['cohorts'])} single-package cohorts; "
          'same 780s test and 900s watchdog deadlines.')
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a') as out:
            out.write('race_matrix=' + json.dumps(assignment) + '\n')


if __name__ == '__main__': main()
