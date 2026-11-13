"""The same exact Go failure policy for package CI and whole release suites."""
import datetime
import json
import os
from pathlib import Path
import re
import sys

def policy(data, today=None, engine=None):
    today=today or datetime.date.today(); entries={}
    # An entry scoped to an engine ("development": the release gate on a development-engine
    # release, PORTICO_CI_ENGINE=development) applies only to runs on that engine; everywhere else,
    # merge CI and qualified-engine releases included, the test runs and must pass.
    engine=os.environ.get('PORTICO_CI_ENGINE','') if engine is None else engine
    for e in data['entries']:
        if not all(e.get(k) for k in ['package','test','owner','expires','reason']): raise ValueError('incomplete quarantine')
        if e.get('engine') not in (None,'development'): raise ValueError('unknown quarantine engine')
        if datetime.date.fromisoformat(e['expires']) < today: raise ValueError('expired quarantine')
        if e.get('engine') and e['engine']!=engine: continue
        key=(e['package'],e['test'])
        if key in entries: raise ValueError('duplicate quarantine')
        entries[key]=e
    if entries and data.get('status')!='approved': raise ValueError('quarantine not approved')
    return entries

FAILURE_STATUSES = frozenset({'test_failed', 'timeout', 'panic_or_race', 'build_failed',
                              'incomplete_events', 'infrastructure'})


def classify(events, expected, returncode, allowed, required_pass=(), *, stderr='', timed_out=False,
             incomplete=False, infrastructure=False):
    terminals={}; failures=set(); started=set(); package_terminal=None
    timeout=timed_out; dangerous=False; build_failed=False
    for e in events:
        output=e.get('Output','')
        if re.match(r'^panic: test timed out after',output): timeout=True
        if re.match(r'^WARNING: DATA RACE',output): dangerous=True
        if e.get('Action')=='build-fail' or re.match(r'^FAIL\s+.*\[build failed\]',output): build_failed=True
        if re.match(r'^(?:panic: |fatal error:)',output):
            name=e.get('Test')
            terminal=next((x.get('Action') for x in reversed(events) if x.get('Test')==name and x.get('Action') in ['pass','fail','skip']),None)
            if terminal not in ['pass','skip']: dangerous=True
        name=e.get('Test'); action=e.get('Action')
        if name and action=='run': started.add(name)
        if not name and action in ['pass','fail','skip']: package_terminal=action
        if name and action in ['pass','fail','skip']: terminals[name]=action
        if name and action=='fail': failures.add(name.split('/')[0])
    if timeout or re.search(r'^panic: test timed out after',stderr,re.M): return 'timeout'
    if dangerous or re.search(r'^(?:WARNING: DATA RACE|panic: |fatal error:)',stderr,re.M): return 'panic_or_race'
    if build_failed or (returncode and re.search(r'^# \S+',stderr,re.M)): return 'build_failed'
    if infrastructure or returncode not in (0,1): return 'infrastructure'
    if incomplete or package_terminal is None or any(t not in terminals for t in set(expected)|started): return 'incomplete_events'
    for name in required_pass:
        exact=[e['Action'] for e in events if e.get('Test')==name and e.get('Action') in ('pass','fail','skip','cancelled','canceled')]
        if package_terminal!='pass' or exact!=['pass']: return 'test_failed'
    if failures:
        if returncode!=1 or package_terminal!='fail': return 'infrastructure'
        return 'quarantined' if failures.issubset(allowed) else 'test_failed'
    return 'pass' if returncode==0 and package_terminal in ['pass','skip'] else 'infrastructure'


def check_suite(text, returncode, data, engine=''):
    allowed = policy(data, engine=engine)
    if returncode not in (0, 1):
        raise ValueError('Go suite infrastructure failure: ' + str(returncode))
    packages = {}
    for line in text.splitlines():
        event = json.loads(line)
        if not isinstance(event, dict) or event.get('Action') == 'build-fail':
            raise ValueError('Go suite build or event failure')
        package = event.get('Package')
        if package:
            packages.setdefault(package, []).append(event)
        elif event.get('Action') != 'build-output':
            raise ValueError('Go suite event has no package')
    if not packages:
        raise ValueError('Go suite reported no packages')
    waived, failed_packages = [], 0
    for package, events in packages.items():
        terminal = next((e['Action'] for e in reversed(events)
                         if not e.get('Test') and e.get('Action') in ('pass', 'fail', 'skip')), None)
        failed_packages += terminal == 'fail'
        expected = {e['Test'] for e in events if e.get('Test') and e.get('Action') == 'run'}
        exceptions = {test for pkg, test in allowed if pkg == package}
        required = {e['test'] for e in data['entries'] if e['package'] == package} if engine != 'development' else set()
        status = classify(events, expected, int(terminal == 'fail'), exceptions, required_pass=required)
        if status not in ('pass', 'quarantined'):
            raise ValueError(package + ': ' + status)
        if status == 'quarantined':
            failures = {e['Test'].split('/')[0] for e in events if e.get('Test') and e.get('Action') == 'fail'}
            waived.extend(package + '.' + test + ': ' + allowed[package, test]['reason'] for test in sorted(failures))
    if bool(returncode) != bool(failed_packages):
        raise ValueError('Go suite exit disagrees with its package results')
    return waived


if __name__ == '__main__':
    if len(sys.argv) != 4:
        raise SystemExit('usage: go_test_policy.py RESULTS EXIT_CODE ENGINE_MANIFEST')
    data = json.loads((Path(__file__).parent / 'quarantine.json').read_text())
    engine = 'development' if json.loads(Path(sys.argv[3]).read_text()).get('development') is True else ''
    for message in check_suite(Path(sys.argv[1]).read_text(), int(sys.argv[2]), data, engine):
        print('Development-engine exception: ' + message)
