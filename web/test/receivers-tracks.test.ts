import test from 'node:test';
import assert from 'node:assert/strict';
import {WatchReceivers} from '../src/screens/watch/receivers.ts';

// The bar's tracks for another device (08 rule 27): read once per title; a title whose tracks can't be read is not asked
// about again on every position report; a choice moves the check without reading the list again.
function receivers(read: () => Promise<unknown>) {
  let snapshot: any = {directoryPhase: 'ready', receivers: [], receiverPlaybackId: 'pbs_1', remote: {sessionId: 'pbs_1', deviceName: 'TV', state: 'playing', itemId: 'itm_a', positionMs: 1000}};
  const listeners = new Set<() => void>();
  const r = new WatchReceivers({api: {request: async () => ({})} as never, runtime: {subscribe: () => () => {}} as never, isCurrent: () => true} as never);
  let reads = 0;
  (r as any).handoff = {getSnapshot: () => snapshot, subscribe: (l: () => void) => {listeners.add(l); return () => listeners.delete(l);},
    remoteTracks: async () => {reads++; return read();}, audio: async () => true, subtitles: async () => true, release() {}, dispose() {}};
  const report = async (change: object = {}) => {snapshot = {...snapshot, remote: {...snapshot.remote, ...change}}; r.refreshPresentation(); await new Promise(resolve => setImmediate(resolve));};
  return {r, report, reads: () => reads};
}

test('a title whose tracks can\'t be read is not asked about again on every position report, only when the title changes', async () => {
  const f = receivers(async () => undefined);
  for (let at = 1; at <= 5; at++) await f.report({positionMs: at * 2000});
  assert.equal(f.reads(), 1, 'once for the title, not once per report');
  await f.report({itemId: 'itm_b'});
  assert.equal(f.reads(), 2, 'a new title is read');
});

test('a choice moves the check on the tracks already read; the list is not read again', async () => {
  const f = receivers(async () => ({audio: [{id: 'a1', name: 'English'}, {id: 'a2', name: 'French'}], subtitles: [{id: 's1', name: 'English'}], audioId: 'a1', subtitleId: null}));
  await f.report();
  assert.equal(f.r.getSnapshot().remote?.tracks?.audioId, 'a1');
  f.r.audio('a2'); f.r.subtitles('s1'); await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual([f.r.getSnapshot().remote?.tracks?.audioId, f.r.getSnapshot().remote?.tracks?.subtitleId], ['a2', 's1']);
  assert.equal(f.reads(), 1);
});
