"""Branch-only hosted controls. Never merge this diagnostic script/workflow."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

root = Path.cwd()
out = root / 'evidence'
out.mkdir()
code = 'a34b9efd41988930934ae68e5ba622c4b69781e7'
subprocess.run(['git', 'diff', '--exit-code', code, '--', 'server', 'apikit', 'scripts'], check=True)
(out / 'source-sha.txt').write_text(code + '\n')
(out / 'workflow-sha.txt').write_bytes(subprocess.check_output(['git', 'rev-parse', 'HEAD']))
(out / 'source-archive-sha256.txt').write_text(hashlib.sha256(subprocess.check_output(['git', 'archive', code])).hexdigest() + '\n')
(out / 'go-version.txt').write_bytes(subprocess.check_output(['go', 'version']))
(out / 'ffmpeg-version.txt').write_bytes(subprocess.check_output(['ffmpeg', '-version']))
tags = subprocess.check_output(['python3', 'scripts/release-build-tags.py', '--capability'], text=True).strip()
(out / 'build-tags.txt').write_text(tags + '\n')
paths = ['server/internal/playback/' + name for name in ['hls.go', 'occurrence_claim.go', 'occurrence_io.go']]
replacements = {}
for path in paths:
    target = out / 'baseline' / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(subprocess.check_output(['git', 'show', code + '^:' + path]))
    replacements[str(root / path)] = str(target)
baseline = out / 'baseline-overlay.json'
baseline.write_text(json.dumps({'Replace': replacements}))
original = root / paths[2]
mutant = out / 'without-sql-recheck.go'
text = original.read_text()
needle = 'if !live || !owner.registry.canWrite(id, owner.entry, run) {'
assert text.count(needle) == 1
mutant.write_text(text.replace(needle, 'if !live {'))
mutation = out / 'mutation-overlay.json'
mutation.write_text(json.dumps({'Replace': {str(original): str(mutant)}}))
retirement = ['TestHLSRestartRetainsPhysicalOwnershipWithoutAcceptingRetiredFailure', 'TestHLSRestartRejectsAProducerWriteQueuedBeforeRetirement']
positive = ['TestHLSFailureFencedToProducerGeneration', 'TestFiniteAndStreamingFailureWithCancelledProducerContext', 'TestOldPrimaryCancellationCannotSelectSuccessorRenditions', 'TestActualOldWindowThrottleCannotCancelSuccessorChild']
seek = ['TestAirPlayReceiverSeekToStartDuringBurnHandoff']
ordinary = ['TestAirPlayReceiverPreparesExactCurrentChoicesBeforeHandoff']

def run(label, package, parents, expected, overlay=None):
    args = ['go', 'test', '-C', 'server', '-p', '1', '-json', '-count=3', '-timeout=780s', '-tags', tags]
    if overlay:
        args += ['-overlay', str(overlay)]
    args += ['-run', '^(' + '|'.join(parents) + ')$', './internal/' + package]
    with (out / (label + '.jsonl')).open('w') as stdout, (out / (label + '.stderr')).open('w') as stderr:
        result = subprocess.run(args, stdout=stdout, stderr=stderr)
    (out / (label + '.exit')).write_text(str(result.returncode) + '\n')
    events = [json.loads(line) for line in (out / (label + '.jsonl')).read_text().splitlines()]
    terminals = [event for event in events if event.get('Action') in ['pass', 'fail', 'skip'] and '/' not in event.get('Test', '/')]
    for parent in parents:
        actual = [event['Action'] for event in terminals if event.get('Test') == parent]
        assert actual == [expected] * 3, (label, parent, actual)
    assert (result.returncode == 0) == (expected == 'pass'), (label, result.returncode)
    if label == 'baseline-http':
        output = ''.join(event.get('Output', '') for event in events)
        assert output.count('receiver seek to start: 500') == 3, output[-5000:]

for phase, overlay in [('baseline', baseline), ('fixed', None)]:
    helper = out / ('portico-server-' + phase)
    build = ['go', 'build', '-C', 'server', '-tags', tags]
    if overlay:
        build += ['-overlay', str(overlay)]
    subprocess.run(build + ['-o', str(helper), './cmd/server'], check=True)
    os.environ['PORTICO_PLAYBACK_TEST_HELPER'] = str(helper)
    run(phase + '-unit', 'playback', retirement + (positive if phase == 'fixed' else []), 'pass' if phase == 'fixed' else 'fail', overlay)
    run(phase + '-http', 'httpapi', seek + (ordinary if phase == 'fixed' else []), 'pass' if phase == 'fixed' else 'fail', overlay)
    helper.unlink()
run('without-sql-recheck', 'playback', retirement[1:], 'fail', mutation)
(out / 'accepted.txt').write_text('Original controls fail, fixed controls pass, SQL-recheck mutation fails; no missing/skipped terminals.\n')
