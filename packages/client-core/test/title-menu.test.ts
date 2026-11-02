import test from 'node:test';
import assert from 'node:assert/strict';
import {homeEntryCapabilities} from '../src/home-selection.ts';
import {validateHomeEntry} from '../src/home.ts';
import {entryActionList} from '../src/presentation/entry-actions.ts';
import {detailMenuCapabilities, limitMenuCapabilities, titleMenuGroups, titleMenuPlatform, type TitleDetailFacts, type TitleMenuPorts} from '../src/presentation/title-menu.ts';

const everything: TitleMenuPorts = {play: true, playlist: true, collection: true, edit: true, delete: true, refresh: true, rate: true};
const offers = (patch: Record<string, unknown> = {}) => ({play: true, queue: true, playlist: true, collection: true, myList: true, favorite: true, watched: true, played: true, rating: true,
  editMetadata: true, refreshMetadata: true, delete: true, download: true, details: true,
  state: {watchlisted: false, favorite: true, watched: false, rating: 6, personalRevision: 1, containerRevision: 1}, selection: {}, ...patch});
const ids = (caps: Parameters<typeof titleMenuGroups>[0]) => entryActionList(titleMenuGroups(caps)).map(action => action.id);

const movie = (patch: Partial<TitleDetailFacts> = {}): TitleDetailFacts => ({kind: 'movie', available: true, actions: [
  {id: 'play', enabled: true, playback: {itemId: 'm', startSeconds: 0}}, {id: 'watchlist', enabled: true}, {id: 'favorite', enabled: true}, {id: 'watched', enabled: true},
  {id: 'rating', enabled: true}, {id: 'add_to_playlist', enabled: true}, {id: 'add_to_collection', enabled: true}, {id: 'metadata_edit', enabled: true}, {id: 'delete', enabled: true}],
  personal: {watchlisted: false, favorite: true, watched: false, rating: null}, ...patch});

test('a leaf read from Detail offers the model\'s full set in its fixed order', () => {
  const caps = limitMenuCapabilities(detailMenuCapabilities(movie()), everything);
  assert.deepEqual(ids(caps), ['play', 'watchlist', 'favorite', 'watched', 'rate', 'addToPlaylist', 'addToCollection', 'details', 'editMetadata', 'refreshMetadata', 'delete']);
  const groups = titleMenuGroups(caps);
  assert.deepEqual(groups.map(group => group.id), ['play', 'personal', 'organize', 'open', 'manage']);
  assert.equal(groups.at(-1)!.actions.at(-1)!.id, 'delete', 'the destructive action is last');
});

test('Detail personal state words the rows and a started title resumes', () => {
  const caps = detailMenuCapabilities(movie({actions: [{id: 'resume', enabled: true, playback: {itemId: 'm', startSeconds: 120}}, {id: 'favorite', enabled: true}, {id: 'watched', enabled: true}],
    personal: {watchlisted: false, favorite: true, watched: true, rating: 8}}));
  assert.equal(caps.progressSeconds, 120);
  const list = entryActionList(titleMenuGroups(limitMenuCapabilities(caps, everything)));
  assert.deepEqual(list.map(action => action.id), ['resume', 'favorite', 'watched', 'details', 'refreshMetadata'], 'no Play from beginning: only Home can do it');
  assert.equal(list.find(action => action.id === 'favorite')!.selected, true);
  assert.equal(list.find(action => action.id === 'watched')!.icon, 'eyeOff', 'a watched title offers to un-mark it');
});

test('only enabled Detail actions become rows; a disabled or absent one is not drawn', () => {
  const caps = detailMenuCapabilities(movie({actions: [{id: 'play', enabled: true, playback: {itemId: 'm', startSeconds: 0}}, {id: 'favorite', enabled: false}, {id: 'delete', enabled: false}, {id: 'metadata_edit', enabled: false}]}));
  assert.deepEqual(ids(limitMenuCapabilities(caps, everything)), ['play', 'details', 'refreshMetadata']);
});

test('an unavailable title or one with no play action has no Play', () => {
  assert.equal(detailMenuCapabilities(movie({available: false})).playable, false);
  assert.equal(detailMenuCapabilities(movie({actions: [{id: 'favorite', enabled: true}]})).playable, false);
});

test('a server grant on a search or container entry becomes the same menu', () => {
  const show = validateHomeEntry({id: 'show', kind: 'show', title: 'Show', libraryId: 'tv', playback: {itemId: 'ep'}, available: true, homeActions: offers()});
  const caps = limitMenuCapabilities(homeEntryCapabilities(show, titleMenuPlatform), everything);
  // a show has no playlist, collection or delete here; its metadata edits as an aggregate; it refreshes as a show
  assert.deepEqual(ids(caps), ['play', 'watchlist', 'favorite', 'watched', 'details', 'editMetadata', 'refreshMetadata']);
  assert.equal(caps.rating, undefined, 'Rate reads a title\'s own Detail, so it is a leaf row');
  assert.equal(caps.download, undefined);
  assert.equal(caps.queue, undefined);
});

test('a leaf search entry with grants keeps its rating and organise rows', () => {
  const movieEntry = validateHomeEntry({id: 'm', kind: 'movie', title: 'Movie', libraryId: 'films', playback: {itemId: 'm', startSeconds: 90}, progressMs: 90_000, available: true, homeActions: offers()});
  const caps = limitMenuCapabilities(homeEntryCapabilities(movieEntry, titleMenuPlatform), everything);
  assert.deepEqual(ids(caps), ['resume', 'watchlist', 'favorite', 'watched', 'rate', 'addToPlaylist', 'addToCollection', 'details', 'editMetadata', 'refreshMetadata', 'delete']);
  assert.equal(caps.rating, 6);
});

test('a screen without a port never draws the row it could not run', () => {
  const caps = homeEntryCapabilities(validateHomeEntry({id: 'm', kind: 'movie', title: 'Movie', libraryId: 'films', playback: {itemId: 'm'}, available: true, homeActions: offers()}), titleMenuPlatform);
  const bare = limitMenuCapabilities(caps, {play: false, playlist: false, collection: false, edit: false, delete: false, refresh: false, rate: false});
  assert.deepEqual(ids(bare), ['watchlist', 'favorite', 'watched', 'details']);
});

test('owner rows follow the grant: nothing from the server, nothing here', () => {
  const caps = homeEntryCapabilities(validateHomeEntry({id: 'm', kind: 'movie', title: 'Movie', libraryId: 'films', playback: {itemId: 'm'}, available: true,
    homeActions: offers({editMetadata: false, refreshMetadata: false, delete: false})}), titleMenuPlatform);
  assert.deepEqual(ids(limitMenuCapabilities(caps, everything)).filter(id => ['editMetadata', 'refreshMetadata', 'delete'].includes(id)), []);
});

test('a title with no Detail read still has Details; a collection keeps Play but has no personal rows', () => {
  assert.deepEqual(ids(limitMenuCapabilities({kind: 'movie', details: true}, everything)), ['details']);
  const collection = homeEntryCapabilities(validateHomeEntry({id: 'c', kind: 'collection', title: 'Collection', libraryId: 'films', homeActions: offers()}), titleMenuPlatform);
  assert.deepEqual(ids(limitMenuCapabilities(collection, everything)), ['play', 'details']);
});
