#!/usr/bin/env python3
"""One-command refresh of ci/merge_durations.json from passing PR go_tests runs.

For every supplied PR number or head SHA this finds the newest completed run
whose `go_tests` shards passed and that still has non-expired `go-N` artifacts
(an unrelated failing job such as hygiene does not disqualify it), reads those
`go test -json` JSONL files with the existing importer, and keeps the longest
`Elapsed` per parent. Parents already in the table are preserved. The table is
rewritten atomically and a summary is printed.

  python3 ci/refresh_go_durations.py --pr 1915 --pr 1920 --pr 1921 --pr 1922
  python3 ci/refresh_go_durations.py --pr 1915,1920 --head <40-character-head-sha>
  python3 ci/refresh_go_durations.py --pr 1915 --inventory ci-plan.json

Run once per train before enqueue. A head with no passing run that kept `go-N`
artifacts fails with a clear error and nothing is written. `--inventory` is an
optional saved planner output (`ci-plan.json`); when supplied, the summary also
reports the modeled shard count for that exact inventory. Without it, run the
planner dry-run to size the new inventory.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys

import plan
import update_merge_durations

REPO = 'PorticoMediaServer/portico'
HEAD_PATTERN = re.compile('[0-9a-f]{40}')


class RefreshError(ValueError):
    pass


def gh_json(endpoint):
    return json.loads(subprocess.check_output(['gh', 'api', endpoint], text=True, timeout=120))


def successful_runs(runs):
    """Newest first; completed runs that may carry a full go_tests result."""
    chosen = [run for run in runs
              if run.get('status') == 'completed' and run.get('conclusion') in ('success', 'failure')]
    return sorted(chosen, key=lambda run: (run.get('created_at', ''), int(run.get('id', 0))),
                  reverse=True)


def go_artifacts(artifacts):
    return [artifact for artifact in artifacts
            if str(artifact.get('name', '')).startswith('go-') and not artifact.get('expired')]


def go_tests_passed(run_id, jobs_for):
    """A failed run still counts when every go_tests shard itself passed."""
    jobs = [job for job in jobs_for(run_id) if str(job.get('name', '')).startswith('go_tests')]
    return bool(jobs) and all(job.get('conclusion') == 'success' for job in jobs)


def newest_go_run(runs, artifacts_for, jobs_for=None):
    """The newest completed run whose go_tests passed and kept go-N artifacts."""
    for run in successful_runs(runs):
        if run.get('conclusion') != 'success':
            if jobs_for is None or not go_tests_passed(run['id'], jobs_for):
                continue
        if go_artifacts(artifacts_for(run['id'])):
            return run
    return None


def head_for_pr(number):
    payload = gh_json(f'repos/{REPO}/pulls/{number}')
    head = str(payload.get('head', {}).get('sha', ''))
    if not HEAD_PATTERN.fullmatch(head):
        raise RefreshError(f'PR #{number} has no 40-character head SHA')
    return head


def runs_for_head(head):
    payload = gh_json(f'repos/{REPO}/actions/runs?head_sha={head}&per_page=100')
    return payload.get('workflow_runs', [])


def artifacts_for_run(run_id):
    payload = gh_json(f'repos/{REPO}/actions/runs/{run_id}/artifacts?per_page=100')
    return payload.get('artifacts', [])


def jobs_for_run(run_id):
    payload = gh_json(f'repos/{REPO}/actions/runs/{run_id}/jobs?per_page=100')
    return payload.get('jobs', [])


def summarize(before, after):
    """(parents added, parents whose measured longest time increased)."""
    added = sorted(set(after) - set(before))
    changed = sorted(key for key in after if key in before and after[key] > before[key])
    return added, changed


def resolve_runs(targets, runs_for, artifacts_for, jobs_for):
    """Pick one passing run per (label, head); fail clearly if a head has none."""
    resolved = []
    for label, head in targets:
        run = newest_go_run(runs_for(head), artifacts_for, jobs_for)
        if run is None:
            raise RefreshError(f'{label} ({head}): no completed run whose go_tests passed with '
                               'non-expired go-N artifacts; wait for go_tests to pass or rerun that head')
        resolved.append((label, head, run))
    return resolved


def modeled_shard_count(inventory_path, table, durations_path):
    """Shard count for a saved planner inventory, using the refreshed table."""
    payload = json.loads(inventory_path.read_text())
    inventory = payload.get('inventory')
    all_inventory = payload.get('all_inventory')
    if not inventory or not all_inventory:
        raise RefreshError(f'{inventory_path} is not a planner output with inventory/all_inventory')
    durations = json.loads(durations_path.read_text())
    durations = {str(key).replace('portico.local/server', 'github.com/PorticoMediaServer/portico/server'): value
                 for key, value in durations.items()}
    durations.update({str(key): float(value) for key, value in table.items()})
    all_names = {}
    for test in all_inventory:
        all_names.setdefault(test['package'], set()).add(test['name'])
    bins, _ = plan.balance_merge(inventory, durations, all_names)
    return len(bins)


def main():
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--pr', action='append', default=[],
                        help='PR number (repeatable or comma-separated)')
    parser.add_argument('--head', action='append', default=[],
                        help='40-character head SHA (repeatable or comma-separated)')
    parser.add_argument('--inventory', type=Path,
                        help='Optional saved planner output for a modeled shard count')
    parser.add_argument('--output', type=Path,
                        default=Path(__file__).with_name('merge_durations.json'))
    args = parser.parse_args()
    prs = [item.strip() for value in args.pr for item in value.split(',') if item.strip()]
    heads = [item.strip() for value in args.head for item in value.split(',') if item.strip()]
    if not prs and not heads:
        parser.error('provide at least one --pr or --head')
    for number in prs:
        if not number.isdigit():
            parser.error('PR numbers are numeric: ' + number)
    for head in heads:
        if not HEAD_PATTERN.fullmatch(head):
            parser.error('head SHAs must be 40 lowercase hex characters: ' + head)

    before = update_merge_durations.load_table(args.output)
    table = dict(before)
    targets = [(f'PR #{number}', head_for_pr(number)) for number in prs]
    targets.extend((f'head {head}', head) for head in heads)

    used = []
    for label, head, run in resolve_runs(targets, runs_for_head, artifacts_for_run, jobs_for_run):
        update_merge_durations.read_run(run['id'], table)
        used.append((label, head, run['id']))

    added, changed = summarize(before, table)
    data = update_merge_durations.write_table(table, args.output)

    print(f'wrote {args.output}: {len(data)} parents, {sum(data.values()):.0f}s modeled')
    print(f'parents added: {len(added)}; longest time increased: {len(changed)}')
    for key in added:
        print(f'  + {key} {data[key]:.2f}s')
    for key in changed:
        print(f'  ~ {key} {before[key]:.2f}s -> {data[key]:.2f}s')
    for label, head, run_id in used:
        print(f'source {label}: {head} run {run_id}')
    if args.inventory:
        try:
            count = modeled_shard_count(args.inventory, table, Path(__file__).with_name('durations.json'))
        except (OSError, ValueError, KeyError) as error:
            raise RefreshError(f'cannot model {args.inventory}: {error}')
        print(f'modeled shards for {args.inventory}: {count}')
    else:
        print('modeled shard count not computed: rerun the planner dry-run '
              '(python3 ci/plan.py --tier merge) or pass --inventory ci-plan.json')


if __name__ == '__main__':
    try:
        main()
    except RefreshError as error:
        sys.exit(f'error: {error}')
