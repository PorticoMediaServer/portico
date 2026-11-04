"""Branch-only native original/fixed controls; never merge this harness."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

root = Path.cwd()
out = root / 'evidence'
out.mkdir()
code = 'd2677056271e2935c922f5eb77bef572abb2e09f'
subprocess.run(['git', 'diff', '--exit-code', code, '--', 'server', 'apikit'], check=True)
(out / 'source-sha.txt').write_text(code + '\n')
(out / 'workflow-sha.txt').write_bytes(subprocess.check_output(['git', 'rev-parse', 'HEAD']))
(out / 'source-archive-sha256.txt').write_text(hashlib.sha256(subprocess.check_output(['git', 'archive', code])).hexdigest() + '\n')
tags = subprocess.check_output(['python3', 'scripts/release-build-tags.py', '--capability'], text=True).strip()
(out / 'go-version.txt').write_bytes(subprocess.check_output(['go', 'version']))
paths = ['server/internal/storage/' + name for name in ['client.go', 'inventory_page.go', 'inventory_page_stream.go', 'operation_scope.go']]
paths += ['server/internal/ingestion/' + name for name in ['inventory.go', 'service.go']]
before = {}
for path in paths:
    target = out / 'baseline' / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(subprocess.check_output(['git', 'show', '9311ea4cef8eea07a71597de1e0e2dc0a7b85a25:' + path]))
    before[str(root / path)] = str(target)
budget = root / 'server/internal/ingestion/page_budget.go'
text = budget.read_text()
start, end = text.index('func nextPageNames('), text.index('// forgetPageBudget')
forced = out / 'legal-smaller-budget.go'
forced.write_text(text[:start] + '''func nextPageNames(budget, used int, held time.Duration) int {
    return max(1, min(budget, inventorySourceNames)/2)
}

''' + text[end:])

def overlay(name, replacements):
    path = out / (name + '-overlay.json')
    path.write_text(json.dumps({'Replace': replacements}))
    return path

def run(label, package, parents, count, expected, replace=None):
    args = ['go', 'test', '-C', 'server', '-p', '1', '-json', '-count=' + str(count), '-timeout=780s', '-tags', tags]
    if replace:
        args += ['-overlay', str(replace)]
    args += ['-run', '^(' + '|'.join(parents) + ')$', './internal/' + package]
    with (out / (label + '.jsonl')).open('w') as stdout, (out / (label + '.stderr')).open('w') as stderr:
        result = subprocess.run(args, stdout=stdout, stderr=stderr)
    (out / (label + '.exit')).write_text(str(result.returncode) + '\n')
    events = [json.loads(line) for line in (out / (label + '.jsonl')).read_text().splitlines()]
    for parent in parents:
        terminals = [e['Action'] for e in events if e.get('Test') == parent and e.get('Action') in ['pass', 'fail', 'skip']]
        assert terminals == [expected] * count, (label, parent, terminals)
    assert (result.returncode == 0) == (expected == 'pass'), (label, result.returncode)

storage = ['TestRetainedProducerHonorsEachContinuationBudget', 'TestRetainedProducerRejectsOverBudgetRawNames', 'TestRetainedProducerUsesUnusedCreditToProveSmallDirectoryEOF']
owners = ['TestPortableScanCancellationRetiresItsRegisteredProducer']
http = ['TestReadingCompleteMultiPageScanAllowsRealHTTPSignIn', 'TestReadingDiscoverOperationStrictAndBounded', 'TestReadingEPUBOversizedMemberRefusedAtOpen', 'TestReadingInitialFacetRetryBacksOffAfterFirstPoll', 'TestReadingOpenSurvivesAFailedFactAdoption', 'TestReadingPrivatePathMovedFolderWithdrawsOldAttribution', 'TestReadingPrivatePathPublicationCoversEveryCandidate', 'TestRestrictedProfileWriteRouteMatrix']
for phase, replacements in [('before', before), ('fixed', {})]:
    original = overlay(phase, replacements)
    expected = 'fail' if phase == 'before' else 'pass'
    helper = Path(os.environ['RUNNER_TEMP']) / ('portico-' + phase)
    subprocess.run(['go', 'build', '-C', 'server', '-overlay', str(original), '-tags', tags, '-o', str(helper), './cmd/server'], check=True)
    os.environ['PORTICO_PLAYBACK_TEST_HELPER'] = str(helper)
    run(phase + '-storage', 'storage', storage, 3, expected, original)
    run(phase + '-owner', 'ingestion', owners, 3, expected, original)
    forced_overlay = overlay(phase + '-legal-budget', replacements | {str(budget): str(forced)})
    run(phase + '-http', 'httpapi', http, 1, expected, forced_overlay)
    helper.unlink()
(out / 'accepted.txt').write_text('Exact original/fixed storage, physical owner and all-eight legal-budget controls have every required terminal; no skipped/missing parents.\n')
