import assert from 'node:assert/strict';
import {test} from 'node:test';
import type {ReadingManifest} from '@portico/client-core';
import {ReaderController} from '../src/controller.ts';
import {createReadingPublication} from '../src/publication.ts';
import type {ReaderAdapter, ReaderDocument, ReaderSearchResult, ReaderView} from '../src/ports.ts';

type Prefs = {theme: string};
const revision = 'a'.repeat(64);
const signal = () => new AbortController().signal;

test('a display-only indexed hit is bound to its query but cannot navigate', async () => {
  const resolved: ReaderSearchResult = {id: 'resolved', excerpt: 'quotation', match: {start: 0, length: 9},
    location: {format: 'epub', cfi: 'epubcfi(/6/2!/4/2)', progression: 0}};
  const display: ReaderSearchResult = {id: 'display', excerpt: 'quotation', match: {start: 0, length: 9}};
  let navigated = 0, marked = 0;
  const view: ReaderView<Prefs> = {async navigate() {navigated++;}, async applyPreferences() {}, async resize() {},
    getLocation() {return null;}, async showSearchResult() {marked++;}, dispose() {}};
  const document: ReaderDocument<Prefs> = {id: 'source', revision, format: 'epub', metadata: {}, toc: [],
    async mount() {return view;}, async *search(_query, _options) {yield resolved; yield display;}, dispose() {}};
  const adapter: ReaderAdapter<Prefs> = {format: 'epub', async open() {return document;}};
  const controller = new ReaderController({adapters: [adapter], preferences: {theme: 'dark'}});
  await controller.open({source: {id: 'source', revision, size: 1, async read() {return new Uint8Array([1]);}}, format: 'epub', container: {clientWidth: 1180, clientHeight: 820} as HTMLElement});
  const results: ReaderSearchResult[] = [];
  for await (const result of controller.search('term', {signal: signal(), limit: 10})) results.push(result);
  assert.equal(results.length, 2);
  assert.equal(results[1]!.location, undefined);
  await controller.navigateSearchResult(results[0]!);
  assert.equal(navigated, 1);
  assert.equal(marked, 1);
  await assert.rejects(controller.navigateSearchResult(results[1]!), {code: 'invalid_location'});
  assert.equal(navigated, 1);
});

test('publication exposes the optional indexed port only for a native EPUB with a spine', async () => {
  const manifest = {sessionId: 'session', itemId: 'item', sourceId: 'asset', revision, format: 'epub', size: 1,
    metadata: {title: 'Title'}, resources: [{path: 'chapter.xhtml', mime: 'application/xhtml+xml', size: 1}], spine: ['chapter.xhtml']} as unknown as ReadingManifest;
  let seen: Readonly<{limit?: number; cursor?: string; signal?: AbortSignal}> | undefined, querySeen = '';
  const page = {sessionId: 'session', sourceId: 'asset', sourceRevision: revision, indexRevision: 2,
    coverage: {status: 'indexed' as const}, hits: [{sectionPath: 'chapter.xhtml', sectionIndex: 0, utf16Offset: 3, excerpt: 'abc def', matchStart: 0, matchLength: 3}]};
  const session = {manifest, signal: signal(), async headPDF() {}, async readRange() {return new Uint8Array();}, async readAsset() {return new Uint8Array([1]);},
    async page() {throw new Error('not reached');},
    async search(query: string, options: Readonly<{limit?: number; cursor?: string; signal?: AbortSignal}>) {querySeen = query; seen = options; return page;}};
  const publication = await createReadingPublication(session);
  assert.ok(publication.source.indexedSearch);
  assert.deepEqual(publication.source.spine, ['chapter.xhtml']);
  assert.equal(Object.isFrozen(publication.source.spine), true);
  const result = await publication.source.indexedSearch!('term', {signal: signal(), limit: 20, cursor: 'cursor'});
  assert.equal(querySeen, 'term');
  assert.equal(seen?.limit, 20);
  assert.equal(seen?.cursor, 'cursor');
  assert.equal(result.sourceRevision, revision);
  assert.equal(result.hits[0]!.utf16Offset, 3);
  assert.equal(Object.isFrozen(result), true);
  assert.equal(Object.isFrozen(result.hits), true);
  publication.dispose();

  const converted = {...manifest, conversion: {originalRevision: revision} as never} as unknown as ReadingManifest;
  const convertedPublication = await createReadingPublication({...session, manifest: converted});
  assert.equal(convertedPublication.source.indexedSearch, undefined);
  convertedPublication.dispose();

  const pdfManifest = {sessionId: 'session', itemId: 'item', sourceId: 'asset', revision, format: 'pdf', size: 1, metadata: {title: 'Title'}} as unknown as ReadingManifest;
  const pdfPublication = await createReadingPublication({...session, manifest: pdfManifest});
  assert.equal(pdfPublication.source.indexedSearch, undefined);
  pdfPublication.dispose();
});
