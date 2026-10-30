#!/usr/bin/env python3
"""Fail closed on incomplete 300k evidence and expose the measured curves."""
import argparse
import json
from pathlib import Path
from quota import private_json


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--markers', required=True)
    p.add_argument('--load', required=True)
    p.add_argument('--out', required=True)
    a = p.parse_args()
    markers = [json.loads(line) for line in Path(a.markers).read_text().splitlines() if line.strip()]
    load = json.loads(Path(a.load).read_text())
    buckets = [row for row in markers if row.get('event') == 'phase-end']
    resident = [row for row in markers if row.get('event') == 'resident-read']
    ends = [row for row in markers if row.get('event') == 'fixture-end']
    convergence = [row for row in markers if row.get('event') == 'convergence']
    findings = []
    expected = list(range(50000, 300001, 50000))
    if [row.get('accepted') for row in buckets] != expected:
        findings.append('missing or incorrect six exact 50k buckets')
    if [row.get('accepted') for row in resident] != [50000, 150000, 300000]:
        findings.append('missing resident reads at50k/150k/300k')
    if not convergence or not convergence[-1].get('passed'):
        findings.append('counts/facets/availability not reconciled')
    if not ends or ends[-1].get('accepted') != 300000 or not 0 < ends[-1].get('ingestElapsedSeconds', 0) <= 7200:
        findings.append('ingest incomplete or exceeds7200s')
    if load.get('kind') != 'ingest-noncapacity' or load.get('ingestTrendQualified') is not True or load.get('passed') is not True:
        findings.append('daily-use trend or user gates failed/incomplete')
    for row in buckets:
        for field in ('ingestPerSecond', 'writerMsPerItem', 'writerShare', 'heapAllocBytes', 'totalPhysicalBytes'):
            if not isinstance(row.get(field), (int, float)):
                findings.append(f"bucket{row.get('phase')} missing {field}")
        if row.get('unavailable'):
            findings.append(f"bucket{row.get('phase')} has unavailable measures")
        if row.get('totalPhysicalBytes', 0) >= 7 * 1024**3:
            findings.append('7GiB stop reached')
    if len(buckets) == 6:
        first = buckets[0]
        for row in buckets[1:]:
            if row.get('writerMsPerItem', 0) > first.get('writerMsPerItem', 0) * 1.2 + 1:
                findings.append(f"writer time/item climbs at{row.get('accepted')} items")
            if row.get('ingestPerSecond', 0) < first.get('ingestPerSecond', 0) * .8:
                findings.append(f"ingest rate declines at{row.get('accepted')} items")
    if resident:
        for row in resident[1:]:
            for op, value in row.get('reads', {}).items():
                if op == 'overall':
                    continue
                baseline = resident[0].get('reads', {}).get(op, {})
                if value.get('p50Ms', 0) > baseline.get('p50Ms', 0)*1.2+10 or value.get('p99Ms', 0) > baseline.get('p99Ms', 0)*1.2+50:
                    findings.append(f"resident {op} climbs at{row.get('accepted')} items")
    report = {'kind': 'p2-65-ingest-trend', 'passed': not findings,
              'basis': 'real scan, distinct tiny files; daily-use trend, no80/100-user capacity claim',
              'findings': findings, 'buckets': buckets, 'resident': resident,
              'userPhases': load.get('phases'), 'userTrend': load.get('trend'),
              'convergence': convergence[-1] if convergence else None,
              'elapsedSeconds': ends[-1].get('elapsedSeconds') if ends else None,
              'ingestElapsedSeconds': ends[-1].get('ingestElapsedSeconds') if ends else None,
              'followUp': 'Record any failed operation and full curve in OUTSTANDING.md before accepting the round'}
    private_json(a.out, report)
    print(json.dumps({'passed': not findings, 'findings': findings, 'out': a.out}))
    if findings:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
