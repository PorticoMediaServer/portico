import assert from 'node:assert/strict';
import test from 'node:test';
import {emptyMissingTitles, listMissingTitles, mergeTitles, parseMissingEmptied, parseMissingTitlesPage, removeLibrary, separateArtist} from '../src/title-identity.ts';
import {entryActionList, entryActions} from '../src/presentation/index.ts';
import {formValue, withFormValue} from '../src/server-admin/server-forms.ts';

const title = {id: 'item-1', libraryId: 'films', kind: 'movie', title: 'Ran', year: 1985, missingSince: '2026-10-01T10:00:00Z', removedAt: '2026-10-31T10:00:00Z'};

test('a missing titles page carries the rule and when each title goes', () => {
  const page = parseMissingTitlesPage({items: [title, {...title, id: 'item-2', kind: 'episode', title: 'Episode 3', parentTitle: 'Kingdom', removedAt: undefined, year: undefined}], nextCursor: '1.2', total: 12, rule: 'after_days', days: 30});
  assert.equal(page.items.length, 2);
  assert.equal(page.items[0]!.removedAt, '2026-10-31T10:00:00Z');
  assert.equal(page.items[1]!.parentTitle, 'Kingdom');
  assert.equal(page.items[1]!.removedAt, undefined);
  assert.equal(page.nextCursor, '1.2');
  assert.equal(page.rule, 'after_days');
  // A rule this app does not know reads as never; a title it cannot read is left out alone and a repeat keeps its first row.
  const mixed = parseMissingTitlesPage({items: [{...title, missingSince: ''}, {...title, id: ''}, title, {...title, title: 'Again'}, null], total: 5, rule: 'soon', days: 30, futureField: 1});
  assert.equal(mixed.rule, 'never');
  assert.deepEqual(mixed.items.map(entry => [entry.id, entry.title]), [[title.id, title.title]]);
  assert.equal(mixed.total, 5);
  // The total, the cursor and the page size stay exact.
  assert.throws(() => parseMissingTitlesPage({items: [title], total: -1, rule: 'never', days: 30}));
  assert.throws(() => parseMissingTitlesPage({items: [title], nextCursor: 7, total: 1, rule: 'never', days: 30}));
  assert.throws(() => parseMissingTitlesPage({items: Array(201).fill(title), total: 201, rule: 'never', days: 30}));
});

test('listing sends only the filters that were given', async () => {
  const paths: string[] = [];
  const api = {request: async <T>(path: string): Promise<T> => { paths.push(path); return {items: [], total: 0, rule: 'never', days: 30} as T; }};
  await listMissingTitles(api);
  await listMissingTitles(api, {libraryId: 'films', cursor: '5.9', limit: 100});
  assert.deepEqual(paths, ['/v1/admin/missing-titles', '/v1/admin/missing-titles?libraryId=films&cursor=5.9&limit=100']);
});

test('emptying repeats while the server says there is more and adds the totals up', async () => {
  const answers = [{titles: 24, files: 3, more: true}, {titles: 24, files: 0, more: true}, {titles: 2, files: 0, more: false}];
  const bodies: unknown[] = [];
  const api = {request: async <T>(_path: string, _method?: string, body?: unknown): Promise<T> => { bodies.push(body); return answers.shift() as T; }};
  const out = await emptyMissingTitles(api, 'films');
  assert.deepEqual(out, {titles: 50, files: 3, more: false});
  assert.deepEqual(bodies, [{libraryId: 'films'}, {libraryId: 'films'}, {libraryId: 'films'}]);
  assert.throws(() => parseMissingEmptied({titles: 1, files: 0}));
});

test('merging sends what the owner confirmed; removing a library asks the rebuilt server and waits for the job that removes it', async () => {
  const calls: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => { calls.push([path, method, body]); return (path.endsWith('/merge') ? {itemId: 'b'} : path.startsWith('/v1/jobs/') ? {id: 'job_1', kind: 'delete_library', state: 'done'} : {id: 'job_1', kind: 'delete_library', state: 'waiting'}) as T; }};
  assert.equal(await mergeTitles(api, 'a', 'b'), 'b');
  await removeLibrary(api, 'films', 'Films', 7, 1);
  assert.deepEqual(calls, [['/v1/admin/items/b/merge', 'POST', {itemId: 'a'}], ['/v1/admin/libraries/films?confirm=Films', 'DELETE', undefined], ['/v1/jobs/job_1', 'GET', undefined]]);
  await assert.rejects(mergeTitles(api, 'a', 'a'));
});

test('Not this artist moves albums and answers the new artist', async () => {
  const calls: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => { calls.push([path, method, body]); return {artistId: 'artist-2'} as T; }};
  assert.equal(await separateArtist(api, ['album-1']), 'artist-2');
  assert.deepEqual(calls, [['/v1/admin/titles/separate-artist', 'POST', {albumIds: ['album-1']}]]);
  await assert.rejects(separateArtist(api, []));
  const manage = entryActionList(entryActions({kind: 'album', separateArtist: true, delete: false})).map(a => a.id);
  assert.deepEqual(manage, ['separateArtist']);
});

test('a setting whose own name has a dot is one key, not a path', () => {
  const draft = {name: 'Portico', 'library.missingTitles': 'after_days', nested: {inner: 1}};
  assert.equal(formValue(draft, 'library.missingTitles'), 'after_days');
  assert.equal(formValue(draft, 'nested.inner'), 1);
  assert.deepEqual(withFormValue(draft, 'library.missingTitles', 'never'), {...draft, 'library.missingTitles': 'never'});
  assert.deepEqual(withFormValue(draft, 'nested.inner', 2), {...draft, nested: {inner: 2}});
});

test('File info commands name whole versions and titles on the rebuilt routes', async () => {
  const {splitTitleVersion} = await import('../src/title-identity.ts');
  const calls: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {calls.push([path, method, body]); return {itemId: 'n'} as T;}};
  assert.equal(await mergeTitles(api, 'a', 'b'), 'n');
  assert.equal(await splitTitleVersion(api, 'version/1'), 'n');
  assert.deepEqual(calls, [['/v1/admin/items/b/merge', 'POST', {itemId: 'a'}], ['/v1/admin/versions/version%2F1/split', 'POST', undefined]]);
});

test('version deletion uses the whole version route and has no fabricated impact preview', async () => {
  const {versionDeletionService} = await import('../src/administration.ts');
  const calls: unknown[] = [];
  const service = versionDeletionService(async <T>(path: string, method?: string, body?: unknown) => {
    calls.push([path, method, body]); return {removed: false, filesDeleted: 2, bytes: 2048, kept: [], alsoRemoved: []} as T;
  });
  await assert.rejects(service.preview('movie', 'version/1'), {code: 'version_delete_preview_unavailable'});
  assert.deepEqual(calls, []);
  const preview = {assetId: 'version/1', confirmation: 'A film', blocked: [], targets: [{itemId: 'movie', title: 'A film', allowMediaDeletion: true}]} as never;
  await assert.rejects(service.remove(preview, 'wrong title', 'operation'));
  const result = await service.remove(preview, 'A film', 'operation');
  assert.deepEqual(calls, [['/v1/admin/versions/version%2F1', 'DELETE', undefined]]);
  assert.equal(result.receipts[0].removed, true, 'the version was deleted even though other versions keep its title alive');
});
