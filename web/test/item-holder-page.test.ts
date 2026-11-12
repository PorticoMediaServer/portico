import test from 'node:test';
import assert from 'node:assert/strict';
import {holderPage} from '../src/screens/detail/model.ts';

test('a show, a season or an album opened by its address goes to its own page in its library; a single title stays', () => {
  assert.deepEqual(holderPage({id: 'itm_show', kind: 'show'}, 'lib_tv'), {libraryId: 'lib_tv', view: 'show', entityId: 'itm_show'});
  assert.deepEqual(holderPage({id: 'itm_season', kind: 'season'}, 'lib_tv'), {libraryId: 'lib_tv', view: 'season', entityId: 'itm_season'});
  assert.deepEqual(holderPage({id: 'itm_album', kind: 'album'}, 'lib_music'), {libraryId: 'lib_music', view: 'album', entityId: 'itm_album'});
  for (const kind of ['movie', 'episode', 'song', 'book']) assert.equal(holderPage({id: 'itm_x', kind}, 'lib'), undefined, kind);
  assert.equal(holderPage({id: 'itm_show', kind: 'show'}, ''), undefined, 'no library, no page to go to');
});
