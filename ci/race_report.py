#!/usr/bin/env python3
"""Observe the recorded Go shards under -race without changing gate verdicts."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess

from plan import ROOT
from run import go


TIMING = re.compile(r'context deadline exceeded|deadline exceeded|timed out|timeout', re.I)


def inspect(stdout, stderr, expected, returncode, timed_out=False):
    events = []
    raw = []
    for line in stdout.splitlines():
        try:
            event = json.loads(line)
            if not isinstance(event, dict): raise ValueError('not an event')
            events.append(event)
        except ValueError:
            raw.append(line)
    outputs = [(e.get('Package', ''), e.get('Test', ''), e.get('Output', '')) for e in events]
    outputs.extend([('', '', '\n'.join(raw)), ('', '', stderr)])
    # A failing exit, a race-related test failure, or an expected timeout log
    # alone is never a race finding. Keep the detector's actual stack report.
    races = []
    for package, test, output in outputs:
        for line in output.splitlines():
            if line.strip() == 'WARNING: DATA RACE':
                races.append({'package': package, 'test': test})
    terminals = {(e.get('Package', ''), e['Test']): e['Action'] for e in events
                 if e.get('Test') and e.get('Action') in ('pass', 'fail', 'skip')}
    started = {(e.get('Package', ''), e['Test']) for e in events
               if e.get('Test') and e.get('Action') == 'run'}
    failures = {key for key, action in terminals.items() if action == 'fail'}
    timing = {key for key in failures if any(
        (package, test) == key and TIMING.search(output) for package, test, output in outputs)}
    package_timeouts = sorted({package for package, test, output in outputs
                               if 'panic: test timed out after' in output})
    package_terminals = {e.get('Package'): e['Action'] for e in events
                         if not e.get('Test') and e.get('Action') in ('pass', 'fail', 'skip')}
    return {'race_findings': races,
            'timing_failures': [list(key) for key in sorted(timing)],
            'package_timeouts': package_timeouts, 'watchdog_timeout': timed_out,
            'other_test_failures': [list(key) for key in sorted(failures - timing)],
            'missing_test_terminals': [list(key) for key in sorted((set(expected) | started) - set(terminals))],
            'missing_package_terminals': sorted({p for p, t in expected} - set(package_terminals)),
            'package_terminals': package_terminals, 'command_exit': returncode,
            'unparsed_stdout_lines': len(raw)}


class RaceReport:
    def __init__(self, plan, index, directory):
        self.path = Path(directory)
        self.path.mkdir(parents=True, exist_ok=True)
        self.data = {'schema': 1, 'shard': index, 'tier': plan['tier'],
                     'selected': plan['shards'][index], 'head': os.environ.get('GITHUB_SHA'),
                     'plan_sha256': hashlib.sha256(json.dumps(plan, sort_keys=True).encode()).hexdigest(),
                     'complete': False, 'batches': []}
        self.save()

    def save(self):
        temporary = self.path / 'report.json.tmp'
        temporary.write_text(json.dumps(self.data, indent=2) + '\n')
        temporary.replace(self.path / 'report.json')

    @staticmethod
    def run(args, cwd):
        process = subprocess.Popen(args, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, start_new_session=True)
        timed_out = False
        try:
            stdout, stderr = process.communicate(timeout=900)
        except subprocess.TimeoutExpired:
            timed_out = True
            try: os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError: pass
            stdout, stderr = process.communicate()
        return subprocess.CompletedProcess(args, process.returncode, stdout, stderr), timed_out

    def __call__(self, module, groups, result, timed_out):
        number = len(self.data['batches'])
        stem = f'batch-{number}'
        (self.path / (stem + '.jsonl')).write_text(result.stdout)
        (self.path / (stem + '.stderr')).write_text(result.stderr)
        expected = [(package, t['name']) for package, tests in groups for t in tests]
        batch = inspect(result.stdout, result.stderr, expected, result.returncode, timed_out)
        batch.update(module=module, command=result.args, stdout=stem + '.jsonl', stderr=stem + '.stderr')
        self.data['batches'].append(batch)
        self.save()

    def finish(self):
        self.data['complete'] = True
        self.save()
        batches = self.data['batches']
        count = lambda key: sum(len(b[key]) for b in batches)
        lines = [f"### Go race observer, shard {self.data['shard']}", '',
                 'Report only; this does not participate in merge or nightly required checks.', '',
                 f"Race findings (`WARNING: DATA RACE`): {count('race_findings')}",
                 f"Timing diagnostics: {count('timing_failures')} failed tests, "
                 f"{count('package_timeouts')} package deadlines, "
                 f"{sum(b['watchdog_timeout'] for b in batches)} watchdog deadlines.",
                 f"Other failed tests: {count('other_test_failures')}; "
                 f"missing test/package terminals: {count('missing_test_terminals')}/"
                 f"{count('missing_package_terminals')}.",
                 f"Nonzero command exits: {sum(b['command_exit'] != 0 for b in batches)}.", '',
                 'Raw detector stacks, test events, stderr and exact commands are in the race artifact.', '']
        for batch in batches:
            for finding in batch['race_findings']:
                lines.append(f"- Race: `{finding['package']}` `{finding['test']}` ({batch['stdout']}, {batch['stderr']})")
        summary = '\n'.join(lines) + '\n'
        (self.path / 'summary.md').write_text(summary)
        print(summary)
        if os.environ.get('GITHUB_STEP_SUMMARY'):
            with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as output: output.write(summary)
        if count('race_findings'):
            print('::warning::Go race detector reported findings; see the race artifact and job summary.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', default='ci-plan.json')
    parser.add_argument('--index', type=int, required=True)
    args = parser.parse_args()
    if os.environ.get('PORTICO_CI_GO_RACE') != '1':
        parser.error('PORTICO_CI_GO_RACE=1 is required for the race observer')
    plan = json.loads(Path(args.plan).read_text())
    report = RaceReport(plan, args.index, ROOT / 'ci-race-results' / f'shard-{args.index}')
    go(plan, args.index, report=report)
    report.finish()


if __name__ == '__main__':
    main()
