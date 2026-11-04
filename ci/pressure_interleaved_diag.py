"""Run the exact cohort A/B/A/B on one VM; never merge this diagnostic."""
import json
import os
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[1]
plan = json.loads((root / 'ci-plan.json').read_text())
packages = sorted({entry['package'] for entry in plan['nightly_diagnostics']
                   if entry in json.loads((root / 'ci/tiers.json').read_text())['nightly_lanes']['pressure']})
out = root / 'pressure-paired-results'
out.mkdir()
(out / 'hardware.json').write_text(json.dumps({'cpus': os.cpu_count(), 'packages': packages,
                                             'native_trace': False}, indent=2) + '\n')

def source(variant):
    subprocess.run([sys.executable, 'ci/pressure_cost_diagnostic.py', variant], cwd=root, check=True)

# Warm both versions before measuring either. Test caches are still disabled in
# the actual cohort; compiling one version must not become an A-only neighbour.
for variant in ['baseline', 'fixed']:
    source(variant)
    subprocess.run(['go', 'test', '-p=4', '-tags', plan['tags'], '-run', '^$'] + packages,
                   cwd=root / 'server', check=True)
subprocess.run([sys.executable, 'ci/run.py', 'helper'], cwd=root, check=True)

records = []
for label, variant in [('pair1-A', 'baseline'), ('pair1-B', 'fixed'),
                       ('pair2-A', 'baseline'), ('pair2-B', 'fixed')]:
    source(variant)
    result = subprocess.run([sys.executable, 'ci/nightly_lane.py', 'pressure'],
                            cwd=root, capture_output=True, text=True)
    (out / (label + '.stdout')).write_text(result.stdout)
    (out / (label + '.stderr')).write_text(result.stderr)
    if (root / 'ci-results').exists():
        (root / 'ci-results').rename(out / label)
    records.append({'label': label, 'variant': variant, 'returncode': result.returncode})
    (out / 'sequence.json').write_text(json.dumps(records, indent=2) + '\n')
    print(label, variant, result.returncode, flush=True)

if any(record['returncode'] for record in records):
    raise SystemExit(1)
