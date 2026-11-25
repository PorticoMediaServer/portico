import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as errors from '../../../packages/client-core/src/presentation/errors';
import * as kitText from '../../../packages/client-core/src/presentation/kit-model/text';
import * as reading from '../../../packages/client-core/src/reading';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// `useReadBook` is what a book's page shows on a phone (phone-read 7, 8, 9), from the reading controller the old book
// screen read: this holds what it shows and what each control does.

const t = defaultI18n.t;
const contents = [{depth: 0, section: 'c1.xhtml', title: 'Book One: Dune'}, {depth: 0, section: 'c2.xhtml', title: 'Muad\'Dib'}, {depth: 0, section: 'c3.xhtml', title: 'The Prophet'}];
const epub = {sourceId: 's1', revision: 'r1', format: 'epub', size: 2_000_000, contents,
  metadata: {title: 'Dune', author: 'Frank Herbert', wordCount: 188_000, estimatedSeconds: 46_800, pageCount: 535, openingLines: 'In the week before…', description: 'A desert planet.', language: 'en'}} as any;
const pdf = {sourceId: 's2', revision: 'r2', format: 'pdf', size: 9_000_000, metadata: {title: 'Dune', pageCount: 535, estimatedSeconds: 46_800}} as any;
const volume = (id: string, number?: number, available = true) => ({id, sourceId: `${id}-s`, title: id, coverUrl: `/${id}.jpg`, inventoryAvailable: available, ...(number === undefined ? {} : {series: {number}})});

async function page(over: Record<string, unknown> = {}, options: {owner?: boolean; shelf?: Record<string, unknown>; series?: unknown; manifest?: any} = {}) {
  const calls: unknown[][] = [], h = hooks();
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]); return Promise.resolve(true);};
  const {useReadBook} = await screenModule(new URL('../src-next/pages/shared/read/useReadBook.ts', import.meta.url), {
    react: {...h.react, useLayoutEffect: () => {}}, '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/errors': errors, '../../../../../../packages/client-core/src/presentation/kit-model/text': kitText,
    '../../../../../../packages/client-core/src/reading': reading, '../../../platform/offline-reading': {nativePublicationReadsAvailable: () => true},
    '../../../screens/read/use-reading-collection-kind': {useReadingCollectionKind: () => 'ebook'}, '../../../screens/read/use-route-revision': {useRouteRevision: () => 1},
    '../../../screens/downloads/request': {askNativeDownload: (request: unknown) => calls.push(['download-volume', request])}});
  const manifest = options.manifest ?? epub;
  const snapshot = {phase: 'ready', manifest, sourceId: manifest.sourceId, versions: [{sourceId: manifest.sourceId, format: manifest.format, size: manifest.size, inventoryAvailable: true}],
    progress: {progress: {stale: false, sourceId: manifest.sourceId, sourceRevision: manifest.revision, location: {format: manifest.format, progression: .5, section: 'c2.xhtml', pageIndex: manifest.format === 'pdf' ? 247 : undefined}}},
    listed: {listed: false}, read: {read: false}, ...over};
  const shelf = {phase: 'ready', items: [volume('v2', 2), volume('v3', 3, false)], nextCursor: 'n1', witness: {}, ...options.shelf};
  const lease = {isCurrent: () => true};
  const controller = {item: {id: 'dune', title: 'Dune item', coverUrl: '/dune.jpg'}, subscribe: () => () => {}, getSnapshot: () => snapshot,
    subscribeShelf: () => () => {}, shelfSnapshot: () => shelf, arm: () => true, load: record('load'), release: () => {},
    series: () => 'series' in options ? options.series : {id: 'dune-series', name: 'Dune', number: 1},
    savedProgression: () => .5, ownerEditLease: () => lease, read: record('read'), toggleList: record('list'), toggleRead: record('mark'),
    download: (...args: unknown[]) => {calls.push(['download', ...args]); return true;}, openSeries: record('series'), selectVersion: record('version'),
    retry: record('retry'), loadSeriesShelf: record('loadShelf'), moreSeriesShelf: record('moreShelf'), retrySeriesShelf: record('retryShelf'),
    shelfItemLifetime: () => ({signal: new AbortController().signal, isCurrent: () => true}), openSeriesItem: record('openVolume')};
  const readingActions = {selection: (offer: any) => ({offer}), actions: {show: (value: unknown) => calls.push(['show', value])}};
  const props = {controller, route: {kind: 'read-book', libraryId: 'books'}, comic: false, owner: options.owner ?? false, context: {}, runtime: {
    artwork: (path?: string) => path ? {uri: path} : undefined, ambient: (path?: string) => path ? {url: path} : undefined, reportError: record('error')},
    onBack: record('back'), onOpenSettings: record('settings'), readingActions, onSheet: (sheet: unknown) => calls.push(['sheet', sheet])};
  const render = () => h.render(() => useReadBook(props));
  return {view: render(), render, calls, lease};
}

test('a book: its series and place, the author with initials, its facts, Continue reading with the progress, My List and Download', async () => {
  const {view, calls} = await page();
  assert.equal(view.kicker.label, t('title.seriesAndBook', {series: 'Dune', number: '1'}));
  view.kicker.onPress(); assert.equal(calls.at(-1)![0], 'series');
  assert.deepEqual(view.byline, {text: 'Frank Herbert', initials: 'FH'});
  assert.deepEqual(view.facts.map((fact: any) => fact.id), ['words', 'duration', 'pages', 'chapters']);
  assert.deepEqual([view.primary.label, view.primary.progress], [t('next.cards.read'), .5]);
  view.primary.onPress(); assert.deepEqual(calls.at(-1), ['read', epub, undefined]);
  assert.deepEqual(view.actions.map((action: any) => action.testID), ['read-book-list', 'read-book-download']);
  view.actions[0].onPress(); assert.equal(calls.at(-1)![0], 'list');
  view.actions[1].onPress(); assert.deepEqual(calls.at(-1), ['download', epub, view.actions[1].anchorRef]);
  assert.deepEqual([view.cover, view.glow, view.synopsis, view.firstLines], [{uri: '/dune.jpg'}, {url: '/dune.jpg'}, 'A desert planet.', 'In the week before…']);
  assert.deepEqual([view.readMore.label, view.readMore.expanded], [t('web.person.readMore'), false]);
});

test('where the reader is: an EPUB its chapter number of the contents, never the section name; a page book its page, then the time left; one version shows no pill', async () => {
  const {view} = await page();
  assert.equal(view.place.version, undefined);
  assert.deepEqual(view.place.facts, [t('next.kit.chapterOf', {number: 2, total: 3}), t('next.reader.aboutLeft', {duration: kitText.durationText(23_400, t)})]);
  const paged = await page({}, {manifest: pdf});
  assert.deepEqual(paged.view.place.facts, [t('next.reader.pageOf', {page: 248, count: 535}), t('next.reader.aboutLeft', {duration: kitText.durationText(23_400, t)})]);
  // A stale place names nothing.
  const stale = await page({progress: {progress: {stale: true, sourceId: 's1', sourceRevision: 'r1', location: {format: 'epub', progression: .5}}}});
  assert.equal(stale.view.place, undefined);
});

test('the contents: every chapter numbered, the one being read marked; a chapter opens there; Details holds the file\'s facts', async () => {
  const {view, render, calls} = await page();
  assert.deepEqual(view.contents.rows.map((row: any) => [row.number, row.text.title, row.current]), [[1, 'Book One: Dune', false], [2, 'Muad\'Dib', true], [3, 'The Prophet', false]]);
  view.contents.onPlay('2'); assert.deepEqual(calls.at(-1), ['read', epub, 2]);
  assert.equal(view.tab, 'contents');
  view.tabs.onValueChange('details'); assert.equal(render().tab, 'details');
  assert.deepEqual(view.details.map((row: any) => row.label), [t('library.tab.series'), t('filter.field.author'), t('next.reader.format'), t('next.reader.fileSize'),
    t('next.selection.language'), t('next.reader.pagesStrip'), t('next.reader.contents')]);
  // A page book has no contents: no tabs, Details only, and it is marked read in My List's place.
  const paged = await page({}, {manifest: pdf});
  assert.deepEqual([paged.view.tabs, paged.view.tab], [undefined, 'details']);
  assert.deepEqual(paged.view.actions.map((action: any) => action.testID), ['read-book-mark', 'read-book-download']);
});

test('the ⋯ menu: Mark read, the owner\'s Edit details and Add to collection as sheets, Details, Settings', async () => {
  const {view, render, calls, lease} = await page({}, {owner: true});
  view.more(); const opened = render();
  assert.equal(opened.menu.open, true);
  assert.deepEqual(opened.menu.items.map((item: any) => item.id), ['read-mark', 'edit-details', 'add-to-collection', 'details', 'settings']);
  assert.deepEqual(opened.menu.items.map((item: any) => [item.icon !== undefined, item.selected]), [[true, undefined], [true, undefined], [true, undefined], [true, undefined], [true, undefined]], 'every row has its icon; none reads as selected');
  opened.menu.items[1].onSelect(); assert.deepEqual(calls.at(-1), ['sheet', {kind: 'edit', lease, manifest: epub}]);
  opened.menu.items[2].onSelect(); assert.deepEqual(calls.at(-1), ['sheet', {kind: 'collection', lease, collection: 'ebook', title: 'Dune'}]);
  opened.menu.items[4].onSelect(); assert.deepEqual(calls.at(-1), ['settings']);
  assert.deepEqual((await page()).view.menu.items.map((item: any) => item.id), ['read-mark', 'add-to-collection', 'details', 'settings'], 'a member edits nothing');
});

test('versions: a pill when there are more than one, opening the versions; another available one reloads the page for it', async () => {
  const versions = [{sourceId: 's1', format: 'epub', size: 2_000_000, inventoryAvailable: true}, {sourceId: 'sx', format: 'pdf', size: 9_000_000, inventoryAvailable: false},
    {sourceId: 'sy', format: 'azw3', size: 3_000_000, inventoryAvailable: true}];
  const {view, render, calls} = await page({versions});
  assert.deepEqual([view.place.version.label, view.place.version.value], [t('next.reader.version'), 'EPUB']);
  view.place.version.onPress(); const opened = render();
  assert.equal(opened.versions.open, true);
  assert.deepEqual(opened.versions.items.map((item: any) => [item.label, !!item.selected, !!item.disabled]), [['EPUB', true, false], ['PDF', false, true], ['AZW3', false, false]]);
  opened.versions.items[2].onSelect(); assert.deepEqual(calls.at(-1), ['version', epub, versions, 'sy']);
});

test('the rest of the series: each volume\'s place, one the server can\'t serve drawn but inert, See all, paging, and a failed page offers Try again', async () => {
  const {view, calls} = await page();
  const [v2, v3] = view.shelf.items;
  assert.deepEqual([v2.caption, v2.unavailable, v3.unavailable, v3.selection, v3.onMore], [t('next.reader.bookNumber', {number: '2'}), undefined, t('next.reader.volumeUnavailable'), undefined, undefined]);
  v2.onNavigate(); assert.equal(calls.at(-1)![0], 'openVolume');
  v2.onMore('anchor'); assert.equal((calls.at(-1) as any)[1].anchorRef, 'anchor');
  view.shelf.onSeeAll(); assert.equal(calls.at(-1)![0], 'series');
  view.shelf.paging.requestNext(); assert.equal(calls.at(-1)![0], 'moreShelf');
  const failed = await page({}, {shelf: {moreError: {code: 'unavailable', message: 'x', retryable: true}}});
  assert.equal(failed.view.shelf.paging.hasMore, false);
  failed.view.shelfState.action.onPress(); assert.equal(failed.calls.at(-1)![0], 'retryShelf');
  assert.equal((await page({}, {series: undefined})).view.shelf, undefined, 'a book in no series has no shelf');
});

test('a failed read offers Try again; while loading the page waits', async () => {
  const failed = await page({phase: 'error', manifest: undefined, error: {code: 'unavailable', message: 'x', retryable: true}});
  assert.equal(failed.view.primary.disabled, true);
  failed.view.state.action.onPress(); assert.deepEqual(failed.calls.at(-1), ['retry']);
  assert.equal((await page({phase: 'loading', manifest: undefined})).view.state.kind, 'loading');
});
