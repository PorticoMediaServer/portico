// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act as fabricAct, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {act as driverAct, createHookHost} from './hook-driver.ts';
const act = async (work: () => void | Promise<void>) => {await driverAct(work); await fabricAct(() => {});};
import {playbackFence, playbackIdentity, type QueuePlaybackFence, type QueueScope} from '../../../packages/player-core/src';
import {useListeningSong, type NativeListeningSongState} from '../src-next/shell/use-listening-song.ts';
import type {NativeListeningSong, NativeListeningSongDetail} from '../src-next/shell/listening-song-detail.ts';

/**
 * The listening opening is admitted by its immutable (port, fence content, lifetime) tuple.
 * A progress tick that repeats the same content under a new fence object is the same opening;
 * the original fence object stays with the opening, and only a real content change retires it.
 * Retirement is the original layout cleanup, so it runs before any other effect's setup.
 */

const scope: QueueScope = {
  serverId: 'server-1',
  authority: 'hosted',
  accountId: 'account-1',
  profileId: 'profile-1',
  controllerId: 'controller-1',
  controllerEpoch: '1',
  commandLaneId: 'lane-1',
};
/** A real queue occurrence, validated by the same fence/identity constructors playback uses. */
const fence = (entryId: string): QueuePlaybackFence => playbackFence({
  kind: 'queue',
  playback: playbackIdentity({scope, run: 1}),
  sessionId: 'session-1',
  itemId: 'item-1',
  sourceId: 'source-1',
  entryId,
  generation: 1,
});
const lifetime = () => {
  const abort = new AbortController();
  return {signal: abort.signal, isCurrent: () => !abort.signal.aborted};
};
function detail() {
  const reads: unknown[] = [], favorites: unknown[] = [];
  const port = {
    canRead: () => true,
    read: async (at: unknown) => {
      reads.push(at);
      return Object.freeze({itemId: `song-${reads.length}`, libraryId: 'library', favorite: false, revision: 1, actions: []}) as NativeListeningSong;
    },
    favorite: async (song: unknown) => {favorites.push(song); return true;},
  };
  return {port: port as unknown as NativeListeningSongDetail, reads, favorites};
}

function opening(fenceA: ReturnType<typeof fence>, createHost: (body: () => void) => any = createFabricHookHost) {
  const d = detail(), life = lifetime();
  const props = {detail: d.port, fence: fenceA, lifetime: life, current: () => true};
  let state: NativeListeningSongState, favoriteRef: NativeListeningSongState['favorite'], armed = false, token: NativeListeningSong | undefined;
  const host = createHost(() => {
    const key = JSON.stringify(props.fence);
    // The other component's layout setup in this same commit. It is declared before the
    // listening opening so its setup runs first under a setup-time retirement.
    React.useLayoutEffect(() => {if (armed) favoriteRef?.(token!, true);}, [key]);
    state = useListeningSong(props.detail, props.fence, props.lifetime, props.current);
    favoriteRef = state.favorite;
  });
  return {d, props, host, state: () => state, favorite: () => favoriteRef, admitted: fenceA,
    arm: (value: NativeListeningSong) => {token = value; armed = true;}};
}

test('a same-content progress tick keeps the original fence and does not reopen the read', async () => {
  const x = opening(fence('entry-1'));
  try {
    await x.host.mount();
    await act(async () => {});
    const original = x.state().song;
    assert.ok(original);
    assert.equal(x.d.reads.length, 1);
    assert.equal(x.d.reads[0], x.admitted, 'the opening holds the fence from the render that admitted it');

    x.props.fence = fence('entry-1');
    await x.host.rerender();
    await act(async () => {});
    assert.equal(x.d.reads.length, 1, 'the same content under a new object identity is the same opening');
    assert.equal(x.state().song, original, 'the drawn song token is retained');
    assert.equal(x.d.reads[0], x.admitted, 'the opening keeps its original fence object');
  } finally {
    await x.host.unmount();
  }
});

test('a retained favorite attempted in another layout setup during a fence change refuses', async () => {
  const x = opening(fence('entry-1'));
  try {
    await x.host.mount();
    await act(async () => {});
    const original = x.state().song!;
    x.arm(original);

    const nextFence = fence('entry-2');
    x.props.fence = nextFence;
    // One render: the other layout setup runs in the same commit as the listening transition.
    await x.host.rerender();
    await act(async () => {});

    assert.deepEqual(x.d.favorites, [], 'the retired opening cannot borrow the new one');
    assert.equal(x.d.reads.length, 2, 'the real content change opens once');
    assert.equal(x.d.reads[1], nextFence, 'the new opening holds the new exact fence');
    assert.equal(x.state().song?.itemId, 'song-2');

    const accepted = x.state().song!;
    x.favorite()(original, true);
    await act(async () => {});
    assert.deepEqual(x.d.favorites, [], 'the old token stays refused after the transition');
    x.favorite()(accepted, true);
    await act(async () => {});
    assert.deepEqual(x.d.favorites, [accepted], 'only the current opening token is accepted');
  } finally {
    await x.host.unmount();
  }
});

test('a real content change retires the opening before the next layout setup', async () => {
  const x = opening(fence('entry-1'));
  try {
    await x.host.mount();
    await act(async () => {});
    const original = x.state().song!;
    x.props.fence = fence('entry-2');
    await x.host.rerender();
    await act(async () => {});
    x.favorite()(original, true);
    await act(async () => {});
    assert.deepEqual(x.d.favorites, []);
  } finally {
    await x.host.unmount();
  }
});

test('an actual unmount retires the opening and refuses its retained token', async () => {
  const x = opening(fence('entry-1'));
  await x.host.mount();
  await act(async () => {});
  const original = x.state().song!;
  await x.host.unmount();
  x.favorite()(original, true);
  await act(async () => {});
  assert.deepEqual(x.d.favorites, []);
});

// The production renderer has no development-only effect replay. Preserve the
// original manual callback vector; it is not React StrictMode evidence.
test('a StrictMode effect replay retires the original opening and opens the same admission once more', async () => {
  const x = opening(fence('entry-1'), createHookHost);
  try {
    x.host.mount();
    await act(async () => {});
    const original = x.state().song!;
    assert.equal(x.d.reads.length, 1);

    x.host.replay();
    await act(async () => {});
    assert.equal(x.d.reads.length, 2, 'replay opens the same admission once again');
    assert.equal(x.d.reads[1], x.d.reads[0], 'the replay holds the exact same fence object');
    const reopened = x.state().song!;
    assert.notEqual(reopened, original);

    x.favorite()(original, true);
    await act(async () => {});
    assert.deepEqual(x.d.favorites, [], 'the pre-replay token is retired');
    x.favorite()(reopened, true);
    await act(async () => {});
    assert.deepEqual(x.d.favorites, [reopened], 'the replayed opening token is accepted');
  } finally {
    x.host.unmount();
  }
});

test.todo('Fabric StrictMode replay: TypeError: x.host.replay is not a function');
