import test from 'node:test';
import assert from 'node:assert/strict';
import {parseEntry} from '../src/playback-v1/types.ts';

const base = {entryId: 'qe_1', position: 0, itemId: 'itm_song', kind: 'track', title: 'In the Flesh?', available: true, images: {}};

test("a queue entry keeps the library the server names, so Now Playing can read the song's own actions", () => {
  assert.equal(parseEntry({...base, libraryId: 'lib_music'}).libraryId, 'lib_music');
  assert.equal(parseEntry(base).libraryId, undefined);
  assert.equal(parseEntry({...base, libraryId: ''}).libraryId, undefined);
});
