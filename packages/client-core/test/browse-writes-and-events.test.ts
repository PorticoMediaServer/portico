import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseSession} from '../src/browse-session.ts';
import {setContainerState, undoContainerState} from '../src/container-state.ts';
import {homeDocument} from '../src/home.ts';
import {homeEntrySelection} from '../src/home-selection.ts';
import {homeSelectionContext} from '../src/home-selection/index.ts';
import {checkedContext, checkedItem} from '../src/selection-model/validation.ts';
import {outboxBody} from '../src/outbox-routes.ts';
import {accessChangedEvent, serverEventsFrom} from '../src/server-events.ts';

// The server's own answers, as seen from a running server.
const marked = {watched: true, playCount: 0, favorite: false, inList: false, hidden: false, undo: {token: 'und_1', expiresAt: '2026-11-10T22:46:29Z'}};
const season = {id: 'itm_season', kind: 'season', title: 'Season 1', libraryId: 'lib_shows', counts: {leaves: 10, unwatchedLeaves: 10},
  state: {watched: false, playCount: 0, favorite: false, inList: false, hidden: false}};

test('marking a season watched offers Undo, and Undo sends the token back and reads the season again', async () => {
  const sent: {path: string; method: string; body: unknown}[] = [];
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    sent.push({path, method, body});
    return (method === 'PUT' ? marked : method === 'POST' ? undefined : season) as T;
  }};
  const state = await setContainerState(api, 'season', 'itm_season', {watched: true});
  assert.equal(state.watched, true);
  assert.equal(state.undoToken, 'und_1');
  assert.deepEqual(sent, [{path: '/v1/items/itm_season/state', method: 'PUT', body: {watched: true}}]);

  const back = await undoContainerState(api, 'season', 'itm_season', {undoToken: state.undoToken!});
  assert.deepEqual(sent.slice(1), [{path: '/v1/items/state:undo', method: 'POST', body: {token: 'und_1'}}, {path: '/v1/items/itm_season', method: 'GET', body: undefined}]);
  assert.equal(back.watched, false);
  assert.equal(back.unwatchedCount, 10);
});

test('a change to a title kept while the server was out of reach is sent with how long ago it was made', () => {
  const kept = {path: '/v1/items/itm_film/state', method: 'PUT', body: {favorite: true}, createdAt: 1_000};
  assert.deepEqual(outboxBody(kept, 61_000), {favorite: true, madeAgoMs: 60_000});
  // A clock that went backwards never says the change is in the future.
  assert.deepEqual(outboxBody(kept, 500), {favorite: true, madeAgoMs: 0});
  // Other kept edits are sent as they were made.
  assert.deepEqual(outboxBody({path: '/v1/lists/lst_1/items', method: 'POST', body: {itemId: 'itm_film'}, createdAt: 1_000}, 61_000), {itemId: 'itm_film'});
});

test('the change feed says what to read again, and an unknown change only moves the place on', () => {
  const change = (data: object, id = '7') => serverEventsFrom({id, event: 'change', data: JSON.stringify(data)});
  const library = {kind: 'library', id: 'lib_films'};
  // Watched on another device: the title's own state and Continue Watching are both read again.
  assert.deepEqual(change({topic: 'state', op: 'updated', id: 'itm_film', libraryId: 'lib_films'}),
    [{id: '7:progress.changed', type: 'progress.changed', resource: library}, {id: '7', type: 'personal.changed', resource: library}]);
  assert.deepEqual(change({topic: 'item', op: 'created', libraryId: 'lib_films'}), [{id: '7', type: 'library.changed', resource: library}]);
  // A profile's access changed: the app starts over with nothing it read before.
  assert.deepEqual(change({topic: 'access'}), [{id: '7', type: accessChangedEvent}]);
  assert.deepEqual(serverEventsFrom({id: '8', event: 'resync', data: ''}), [{id: '8', type: 'stream.resync'}]);
  // A newer server's topic, or data this app cannot read, keeps the place and changes nothing on screen.
  assert.deepEqual(change({topic: 'something-new'}), [{id: '7', type: 'stream.unknown'}]);
  assert.deepEqual(serverEventsFrom({id: '9', event: 'change', data: '{'}), [{id: '9', type: 'stream.unread'}]);
  assert.deepEqual(serverEventsFrom({event: 'change', data: '{}'}), []);
});

test('Home with a film and a book to read can be selected from: the film is selectable, the book only has its card', () => {
  const state = {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false};
  const home = homeDocument({rows: [{id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [
    {id: 'itm_film', kind: 'movie', title: 'A film', libraryId: 'lib_films', images: {}, state},
    {id: 'itm_book', kind: 'book', title: 'A book', libraryId: 'lib_books', images: {}, state},
  ]}]} as never);
  const [film, book] = home.rows[0]!.entries;
  assert.deepEqual(home.rows[0]!.entries.map(entry => entry.kind), ['movie', 'ebook']);
  // Home once failed to draw at all here: an empty selection context, then a book handed to selection.
  checkedContext(homeSelectionContext({serverId: 'srv', authority: 'local', accountId: 'usr', profileId: 'prf', viewId: 'home'}, home));
  assert.equal(checkedItem(homeEntrySelection(film as never, {platform: 'web'})!).id, 'itm_film');
  assert.equal(homeEntrySelection(book as never, {platform: 'web'}), undefined);
});

test('a list page never waits for the count, and a jump opens the list at its position', async () => {
  const asked: string[] = [], facts: object[] = [];
  let answerCount = (_: unknown): void => {};
  const film = (n: number) => ({id: 'itm_' + n, kind: 'movie', title: 'Film ' + n, images: {}, state: {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false}});
  const session = new BrowseSession({query: {libraryId: 'lib_films', pivot: 'movies', capabilities: {} as never, nodes: [], sorts: [{field: 'title', direction: 'asc'}]},
    current: () => true, item: entry => entry, onFacts: value => {facts.push(value);},
    request: (path: string) => {
      const url = new URL(path, 'http://server'), query = url.searchParams;
      // The letter rail: its own request, never part of a page.
      if (url.pathname === '/v1/facets') return Promise.resolve({facet: 'letter', values: [{value: '#', count: 3}, {value: 'A', count: 40}, {value: 'B', count: 25}]});
      asked.push(query.toString());
      if (query.get('total') === 'true') return new Promise(resolve => {answerCount = resolve;});
      const at = Number(query.get('at') ?? (query.get('cursor') ? 2 : 0));
      return Promise.resolve({items: [film(at), film(at + 1)], ...(at + 2 < 450 ? {nextCursor: 'after-' + (at + 1)} : {})});
    }});
  const none = new AbortController().signal;
  // The first page is shown while the count is still out: the list is "at least" what has been read.
  const first = await session.fetchPage(0, 2, none);
  assert.deepEqual([first.items.length, first.total, first.totalIsLowerBound], [2, 3, true]);
  assert.equal(asked.filter(query => query.includes('total=true')).length, 1, 'the count is asked once, by itself');
  assert.ok(asked.some(query => !query.includes('total') && query.includes('limit=2')), 'the page did not ask for it');
  answerCount({items: [film(0)], total: 450}); await new Promise(resolve => setImmediate(resolve));
  // Both facts reach the screen without another page being read: the length, and where each letter starts.
  assert.deepEqual(facts, [{positionIndex: {'#': 0, A: 3, B: 43}}, {total: 450}]);
  // The next page follows its cursor and now knows the total.
  const second = await session.fetchPage(2, 2, none);
  assert.deepEqual([second.total, second.totalIsLowerBound ?? false], [450, false]);
  assert.match(asked.at(-1)!, /cursor=after-1/);
  // A letter or the scroll bar: the list opens there in one request.
  const far = await session.fetchPage(300, 2, none);
  assert.match(asked.at(-1)!, /at=300/);
  assert.equal(far.items[0]!.id, 'itm_300');
});
