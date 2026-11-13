import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HomeService} from '../src/home-service.ts';
import {HomeLayoutEditor} from '../src/home-layout-editor.ts';
import {validateContentEntry} from '../src/library-content.ts';

// Customize Home is saved as two of the profile's settings and applied by GET /v1/home.
const settings = JSON.parse(readFileSync(new URL('./fixtures/next-preferences.json', import.meta.url), 'utf8'));
const item = (id: string) => ({id, kind: 'movie', title: id, libraryId: 'lib_films', state: {watched: false, playCount: 0, favorite: false, inList: false}});
function server(saved: Readonly<{order?: string[]; hidden?: string[]}> = {}) {
  const sent: unknown[] = [];
  let order = saved.order ?? [], hidden = saved.hidden ?? [], views: string[] = [], offline = false;
  // As GET /v1/home answers: in the saved order without hidden rows; with hidden=include, a hidden row in its place, named, with no items.
  // As server-next does, a saved view's row comes only from home.savedViews.
  const rows = (include: boolean) => [{id: 'continue', title: 'Continue Watching', shape: 'landscape', items: [item('a')]}, {id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [item('b')]},
    ...views.map(id => ({id: 'view:' + id, title: 'Nineties drama', shape: 'poster', items: [item('c')], library: 'lib_films', query: 'genre=Drama'}))]
    .sort((x, y) => (order.indexOf(x.id) + 1 || 99) - (order.indexOf(y.id) + 1 || 99))
    .flatMap(row => row.id !== 'continue' && hidden.includes(row.id) ? include ? [{...row, items: [], hidden: true}] : [] : [row]);
  const asked: string[] = [];
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    if (path.startsWith('/v1/home')) {asked.push(path); return {rows: rows(path.includes('hidden=include'))} as T;}
    if (method === 'PATCH') {
      if (offline) throw Object.assign(new Error('Saved on this device.'), {code: 'sync_pending'});
      sent.push(body); const values = (body as {values: Record<string, string[] | null>}).values;
      order = values['home.rowOrder'] ?? []; hidden = values['home.hiddenRowIds'] ?? []; views = values['home.savedViews'] ?? [];
    }
    return {...settings, effective: {...settings.effective, 'home.rowOrder': order, 'home.hiddenRowIds': hidden, 'home.savedViews': views}} as T;
  }};
  return {api, sent, asked, goOffline: () => {offline = true;}};
}

test('Customize Home saves the order and hidden rows as settings, Home follows, and Reset takes both away', async () => {
  const {api, sent} = server(), home = new HomeService(api), editor = new HomeLayoutEditor(home, {requestId: () => 'r', isCurrent: () => true});
  await home.load(); await editor.load();
  assert.deepEqual(editor.getSnapshot().draft!.order, ['continue', 'recently_added']);
  editor.move('recently_added', 0); editor.show('recently_added', false);
  assert.equal(await editor.save(), true);
  assert.deepEqual(sent, [{scope: 'profile-server', deviceClass: 'web', values: {'home.rowOrder': ['recently_added', 'continue'], 'home.hiddenRowIds': ['recently_added'], 'home.savedViews': []}}]);
  assert.deepEqual(home.getSnapshot().document!.rows.map(row => row.id), ['continue']);
  await editor.load();
  const hidden = editor.getSnapshot().draft!.view.rows.find(row => row.id === 'recently_added')!;
  assert.equal(hidden.title, 'Recently Added'); assert.equal(hidden.hidden, true);
  assert.equal(await editor.reset(), true);
  assert.deepEqual(sent[1], {scope: 'profile-server', deviceClass: 'web', values: {'home.rowOrder': null, 'home.hiddenRowIds': null, 'home.savedViews': null}});
  assert.deepEqual(home.getSnapshot().document!.rows.map(row => row.id), ['continue', 'recently_added']);
});

test('a saved view added in Customize is still on Home after a reload, and taking it off Home removes it', async () => {
  const {api, sent} = server(), home = new HomeService(api), editor = new HomeLayoutEditor(home, {requestId: () => 'r', isCurrent: () => true});
  await home.load(); await editor.load();
  const view = {id: 'v1', kind: 'view', status: 'ready', name: 'Nineties drama', definition: {libraryId: 'lib_films', pivot: 'movies', query: null, sort: [], presentation: 'grid'}};
  editor.add(view as never); assert.equal(await editor.save(), true);
  assert.deepEqual((sent[0] as {values: Record<string, unknown>}).values['home.savedViews'], ['v1'], 'the server builds the row from this setting');
  const reloaded = new HomeService(api); await reloaded.load();
  assert.deepEqual(reloaded.getSnapshot().document!.rows.map(row => row.id), ['continue', 'recently_added', 'view:v1']);
  await editor.load();
  assert.ok(editor.getSnapshot().draft!.order.includes('view:v1'), 'Customize reads it back in place');
  editor.remove('view:v1'); assert.equal(await editor.save(), true);
  assert.deepEqual((sent[1] as {values: Record<string, unknown>}).values['home.savedViews'], []);
  await reloaded.load();
  assert.deepEqual(reloaded.getSnapshot().document!.rows.map(row => row.id), ['continue', 'recently_added']);
});

test('after a restart Customize Home names a hidden row from the server and can show it again; Home itself never asks for hidden rows', async () => {
  const {api, sent, asked} = server({hidden: ['recently_added']}), home = new HomeService(api), editor = new HomeLayoutEditor(home, {requestId: () => 'r', isCurrent: () => true});
  await home.load();
  assert.deepEqual(home.getSnapshot().document!.rows.map(row => row.id), ['continue']);
  await editor.load();
  assert.deepEqual(asked, ['/v1/home', '/v1/home?hidden=include']);
  const hidden = editor.getSnapshot().draft!.view.rows.find(row => row.id === 'recently_added')!;
  assert.equal(hidden.title, 'Recently Added'); assert.equal(hidden.hidden, true);
  editor.show('recently_added', true); assert.equal(await editor.save(), true);
  assert.deepEqual((sent[0] as {values: Record<string, unknown>}).values['home.hiddenRowIds'], []);
  assert.deepEqual(home.getSnapshot().document!.rows.map(row => row.id), ['continue', 'recently_added']);
});

test('a layout saved while the server is out of reach counts as saved, and the phone names its own kind of device', async () => {
  const {api, sent, goOffline} = server(), home = new HomeService(api, {deviceClass: 'mobile'}), editor = new HomeLayoutEditor(home, {requestId: () => 'r', isCurrent: () => true});
  await home.load(); await editor.load();
  editor.show('recently_added', false); assert.equal(await editor.save(), true);
  assert.equal((sent[0] as {deviceClass: string}).deviceClass, 'mobile');
  goOffline(); editor.show('recently_added', true);
  assert.equal(await editor.save(), true);
  assert.equal(editor.getSnapshot().error, undefined);
});

test('new entry fields are accepted', () => {
  const parsed = validateContentEntry({id: 'm1', kind: 'movie', title: 'Title', year: 2024, contentRating: 'PG-13', genres: ['Drama', 'Crime'], watchlisted: true});
  assert.equal(parsed.year, 2024);
  assert.equal(parsed.contentRating, 'PG-13');
  assert.deepEqual([...(parsed.genres ?? [])], ['Drama', 'Crime']);
  assert.equal(parsed.watchlisted, true);
  assert.throws(() => ((parsed.genres as unknown as unknown[]).push('x')), TypeError);
  const absent = validateContentEntry({id: 'm1', kind: 'movie', title: 'Title'});
  assert.equal(absent.year, undefined);
  assert.equal(absent.watchlisted, undefined);
});

test('a new entry field that cannot be read is left out and the entry is kept', () => {
  const bad: Record<string, unknown>[] = [{year: 0}, {year: -5}, {year: 2024.5}, {year: '2024'}, {contentRating: 13}, {contentRating: 'x'.repeat(65)}, {watchlisted: 'yes'}, {watchlisted: 1}];
  for (const field of bad) {
    const parsed = validateContentEntry({id: 'm1', kind: 'movie', title: 'T', ...field});
    assert.deepEqual(parsed, {id: 'm1', kind: 'movie', title: 'T'});
  }
  // What the entry is stays exact.
  for (const payload of [{id: '', kind: 'movie', title: 'T'}, {id: 'm1', kind: 'hologram', title: 'T'}, {id: 'm1', kind: 'movie', title: 'x'.repeat(2049)}, {id: 'm1', kind: 'movie', title: 'T', libraryId: ''}]) {
    assert.throws(() => validateContentEntry(payload), {code: 'invalid_projection'});
  }
});

// §4a and be/pages (artists carry three genres, albums more): genres degrade, never fail the entry.
test('genres keep the readable names and drop the rest', () => {
  assert.deepEqual([...validateContentEntry({id: 'm1', kind: 'movie', title: 'T', genres: ['a', 'b', 'c']}).genres!], ['a', 'b', 'c']);
  assert.deepEqual([...validateContentEntry({id: 'm1', kind: 'movie', title: 'T', genres: ['Drama', 123, '']}).genres!], ['Drama']);
  assert.equal(validateContentEntry({id: 'm1', kind: 'movie', title: 'T', genres: 'Drama'}).genres, undefined);
  assert.equal(validateContentEntry({id: 'm1', kind: 'movie', title: 'T', genres: [123]}).genres, undefined);
});

test('an episode entry carries its own still; a malformed still is left out like any art path', () => {
  const base = {id: 'e1', kind: 'episode', title: 'Pilot', backdropUrl: '/v1/metadata/show/s1/art/backdrop?v=1'};
  assert.equal(validateContentEntry({...base, stillUrl: '/v1/metadata/item/e1/art/still?v=2&w=640'}).stillUrl, '/v1/metadata/item/e1/art/still?v=2&w=640');
  assert.equal(validateContentEntry(base).stillUrl, undefined);
  assert.equal(validateContentEntry({...base, stillUrl: 42}).stillUrl, undefined);
  assert.equal(validateContentEntry({...base, stillUrl: 42}).backdropUrl, base.backdropUrl);
});
