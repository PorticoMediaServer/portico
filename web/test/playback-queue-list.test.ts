import test from 'node:test';
import assert from 'node:assert/strict';
import type {QueuePlaybackView} from '../../packages/client-core/src/queue-controller.ts';
import type {QueueSnapshot, QueueState} from '../../packages/client-core/src/queues.ts';
import type {V1QueueEditIntent} from '../../packages/client-core/src/playback-v1/v1-queue-player.ts';
import {playbackFence, queueScope} from '../../packages/player-core/src/index.ts';
import {createWebQueueListPort} from '../src/platform/playback/queue-list.ts';

function fixture(hidden = false, selectable = true, kind = 'movie') {
  const scope = queueScope({serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile',
    controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'});
  const queue: QueueSnapshot = {id: 'queue', scope, revision: '1', highestSeenSequence: '0', replayWindow: {results: 256, bodies: 32},
    repeat: 'off', shuffled: false, currentEntryId: 'current', currentPlaybackId: 'session', entries: ['current', 'first', 'second'].map<QueueSnapshot['entries'][number]>((id, index) =>
      hidden && index === 2 ? {id, removed: false, hidden: true} : {id, removed: false, hidden: false, itemId: 'item_' + id,
        editionId: null, partId: null, sourceContext: {kind: 'item', id: 'item_' + id, revision: null, entryId: null}})};
  let view: QueuePlaybackView = {queue, window: {queueId: queue.id, revision: queue.revision, segments: [],
    entries: queue.entries.map((entry, position) => ({entryId: entry.id, position, available: !entry.hidden}))},
    highestSequence: '0', current: {id: 'session', generation: 1, state: 'playing'},
    next: {entryId: 'first', available: true, reason: 'ready'}, items: queue.entries.flatMap(entry => entry.hidden ? [] :
      [{entryId: entry.id, itemId: entry.itemId, title: entry.id, kind, libraryId: 'library'}]), remainingCount: 2,
    postPlay: {nextEntryId: 'first', available: true, reason: 'ready', countdownSeconds: 0, autoplay: false,
      passoutCheckDue: false, automaticAdvances: 0, startDuringCredits: false}};
  let workspace: QueueState = {phase: 'ready', error: null, queue};
  const edits: {revision: string; intent: V1QueueEditIntent; signal: AbortSignal}[] = [], life = new AbortController();
  const plays: {entryId: string; revision: string; signal: AbortSignal}[] = [];
  const port = createWebQueueListPort({queueId: queue.id, selectable, isCurrent: () => true,
    fence: playbackFence({kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', generation: 1,
      itemId: 'item_current', entryId: 'current', sourceId: 'source'}),
    read: () => ({view, workspace}), subscribe: () => () => {},
    play: async (entryId, revision, lifetime) => {plays.push({entryId, revision, signal: lifetime.signal});},
    edit: async (revision, intent, lifetime) => {edits.push({revision, intent, signal: lifetime.signal});}});
  return {port, edits, plays, life, lifetime: {signal: life.signal, isCurrent: () => !life.signal.aborted},
    setWindow(window: QueuePlaybackView['window']) {view = {...view, window};},
    replace() {view = {...view};workspace = {...workspace};}};
}

test('Up Next move passes only the drawn adjacent upcoming block and original revision/lifetime', async () => {
  const f = fixture(), snapshot = f.port.getSnapshot();
  await f.port.move(snapshot, 'second', -1, f.lifetime);
  assert.deepEqual(f.edits.map(edit => ({revision: edit.revision, intent: edit.intent})),
    [{revision: '1', intent: {action: 'reorder', entryIds: ['second', 'first']}}]);
  assert.equal(f.edits[0]!.signal, f.life.signal);
  assert.equal(snapshot.entries[1]!.canMoveUp, false, 'the current occurrence is never a move target');
});

test('Up Next refuses retired panel gestures and replacement snapshots before I/O', async () => {
  const retired = fixture(), snapshot = retired.port.getSnapshot();retired.life.abort();
  await assert.rejects(retired.port.remove(snapshot, 'first', retired.lifetime));
  assert.equal(retired.edits.length, 0);
  const replaced = fixture(), old = replaced.port.getSnapshot();replaced.replace();
  await assert.rejects(replaced.port.remove(old, 'first', replaced.lifetime));
  assert.equal(replaced.edits.length, 0);
});

test('Up Next never moves across an unavailable neighbor or removes a hidden row', async () => {
  const f = fixture(true), snapshot = f.port.getSnapshot();
  assert.equal(snapshot.entries[1]!.canMoveDown, false);
  assert.equal(snapshot.entries[2]!.canRemove, false);
  await assert.rejects(f.port.move(snapshot, 'first', 1, f.lifetime));
  await assert.rejects(f.port.remove(snapshot, 'second', f.lifetime));
  assert.equal(f.edits.length, 0);
});

test('Up Next selection retains the drawn occurrence/revision and refuses retired gestures', async () => {
  const f = fixture(), snapshot = f.port.getSnapshot();
  await f.port.play(snapshot, 'first', f.lifetime);
  assert.deepEqual(f.plays, [{entryId: 'first', revision: '1', signal: f.life.signal}]);
  const retired = fixture(), old = retired.port.getSnapshot();retired.life.abort();
  await assert.rejects(retired.port.play(old, 'first', retired.lifetime));
  assert.equal(retired.plays.length, 0);
  const replaced = fixture(), previous = replaced.port.getSnapshot();replaced.replace();
  await assert.rejects(replaced.port.play(previous, 'first', replaced.lifetime));
  assert.equal(replaced.plays.length, 0);
});

test('Up Next selection is absent for unsupported ownership and non-video rows', async () => {
  for (const f of [fixture(false, false), fixture(false, true, 'song'), fixture(false, true, 'unknown')]) {
    const snapshot = f.port.getSnapshot();
    assert.equal(snapshot.entries[1]!.canPlay, false);
    await assert.rejects(f.port.play(snapshot, 'first', f.lifetime));
    assert.equal(f.plays.length, 0);
  }
});

test('Up Next refuses a move across absolute gaps while keeping an available loaded selection', async () => {
  const f = fixture();
  f.setWindow({queueId: 'queue', revision: '1', segments: [], entries: [
    {entryId: 'current', position: 0, available: true}, {entryId: 'first', position: 2, available: true},
    {entryId: 'second', position: 4, available: true}]});
  const snapshot = f.port.getSnapshot();
  assert.equal(snapshot.entries[1]!.canMoveDown, false);
  assert.equal(snapshot.entries[2]!.canMoveUp, false);
  await assert.rejects(f.port.move(snapshot, 'second', -1, f.lifetime));
  assert.equal(f.edits.length, 0);
  assert.equal(snapshot.entries[2]!.canPlay, true);
});

test('Up Next never invents missing or foreign wire availability and positions', () => {
  const examples: QueuePlaybackView['window'][] = [undefined,
    {queueId: 'other', revision: '1', segments: [], entries: []},
    {queueId: 'queue', revision: '2', segments: [], entries: []},
    {queueId: 'queue', revision: '1', segments: [], entries: [
      {entryId: 'current', position: 0, available: true}, {entryId: 'first', available: true},
      {entryId: 'second', position: 2, available: true}]},
    {queueId: 'queue', revision: '1', segments: [], entries: [
      {entryId: 'current', position: 0, available: true}, {entryId: 'first', position: 1},
      {entryId: 'second', position: 2, available: false}]}];
  for (const window of examples) {
    const f = fixture();f.setWindow(window);
    const snapshot = f.port.getSnapshot();
    for (const row of snapshot.entries) assert.deepEqual([row.canPlay, row.canRemove, row.canMoveUp, row.canMoveDown], [false, false, false, false]);
    assert.equal(snapshot.entries[1]!.title, 'first', 'known metadata does not establish queue availability');
  }
});
