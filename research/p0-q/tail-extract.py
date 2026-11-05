#!/usr/bin/env python3
"""Extract historical retirement tails; never reconstruct absent payload custody."""
import argparse, collections, hashlib, json
from pathlib import Path

ap = argparse.ArgumentParser()
ap.add_argument('input', type=Path)
ap.add_argument('output', type=Path)
a = ap.parse_args()
results = []
for path in sorted(a.input.glob('*/result.jsonl')):
    env = None
    events = collections.defaultdict(list)
    operations = {}
    for line in path.open():
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if not isinstance(e, dict) or 'event' not in e:
            continue
        kind, data = e['event'], e['data']
        if kind == 'environment':
            env = data
        elif kind == 'operation':
            operations[data['tag']] = data['latency']
        elif kind in ('slow_writer', 'native_cpu_sample', 'passive_checkpoint'):
            events[kind].append(e)
    if not env or env['items'] != 1000000:
        continue
    def envelope(t):
        cps = events['passive_checkpoint']
        previous = [e for e in cps if e['data']['end_unix_ns'] <= t['start_unix_ns']]
        following = [e for e in cps if e['data']['start_unix_ns'] >= t['end_unix_ns']]
        return {'intersecting_checkpoints': [e for e in cps
                if e['data']['start_unix_ns'] < t['end_unix_ns']
                and t['start_unix_ns'] < e['data']['end_unix_ns']],
            'previous_completed_checkpoint': max(previous, key=lambda e:e['data']['end_unix_ns'], default=None),
            'next_checkpoint': min(following, key=lambda e:e['data']['start_unix_ns'], default=None),
            'scope': 'CP-attached WAL observations, not pre/post transaction samples; never subtract across different salts'}
    phases = []
    for tag in ('generation.retire_head.tx', 'generation.retire.reclaim.tx'):
        tails = [e for e in events['slow_writer'] if e['data']['tag'] == tag]
        selected = max(tails, key=lambda e: e['data']['ms'], default=None)
        entry = {'tag': tag, 'population': operations.get(tag), 'over_25_ms_count': len(tails), 'worst': selected}
        if selected:
            t = selected['data']
            commits = [e for e in events['native_cpu_sample']
                       if e['data']['tag'] == tag[:-3]+'.commit'
                       and t['start_unix_ns'] <= e['data']['start_unix_ns']
                       and e['data']['end_unix_ns'] <= t['end_unix_ns']]
            entry['matching_commit_samples'] = commits
            entry.update(envelope(t))
        entry['unrecorded_fields'] = {
            'subject_ids': None, 'old_image_ids': None, 'bank_ids_per_head': None,
            'fragment_ids': None, 'actual_tail_row_count': None,
            'old_prepared_bytes': None, 'native_work_per_head': None,
            'per_statement_vm_pages': None, 'per_statement_timelines': None,
            'physical_return_separate_from_telemetry': None,
        }
        phases.append(entry)
    idle = [e for e in events['native_cpu_sample']
        if e['data']['tag'] == 'generation.retire_head.commit'
        and e['data']['ms'] > 25 and not envelope(e['data'])['intersecting_checkpoints']]
    idle = max(idle, key=lambda e:e['data']['ms'], default=None)
    results.append({'artifact': path.parent.name, 'raw_sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
        'schema': env['candidate_sha256'], 'engine': env['sqlite'],
        'scope': 'Historical P0 fixed-batch baseline; no reconstructed payload or owned-command qualification',
        'phases': phases,
        'checkpoint_idle_over_25_ms': {'commit':idle,'envelope':envelope(idle['data']) if idle else None},
        'all_recorded_retirement_slow_events': [e for kind in ['slow_writer','native_cpu_sample'] for e in events[kind]
            if e['data']['tag'].startswith(('generation.retire_head.','generation.retire.reclaim.'))]})
a.output.write_text(json.dumps(results, indent=2, ensure_ascii=False)+'\n')
for r in results:
    print(r['artifact'])
    for p in r['phases']:
        print(p['tag'], p['over_25_ms_count'], p['population'],
              [(e['data']['ms'], e['data']['thread_cpu_ms'], e['data']['sched_wait_ms']) for e in p.get('matching_commit_samples', [])],
              [e['data']['ms'] for e in p.get('intersecting_checkpoints', [])])
