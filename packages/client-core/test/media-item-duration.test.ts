import test from 'node:test';
import assert from 'node:assert/strict';
import {parseMediaItem} from '../src/media-item.ts';

const item = (over: Record<string, unknown> = {}) => {
  const value: Record<string, unknown> = {id: 'm1', kind: 'movie', title: 'One', libraryId: 'l1', available: true, durationMs: 5_400_000, progressMs: 0, ...over};
  for (const key of Object.keys(value)) if (value[key] === undefined) delete value[key];
  return value;
};

test('P2-29: an item length the server cannot represent stays unknown, never zero; anything else stays exact', () => {
  assert.equal(parseMediaItem(item()).duration, 5400);
  const unknown = parseMediaItem(item({durationMs: undefined, durationState: 'unrepresentable'}));
  assert.equal(unknown.duration, undefined);assert.equal(unknown.durationUnrepresentable, true);
  assert.equal('durationState' in unknown, false);
  assert.throws(() => parseMediaItem(item({durationMs: undefined})), 'a length without its explanation is unreadable');
  assert.throws(() => parseMediaItem(item({durationState: 'unrepresentable'})), 'a state beside a length is contradictory');
  assert.throws(() => parseMediaItem(item({durationState: 'later'})));
});
