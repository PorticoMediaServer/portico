import test from 'node:test';
import assert from 'node:assert/strict';
import {parseQueuePostPlay, parseQueueSnapshot, playbackFence, PlaybackPolicy} from '../src/index.ts';
import {channelFence, fence, frame, queue, scope} from './fixtures.ts';

const raw = (entryId: string | null = 'g37.2') => ({nextEntryId: entryId, available: true, reason: 'ready', countdownMs: 5000, autoplay: true, passoutCheckDue: false, automaticAdvances: 1, startDuringCredits: false});

test('real dotted v1 next occurrences survive post-play parsing, projection matching and policy effects unchanged', () => {
  const next = {entryId: 'g37.2', available: true, reason: 'ready'};
  const post = parseQueuePostPlay(raw(), next);
  assert.equal(post.nextEntryId, next.entryId); assert.ok(Object.isFrozen(post));
  assert.throws(() => parseQueuePostPlay(raw(), {...next, entryId: 'g37.3'}));
  const current = playbackFence({...fence, entryId: 'g37.1'}), policy = new PlaybackPolicy();
  policy.begin(current, [], 1400);
  policy.update(frame({fence: current, postPlay: post, positionSeconds: 1400, ended: true}));
  const due = policy.update(frame({fence: current, postPlay: post, positionSeconds: 1400, ended: true, nowMs: 5000}));
  assert.deepEqual(due!.commands, [{kind: 'advance', mode: 'automatic', nextEntryId: next.entryId, fence: current}]);
});

test('occurrence grammar remains bounded and refuses paths, whitespace, controls, escaping and non-strings', () => {
  for (const value of ['', 'g1.0/next', 'g1.0\\next', 'g1.0?x', 'g1.0#x', 'g1.0%2Fnext', 'g1.0:next', 'g1.0 next', 'g1.0\n', 'g1.0\r', 'g1.0\0', 'g'.repeat(129), 1, undefined]) {
    assert.throws(() => parseQueuePostPlay({...raw(), nextEntryId: value}), String(value));
    assert.throws(() => playbackFence({...fence, entryId: value} as never), String(value));
  }
  assert.equal(parseQueuePostPlay(raw('g'.repeat(128))).nextEntryId!.length, 128);
  assert.equal(parseQueuePostPlay({...raw(null), available: false, reason: 'end'}).nextEntryId, null);
});

test('dotted occurrence acceptance does not loosen generic identity, QueueSnapshot or channel/queue separation', () => {
  for (const field of ['sessionId', 'itemId', 'sourceId', 'showId', 'seasonId']) assert.throws(() => playbackFence({...fence, [field]: 'g37.2'}));
  for (const field of ['sessionId', 'channelId', 'channelGeneration', 'sourceId']) assert.throws(() => playbackFence({...channelFence, [field]: 'g37.2'}));
  const snapshot = queue(); snapshot.entries[0]!.id = 'g37.2'; snapshot.currentEntryId = 'g37.2';
  assert.throws(() => parseQueueSnapshot(snapshot, scope));
  assert.throws(() => playbackFence({...channelFence, entryId: 'g37.2'} as never));
  const policy = new PlaybackPolicy();
  assert.throws(() => policy.begin(channelFence as never, [], 1400));
});
