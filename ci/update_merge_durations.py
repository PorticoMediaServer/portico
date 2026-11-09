#!/usr/bin/env python3
"""Rebuild ci/merge_durations.json from hosted merge-tier go_tests results.

Every `go_tests (N)` job uploads `go-N` (its ci-results directory): one JSONL per
package from `go test -json`. A parent test's terminal event carries `Elapsed`,
which includes its subtests. The planner prices merge and nightly shards from
this table, so the handful of long crash-replay and source-census parents cannot
pile into one 780-second batch just because they were unmeasured.

  python3 ci/update_merge_durations.py --results _scratch/run-A --results _scratch/run-B
  python3 ci/update_merge_durations.py --runs 42534033075,42529536239

`--runs` fetches the `go-<N>` artifacts through the Actions API; `gh run
download` mangles some artifact names. The table keeps the longest observed
seconds per parent across every supplied run. A parent that is absent falls back
to ci/durations.json, then to the planner's conservative default.
"""
import argparse
import io
import json
from pathlib import Path
import subprocess
import sys
import zipfile

REPO = 'PorticoMediaServer/portico'


def observe(lines, table):
    for line in lines:
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        if event.get('Test') and event.get('Action') in ('pass', 'fail', 'skip') and 'Elapsed' in event:
            package = str(event.get('Package', ''))
            key = package + '.' + event['Test'].split('/')[0]
            if not package:
                raise ValueError('go test event without a package: ' + key)
            try:
                elapsed = float(event['Elapsed'])
            except (TypeError, ValueError):
                raise ValueError('non-numeric Elapsed for ' + key)
            if elapsed < 0:
                raise ValueError('negative Elapsed for ' + key)
            table[key] = max(table.get(key, 0.0), elapsed)


def read_results(directory, table):
    files = sorted(Path(directory).rglob('*.jsonl'))
    if not files:
        raise ValueError(f'no go test JSONL under {directory}')
    for path in files:
        with path.open(encoding='utf-8') as stream:
            observe(stream, table)


def gh(*args):
    return subprocess.check_output(['gh', *args], text=True, timeout=120)


def read_run(run_id, table):
    listing = gh('api', f'repos/{REPO}/actions/runs/{run_id}/artifacts?per_page=100')
    artifacts = json.loads(listing)['artifacts']
    selected = [a for a in artifacts if a['name'].startswith('go-') and not a['expired']]
    if not selected:
        raise ValueError(f'run {run_id} has no go-* artifacts')
    for artifact in selected:
        # The API form is the documented one: gh run download fails on some names.
        raw = subprocess.check_output(['gh', 'api', f"repos/{REPO}/actions/artifacts/{artifact['id']}/zip"],
                                      timeout=120)
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            names = sorted(name for name in archive.namelist() if name.endswith('.jsonl'))
            if not names:
                raise ValueError(f"artifact {artifact['name']} has no go test JSONL")
            for name in names:
                with archive.open(name) as member:
                    observe(io.TextIOWrapper(member, encoding='utf-8'), table)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--results', type=Path, action='append', default=[],
                        help='Directory containing an unzipped go-N artifact (repeatable)')
    parser.add_argument('--runs', default='', help='Comma-separated merge-tier workflow run IDs')
    parser.add_argument('--output', type=Path, default=Path(__file__).with_name('merge_durations.json'))
    args = parser.parse_args()
    runs = [run.strip() for run in args.runs.split(',') if run.strip()]
    if not args.results and not runs:
        parser.error('provide --results and/or --runs')
    table = {}
    for directory in args.results:
        read_results(directory, table)
    for run in runs:
        if not run.isdigit():
            parser.error('run IDs are numeric: ' + run)
        read_run(run, table)
    if not table:
        raise ValueError('no parent test durations found')
    data = {key: round(value, 2) for key, value in sorted(table.items())}
    temporary = args.output.with_suffix('.tmp')
    temporary.write_text(json.dumps(data, indent=1, allow_nan=False) + '\n')
    temporary.replace(args.output)
    sys.stdout.write(f'{len(data)} parent tests, {sum(data.values()):.0f}s total -> {args.output}\n')


if __name__ == '__main__':
    main()
