import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync, statSync} from 'node:fs';
import {join} from 'node:path';
import {registerHooks} from 'node:module';
import {transformTsx} from './transform-tsx.mjs';

// The specimen is a .tsx module by lane path; only its data and ports are exercised here.
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});

const {createSeeAllSpecimen} = await import('../src/dev/entries/see-all.tsx');
const {fetchHomeRow} = await import('@core/home.ts');
const {readHomeRowCapabilities, homeRowPivot} = await import('@core/home-row-browse.ts');
const {BrowseController} = await import('../src/screens/browse/controller.ts');

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('the public specimen describes a plain recommendation row with the drawn title and reason', async () => {
  const specimen = createSeeAllSpecimen();
  const row = await fetchHomeRow(specimen, specimen.rowId, {limit: 1});
  assert.equal(row.id, 'recommended');
  assert.equal(row.title, 'Recommended for you');
  assert.equal(row.titleText?.code, 'home.row.recommended');
  assert.equal(row.kind, 'recommendation');
  assert.equal(row.artworkShape, 'poster');
  assert.equal(row.why?.code, 'next.home.whyRecommendedFromActivity');
  assert.equal(row.why?.params?.titles, 'Inside Out, The Last of Us and Rick and Morty');
  assert.deepEqual(row.whyTitles?.map(title => title.name), ['Inside Out', 'The Last of Us', 'Rick and Morty']);
  assert.equal(row.total, 72);
  assert.equal(row.cacheTtlMs, 300_000);

  const capabilities = await readHomeRowCapabilities(specimen, specimen.rowId, undefined);
  assert.deepEqual(capabilities.resolvedPivot?.supportedViews, ['grid', 'list']);
  assert.deepEqual(capabilities.resolvedPivot?.sorts, []);
  assert.deepEqual(capabilities.fields, []);
});

test('the drawn row pages through all 72 titles in its own order', async () => {
  const specimen = createSeeAllSpecimen();
  const first = await fetchHomeRow(specimen, specimen.rowId, {limit: 60});
  assert.equal(first.total, 72);
  assert.equal(first.entries.length, 60);
  assert.deepEqual(first.entries.slice(0, 3).map(entry => entry.title), ['JUJUTSU KAISEN', 'Toy Story 3', 'My Hero Academia']);
  assert.deepEqual(first.entries.slice(0, 3).map(entry => entry.libraryId), ['anime', 'movies', 'anime']);
  const second = await fetchHomeRow(specimen, specimen.rowId, {cursor: first.nextCursor, limit: 60});
  assert.equal(second.entries.length, 12);
  assert.equal(second.hasMore, false);
  assert.equal(new Set([...first.entries, ...second.entries].map(entry => entry.id)).size, 72);
});

test('the web controller sends a Discover row scope and its applied filter to the row and facets', async () => {
  const requests: URL[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    const url = new URL(path, 'http://test');
    requests.push(url);
    if (url.pathname === '/v1/libraries/films') return {id: 'films', name: 'Films', kind: 'movies', filters: [{id: 'genre', type: 'tag'}]} as T;
    if (url.pathname === '/v1/home/rows/recently_added') return {id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [], total: 0} as T;
    if (url.pathname === '/v1/facets') return {facet: 'genre', values: [{value: 'drama', label: 'Drama', count: 1}]} as T;
    throw new Error(`unexpected_fixture_request: ${path}`);
  }};
  const predicates = [{variant: 'predicate', field: 'genre', operator: 'equals', value: 'drama'}] as const;
  const controller = new BrowseController(api, {libraryId: 'films', pivot: homeRowPivot, homeRowId: 'recently_added', predicates}, () => true);
  try {
    await controller.load();
    const signal = new AbortController().signal;
    assert.equal((await controller.count(predicates, signal)).total, 0);
    assert.equal((await controller.facets('genre', '', predicates, signal)).values[0].label, 'Drama');
    const facet = requests.find(url => url.pathname === '/v1/facets')!;
    assert.equal(facet.searchParams.get('homeRow'), 'recently_added');
    for (const request of requests.filter(url => url.pathname !== '/v1/libraries/films')) {
      assert.equal(request.searchParams.get('library'), 'films');
      assert.equal(request.searchParams.get('genre'), 'drama');
    }
  } finally {controller.dispose();}
});

test('the entry loads the registered production row route through genuine providers on the development server only', () => {
  const entry = read('../src/dev/entries/see-all.tsx'), html = read('../src/dev/entries/see-all.html');
  assert.match(entry, /if \(import\.meta\.env && !import\.meta\.env\.DEV\) throw new Error/);
  assert.match(entry, /createMemoryHistory\(\{initialEntries: \[`\/home\/rows\/\$\{specimen\.rowId\}`\]\}\)/);
  assert.match(entry, /<WebScreenProvider value=\{\{binding, context: contextBase\}/);
  assert.match(entry, /<ShellBindingsProvider value=\{binding as never\}>/);
  assert.match(entry, /<RouterProvider router=\{router\} \/>/);
  assert.match(entry, /import\('\.\.\/\.\.\/router'\)/);
  assert.match(html, /<script type="module" src="\.\/see-all\.tsx"><\/script>/);
  assert.doesNotMatch(entry, /accounts\.html|ActivityReference|design-ui-v1/);
});

test('the public specimen carries no credentials, private frame artwork or production import', () => {
  const specimen = read('../src/dev/entries/see-all.tsx');
  // The only token is the public placeholder the session port requires; nothing real is carried.
  assert.doesNotMatch(specimen, /Bearer |eyJ[A-Za-z0-9_-]{10,}|password/i);
  assert.match(specimen, /accessToken: 'specimen-public'/);
  assert.doesNotMatch(specimen, /assets\/pool|--bd\.jpg/);
  assert.doesNotMatch(specimen, /\/v1\/(?!home\/rows|browse)/);

  const root = new URL('../src/', import.meta.url).pathname;
  const files = (directory: string): string[] => readdirSync(directory).flatMap(name => {
    const path = join(directory, name);
    return statSync(path).isDirectory() ? files(path) : /\.(ts|tsx)$/.test(name) ? [path] : [];
  });
  const production = files(root).filter(path => !path.includes(`${join('src', 'dev')}`));
  assert.deepEqual(production.filter(path => readFileSync(path, 'utf8').includes('dev/entries/see-all')), []);
  assert.deepEqual(production.filter(path => readFileSync(path, 'utf8').includes('__porticoSeeAllSpecimen')), []);
});

test('the row screen is See all on the library frame over its own wash, seven columns at 1440', () => {
  const screen = read('../src/screens/browse/HomeRow.tsx'), route = read('../src/screens/browse/HomeRow.route.tsx'), css = read('../src/screens/browse/home-row-r46.module.css');
  assert.match(screen, /import styles from '\.\/home-row-r46\.module\.css'/);
  assert.match(screen, /homeRowReasonParts/);
  assert.match(screen, /return compact \? \{source: sources\[0\], treatment: 'library-phone'\} : \{sources, treatment: 'library'\};/);
  assert.match(screen, /homeRowAmbientView\(ambient, compact\)/);
  assert.match(screen, /<AmbientArt className=\{compact \? styles\.ambientPhone : styles\.ambient\} \{\.\.\.ambientView\} width="100%" height=\{compact \? 760 : 560\}/);
  assert.match(screen, /variant="primary" size="hero" icon="i-play"/);
  assert.match(screen, /variant="glass" size="hero" icon="i-shuffle"/);
  // The frame draws seven fluid columns at 1440 on Medium; 170 is that column's card size.
  assert.match(route, /cardWidth: \(width < 600 \? 103 : 170\)/);
  assert.match(route, /ambient=\{ambient\}/);
  // The library frame draws the header: Back to Home, the title, the why line as its intro, Play and Shuffle.
  assert.match(screen, /<LibraryBrowse \{\.\.\.browse\} showSaveView=\{false\} header=\{\{title: serverTextLabel/);
  assert.doesNotMatch(css, /\.why \{|\.title \{/);
  assert.doesNotMatch(css, /#[0-9a-fA-F]{3,8}\b|\b(rgba?|hsla?)\(/);
});

test('the bar is the frame toolbar: one quick toggle, Best match, the viewer library names', () => {
  const route = read('../src/screens/browse/HomeRow.route.tsx'), toolbar = read('../src/kit/browse/BrowseToolbar.tsx');
  const catalogue = read('../../packages/i18n/src/catalog/en-US/next/browse.ts');
  assert.match(route, /sortLabel: defaultI18n\.t\('next\.browse\.bestMatch'\)/);
  assert.match(route, /quickIds: \['unwatched'\]/);
  assert.match(route, /scopeLabel: kind => context\.identity\?\.libraries\.find/);
  assert.match(toolbar, /sortLabel\?: string/);
  assert.match(toolbar, /quickIds\?: readonly string\[\]/);
  assert.match(toolbar, /const quick = quickIds === undefined \? model\.quick : model\.quick\.filter/);
  assert.match(catalogue, /'next\.browse\.bestMatch': 'Best match'/);
});
