#!/usr/bin/env python3
"""Read hosted CI timings and append timestamped repository-capacity evidence.

Uses only the standard library and the authenticated gh CLI. No workflow is started.
"""
import argparse
from collections import Counter, defaultdict
from datetime import datetime, timezone
import json
import math
from pathlib import Path
import re
import subprocess
from urllib.parse import urlencode


def timestamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')) if value else None


def api(path):
    return json.loads(subprocess.check_output(['gh', 'api', path], text=True, timeout=90))


def pages(path, key, fetch=api):
    result = []
    for page in range(1, 1001):
        data = fetch(path + ('&' if '?' in path else '?') + urlencode({'per_page': 100, 'page': page}))
        items = data[key]
        result.extend(items)
        if len(items) < 100:
            return result
    raise ValueError('API pagination exceeded 1000 pages; refusing a partial report')


def started(job):
    # Queued jobs may carry started_at == created_at. Canceled/skipped jobs can
    # also retain that placeholder without ever receiving a runner.
    if job['status'] != 'in_progress' and not (
        job['status'] == 'completed' and job.get('conclusion') != 'skipped'
        and (job.get('runner_id') or any(s.get('started_at') for s in job.get('steps', [])))
    ):
        return None
    return timestamp(job.get('started_at'))


def seconds(begin, end):
    return max(0.0, (end - begin).total_seconds()) if begin and end else None


def job_timing(job):
    begin = started(job)
    end = timestamp(job.get('completed_at')) if job['status'] == 'completed' else None
    return {
        'id': job['id'], 'name': job['name'], 'status': job['status'],
        'conclusion': job.get('conclusion'), 'created_at': job.get('created_at'),
        'started_at': begin.isoformat() if begin else None,
        'completed_at': end.isoformat() if end else None,
        'queue_seconds': seconds(timestamp(job.get('created_at')), begin),
        'run_seconds': seconds(begin, end),
        'labels': job.get('labels', []), 'runner_name': job.get('runner_name'),
        'steps': [{'name': s['name'], 'conclusion': s.get('conclusion'),
                   'seconds': seconds(timestamp(s.get('started_at')), timestamp(s.get('completed_at')))}
                  for s in job.get('steps', [])],
    }


def summarize(run, jobs):
    rows = [job_timing(j) for j in jobs]
    completed = [timestamp(j['completed_at']) for j in rows if j['completed_at']]
    end = max(completed) if completed else None
    finished = bool(jobs) and run['status'] == 'completed' and all(
        j['status'] == 'completed' and (j.get('conclusion') == 'skipped' or
        (started(j) and j.get('completed_at'))) for j in jobs)
    # The run creation belongs to attempt one. A rerun's API exposes its own
    # start, but not a reliable attempt-request timestamp; label that boundary.
    origin = run.get('run_started_at') if run.get('run_attempt', 1) > 1 else run['created_at']
    # Jobs API has no needs graph. Identify the gate's latest-finishing
    # dependency rather than claiming to recover an arbitrary workflow DAG.
    dependencies = [j for j in rows if j['name'] not in ('ci/required', 'ci/short', 'ci/merge-queue')
                    and j['completed_at'] and j['run_seconds'] is not None]
    critical = max(dependencies, key=lambda j: timestamp(j['completed_at']), default=None)
    planner = next((j for j in rows if j['name'] == 'planner'), None)
    gate = next((j for j in rows if j['name'] == 'ci/required'), None)
    return {
        'id': run['id'], 'attempt': run.get('run_attempt', 1), 'event': run['event'],
        'status': run['status'], 'conclusion': run.get('conclusion'), 'head_sha': run['head_sha'],
        'url': run['html_url'], 'created_at': run['created_at'],
        'wall_origin': 'attempt_start' if run.get('run_attempt', 1) > 1 else 'run_created',
        'wall_seconds': seconds(timestamp(origin), end) if finished else None,
        'runner_minutes': sum(j['run_seconds'] or 0 for j in rows) / 60 if finished else None,
        'latest_gate_dependency': critical['name'] if critical else None,
        'planner_to_dependency_seconds': seconds(timestamp(planner['started_at']), timestamp(critical['completed_at']))
            if planner and critical else None,
        'gate_overhead_seconds': seconds(timestamp(critical['completed_at']), timestamp(gate['completed_at']))
            if critical and gate else None,
        'jobs': rows,
    }


def percentile(values, fraction):
    # Nearest rank: explicit even for small baseline populations.
    return sorted(values)[max(0, math.ceil(len(values) * fraction) - 1)] if values else None


def event_stats(reports):
    result = {}
    for event in ('pull_request', 'merge_group'):
        group = [r for r in reports if r['event'] == event]
        values = [r['wall_seconds'] for r in group if r['conclusion'] == 'success' and r['wall_seconds'] is not None]
        result[event] = {'observed': len(group), 'successful_completed': len(values),
                         'conclusions': dict(Counter(r['conclusion'] or r['status'] for r in group)),
                         'p50_seconds': percentile(values, .5), 'p90_seconds': percentile(values, .9),
                         'p95_seconds': percentile(values, .95)}
    return result


def concurrency_timeline(jobs, until):
    """Reconstruct intervals only for the supplied jobs, not account entitlement."""
    events = defaultdict(Counter)
    for job in jobs:
        created = timestamp(job.get('created_at'))
        if not created or job.get('conclusion') == 'skipped':
            continue
        begin = started(job)
        end = timestamp(job.get('completed_at')) if job['status'] == 'completed' else None
        if begin:
            events[created]['queued'] += 1
            events[begin]['queued'] -= 1
            events[begin]['in_progress'] += 1
            events[end or until]['in_progress'] -= 1
        else:
            events[created]['queued'] += 1
            events[end or until]['queued'] -= 1
    counts = Counter()
    rows = []
    for when, delta in sorted(events.items()):
        if when >= until:
            break
        counts.update(delta)
        rows.append({'at': when.isoformat(), 'in_progress': counts['in_progress'], 'queued': counts['queued']})
    return rows


def capacity_snapshot(repo, fetch=api):
    observed_from = datetime.now(timezone.utc).isoformat()
    runs = {}
    for status in ('queued', 'in_progress', 'waiting', 'pending', 'requested'):
        for run in pages(f'repos/{repo}/actions/runs?status={status}', 'workflow_runs', fetch):
            runs[run['id']] = run
    jobs = {}
    for run in runs.values():
        for job in pages(f"repos/{repo}/actions/runs/{run['id']}/attempts/{run.get('run_attempt', 1)}/jobs", 'jobs', fetch):
            if job['status'] != 'completed':
                jobs[job['id']] = {'id': job['id'], 'run_id': run['id'], 'name': job['name'],
                                  'status': job['status'], 'created_at': job.get('created_at'),
                                  'started_at': started(job).isoformat() if started(job) else None,
                                  'labels': job.get('labels', []), 'runner_name': job.get('runner_name')}
    statuses = Counter(j['status'] for j in jobs.values())
    return {'repository': repo, 'observed_from': observed_from,
            'observed_until': datetime.now(timezone.utc).isoformat(),
            'scope': 'All nonterminal jobs found in active repository workflow runs; sequential API observation, not an atomic or organization-wide snapshot.',
            'in_progress': statuses['in_progress'],
            'queued': sum(n for state, n in statuses.items() if state != 'in_progress'),
            'statuses': dict(statuses), 'jobs': list(jobs.values())}


def capacity_history(path, repo):
    rows=[]
    for line in path.read_text().splitlines():
        if not line.strip(): continue
        sample=json.loads(line)
        if sample['repository']!=repo: continue
        rows.append({k:sample[k] for k in ('observed_from','observed_until','in_progress','queued')})
    return rows


def display(data):
    for event, stats in data['events'].items():
        print(f"{event}: {json.dumps(stats, sort_keys=True)}")
    for run in data['runs']:
        print(f"\n{run['url']} attempt {run['attempt']} {run['event']} {run['status']}/{run['conclusion']}")
        print(f"wall={run['wall_seconds']}s runner-minutes={run['runner_minutes']} latest gate dependency={run['latest_gate_dependency']}")
        print('job | status | queue seconds | run seconds')
        for job in run['jobs']:
            print(f"{job['name']} | {job['status']}/{job['conclusion']} | {job['queue_seconds']} | {job['run_seconds']}")
    if data.get('capacity'):
        sample = data['capacity']
        print(f"\nRepository capacity observed {sample['observed_from']} to {sample['observed_until']}: "
              f"{sample['in_progress']} in progress, {sample['queued']} queued/waiting. {sample['scope']}")

    if data.get('capacity_history'):
        print('\nObserved repository capacity over time (sequential snapshots):')
        print('observation start | observation end | in progress | queued/waiting')
        for row in data['capacity_history']:
            print(f"{row['observed_from']} | {row['observed_until']} | {row['in_progress']} | {row['queued']}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('runs', nargs='*', type=int)
    parser.add_argument('--repo', default='PorticoMediaServer/portico')
    parser.add_argument('--since', help='UTC cutoff for Merge checks runs, e.g. 2026-10-31T18:45:00Z')
    parser.add_argument('--json', action='store_true', help='Full report, including step times and reconstructed selected-run concurrency')
    parser.add_argument('--snapshot', type=Path, help='Append one live all-workflow capacity observation to this JSONL file; rerun to collect a time series')
    parser.add_argument('--history', type=Path, help='Report an existing capacity JSONL time series without live API calls')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', args.repo):
        parser.error('repo must be OWNER/REPO')
    if not args.runs and not args.since and not args.snapshot and not args.history:
        parser.error('provide run IDs, --since, --snapshot or --history')
    runs = {}
    if args.since:
        timestamp(args.since)
        path = f'repos/{args.repo}/actions/workflows/merge.yml/runs?' + urlencode({'created': '>=' + args.since})
        runs.update((r['id'], r) for r in pages(path, 'workflow_runs'))
    for run_id in args.runs:
        run = api(f'repos/{args.repo}/actions/runs/{run_id}')
        runs[run['id']] = run
    reports, all_jobs = [], []
    for run in sorted(runs.values(), key=lambda r: r['id']):
        jobs = pages(f"repos/{args.repo}/actions/runs/{run['id']}/attempts/{run.get('run_attempt', 1)}/jobs", 'jobs')
        all_jobs.extend(jobs)
        reports.append(summarize(run, jobs))
    data = {'repository': args.repo, 'observed_at': datetime.now(timezone.utc).isoformat(),
            'events': event_stats(reports), 'runs': reports,
            'selected_run_concurrency': concurrency_timeline(all_jobs, datetime.now(timezone.utc))}
    if args.snapshot:
        sample = capacity_snapshot(args.repo)
        args.snapshot.parent.mkdir(parents=True, exist_ok=True)
        with args.snapshot.open('a') as stream:
            stream.write(json.dumps(sample, sort_keys=True) + '\n')
        data['capacity'] = sample
    history=args.history or args.snapshot
    if history:
        data['capacity_history']=capacity_history(history,args.repo)
    if args.json:
        print(json.dumps(data, indent=2))
    else:
        display(data)


if __name__ == '__main__':
    main()
