"""Immutable before/after product source with failure-only test diagnostics."""
import json
import os
import shutil
from pathlib import Path
import subprocess

root = Path.cwd()
out = root / 'evidence'
out.mkdir()
(out / 'workflow-sha.txt').write_bytes(subprocess.check_output(['git', 'rev-parse', 'HEAD']))
for label, sha in [('before', 'd2f1d74ebc30c99bd0fdbe90d793608129399d8e'), ('after', '41583beb25d39891fdc20ee842b66c2636977e11')]:
    tree = Path(os.environ['RUNNER_TEMP']) / ('recording-' + label)
    subprocess.run(['git', 'worktree', 'add', '--detach', str(tree), sha], check=True)
    (out / (label + '-source.txt')).write_text(sha + '\n')
    test = tree / 'server/internal/recordingmedia/recording_layout_store_test.go'
    text = test.read_text().replace('"context"', '"context"\n "bytes"\n "runtime/pprof"')
    needle = 't.Fatal("production recording worker did not publish both occurrences")'
    assert text.count(needle) == 1
    text = text.replace(needle, 'recordingWaitDiagnostics(t, db)\n\t\t\t' + needle)
    text += '''
func recordingWaitDiagnostics(t *testing.T, db *sql.DB) {
    t.Helper()
    for _, query := range []string{
        `SELECT id,state FROM dvr_recordings ORDER BY id`,
        `SELECT recording_id,phase FROM dvr_recording_layouts ORDER BY recording_id`,
    } {
        rows, err := db.Query(query)
        if err != nil { t.Log(query, err); continue }
        for rows.Next() { var a,b string; if err := rows.Scan(&a,&b); err != nil { t.Log(err) } else { t.Log(query,a,b) } }
        rows.Close()
    }
    t.Logf("SQL stats: %+v", db.Stats())
    var stacks bytes.Buffer
    _ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)
    t.Log(stacks.String())
}
'''
    text = text.replace('var out dvr.CaptureResult', 'println("RECORDING CAPTURE ENTER", time.Now().UTC().Format(time.RFC3339Nano), in.ArtifactKey)\n\tvar out dvr.CaptureResult', 1)
    text = text.replace('if err = checkpoint(cp); err != nil {', 'println("RECORDING CHECKPOINT", time.Now().UTC().Format(time.RFC3339Nano), in.ArtifactKey)\n\tif err = checkpoint(cp); err != nil {', 1)
    test.write_text(text)
    (out / (label + '-diagnostic.patch')).write_bytes(subprocess.check_output(['git', 'diff'], cwd=tree))
    tags = subprocess.check_output(['python3', 'scripts/release-build-tags.py', '--capability'], cwd=tree, text=True).strip()
    with (out / (label + '.jsonl')).open('w') as stdout, (out / (label + '.stderr')).open('w') as stderr:
        result = subprocess.run(['go', 'test', '-C', 'server', '-p', '1', '-json', '-count=5', '-timeout=780s', '-tags', tags, '-run', '^TestRecordingLayoutActualStoreAndIO$', './internal/recordingmedia'], cwd=tree, stdout=stdout, stderr=stderr)
    (out / (label + '.exit')).write_text(str(result.returncode) + '\n')
    events = [json.loads(line) for line in (out / (label + '.jsonl')).read_text().splitlines()]
    terminals = [e['Action'] for e in events if e.get('Test') == 'TestRecordingLayoutActualStoreAndIO' and e.get('Action') in ['pass', 'fail', 'skip']]
    assert len(terminals) == 5 and set(terminals) <= {'pass', 'fail'}, (label, terminals)

    cohort = ['TestLayoutAtomicLinkRetiresOnlyExactOccurrence', 'TestLayoutRefusesUnownedEqualByteCollision', 'TestLayoutRejectsSymlinksAndEscapesWithoutTouchingOutside', 'TestLayoutReceiptBindsRootSourceOwnerAndTarget', 'TestLayoutCancellationNeverPublishesOrDeletes', 'TestLayoutTargetSubstitutionAndRootReplacementStayUntouched', 'TestLayoutPinnedParentCannotBeRedirected', 'TestLayoutRejectsCorruptClosedBytesAndNonRegularTarget', 'TestLayoutMeasureCountsPhysicalPayloadOnceAcrossAllNames', 'TestRecordingLayoutDriverPreparesBeforeIOAndRecoversExactLink', 'TestLayoutConcurrentOwnersNeverReplaceOrAdoptEachOther', 'TestRecordingLayoutActualStoreAndIO', 'TestRecoveryPersistsFinalCheckpointBeforeValidation', 'TestSharedObjectUsesOneInodeAndLastOwnerReleasesBytes']
    with (out / (label + '-cohort.jsonl')).open('w') as stdout, (out / (label + '-cohort.stderr')).open('w') as stderr:
        result = subprocess.run(['go', 'test', '-C', 'server', '-p', '1', '-json', '-count=5', '-timeout=780s', '-tags', tags, '-run', '^(' + '|'.join(cohort) + ')$', './internal/recordingmedia'], cwd=tree, stdout=stdout, stderr=stderr)
    (out / (label + '-cohort.exit')).write_text(str(result.returncode) + '\n')
    events = [json.loads(line) for line in (out / (label + '-cohort.jsonl')).read_text().splitlines()]
    terminals = [e['Action'] for e in events if e.get('Test') == 'TestRecordingLayoutActualStoreAndIO' and e.get('Action') in ['pass', 'fail', 'skip']]
    assert len(terminals) == 5 and set(terminals) <= {'pass', 'fail'}, (label, terminals)

    if label == 'after':
        shutil.copyfile(root / 'ci-plan.json', tree / 'ci-plan.json')
        subprocess.run(['npm', 'ci', '--no-audit', '--no-fund'], cwd=tree, check=True)
        subprocess.run(['npm', 'run', 'build'], cwd=tree / 'packages/contracts', check=True)
        subprocess.run(['python3', 'ci/run.py', 'helper'], cwd=tree, check=True)
        result = subprocess.run(['python3', 'ci/run.py', 'go', '--index', '0'], cwd=tree)
        (out / 'full-go0.exit').write_text(str(result.returncode) + '\n')
        shutil.copytree(tree / 'ci-results', out / 'full-go0')
