import test from 'node:test';
import assert from 'node:assert/strict';
import {createAudiobookBindings} from './bindings.ts';
import type {ScreenContext} from '../context.ts';
import {withQueries} from '../../../../../packages/client-core/test/fixtures/viewer-queries.ts';
const target = {libraryId: 'audio', view: 'book' as const, entityId: 'book'};
function harness(kind = 'audiobook') {
  let active = true, calls = 0;
  const starts: unknown[] = [];
  // The rebuilt server's answers: the library (an audiobook library, or another kind) and the book's detail, a
  // single file with its chapter markers and the listener's place.
  const raw = {revision: 1};
  const book = {id: 'book', kind: 'audiobook', title: 'Book', libraryId: 'audio', images: {}, addedAt: '2026-11-12T00:00:00Z', state: {positionMs: 12000},
    versions: [{id: 'v1', parts: [{size: 1, streams: []}], markers: [{kind: 'chapter', startMs: 0, endMs: 60_000, title: 'Chapter 1'}]}]};
  const answer = (path: string) => new URL(path, 'https://server.test').pathname === '/v1/items/book' ? {...book, title: `Book ${raw.revision}`}
    : {id: 'audio', name: 'Audio', kind: kind === 'audiobook' ? 'audiobooks' : 'books'};
  // The bindings read through the viewer's cache: a cache over this fake API.
  const context = withQueries(({api: {request: async (path: string) => {calls++;return answer(path);}}, scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => active,
    effects: {play: async (value: unknown) => {starts.push(value);}, openEntry: () => {}}}) as never) as unknown as ScreenContext;
  const binding = createAudiobookBindings(context, target);
  /** The page moved on the server (a newer catalog revision) and a hint says so: the next load reads the new page. */
  const moved = () => {raw.revision++;return context.queries.client.invalidateQueries();};
  return {binding, raw, starts, moved, calls: () => calls, retire: () => {active = false;}};
}
test('constructor is inert and a non-audiobook library cannot authorize the page', async () => {
  const h = harness('book');assert.equal(h.calls(), 0);
  await assert.rejects(h.binding.load(), {name: 'AbortError'});assert.deepEqual(h.starts, []);h.binding.dispose();
});
test('resume delegates current stored progress and preference to the authenticated server', async () => {
  const h = harness();await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
  await h.binding.primary(page);assert.deepEqual(h.starts, [{libraryId: 'audio', itemId: 'book', startSeconds: 0, storedResume: true}]);h.binding.dispose();
});
test('explicit chapter and restart keep captured seconds without stored resume', async () => {
  const h = harness();await h.binding.load();const page = h.binding.service.getSnapshot().projection!, row = h.binding.service.getSnapshot().appended[0].entries[0];
  await h.binding.play(page, row, 42);await h.binding.primary(page, true);
  assert.deepEqual(h.starts, [{libraryId: 'audio', itemId: 'book', startSeconds: 42}, {libraryId: 'audio', itemId: 'book', startSeconds: 0}]);h.binding.dispose();
});
test('a forged row or retired authority cannot start playback', async () => {
  const h = harness();await h.binding.load();const page = h.binding.service.getSnapshot().projection!, row = h.binding.service.getSnapshot().appended[0].entries[0];
  await assert.rejects(h.binding.play(page, {...row}), {name: 'AbortError'});
  h.retire();await assert.rejects(h.binding.primary(page), {name: 'AbortError'});assert.deepEqual(h.starts, []);h.binding.dispose();
});
