import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeSearchDestination} from '../src-next/screens/search/model.ts';
import {SEARCH_CHIPS} from '../../../packages/client-core/src/presentation/search-model.ts';
import type {SearchGroup} from '../../../packages/client-core/src/search.ts';

const scope = {serverId: 'server-a', viewerId: 'local/account-a/profile-a'};
const tick = async () => {for (let n = 0; n < 6; n++) await new Promise<void>(resolve => setImmediate(resolve));};
const history = (q?: string) => ({serverId: scope.serverId, viewerFence: 'search-history-fence', maxEntries: 8,
  entries: q ? [{q, updatedAt: '2026-11-02T00:00:00Z', uses: 1}] : []});
/** A server answering the groups asked for; `reading` is installed when `readingInstalled`. */
function answer(path: string, readingInstalled: boolean) {
  const query = new URL(path, 'https://test.invalid').searchParams;
  const one = query.get('group') as SearchGroup | null;
  const asked = (one ? [one] : (query.get('groups') ?? 'movies').split(',')) as SearchGroup[];
  const capabilities = SEARCH_CHIPS.filter(chip => chip.id !== 'all').map(chip => ({id: chip.id, title: chip.id, available: chip.id !== 'reading' || readingInstalled, reason: '', sorts: ['relevance']}));
  return {scope: {serverId: scope.serverId, libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'search-fence'},
    revision: {catalog: 1, viewer: 1},
    query: {q: query.get('q'), group: one ?? '', groups: asked, limit: Number(query.get('limit')), sort: 'relevance', direction: 'desc', libraryIds: [], recorded: query.get('record') === '1'},
    capabilities: {groups: capabilities, sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 1000},
    groups: asked.map(id => ({id, entityKind: 'movie', title: id, status: 'success', errorCode: '', totalCount: id === 'movies' ? 1 : 0, totalCountExact: true, nextCursor: '',
      items: id === 'movies' ? [{id: 'movie-a', kind: 'movie', title: 'Harbor', libraryId: 'movies', navigation: {view: 'item', entityId: 'movie-a'}}] : []}))};
}
function setup(readingInstalled = false, remember = true) {
  const calls: URLSearchParams[] = [];
  let recorded: string | undefined;
  const destination = new NativeSearchDestination({scope, current: () => true, rememberHistory: () => remember, reportError: error => {throw error;},
    api: {request: async <T>(path: string) => {
      if (path.includes('/history')) return history(recorded) as T;
      const params = new URL(path, 'https://test.invalid').searchParams;
      calls.push(params);
      if (params.get('record') === '1') recorded = params.get('q') ?? undefined;
      return answer(path, readingInstalled) as T;
    }}});
  return {destination, calls};
}

test('the All view asks for exactly the drawn groups, never Collections or Authors (SR-6)', async () => {
  const {destination, calls} = setup();
  try {
    destination.setText('harbor'); await destination.commit(); await tick();
    const groups = calls[0]!.get('groups')!.split(',');
    assert.deepEqual(groups, SEARCH_CHIPS.map(chip => chip.id).filter(id => id !== 'all' && id !== 'reading'));
    assert.equal(groups.includes('collections') || groups.includes('authors'), false);
    assert.equal(calls.length, 1, 'no Reading repeat while the server does not offer it');
  } finally {destination.dispose();}
});

test('once the server says Reading is installed, a read-only repeat adds it, as web does', async () => {
  const {destination, calls} = setup(true);
  try {
    destination.setText('harbor'); await destination.commit(); await tick();
    assert.equal(calls.length, 2);
    assert.ok(calls[1]!.get('groups')!.split(',').includes('reading'));
    assert.equal(calls[1]!.get('record'), null, 'the repeat records nothing');
    destination.setText('harbour'); await tick(); await new Promise(resolve => setTimeout(resolve, 300)); await tick();
    assert.ok(calls.at(-1)!.get('groups')!.split(',').includes('reading'), 'later queries include Reading at once');
  } finally {destination.dispose();}
});

test('opening a result records its query once, by its own read, leaving the drawn answer in place (SR-3)', async () => {
  const {destination, calls} = setup();
  try {
    destination.setText('harbor'); await new Promise(resolve => setTimeout(resolve, 300)); await tick();
    const drawn = destination.getSnapshot().search, before = calls.length;
    assert.equal(calls.at(-1)!.get('record'), null, 'typing never records');
    destination.recordOpened(); await tick();
    assert.equal(calls.length, before + 1);
    assert.equal(calls.at(-1)!.get('record'), '1');
    assert.equal(calls.at(-1)!.get('q'), 'harbor');
    assert.ok(calls.at(-1)!.get('group'), 'the record is a one-group read');
    assert.equal(destination.getSnapshot().search, drawn, 'the drawn answer and its generation are untouched');
    assert.deepEqual(destination.getSnapshot().recent.map(entry => entry.q), ['harbor'], 'Recent searches shows it');
    destination.recordOpened(); await tick();
    assert.equal(calls.length, before + 1, 'a second open of the same query records nothing more');
  } finally {destination.dispose();}
});

test('with history off, opening a result records nothing', async () => {
  const {destination, calls} = setup(false, false);
  try {
    destination.setText('harbor'); await new Promise(resolve => setTimeout(resolve, 300)); await tick();
    const before = calls.length;
    destination.recordOpened(); await tick();
    assert.equal(calls.length, before);
  } finally {destination.dispose();}
});
