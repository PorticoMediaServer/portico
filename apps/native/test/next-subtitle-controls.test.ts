import test from 'node:test';
import assert from 'node:assert/strict';
import {boundSubtitleControls} from '../src-next/platform/playback/subtitle-controls.ts';
import type {SubtitleSnapshot} from '../../../packages/client-core/src/subtitles.ts';

function fixture() {
  let current = true;
  const snapshot: SubtitleSnapshot = {plan: null, provider: {enabled: true}, busy: false, loading: false, suppressed: false, error: null, canRetry: false,
    candidates: [{id: 'candidate-1', language: 'en', title: 'Provider title', attribution: 'Provider attribution'}]};
  const calls: unknown[][] = [];
  const service = {
    getSnapshot: () => snapshot,
    refresh: async () => {calls.push(['refresh']);}, retry: async () => {calls.push(['retry']);},
    search: async (language: string) => {calls.push(['search', language]);},
    apply: async (candidate: unknown) => {calls.push(['apply', candidate]); return 'd42';},
  };
  const controls = boundSubtitleControls(service, () => current, async trackId => {calls.push(['choose', trackId]);});
  return {controls, calls, snapshot, retire: () => { current = false; }};
}

test('a downloaded subtitle is the offered result, and it is turned on once downloaded', async () => {
  const f = fixture();
  await f.controls.search('en');
  await f.controls.apply('candidate-1');
  assert.deepEqual(f.calls, [['search', 'en'], ['apply', f.snapshot.candidates[0]], ['choose', 'd42']]);
  await assert.rejects(f.controls.apply('not-offered'), 'an id the search did not offer is never sent');
  assert.equal(f.calls.length, 3);
});

test('retired sheet commands cannot call the old source service', async () => {
  const f = fixture(); f.retire();
  for (const action of [() => f.controls.refresh(), () => f.controls.retry(), () => f.controls.search(), () => f.controls.apply('candidate-1')]) await assert.rejects(action());
  assert.equal(f.calls.length, 0);
});
