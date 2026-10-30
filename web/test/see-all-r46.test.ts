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
const {validateHomeRow} = await import('@core/home.ts');
const {parseBrowseCapabilities, parseBrowseResult, parseBrowseCounts, acrossLibraries} = await import('@core/browse.ts');

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('the public specimen is the drawn recommendation row over the across-libraries titles vocabulary', async () => {
  const specimen = createSeeAllSpecimen();
  const row = validateHomeRow(await specimen.request(`/v1/home/rows/${specimen.rowId}?limit=1`));
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

  const capabilities = parseBrowseCapabilities(await specimen.request('/v1/browse-capabilities?pivot=titles'));
  // A group across libraries names no library, and the scope chips need the library filter.
  assert.equal(capabilities.library.id, acrossLibraries);
  assert.equal(capabilities.library.kind, '');
  assert.equal(capabilities.resolvedPivot?.id, 'titles');
  assert.deepEqual(capabilities.resolvedPivot?.supportedViews, ['grid', 'list']);
  assert.ok(capabilities.fields.some(field => field.id === 'library' && field.operators.includes('in')));
  assert.ok(capabilities.fields.some(field => field.id === 'homeRow'));
  assert.equal(capabilities.sorts.find(sort => sort.id === 'groupOrder')?.requiresGroup, true);
  assert.equal(capabilities.quickFilters.find(quick => quick.id === 'unwatched')?.query.field, 'playState');
  // An opaque cursor keeps the server's own relative lifetime.
  assert.equal(capabilities.queryLimits.cursorTtlMs, 1_800_000);
});

test('the drawn page is the row own order, 72 titles in 49 films, 16 shows and 7 anime', async () => {
  const specimen = createSeeAllSpecimen();
  const page = parseBrowseResult(await specimen.request('/v1/browse', 'POST', {pivot: 'titles',
    query: {variant:'predicate',field: 'homeRow', operator: 'equals', value: 'recommended'}, sort: [{field: 'groupOrder', direction: 'asc'}],
    displayHomeRow: 'recommended', limit: 60, range: {start: 0}}));
  assert.equal(page.pivot, 'titles');
  assert.equal(page.pageInfo.total, 72);
  assert.equal(page.pageInfo.start, 0);
  assert.equal(page.entries.length, 60);
  assert.deepEqual(page.entries.slice(0, 19).map(entry => entry.title), [
    'JUJUTSU KAISEN', 'Toy Story 3', 'My Hero Academia', 'Forrest Gump', 'Spider-Man: Into the Spider-Verse',
    'One Night Only', 'Drawn Together', 'The Mandalorian', "One Flew Over the Cuckoo's Nest", 'Spirited Away',
    'The Godfather', 'Toy Story 2', 'Demon Slayer: Kimetsu no Yaiba', 'You+Me - Against the World',
    'The Godfather Part II', 'Spider-Man: Across the Spider-Verse', 'Practical Magic 2', 'WALL·E', 'Stranger Things']);
  assert.deepEqual(page.entries.slice(0, 3).map(entry => entry.kind), ['show', 'movie', 'show']);
  assert.deepEqual(page.entries.slice(0, 3).map(entry => entry.libraryId), ['anime', 'movies', 'anime']);

  const counts = parseBrowseCounts(await specimen.request('/v1/browse/counts', 'POST', {pivot: 'titles', queries: [
    {id: 'all', query: null},
    {id: 'movie', query: {variant:'predicate',field: 'library', operator: 'in', value: ['movies']}},
    {id: 'tv', query: {variant:'predicate',field: 'library', operator: 'in', value: ['tv']}},
    {id: 'anime', query: {variant:'predicate',field: 'library', operator: 'in', value: ['anime']}},
  ]}), ['all', 'movie', 'tv', 'anime']);
  assert.deepEqual(counts.map(count => count.total), [72, 49, 16, 7]);
});

test('the page answers the screen own filters and sorts through the same canonical documents', async () => {
  const specimen = createSeeAllSpecimen();
  const filtered = parseBrowseResult(await specimen.request('/v1/browse', 'POST', {
    query: {variant:'group',all: [{variant:'predicate',field: 'homeRow', operator: 'equals', value: 'recommended'}, {variant:'predicate',field: 'playState', operator: 'equals', value: 'unplayed'}]},
    sort: [{field: 'title', direction: 'asc'}], limit: 60, range: {start: 0}}));
  assert.equal(filtered.pageInfo.total, 72);
  assert.deepEqual(filtered.entries.slice(0, 3).map(entry => entry.title), ['12 Angry Men', 'Alien', 'Aliens']);
  // A title sort is the server's answer, so the rail's anchors are absent rather than guessed.
  assert.deepEqual(filtered.positionIndex, []);

  const watched = parseBrowseResult(await specimen.request('/v1/browse', 'POST', {
    query: {variant:'predicate',field: 'playState', operator: 'equals', value: 'played'}, sort: [{field: 'groupOrder', direction: 'asc'}], limit: 60, range: {start: 0}}));
  assert.equal(watched.pageInfo.total, 0);
  assert.equal(watched.entries.length, 0);

  const second = parseBrowseResult(await specimen.request('/v1/browse', 'POST', {
    query: null, sort: [{field: 'groupOrder', direction: 'asc'}], limit: 60, range: {start: 60}}));
  assert.equal(second.entries.length, 12);
  assert.equal(second.pageInfo.hasMore, false);
  assert.equal(specimen.records.filter(event => event.kind === 'browse').length, 3);
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

test('the row screen is the drawn see-all header over the kit page, seven columns at 1440', () => {
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
  assert.match(css, /\.title \{ margin: 14px 0 12px; font-size: var\(--type-lockup-long-size\)/);
  assert.match(css, /\.why \{ max-width: 760px; margin: 0 0 26px;/);
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
