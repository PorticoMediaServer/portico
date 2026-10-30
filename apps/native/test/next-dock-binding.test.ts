import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackDock, type DockPlayback, type PlaybackFence, type QueuePlaybackFence, type QueueScope} from '../../../packages/player-core/src/index.ts';
import {bindNativeDock, type NativeDockAdapter} from '../src-next/kit/dock/native-binding.ts';

const scope: QueueScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', sourceId: 'source', itemId: 'item', entryId: 'entry', generation: 1};
const local: DockPlayback = {fence, kind: 'video', queue: null, positionSeconds: 30, playing: true, expanded: false};
function port() {
  const dock = new PlaybackDock(scope), calls: string[] = [];
  dock.start(local);
  let intent = 'playing';
  const adapter = {dock, getSnapshot: () => ({dock: dock.getSnapshot(), fence: dock.getSnapshot().local?.fence, playback: {intent}, capabilities: {play: true, pause: true, stop: true, seek: true, previous: false, next: true, cast: false}}),
    onTransition: async () => {calls.push('transition');}, toggle: async () => {calls.push('toggle'); intent = intent === 'playing' ? 'paused' : 'playing';}, seek: async (_f: PlaybackFence, position: number) => {calls.push(`seek:${position}`);}, replay: async () => {calls.push('replay');}, previous: async () => {calls.push('previous');}, next: async () => {calls.push('next');}, open: () => {calls.push('open');},
  } as unknown as NativeDockAdapter;
  return {adapter, dock, calls};
}
test('pause is an explicit intent; a repeated click never turns a paused session back on', async () => {
  const {adapter, calls} = port(), action = bindNativeDock(adapter);
  assert.equal(await action({type: 'pause', fence}), true);
  assert.equal(await action({type: 'pause', fence}), false);
  assert.deepEqual(calls, ['toggle']);
});
test('stale source and unavailable Cast never reach the real adapter', async () => {
  const {adapter, dock, calls} = port(), action = bindNativeDock(adapter);
  assert.equal(await action({type: 'cast', fence}), false);
  dock.start({...local, fence: {...fence, sourceId: 'replacement', generation: 2}});
  assert.equal(await action({type: 'seek', fence, positionSeconds: 90}), false);
  assert.deepEqual(calls, []);
});
test('close produces the accepted stop transition once; it does not duplicate a separate engine Stop', async () => {
  const {adapter, dock, calls} = port(), action = bindNativeDock(adapter);
  assert.equal(await action({type: 'close', fence}), true);
  assert.equal(dock.occupant(), null);
  assert.deepEqual(calls, ['transition']);
});
test('explicit restoration calls the fenced runtime operation and never toggles an arbitrary current session', async () => {
  const {adapter, dock, calls} = port();
  dock.start({...local, kind: 'music', fence: {...fence, sessionId: 'audio', sourceId: 'audio'}});
  adapter.restoreBookmark = async (expected, explicitly) => {assert.deepEqual(expected, fence); assert.equal(explicitly, true); calls.push('restore-video'); return fence;};
  const action = bindNativeDock(adapter);
  assert.equal(await action({type: 'restore-video', fence}), true);
  assert.deepEqual(calls, ['restore-video']);
});


test('view-only Cast commands never reach runtime or remote source-restoration ports',async()=>{
 const {adapter,calls}=port();const action=bindNativeDock(adapter,{remote:async()=>{calls.push('remote');}});
 const fence={kind:'cast-view' as const,attachmentId:'11111111-1111-4111-8111-111111111111',remote:{controller:{bindingId:'binding',controllerGeneration:2,connectionGeneration:3},occurrence:{sessionId:'remote-session',generation:4,itemId:'item'}}};
 for(const type of ['stop','continue-here','pause','seek'] as const)assert.equal(await action({type,fence,positionSeconds:20,remote:{id:'return',sequence:'1'}}),false);
 assert.deepEqual(calls,[]);
});
