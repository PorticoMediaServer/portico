"""Branch-only diagnostic. The overlay forces a legal smaller budget, never a larger cap."""
import json
from pathlib import Path
import subprocess

root = Path.cwd()
out = root / 'evidence'
out.mkdir()
(out / 'source-sha.txt').write_bytes(subprocess.check_output(['git', 'rev-parse', 'HEAD']))
(out / 'go-version.txt').write_bytes(subprocess.check_output(['go', 'version']))
tags = subprocess.check_output(['python3', 'scripts/release-build-tags.py', '--capability'], text=True).strip()
parents = [
    'TestReadingCompleteMultiPageScanAllowsRealHTTPSignIn',
    'TestReadingDiscoverOperationStrictAndBounded',
    'TestReadingEPUBOversizedMemberRefusedAtOpen',
    'TestReadingInitialFacetRetryBacksOffAfterFirstPoll',
    'TestReadingOpenSurvivesAFailedFactAdoption',
    'TestReadingPrivatePathMovedFolderWithdrawsOldAttribution',
    'TestReadingPrivatePathPublicationCoversEveryCandidate',
    'TestRestrictedProfileWriteRouteMatrix',
]
path = root / 'server/internal/ingestion/page_budget.go'
text = path.read_text()
start = text.index('func nextPageNames(')
end = text.index('// forgetPageBudget', start)
forced = out / 'legal-smaller-budget.go'
forced.write_text(text[:start] + '''// Force one valid adaptive reduction at each committed page, without sleeping.
func nextPageNames(budget, used int, held time.Duration) int {
    return max(1, min(budget, inventorySourceNames)/2)
}

''' + text[end:])
overlay = out / 'adaptive-overlay.json'
overlay.write_text(json.dumps({'Replace': {str(path): str(forced)}}))

def run(label, package, names, count, overlay=None):
    args = ['go', 'test', '-C', 'server', '-p', '1', '-json', '-count=' + str(count), '-timeout=780s', '-tags', tags]
    if overlay:
        args += ['-overlay', str(overlay)]
    args += ['-run', '^(' + '|'.join(names) + ')$', './internal/' + package]
    with (out / (label + '.jsonl')).open('w') as stdout, (out / (label + '.stderr')).open('w') as stderr:
        result = subprocess.run(args, stdout=stdout, stderr=stderr)
    (out / (label + '.exit')).write_text(str(result.returncode) + '\n')
    events = [json.loads(line) for line in (out / (label + '.jsonl')).read_text().splitlines()]
    for parent in names:
        actual = [e['Action'] for e in events if e.get('Test') == parent and e.get('Action') in ['pass', 'fail', 'skip']]
        assert actual == ['fail'] * count, (label, parent, actual)
    assert result.returncode != 0, label
    return events

controls = ['TestRetainedProducerAdaptiveBudgetDiagnostic', 'TestRetainedRealDirectoryAdaptiveBudgetDiagnostic']
events = run('portable-boundary', 'storage', controls, 3)
for parent in controls:
    for limit, outcome in [('16', 'pass'), ('8', 'fail')]:
        actual = [e['Action'] for e in events if e.get('Test') == parent + '/' + limit and e.get('Action') in ['pass', 'fail', 'skip']]
        assert actual == [outcome] * 3, (parent, limit, actual)
run('http-legal-smaller-budget', 'httpapi', parents, 1, overlay)
(out / 'accepted.txt').write_text('Both real/synthetic fixed-budget EOF controls pass; changed-budget controls fail. All eight HTTP parents fail on the legal smaller-budget boundary. No missing/skipped terminals.\n')
