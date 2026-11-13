import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryBrowseCapabilities, libraryItemQuery, libraryQueryControls} from '../src/library-browse.ts';

import {BrowseQueryBuilder, parseBrowseCapabilities} from '../src/browse.ts';
import {browseToolbarModel} from '../src/presentation/kit-model/index.ts';
import {defaultI18n} from '../../i18n/src/index.ts';
import {readItems} from '../src/items.ts';
import type {Library} from '../../contracts/src/next/api.ts';

type Caps = {pivots: {id: string; entityKinds: string[]}[]; sorts: {id: string}[]};
const library = (kind: string, views: string[], sorts: string[] = ['title', 'added']) =>
  ({id: 'lib', name: 'Library', kind, views: views.map(each => ({kind: each})), sorts: sorts.map(id => ({id, order: 'asc'})), filters: []}) as never;

test('a film or show library offers its collections as a tab of their own', () => {
  const caps = libraryBrowseCapabilities(library('movies', ['movie', 'collection']), 'collections') as Caps & {resolvedPivot: {entityKinds: string[]}};
  assert.deepEqual(caps.pivots.map(pivot => pivot.id), ['movies', 'collections']);
  assert.deepEqual(caps.resolvedPivot.entityKinds, ['collection']);
  assert.deepEqual(libraryItemQuery({id: 'lib'}, 'collections', [], [{field: 'title', direction: 'asc'}]), {library: 'lib', kind: 'collection', sort: 'title', order: 'asc'});
});

test('a show library\'s Recently aired sorts by the newest episode; music does not offer it under that name', () => {
  const shows = libraryBrowseCapabilities(library('shows', ['show', 'episode'], ['title', 'latest']), 'shows') as Caps;
  assert.deepEqual(shows.sorts.map(sort => sort.id), ['title', 'latestAired']);
  assert.deepEqual(libraryItemQuery({id: 'lib'}, 'shows', [], [{field: 'latestAired', direction: 'desc'}]), {library: 'lib', kind: 'show', sort: 'latest', order: 'desc'});
  const anime = libraryBrowseCapabilities(library('anime', ['show', 'episode'], ['title', 'latest']), 'shows') as Caps;
  assert.deepEqual(anime.sorts.map(sort => sort.id), ['title', 'latestAired']);
  const music = libraryBrowseCapabilities(library('music', ['artist', 'album', 'track'], ['title', 'latest']), 'albums') as Caps;
  assert.deepEqual(music.sorts.map(sort => sort.id), ['title']);
});

test('a reading library\'s list asks for its own kinds: books in a book library, comics with their series in a comic library', () => {
  const books = library('books', ['book', 'series']), comics = library('comics', ['comic', 'series']), audiobooks = library('audiobooks', ['audiobook', 'series']);
  assert.equal(libraryItemQuery(books, 'books', [], []).kind, 'book');
  assert.equal(libraryItemQuery(audiobooks, 'books', [], []).kind, 'audiobook');
  assert.equal(libraryItemQuery(comics, 'comics', [], []).kind, 'comic,series');
  // Without the library's lists (a page read by its library id alone) Books asks for both
  // kinds: each library holds only its own, so either library's list is its books.
  assert.equal(libraryItemQuery({id: 'lib'}, 'books', [], []).kind, 'audiobook,book');
  // Its Series list comes first, as a tab, not as a list of comics.
  assert.deepEqual((libraryBrowseCapabilities(comics, 'comics') as Caps).pivots.map(pivot => pivot.id), ['series', 'comics']);
});


test('the advertised In progress filter reaches reading and watch rows, the shared kit labels and the item request', async () => {
  for (const [kind, pivot, entity] of [['books', 'books', 'book'], ['comics', 'comics', 'comic'], ['movies', 'movies', 'movie'], ['shows', 'shows', 'show'], ['anime', 'shows', 'show'], ['audiobooks', 'books', 'audiobook']]) {
    const wire: Library = {id: 'lib', name: 'Library', kind: kind!, views: [{kind: entity!}], sorts: [{id: 'title', order: 'asc'}],
      filters: ['watched', 'inProgress', 'favorite', 'inList'].map(id => ({id, type: 'boolean'}))};
    const caps = parseBrowseCapabilities(libraryBrowseCapabilities(wire, pivot!));
    const model = browseToolbarModel(caps, caps.library.kind, [], {field: 'title', direction: 'asc'}, {t: defaultI18n.t, locale: 'en-US', textEntry: false});
    assert.deepEqual(model.quick.map(q => q.label?.text), kind === 'books' || kind === 'comics'
      ? ['Unread', 'In progress', 'Finished', 'My List'] : ['Unplayed', 'In progress', 'Favorites', 'My List']);
    const built = new BrowseQueryBuilder(caps, pivot!).quickFilter('in-progress').build();
    const query = libraryItemQuery(wire, pivot!, [built.query!], []);
    assert.equal(query.inProgress, true);
    assert.equal(query.watched, undefined);
    const asked: string[] = [];
    await readItems({request: async <T>(path: string) => {asked.push(path); return {items: []} as T;}}, query);
    assert.equal(asked.length, 1);
    assert.equal(new URL(asked[0]!, 'https://portico.invalid').searchParams.get('inProgress'), 'true');
    assert.deepEqual(libraryQueryControls(new URLSearchParams({kind: query.kind!, inProgress: 'true'}).toString())?.nodes, [built.query]);
    const older = parseBrowseCapabilities(libraryBrowseCapabilities({...wire, filters: wire.filters!.filter(f => f.id !== 'inProgress')}, pivot!));
    assert.equal(older.quickFilters.some(q => q.id === 'in-progress'), false);
  }
});
