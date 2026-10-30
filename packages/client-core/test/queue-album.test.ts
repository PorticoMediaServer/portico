import test from 'node:test';
import assert from 'node:assert/strict';
import {consecutiveOnAlbum, parseEntry} from '../src/playback-v1/types.ts';

const entry = (o: Record<string, unknown>) => parseEntry({entryId: 'e' + JSON.stringify(o).length, position: 0, itemId: 'i', kind: 'song', title: 't', available: true, ...o});

test('queue entries read their album, disc and track (absent when unknown)', () => {
  const e = entry({albumId: 'a', disc: 1, track: 3});
  assert.equal(e.albumId, 'a'); assert.equal(e.disc, 1); assert.equal(e.track, 3);
  assert.equal(entry({}).albumId, undefined);
});

test('consecutive tracks of one album join; anything else may crossfade', () => {
  assert.equal(consecutiveOnAlbum(entry({albumId: 'a', disc: 1, track: 3}), entry({albumId: 'a', disc: 1, track: 4})), true);
  assert.equal(consecutiveOnAlbum(entry({albumId: 'a', disc: 1, track: 12}), entry({albumId: 'a', disc: 2, track: 1})), true, 'next disc');
  assert.equal(consecutiveOnAlbum(entry({albumId: 'a', disc: 1, track: 3}), entry({albumId: 'a', disc: 1, track: 7})), false, 'a skip on the album (shuffle)');
  assert.equal(consecutiveOnAlbum(entry({albumId: 'a', track: 3}), entry({albumId: 'b', track: 4})), false, 'another album');
  assert.equal(consecutiveOnAlbum(entry({albumId: 'a'}), entry({albumId: 'a'})), true, 'track numbers unknown');
  assert.equal(consecutiveOnAlbum(entry({}), entry({})), false);
});
