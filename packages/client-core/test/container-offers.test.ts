import test from 'node:test';
import assert from 'node:assert/strict';
import {validateContentEntry} from '../src/library-content.ts';
import {readCatalogueSelectionItem} from '../src/catalogue-selection.ts';

// MOB-030: a displayed container keeps its aggregate selection offers from a
// Browse or content page, and the catalogue selection adapter honours them.
const offers = {play: true, queue: true, playlist: false, collection: false, myList: false, favorite: true, watched: false, played: true, rating: false,
  editMetadata: false, refreshMetadata: false, delete: false, download: false, details: true,
  state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 0, containerRevision: 0},
  selection: {play: true, queue: true, playlist: false, collection: false, myList: false, favorite: true, watched: false, download: false, edit: false, refresh: false, delete: false}};

test('a container entry keeps its offers; a leaf never borrows them', async () => {
  const album = validateContentEntry({id: 'album', kind: 'album', title: 'Album', libraryId: 'music', homeActions: offers});
  assert.equal(album.homeActions?.selection.favorite, true);
  const song = validateContentEntry({id: 'song', kind: 'song', title: 'Song', libraryId: 'music', homeActions: offers});
  assert.equal(song.homeActions, undefined);
  const unreadable = validateContentEntry({id: 'album', kind: 'album', title: 'Album', libraryId: 'music', homeActions: {play: true}});
  assert.equal(unreadable.homeActions, undefined, 'an unreadable offer grants nothing');
  const item = await readCatalogueSelectionItem({api: {request: async () => { throw new Error('no read'); }} as any, scope: {serverId: 's', viewerId: 'v'} as any,
    requestId: async () => 'r', signal: new AbortController().signal, isCurrent: () => true}, album, {favorite: true, play: true, myList: true});
  assert.equal(item.capabilities.favorite, true);
  assert.equal(item.capabilities.play, true);
  assert.equal(item.capabilities.myList, false, 'a port without its offer stays unavailable');
});
