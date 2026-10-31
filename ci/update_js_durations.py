#!/usr/bin/env python3
"""Import durations from a complete, passing set of exact-plan JS receipts."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
from js_shards import digest, reconcile


def update(existing, plan, records, source):
    seconds = dict(existing['seconds'])
    for record in records.values():
        for result in record['files']:
            seconds[result['file']] = result['seconds']
    provenance = {'head': plan['head'], 'plan_sha256': digest(plan), 'source': source,
                  'captured_at': datetime.now(timezone.utc).isoformat(),
                  'files': sum(len(r['files']) for r in records.values()),
                  'node_versions': sorted({r['node_version'] for r in records.values()})}
    return {'version': 1, 'provenance': [*existing['provenance'], provenance], 'seconds': dict(sorted(seconds.items()))}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', type=Path, required=True)
    parser.add_argument('--results', type=Path, required=True)
    parser.add_argument('--source', required=True, help='Public run URL or local measurement description')
    parser.add_argument('--output', type=Path, default=Path(__file__).with_name('js-durations.json'))
    args = parser.parse_args()
    plan = json.loads(args.plan.read_text())
    records = reconcile(plan, args.results)
    data = update(json.loads(args.output.read_text()), plan, records, args.source)
    temporary = args.output.with_suffix('.tmp')
    temporary.write_text(json.dumps(data, indent=2, allow_nan=False) + '\n')
    temporary.replace(args.output)


if __name__ == '__main__':
    main()
