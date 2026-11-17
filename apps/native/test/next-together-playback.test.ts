import test from 'node:test';
import assert from 'node:assert/strict';
import {bindNativeTogether} from '../src-next/screens/settings-live/together-playback';
import {nativeTogetherSession} from '../src-next/screens/settings-live/together-session';
import {groupPort} from '../src-next/screens/settings-live/group-port';
import {roomFixture} from './helpers/room-fixture';
import type {Room} from '../../../packages/client-core/src/room-protocol';

const settle = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => {let resolve!: () => void;const promise = new Promise<void>(done => {resolve = done;});return {promise, resolve};};

test('native Room adopts each occurrence once, cancels old openings and applies the latest paused seek', async () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home'};
  const scope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
  const entry = (entryId: string) => ({entryId, itemId: 'movie', available: true});
  let group = roomFixture({clock: {entryId: 'first', positionMs: 1000, at: new Date().toISOString(), playing: true, ready: 1, waitingFor: 0}, queue: [entry('first'), entry('second')]});
  let snapshot: any = {authority, queue: {}, playback: {playbackRun: 0, intent: 'paused', phase: 'idle'}}, run = 0;
  const starts: any[] = [], seeks: number[] = [], actions: any[] = [], messages: string[] = [];
  const playback: any = {
    getSnapshot: () => snapshot,
    start: async (input: any) => {
      const opening = {...input, ...deferred()};starts.push(opening);await opening.promise;
      if (!input.lifetime.isCurrent()) return;
      const id = `session-${++run}`, fence = {kind: 'queue', itemId: 'movie', sourceId: 'version', entryId: `local-${run}`, sessionId: id, generation: 1, playback: {scope, run}};
      snapshot = {authority, fence, retired: false,
        session: {id, itemId: 'movie', role: 'local', state: 'active', presentation: {generation: 1}, versionId: 'version', queue: {queueId: 'queue', entryId: fence.entryId}},
        queue: {view: {queue: {id: 'queue', scope, currentEntryId: fence.entryId, currentPlaybackId: id, entries: [{id: fence.entryId, itemId: 'movie'}]}}},
        playback: {playbackRun: run, itemId: 'movie', session: {id, generation: 1}, phase: 'ready', intent: input.state, duration: 30, positionSeconds: input.target.startSeconds}};
    },
    toggle: async () => {snapshot.playback.intent = snapshot.playback.intent === 'playing' ? 'paused' : 'playing';},
    seek: async (_fence: unknown, seconds: number) => {seeks.push(seconds);snapshot.playback.positionSeconds = seconds;},
    skip: async () => {}, next: async () => {}, previous: async () => {}, replay: async () => {}, queuePlay: async () => {}, setRate() {}, open() {},
  };
  const directSeek = playback.seek;
  const context: any = {authority: () => authority, current: () => true, session: () => undefined, subscribeAuthority: () => () => {}, playback,
    events: {on: () => () => {}, onResync: () => () => {}, subscribeStatus: () => () => {}, getStatus: () => ({connected: false})},
    navigation: {getSnapshot: () => ({route: {kind: 'together'}}), subscribe: () => () => {}},
    api: {baseUrl: 'https://room.test', request: async () => ({id: 'movie', libraryId: 'films', kind: 'movie', title: 'Movie', available: true, durationMs: 30000, progressMs: 0}),
      requestRaw: async (path: string, _method: string, options: any) => {
        if (path.endsWith('/playback')) actions.push(options.body);
        return {status: 200, headers: {}, body: group};
      }},
  };
  const service = nativeTogetherSession(context);
  service.report = async () => {};service.notice = value => {messages.push(value);};
  await service.open(group.id);
  const dispose = bindNativeTogether(context, true);
  const publish = async (patch: Partial<Room>) => {group = roomFixture({...group, ...patch});await service.refresh();await settle();};
  try {
    await settle();assert.equal(starts.length, 1);
    await publish({clock: {...group.clock, entryId: 'second', positionMs: 2000}});
    assert.equal(starts[0].lifetime.signal.aborted, true);
    assert.equal(starts.length, 2, 'same title, separate occurrence opens its own source');
    await publish({clock: {...group.clock, positionMs: 8000, playing: false}});
    starts[0].resolve();await settle();assert.equal(groupPort()?.matches?.(), false);
    starts[1].resolve();await settle();await settle();
    assert.equal(starts.length, 2);assert.equal(groupPort()?.matches?.(snapshot.fence), true);
    assert.equal(snapshot.playback.positionSeconds, 8);assert.equal(snapshot.playback.intent, 'paused');
    assert.equal(seeks.at(-1), 8, 'latest paused seek wins over the old source-opening position');
    await playback.seek(snapshot.fence, 9);assert.deepEqual(actions.at(-1), {action: 'seek', positionMs: 9000});
    await playback.previous(snapshot.fence);assert.deepEqual(actions.at(-1), {action: 'watch', entryId: 'first'});
    snapshot.playback.intent = 'playing';
    await publish({queue: [entry('first'), {entryId: 'second', available: false}]});
    assert.equal(snapshot.playback.intent, 'paused', 'a redacted occurrence cannot keep the old title playing');
    assert.equal(groupPort()?.matches?.(), false);assert.equal(starts.length, 2);
    await publish({queue: [entry('first'), entry('second')]});
    assert.equal(starts.length, 3, 'restored availability reopens the same occurrence');
    starts[2].resolve();await settle();await settle();assert.equal(groupPort()?.matches?.(), true);
    await publish({id: 'successor', you: {...group.you, memberId: 'successor-member'}});
    assert.equal(starts.length, 4);assert.equal(groupPort()?.matches?.(), false);
    dispose();assert.equal(starts[3].lifetime.signal.aborted, true);
    starts[3].resolve();await settle();assert.equal(groupPort(), undefined);
    assert.equal(playback.seek, directSeek, 'retirement restores only this owner’s wrappers');
    assert.deepEqual(messages, []);
  } finally {dispose();service.dispose();}
});
