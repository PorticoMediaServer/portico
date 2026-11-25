import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence, type DockPlayback, type PlaybackFence, type QueuePlaybackFence, type QueueScope} from '../../../packages/player-core/src';
import {NATIVE_START_DEADLINE_MS, NATIVE_START_DRAWN_MS, NativeStartWatch, entryDisplay, type NativeStartWatchHost} from '../src-next/platform/playback/start-watch';
import type {NativeStartDisplay} from '../src-next/platform/playback/types';

const scope: QueueScope = {
  serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile',
  controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1',
};
const makeFence = (generation = 1): QueuePlaybackFence => playbackFence({
  kind: 'queue', playback: {scope, run: 1},
  sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation,
});

function fakeClock() {
  let time = 0, serial = 0;
  const jobs = new Map<number, {at: number; fn: () => void}>();
  return {
    now: () => time,
    setTimer(fn: () => void, ms: number) {
      const id = ++serial;
      jobs.set(id, {at: time + ms, fn});
      return id;
    },
    clearTimer(id: unknown) {jobs.delete(id as number);},
    advance(ms: number) {
      const end = time + ms;
      for (;;) {
        const next = [...jobs.entries()].filter(([, job]) => job.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        jobs.delete(next[0]);
        time = next[1].at;
        next[1].fn();
      }
      time = end;
    },
    get pending() {return jobs.size;},
  };
}

function fixture() {
  const timers = fakeClock();
  let retired = false, hasSnapshot = true, publishes = 0, operations = 0;
  let currentFence: PlaybackFence | undefined;
  let hasSession = false, hasOptions = false, channelStalled = false;
  let localDock: DockPlayback | null = null;
  let fallbackDisplay: NativeStartDisplay | undefined;
  const stoppedFences: PlaybackFence[] = [];
  const host: NativeStartWatchHost = {
    timers,
    isRetired: () => retired,
    hasSnapshot: () => hasSnapshot,
    publish: () => {publishes++;},
    beginOperation: () => ++operations,
    fence: () => currentFence,
    currentSession: () => hasSession ? ({id: 'session'} as never) : undefined,
    hasOptions: () => hasOptions,
    localDock: () => localDock,
    fallbackDisplay: () => fallbackDisplay,
    diagnosticKind: () => 'movie',
    isChannelStalled: () => channelStalled,
    stop: async fence => {stoppedFences.push(fence);},
  };
  const watch = new NativeStartWatch(host);
  return {
    watch, timers, stoppedFences,
    get publishes() {return publishes;},
    get operations() {return operations;},
    setRetired(value: boolean) {retired = value;},
    setHasSnapshot(value: boolean) {hasSnapshot = value;},
    setFence(value: PlaybackFence | undefined) {currentFence = value;},
    setSession(value: boolean) {hasSession = value;},
    setOptions(value: boolean) {hasOptions = value;},
    setChannelStalled(value: boolean) {channelStalled = value;},
    setLocalDock(value: DockPlayback | null) {localDock = value;},
    setFallbackDisplay(value: NativeStartDisplay | undefined) {fallbackDisplay = value;},
  };
}

test('NativeStartWatch draws starting state immediately and ends cleanly once its accepted fence mounts on the dock', () => {
  const f = fixture();
  const fence = makeFence(1);
  let stopped = 0;
  const handle = f.watch.begin(entryDisplay({id: '1', kind: 'movie', title: 'Solaris', posterUrl: '/poster.jpg'} as never), async () => {});
  handle.stopWith(() => {stopped++;});

  const initial = f.watch.fields(undefined);
  assert.ok(initial.starting);
  assert.equal(initial.starting.display.title, 'Solaris');
  assert.equal(initial.starting.display.posterUrl, '/poster.jpg');
  assert.equal(initial.startTimedOut, undefined);
  assert.equal(f.timers.pending, 2);

  // Another fence on the dock cannot dismiss this start before acceptance or mismatch.
  f.setLocalDock({fence: makeFence(99), kind: 'video', queue: null, positionSeconds: 0, playing: true, expanded: false});
  assert.ok(f.watch.fields(makeFence(99)).starting);

  handle.accepted(fence);
  assert.ok(f.watch.fields(makeFence(99)).starting, 'wrong dock fence keeps watch active');

  f.setLocalDock({fence, kind: 'video', queue: null, positionSeconds: 0, playing: true, expanded: false});
  const settled = f.watch.fields(fence);
  assert.equal(settled.starting, undefined);
  assert.equal(settled.startTimedOut, undefined);
  assert.equal(f.timers.pending, 0, 'all timers cleared once drawn');
  assert.equal(stopped, 0);
});

test('NativeStartWatch expires at NATIVE_START_DEADLINE_MS, invokes stop, and exposes one-shot retry and close', async () => {
  const f = fixture();
  let stopped = 0, retries = 0;
  const handle = f.watch.begin({title: 'Dune'}, async () => {retries++;});
  handle.stopWith(() => {stopped++;});

  // Drawn check at 5s does not time out the start.
  f.timers.advance(NATIVE_START_DRAWN_MS);
  assert.ok(f.watch.fields(undefined).starting);
  assert.equal(f.watch.fields(undefined).startTimedOut, undefined);
  assert.equal(stopped, 0);

  // Reaching 20s deadline trips timeout once.
  f.timers.advance(NATIVE_START_DEADLINE_MS - NATIVE_START_DRAWN_MS);
  assert.equal(stopped, 1);
  const timedOut = f.watch.fields(undefined).startTimedOut;
  assert.ok(timedOut);
  assert.equal(f.watch.fields(undefined).starting, undefined);

  await timedOut.retry();
  await timedOut.retry();
  assert.equal(retries, 1, 'retry is consumed once');
  assert.equal(f.watch.fields(undefined).startTimedOut, undefined);

  // Close clears a subsequent timeout cleanly.
  f.watch.begin({title: 'Dune'}, async () => {retries++;});
  f.timers.advance(NATIVE_START_DEADLINE_MS);
  const second = f.watch.fields(undefined).startTimedOut;
  assert.ok(second);
  second.close();
  assert.equal(f.watch.fields(undefined).startTimedOut, undefined);
});

test('NativeStartWatch Stop button cancels in-flight watched start and turns completion into AbortError', async () => {
  const f = fixture();
  let resolveRun!: () => void;
  const pending = f.watch.watched({title: 'Arrival'}, async () => {}, () => new Promise<void>(resolve => {resolveRun = resolve;}));
  const starting = f.watch.fields(undefined).starting;
  assert.ok(starting);
  starting.stop();
  assert.equal(f.operations, 1);
  assert.equal(f.watch.fields(undefined).starting, undefined);
  resolveRun();
  await assert.rejects(pending, {name: 'AbortError'});
});

test('NativeStartWatch supports untitled fallback naming, Live no-deadline mode, channel_source stalled reason, and dispose', () => {
  const f = fixture();
  f.watch.begin({icon: 'i-music'}, async () => {}, 'none');
  assert.equal(f.timers.pending, 1, 'only the drawn timer runs when deadlineFrom is none');

  const first = f.watch.fields(undefined).starting;
  assert.ok(first);
  assert.equal(first.display.icon, 'i-music');
  assert.equal(first.display.title, undefined);

  f.setFallbackDisplay({title: 'Channel 4'});
  f.setChannelStalled(true);
  const named = f.watch.fields(undefined).starting;
  assert.ok(named);
  assert.equal(named.display.title, 'Channel 4');
  assert.equal(named.display.icon, 'i-music');
  assert.equal(named.reason, 'channel_source');

  f.timers.advance(NATIVE_START_DEADLINE_MS * 2);
  assert.equal(f.watch.fields(undefined).startTimedOut, undefined, 'Live start has no start deadline');

  f.watch.dispose();
  assert.equal(f.timers.pending, 0);
  assert.equal(f.watch.fields(undefined).starting, undefined);
});
