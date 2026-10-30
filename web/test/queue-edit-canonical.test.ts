import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '@core/index.ts';
import {FakePlaybackServer, fakeHttp} from '@core/playback-v1/testing/fake-server.ts';
import {V1PlaybackApi} from '@core/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '@core/playback-v1/v1-queue-player.ts';
import {queueItemInput} from '@core/queue-controller.ts';
import type {V1PlaybackTarget} from '@core/playback-v1/v1-queue-player.ts';
import {admitCommand, createQueueEditOpening, localEditOutput, type WebQueueEditPage, type WebQueueEditOutputRead} from '../src/platform/playback/queue-edit-adapter.ts';
import type {PlaybackFence} from '../../packages/player-core/src/index.ts';

/** Composition through the REAL public captureEditWindow reader and parsed wire facts. */
async function fixture(count = 320, counter?: {calls: number}) {
  const server = new FakePlaybackServer({itemKind: () => 'movie'}), inner = fakeHttp(server);
  const http = counter ? {send: async (request: never) => {counter.calls++; return inner.send(request);}} as ReturnType<typeof fakeHttp> : inner;
  let serial = 0;
  const api = new V1PlaybackApi(http, {key: () => `api-${++serial}`});
  const playback = new PlaybackService(api, () => `run-${++serial}`);
  const player = new V1QueuePlayer({http, api, playback, viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}, key: () => `edit-key-${++serial}`});
  const stop = player.connect();
  await player.playEntries(Array.from({length: count}, (_, i) => queueItemInput(`item-${i}`)));
  const session = player.currentSession()!, view = player.getSnapshot().view!;
  const target: V1PlaybackTarget = {sessionId: session.id, generation: session.presentation.generation, playbackRun: playback.getSnapshot().playbackRun,
    itemId: session.itemId!, versionId: session.versionId!, queueId: session.queue!.queueId, entryId: session.queue!.entryId};
  return {player, playback, stop, target, revision: view.queue.revision, counter};
}

function opening(f: Awaited<ReturnType<typeof fixture>>, state: {current: boolean}) {
  const reader = f.player.captureEditWindow(f.target, f.revision, {signal: new AbortController().signal,
    isCurrent: () => state.current, readOutput: () => ({kind: 'local' as const, target: f.target})});
  return createQueueEditOpening({reader, current: () => state.current, currentEntryId: f.target.entryId,
    lifetime: {signal: new AbortController().signal, isCurrent: () => state.current}, hidden: () => false});
}

async function step(openingPort: ReturnType<typeof createQueueEditOpening>, count: number): Promise<WebQueueEditPage> {
  for (let i = 0; i < count; i++) await openingPort.next();
  return openingPort.getSnapshot().page!;
}

test('a first-page adjacent pair moves through the canonical plan with only that pair', async () => {
  const f = await fixture(), state = {current: true}, port = opening(f, state);
  try {
    await port.open();
    const first = port.getSnapshot().page!;
    await port.move(first, first.rows[1].entryId, 1);
    const after = port.getSnapshot().page!;
    assert.equal(port.getSnapshot().phase, 'ready');
    assert.notEqual(after.revision, first.revision);
    assert.equal(after.rows[0].entryId, first.rows[0].entryId);   // the current row is unchanged
    assert.equal(after.rows[1].entryId, first.rows[2].entryId);   // the unmoved row keeps its id
    assert.equal(after.rows[2].itemId, first.rows[1].itemId);     // the offered pair swapped
    assert.equal(after.rows[1].itemId, first.rows[2].itemId);
    assert.notEqual(after.rows[2].entryId, first.rows[1].entryId); // a move mints the moved entry a new id
  } finally {port.dispose(); f.stop();}
});

test('page two edits through the canonical plan using the opening anchor, not a presenter row', async () => {
  const f = await fixture(), state = {current: true}, port = opening(f, state);
  try {
    await port.open();
    const second = await step(port, 1);
    assert.equal(second.start, 50);
    await port.move(second, second.rows[2].entryId, 1);
    assert.equal(port.getSnapshot().phase, 'ready');
    assert.equal(port.getSnapshot().page!.start, second.start);
    await port.remove(port.getSnapshot().page!, port.getSnapshot().page!.rows[1].entryId);
    assert.equal(port.getSnapshot().phase, 'ready');
  } finally {port.dispose(); f.stop();}
});

test('a retired opening lifetime cannot mutate: zero canonical IO and no accepted publication', async () => {
  const counter = {calls: 0};
  const f = await fixture(320, counter), state = {current: true}, port = opening(f, state);
  try {
    await port.open();
    const shown = port.getSnapshot().page!;
    state.current = false;
    const before = counter.calls;
    await assert.rejects(() => port.remove(shown, shown.rows[1].entryId), /queue_edit_stale/);
    assert.equal(counter.calls, before);      // no canonical queue IO for the refused mutation
    assert.equal(port.getSnapshot().phase, 'retired');
  } finally {port.dispose(); f.stop();}
});

/** The runtime's local witness, driven by a controlled read so a getter can flip the dock. */
function witness(state: {dockRemote: boolean; stack: object; source: object; current: boolean; group: boolean},
  target: Parameters<typeof localEditOutput>[0]['target'], fence: PlaybackFence) {
  const read = (): WebQueueEditOutputRead => ({stack: state.stack, source: state.source, current: state.current, fence, presentation: true,
    dockLocalFence: fence, dockRemote: state.dockRemote, group: state.group,
    session: {id: target.sessionId, role: 'local', kind: 'audio', itemId: target.itemId, versionId: target.versionId,
      generation: target.sessionGeneration, queueId: target.queueId, entryId: target.entryId},
    viewQueueId: target.queueId, viewEntryId: target.entryId, playbackRun: target.playbackRun});
  return localEditOutput({originalStack: state.stack, originalSource: state.source, fence, target, same: (a, b) => a === b, read});
}

test('a local dock that becomes remote during the getters refuses with no local proof', () => {
  const stack = {}, source = {};
  const target = {libraryId: 'library', itemId: 'item', sessionId: 'session', sessionGeneration: 2, sourceId: 'source',
    versionId: 'version', playbackRun: 4, queueId: 'queue', entryId: 'entry'};
  const fence = {kind: 'queue', sessionId: 'session', generation: 2, itemId: 'item', entryId: 'entry', sourceId: 'source'} as unknown as PlaybackFence;
  const state = {dockRemote: false, stack, source, current: true, group: false};
  const baseline = witness(state, target, fence);
  assert.equal(baseline.kind, 'local');
  // The second read happens after the getters: the dock turned remote (or a successor took over).
  let reads = 0;
  const flip = localEditOutput({originalStack: stack, originalSource: source, fence, target, same: (a, b) => a === b,
    read: () => {reads++; return {...({stack, source, current: true, fence, presentation: true, dockLocalFence: fence, group: false,
      session: {id: 'session', role: 'local', kind: 'audio', itemId: 'item', versionId: 'version', generation: 2, queueId: 'queue', entryId: 'entry'},
      viewQueueId: 'queue', viewEntryId: 'entry', playbackRun: 4} as WebQueueEditOutputRead), dockRemote: reads > 1};}});
  assert.equal(flip.kind, 'unknown');
  assert.equal(witness({...state, group: true}, target, fence).kind, 'unknown');
  const successor = localEditOutput({originalStack: stack, originalSource: source, fence, target, same: (a, b) => a === b,
    read: () => ({stack: {}, source, current: true, fence, presentation: true, dockLocalFence: fence, dockRemote: false, group: false,
      session: {id: 'session', role: 'local', kind: 'audio', itemId: 'item', versionId: 'version', generation: 2, queueId: 'queue', entryId: 'entry'},
      viewQueueId: 'queue', viewEntryId: 'entry', playbackRun: 4})});
  assert.equal(successor.kind, 'unknown');   // a replacement stack is not the captured one
});

test('a takeover during the getters refuses the opening with zero canonical queue IO', async () => {
  const counter = {calls: 0};
  const f = await fixture(320, counter);
  let reads = 0, takeover = false;
  const context = {signal: new AbortController().signal,
    isCurrent: () => true,
    readOutput: () => {reads++; if (takeover && reads > 1) return {kind: 'unknown' as const};
      return {kind: 'local' as const, target: f.target};}};
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, context);
    const page = await reader.read();
    takeover = true;                        // the dock turned remote / a successor took over after the first read
    const before = counter.calls;
    await assert.rejects(() => reader.edit(page, {action: 'remove', entryId: page.items[1]!.entryId}));
    assert.equal(counter.calls, before);    // no canonical queue IO for the refused edit
  } finally {f.stop();}
});

test('a last-read owner flip is refused, not sampled once (pure tail)', () => {
  const stack = {}, source = {};
  const target = {libraryId: 'library', itemId: 'item', sessionId: 'session', sessionGeneration: 2, sourceId: 'source',
    versionId: 'version', playbackRun: 4, queueId: 'queue', entryId: 'entry'};
  const fence = {kind: 'queue', sessionId: 'session', generation: 2, itemId: 'item', entryId: 'entry', sourceId: 'source'} as unknown as PlaybackFence;
  let reads = 0;
  const local = () => ({stack, source, current: true, fence, presentation: true, dockLocalFence: fence, dockRemote: false, group: false,
    session: {id: 'session', role: 'local', kind: 'audio', itemId: 'item', versionId: 'version', generation: 2, queueId: 'queue', entryId: 'entry'},
    viewQueueId: 'queue', viewEntryId: 'entry', playbackRun: 4} as WebQueueEditOutputRead);
  const tail = localEditOutput({originalStack: stack, originalSource: source, fence, target, same: (a, b) => a === b,
    read: () => {reads++; return {...local(), ...(reads > 1 ? {group: true} : {})};}});
  assert.equal(tail.kind, 'unknown');
});

test('a command predicate that retires the owner and still answers true is refused', () => {
  let live = true;
  const command = {signal: new AbortController().signal, isCurrent: () => {live = false; return true;}};
  assert.equal(admitCommand(() => live, command), false);
});

test('a command predicate that retires the owner after it was proved still refuses', () => {
  let owner = 0;
  const command = {signal: new AbortController().signal, isCurrent: () => true};
  assert.equal(admitCommand(() => {owner++; return true;}, command), true);
  owner = 0;
  assert.equal(admitCommand(() => {owner++; return owner < 2;}, command), false); // second proof fails
  const aborted = new AbortController();
  aborted.abort();
  assert.equal(admitCommand(() => true, {signal: aborted.signal, isCurrent: () => true}), false);
});
