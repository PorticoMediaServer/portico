import test from 'node:test';
import assert from 'node:assert/strict';
import {WatchReceivers} from '../src/screens/watch/receivers.ts';

// Stopping a Portico device from the bar (08 rule 27): a stop the device or server refuses leaves the bar playing,
// with what failed; Play here lets the bar go only once the device has stopped.
function receivers(stops: boolean) {
  const snapshot: any = {directoryPhase: 'ready', receivers: [], receiverPlaybackId: 'pbs_1', remote: {sessionId: 'pbs_1', deviceName: 'TV', state: 'playing', itemId: 'itm_a', positionMs: 1000}};
  const r = new WatchReceivers({api: {request: async () => ({})} as never, runtime: {subscribe: () => () => {}} as never, isCurrent: () => true} as never);
  let released = 0;
  (r as any).handoff = {getSnapshot: () => ({...snapshot, ...(stops ? {} : {message: 'The TV couldn\u2019t be reached. Try again.'})}), subscribe: () => () => {},
    remoteTracks: async () => undefined, inUse: () => ({}), stop: async () => {if (stops) {snapshot.receiverPlaybackId = undefined; snapshot.remote = undefined;} return stops;}, release() {released++;}, dispose() {}};
  r.refreshPresentation();
  return {r, released: () => released};
}
const settle = () => new Promise(resolve => setImmediate(resolve));

test('Play here: a device that stops lets the bar go', async () => {
  const f = receivers(true);
  assert.equal(await f.r.handBack(), true);
  assert.equal(f.r.getSnapshot().remote, undefined); assert.equal(f.released(), 1);
});

test('Play here: a device that will not stop keeps its bar, playing, with the server\'s words', async () => {
  const f = receivers(false);
  assert.equal(await f.r.handBack(), false);
  const bar = f.r.getSnapshot().remote;
  assert.equal(bar?.stopped, false); assert.equal(bar?.problem, 'The TV couldn\u2019t be reached. Try again.');
  assert.equal(f.released(), 0, 'the device is not let go');
});

test('Stop: a refused stop puts the bar back playing and says why', async () => {
  const f = receivers(false);
  f.r.stop(); await settle();
  const bar = f.r.getSnapshot().remote;
  assert.equal(bar?.stopped, false); assert.equal(bar?.problem, 'The TV couldn\u2019t be reached. Try again.');
});

test('a stop that goes through after a refused one shows the bar stopped, without the old failure', async () => {
  const f = receivers(false);
  f.r.stop(); await settle();
  assert.ok(f.r.getSnapshot().remote?.problem);
  (f.r as any).handoff.stop = async () => true;
  f.r.stop(); await settle();
  assert.equal(f.r.getSnapshot().remote?.stopped, true); assert.equal(f.r.getSnapshot().remote?.problem, undefined);
});
