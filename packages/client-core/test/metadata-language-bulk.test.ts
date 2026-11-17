import test from 'node:test';
import assert from 'node:assert/strict';
import {readBulkMetadataPreview, readWholeMetadataPreview, saveBulkMetadata} from '../src/metadata-bulk-rebuilt.ts';
import {rebuiltMetadata} from './fixtures/rebuilt-metadata.ts';

const targets = ['a', 'b'].map(id => ({kind: 'item' as const, contentKind: 'movie', id, libraryId: id === 'a' ? 'one' : 'two'}));
function fixture(fail: readonly string[] = []) {
  const fake = rebuiltMetadata({a: {fields: {metadataLanguage: null, language: 'duplicate-ignored'}}, b: {fields: {metadataLanguage: 'en'}}}, {fail});
  const libraries: string[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path.startsWith('/v1/admin/libraries/')) {
      libraries.push(path);
      return {metadataProvider: 'tmdb', metadataProviders: [{id: 'tmdb', languages: path.endsWith('/one') ? ['en', 'fr', 'pt-BR'] : ['fr', 'de', 'pt-BR']}]} as T;
    }
    const answer = fake.answer(path, method, body);if (answer === undefined) throw new Error('unexpected ' + path);return answer as T;
  }};
  return {fake, api, libraries};
}

test('film language uses the canonical field, Mixed values and choices shared by every selected library', async () => {
  const f = fixture(), preview = await readBulkMetadataPreview(f.api, targets, 'server');
  const schema = preview.schema.filter(spec => spec.field === 'metadataLanguage' || spec.field === 'language');
  assert.deepEqual(schema, [{field: 'metadataLanguage', label: 'Metadata language', group: 'general', type: 'enum', bulk: true, allowed: ['', 'fr', 'pt-BR']}]);
  assert.equal(preview.fields.metadataLanguage.state, 'mixed');
  assert.deepEqual(preview.fields.metadataLanguage.values.map(value => value.value), ['', 'en']);
  assert.equal(preview.fields.language, undefined);assert.equal(f.libraries.length, 2);
});

test('leaving language alone omits it; a changed override uses the bulk job wire key', async () => {
  const f = fixture();
  await saveBulkMetadata(f.api, {operationId: 'year', targets: [{kind: 'item', id: 'a', expectedRevision: '1'}], fields: {year: {value: '1984'}}});
  assert.equal(Object.hasOwn(f.fake.jobs[0].params, 'metadataLanguage'), false);
  assert.equal(Object.hasOwn(f.fake.jobs[0].params, 'language'), false);
  await saveBulkMetadata(f.api, {operationId: 'language', targets: [{kind: 'item', id: 'a', expectedRevision: '2'}], fields: {metadataLanguage: {value: 'fr'}}});
  assert.deepEqual(f.fake.jobs[1].params, {metadataLanguage: 'fr', lock: true});
  assert.equal(f.fake.docs.a.fields?.metadataLanguage, 'fr');
});

test('a failed job reads back the canonical override, and clearing it sends the library-default value', async () => {
  const f = fixture(['b']);
  const edit = {operationId: 'change', targets: targets.map(target => ({kind: target.kind, id: target.id, expectedRevision: '1'})), fields: {metadataLanguage: {value: 'fr'}}, lockEdited: true};
  const result = await saveBulkMetadata(f.api, edit) as {updated: number; failed: number};
  assert.deepEqual([result.updated, result.failed], [1, 1]);
  const clear = fixture();
  await saveBulkMetadata(clear.api, {...edit, operationId: 'clear', fields: {metadataLanguage: {value: ''}}});
  assert.deepEqual(clear.fake.jobs[0].params, {metadataLanguage: '', lock: true});
  assert.equal(clear.fake.docs.a.fields?.metadataLanguage, null);assert.equal(clear.fake.docs.b.fields?.metadataLanguage, null);
});

test('a show or an old document without the canonical field offers no language control', async () => {
  for (const doc of [{kind: 'show', fields: {metadataLanguage: 'fr'}}, {kind: 'movie'}]) {
    const fake = rebuiltMetadata({a: doc});let libraryReads = 0;
    const preview = await readBulkMetadataPreview({request: async <T>(path: string): Promise<T> => {if (path.includes('/libraries/')) libraryReads++;return fake.answer(path) as T;}}, [targets[0]], 'server');
    assert.equal(preview.schema.some(spec => spec.field === 'metadataLanguage'), false);assert.equal(libraryReads, 0);
  }
});

test('an admin denied the Owner-only library read still previews other fields for explicit and whole-view edits', async () => {
  const f = fixture();
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path.startsWith('/v1/admin/libraries/')) throw Object.assign(new Error('Owner required'), {status: 403});
    if (path.startsWith('/v1/items?')) return {items: [{id: 'a', libraryId: 'one', kind: 'movie', title: 'A'}]} as T;
    return f.api.request<T>(path, method, body);
  }};
  const previews = [await readBulkMetadataPreview(api, targets, 'server'),
    await readWholeMetadataPreview(api, {libraryId: 'one', pivot: 'movies'}, 'server', 5000, 'movie')];
  for (const preview of previews) {
    assert.ok(preview.schema.some(spec => spec.field === 'year'));
    assert.ok(preview.fields.year);
    assert.equal(preview.schema.some(spec => spec.field === 'metadataLanguage'), false);
    assert.equal(preview.fields.metadataLanguage, undefined);
  }
});
