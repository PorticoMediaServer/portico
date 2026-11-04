"""The data-layer cutover must keep these existing behavioral oracles mandatory."""
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PREFIX = 'github.com/PorticoMediaServer/portico/server/internal/'
GOLDEN = {
    'httpapi': [
        'TestPlaybackV1WireCompatibility',
        'TestHLSManifestRepresentationRangeReloadAndGrantRevocation',
        'TestPlaybackV1CapabilitiesAndOptions',
        'TestPlaybackV1SessionLifecycle',
        'TestPlaybackV1QualityDefaultsAndAutomaticSteps',
        'TestPlaybackV1InPlayerTrackChoicesLastOnePlayback',
        'TestPlaybackV1SubtitleOriginalUsesGrantRangesAndRevocation',
        'TestPlaybackV1QueueTransitionFences',
        'TestPlaybackV1CommitNextRefusesPreparedGenerationDrift',
        'TestPlaybackV1CommittedPlanIsThePreparedOne',
        'TestPlaybackV1ExpiredPreparationIsSwept',
        'TestAirPlayReceiverPreparesExactCurrentChoicesBeforeHandoff',
        'TestAirPlayReceiverSeekToStartDuringBurnHandoff',
        'TestCastReceiverCurrentOccurrenceRoutes',
        'TestCastLostReconnectRecovers',
        'TestRevokedSignerCannotRedeemOrReconnectACastTarget',
    ],
    'playback': ['TestOccurrenceStaysLiveAcrossTokenRotation'],
    'playbackv1': [
        'TestConvertingSeekRestartsOnlyAfterRevisionCommit',
        'TestAirPlayReceiverRenewalWinsStaleExpiryAndEndWinsLateRenewal',
        'TestInitialCastStartBindsSelectedEntryAndRefusesConcurrentSession',
        'TestGrantReplacementWritesEvidenceUnderTheOldGeneration',
    ],
    'mediaanalysis': ['TestTrickplayV1WireCompatibility'],
    'downloads': [
        'TestOfflineV1PackageCompatibility',
        'TestReceiptReplayRespectsRevocation',
        'TestReceiptSignVerifyRevalidateAndRevoke',
        'TestBundleCarriesTheFilmsFacts',
        'TestBundleListsTheFilesChapters',
    ],
}

# Explicit reviewed membership, including the seven pre-existing diagnostics.
# The planner's exclusion list must match these exact owners once each.
EXPECTED_DIAGNOSTIC_OWNERS = {
    (PREFIX+'catalog','TestP252bOnePinCompleteWriterHold'),
    (PREFIX+'catalog','TestReadingLibraryRemovalCostIsLinear'),
    (PREFIX+'catalog','TestFacetRuntimeColdCompletesUnderContinuousTaggedIngest'),
    (PREFIX+'compactcatalog','TestDerivedWorkerCompletesWithForegroundPressure'),
    (PREFIX+'metadata','TestMBStagingTenThousandCompleteFinalWriterHold'),
    (PREFIX+'httpapi','TestReadPathGoldenEquivalence'),
    (PREFIX+'ingestion','TestScannerTenThousandInventoryAndNames'),
    (PREFIX+'httpapi','TestMixedViewerLoadUnderBackgroundWork'),
} | {(PREFIX+package,name) for package,names in GOLDEN.items() for name in names}

class PlaybackCompatibility(unittest.TestCase):
    def test_cutover_oracles_are_mandatory_nightly_parents(self):
        tiers = json.loads((ROOT/'ci/tiers.json').read_text())
        golden = {(t['package'],t['name']) for t in tiers['nightly_lanes']['golden']}
        required = {(PREFIX+pkg,name) for pkg,names in GOLDEN.items() for name in names}
        self.assertFalse(required-golden, 'missing cutover gates: '+str(sorted(required-golden)))
        pressure = {(t['package'],t['name']) for t in tiers['nightly_lanes']['pressure']}
        self.assertNotIn((PREFIX+'httpapi','TestMixedViewerLoadUnderBackgroundWork'),pressure)
        self.assertCountEqual([(t['package'],t['name']) for t in tiers['nightly_lanes']['mixed-load']],
                              [(PREFIX+'httpapi','TestMixedViewerLoadUnderBackgroundWork')])

    def test_mixed_load_job_alone_enables_smoke(self):
        from nightly_lane import configure_environment
        from unittest.mock import patch
        import os
        with patch.dict(os.environ,{},clear=True):
            configure_environment('golden')
            self.assertNotIn('PORTICO_PERFORMANCE_TIER',os.environ)
            configure_environment('pressure')
            self.assertNotIn('PORTICO_PERFORMANCE_TIER',os.environ)
            configure_environment('mixed-load')
            self.assertEqual(os.environ['PORTICO_PERFORMANCE_TIER'],'smoke')
        with patch.dict(os.environ,{'PORTICO_PERFORMANCE_TIER':'release'},clear=True):
            configure_environment('mixed-load')
            self.assertEqual(os.environ['PORTICO_PERFORMANCE_TIER'],'release')

    def test_golden_requires_actual_cast_receiver_suite_success(self):
        from nightly_lane import run_receiver_contracts
        from unittest.mock import patch
        import subprocess
        with patch('nightly_lane.subprocess.run') as execute:
            run_receiver_contracts('pressure')
            execute.assert_not_called()
            run_receiver_contracts('golden')
            command = execute.call_args.args[0]
            self.assertEqual(command[:3], ['node','--test','--test-concurrency=2'])
            for file in ['receiver.test.cjs','text.test.cjs']:
                self.assertIn(str(ROOT/'server/receiver/cast/test'/file),command)
            self.assertTrue(execute.call_args.kwargs['check'])
            execute.side_effect = subprocess.CalledProcessError(1,command)
            with self.assertRaises(subprocess.CalledProcessError):
                run_receiver_contracts('golden')

    def test_cli_runs_contracts_after_go_and_enables_pressure_measurement(self):
        import os
        import runpy
        import sys
        from unittest.mock import patch
        tiers = json.loads((ROOT/'ci/tiers.json').read_text())
        inventory = [dict(test,module='server') for tests in tiers['nightly_lanes'].values() for test in tests]
        original = Path.read_text
        def read(path,*args,**kwargs):
            if path.name == 'ci-plan.json':
                return json.dumps({'all_inventory':inventory})
            return original(path,*args,**kwargs)
        for lane in ['golden','pressure','mixed-load']:
            order = []
            selected = []
            def execute_go(plan,index):
                order.append('go')
                selected.extend((test['package'],test['name']) for test in plan['shards'][index])
            with self.subTest(lane=lane), patch.dict(os.environ,{},clear=True), \
                 patch.object(sys,'argv',['nightly_lane.py',lane]), \
                 patch.object(Path,'read_text',read), \
                 patch('run.ensure_ffmpeg'), \
                 patch('run.go',side_effect=execute_go), \
                 patch('subprocess.run',side_effect=lambda *_args,**_kw:order.append('node')):
                runpy.run_path(str(ROOT/'ci/nightly_lane.py'),run_name='__main__')
                self.assertEqual(order,['go','node'] if lane=='golden' else ['go'])
                self.assertEqual(os.environ.get('PORTICO_PERFORMANCE_TIER'),'smoke' if lane=='mixed-load' else None)
                self.assertCountEqual(selected,[(t['package'],t['name']) for t in tiers['nightly_lanes'][lane]])
                if lane=='pressure':
                    self.assertFalse(any(package==PREFIX+'httpapi' for package,_ in selected))

    def test_mixed_load_has_an_isolated_diagnostic_job(self):
        tiers = json.loads((ROOT/'ci/tiers.json').read_text())
        pair = lambda test: (test['package'],test['name'])
        mixed = (PREFIX+'httpapi','TestMixedViewerLoadUnderBackgroundWork')
        self.assertNotIn(mixed,{pair(t) for t in tiers['nightly_lanes']['pressure']})
        self.assertCountEqual([pair(t) for t in tiers['nightly_lanes']['mixed-load']],[mixed])
        # Keep the original pressure cohort exactly: neither smoke compilation
        # nor its test body can enter this matrix job's process pool.
        self.assertCountEqual([pair(t) for t in tiers['nightly_lanes']['pressure']], [
            (PREFIX+'catalog','TestP252bOnePinCompleteWriterHold'),
            (PREFIX+'catalog','TestReadingLibraryRemovalCostIsLinear'),
            (PREFIX+'catalog','TestFacetRuntimeColdCompletesUnderContinuousTaggedIngest'),
            (PREFIX+'compactcatalog','TestDerivedWorkerCompletesWithForegroundPressure'),
            (PREFIX+'metadata','TestMBStagingTenThousandCompleteFinalWriterHold'),
        ])
        workflow = (ROOT/'.github/workflows/nightly.yml').read_text()
        import re
        job = re.search(r'^  diagnostic:\n(.*?)(?=^  [\w-]+:\n|\Z)',workflow,re.M|re.S)
        self.assertIsNotNone(job)
        self.assertIn('lane: [pressure, golden, scanner, mixed-load]',job[1])
        self.assertIn('runs-on: ubuntu-24.04',job[1])
        self.assertIn('fail-fast: false',job[1])
        self.assertIn('python3 ci/nightly_lane.py "$LANE"',job[1])
