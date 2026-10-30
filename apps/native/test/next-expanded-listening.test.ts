/** Authored source vectors, UNRUN. Real PlaybackDock projection only; no native
 * page, player, output, focus or transition acceptance is implied. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackDock, type DockPlayback, type QueuePlaybackFence, type QueueScope} from '../../../packages/player-core/src/index.ts';
import {captureListeningOpening, expandedListeningOccupant, localListeningOccupant} from '../src-next/shell/listening-presentation.ts';
import type {DockPresentation} from '../src-next/kit/dock/types.ts';

const scope: QueueScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', sourceId: 'source', itemId: 'item', entryId: 'entry', generation: 1};
const presentation: DockPresentation = {fence, identity: {id: 'item', kind: 'music', title: 'Track', subtitle: []}, skipBackSeconds: 10,
  capabilities: {play: true, pause: true, stop: true, seek: true, previous: false, next: false, cast: false}};
const playback = (kind: 'music' | 'audiobook' | 'video'): DockPlayback => ({kind, fence, queue: null, positionSeconds: 30, playing: true, expanded: false});

for (const kind of ['music', 'audiobook'] as const) test(`the real dock projects its exact expanded ${kind} as a page`, () => {
  const dock = new PlaybackDock(scope);
  dock.start(playback(kind)); dock.expand(fence);
  const snapshot = dock.getSnapshot();
  assert.equal(dock.occupant(), null, 'expanded locals are deliberately absent from the dock');
  const page = expandedListeningOccupant(dock, snapshot, presentation);
  assert.equal(page?.mode, kind);
  assert.equal(page?.playback, snapshot.local, 'keep the actual admitted descriptor');
  assert.equal(page?.presentation, presentation);
});

test('collapsed and picture occupants do not become listening pages', () => {
  const dock = new PlaybackDock(scope);
  dock.start(playback('music'));
  assert.equal(expandedListeningOccupant(dock, dock.getSnapshot(), presentation), null);
  dock.start(playback('video')); dock.expand(fence);
  assert.equal(expandedListeningOccupant(dock, dock.getSnapshot(), presentation), null);
});

test('stale snapshot, mismatched presentation and remote custody remain refused', () => {
  const dock = new PlaybackDock(scope);
  dock.start(playback('music')); dock.expand(fence);
  const original = dock.getSnapshot();
  assert.equal(expandedListeningOccupant(dock, original, {...presentation, fence: {...fence, generation: 2}}), null);
  assert.equal(expandedListeningOccupant(dock, original, undefined), null);
  dock.update({...original.local!, positionSeconds: 31});
  assert.equal(expandedListeningOccupant(dock, original, presentation), null);
  dock.beginRemote({id: 'receiver', kind: 'cast', sequence: '1', phase: 'connecting', playback: playback('music'), positionSeconds: 31}, 0);
  assert.equal(expandedListeningOccupant(dock, dock.getSnapshot(), presentation), null);
});


for (const kind of ['music', 'audiobook'] as const) test(`mini ${kind} offers only its original read-only paint, without expanding the real dock`, () => {
  const dock = new PlaybackDock(scope); dock.start(playback(kind));
  const original = dock.getSnapshot(), paint = localListeningOccupant(dock, original, presentation);
  assert.equal(paint?.playback, original.local);
  assert.equal(paint?.playback.expanded, false);
  assert.equal(paint?.presentation, presentation);
  assert.equal(dock.getSnapshot(), original, 'projection publishes no model transition');
  assert.equal(expandedListeningOccupant(dock, original, presentation), null);
  assert.equal(localListeningOccupant(dock, original, {...presentation, fence: {...fence, generation: 2}}), null);
});


test('a full UI opening retains its original page even when navigation supplies a successor', () => {
  const original = {}, successor = {};
  const mini = captureListeningOpening(undefined, false, original);
  const full = captureListeningOpening(mini, true, original);
  assert.equal(full.scope, original);
  assert.notEqual(full.key, mini.key);
  const stillFull = captureListeningOpening(full, true, successor);
  assert.equal(stillFull, full);
  assert.equal(stillFull.scope, original, 'the original current-page getter must now refuse, never rebind');
});
test('close and same-source reopen capture distinct actual UI openings on the current mini page', () => {
  const original = {}, successor = {};
  const first = captureListeningOpening(undefined, true, original);
  const mini = captureListeningOpening(first, false, successor);
  assert.equal(mini.scope, successor); assert.equal(mini.key, first.key);
  const second = captureListeningOpening(mini, true, successor);
  assert.notEqual(second.key, first.key); assert.equal(second.scope, successor);
  assert.equal(captureListeningOpening(second, true, successor), second);
  assert.equal(Object.isFrozen(second), true); assert.equal(Object.isFrozen(second.key), true);
});
