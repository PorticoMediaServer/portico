import test from 'node:test';
import assert from 'node:assert/strict';
import {LibraryContentService} from '../src/library-content.ts';
import {withQueries} from './fixtures/viewer-queries.ts';

const scope = {viewerId: JSON.stringify(['hosted', 'account', 'profile']), serverId: 'server'};
const film = (id: string) => ({id, kind: 'movie', title: `Film ${id}`, images: {poster: `/v1/images/${id}`}});

function service(answers: (path: string) => unknown, paths: string[] = []) {
  return new LibraryContentService(withQueries({scope, api: {request: async <T>(path: string) => { paths.push(path); return answers(path) as T; }}}));
}

test('a library\'s Discover is its landing page: each of its rows is a rail, in the server\'s order, with the server\'s own titles', async () => {
  const paths: string[] = [];
  const nav = service(path => path === '/v1/libraries/lib_a/landing'
    ? {rows: [{id: 'continue', title: 'Continue Watching', shape: 'landscape', items: [film('m1')]},
      {id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [film('m2'), film('m3'), {id: 'x', kind: 'something-new', title: 'Not a card'}]},
      {id: 'mood:cozy', title: 'Cozy stories for you', shape: 'poster', items: []}]}
    : {id: 'lib_a', name: 'Films', kind: 'movies'}, paths);
  await nav.select({libraryId: 'lib_a', view: 'discover'});
  const snap = nav.getSnapshot();
  assert.equal(snap.phase, 'ready');
  assert.deepEqual([...paths].sort(), ['/v1/libraries/lib_a', '/v1/libraries/lib_a/landing']);
  // A row with nothing the app can draw has no rail; a card of an unknown kind is left out alone.
  assert.deepEqual(snap.sections.map(section => [section.id, section.type, section.heading.fallback, section.entries.map(entry => entry.id)]),
    [['continue', 'rail', 'Continue Watching', ['m1']], ['recently_added', 'rail', 'Recently Added', ['m2', 'm3']]]);
  // The layout reads the library's kind in the app's words.
  assert.equal(snap.projection?.scope.libraryKind, 'movie');
  assert.equal(snap.projection?.scope.view, 'discover');
  assert.equal(snap.sections[0]!.entries[0]!.libraryId, 'lib_a');
  nav.dispose();
});

test('an audiobooks library\'s Discover says its kind, so it draws square cards; an empty landing is an empty Discover, not a failure', async () => {
  const nav = service(path => path.endsWith('/landing') ? {rows: []} : {id: 'lib_b', name: 'Audiobooks', kind: 'audiobooks'});
  await nav.select({libraryId: 'lib_b', view: 'discover'});
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.equal(nav.getSnapshot().projection?.scope.libraryKind, 'audiobook');
  assert.deepEqual(nav.getSnapshot().sections, []);
  nav.dispose();
});
