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
  assert.equal(parseMediaItem(item({durationMs: undefined})).duration, undefined, 'the current Item may omit an unknown length');
  assert.throws(() => parseMediaItem(item({durationState: 'unrepresentable'})), 'a state beside a length is contradictory');
  assert.throws(() => parseMediaItem(item({durationState: 'later'})));
});

test('current item replies carry nested progress, optional length/availability and artwork', () => {
 const reply = {id: 'itm_1', kind: 'movie', libraryId: 'lib_1', title: 'Film', state: {positionMs: 12345}, images: {poster: '/poster', backdrop: '/backdrop'}, summary: 'Story'};
 const parsed = parseMediaItem(reply);
 assert.equal(parsed.progressSeconds, 12.345);assert.equal(parsed.available, true);assert.equal(parsed.duration, undefined);
 assert.equal(parsed.posterUrl, '/poster');assert.equal(parsed.backdropUrl, '/backdrop');assert.equal(parsed.overview, 'Story');
 assert.equal(parseMediaItem({...reply, available: false, durationMs: 60000}).duration, 60);
 assert.equal(parseMediaItem({...reply, kind: 'track'}).kind, 'song');
 assert.equal(parseMediaItem({...reply, kind: 'audiobook'}).kind, 'audiobook_file');
 for (const patch of [{durationMs: -1}, {available: 'yes'}, {state: {positionMs: -1}}]) assert.throws(() => parseMediaItem({...reply, ...patch}));
});
