import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync, statSync} from 'node:fs';
import {join} from 'node:path';
import {registerHooks} from 'node:module';
import {transformTsx} from './transform-tsx.mjs';

// The specimen is a .tsx module by lane path; only its data/ports are exercised here.
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});

const {createDetailSpecimen} = await import('../src/dev/entries/detail-specimen.tsx');
const {DetailService} = await import('@core/detail.ts');
const {validateItemRecommendations} = await import('@core/recommendations.ts');

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('the public specimen parses through the real detail service at the drawn frame-1 facts', async () => {
  const specimen = createDetailSpecimen();
  const service = new DetailService({api: specimen.api, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
    requestId: async () => crypto.randomUUID()});
  await service.select({libraryId: 'movies', itemId: 'dune-part-two'});
  const snapshot = service.getSnapshot();
  assert.equal(snapshot.phase, 'ready');
  const data = snapshot.data!;
  assert.equal(data.item.title, 'Dune: Part Two');
  assert.equal(data.item.year, 2024);
  assert.equal(data.item.duration, 9_960);
  assert.equal(data.item.progressSeconds, 0);
  assert.equal(data.item.available, true);
  assert.equal(data.collections?.[0]?.name, 'Dune collection');
  assert.equal(data.collections?.[0]?.memberCount, 2);
  assert.equal(data.facts?.tagline, 'Long live the fighters.');
  assert.equal(data.facts?.contentRating, 'PG-13');
  assert.deepEqual(data.metadata.genres.map(genre => genre.name), ['Science Fiction', 'Adventure']);
  assert.deepEqual(data.metadata.credits.filter(credit => credit.role === 'Director').map(credit => credit.name), ['Denis Villeneuve']);
  assert.deepEqual(data.metadata.credits.filter(credit => credit.department === 'Acting').slice(0, 4).map(credit => credit.name),
    ['Timothée Chalamet', 'Zendaya', 'Rebecca Ferguson', 'Javier Bardem']);
  assert.equal(data.actions.find(action => action.id === 'play')?.playback?.startSeconds, 0);
  assert.ok(data.actions.some(action => action.id === 'watchlist' && action.enabled));
  assert.ok(data.actions.some(action => action.id === 'watched' && action.enabled));
  assert.equal(specimen.records.filter(event => event.kind === 'request' && event.value && (event.value as {path?: string}).path === '/v1/items/dune-part-two/detail').length > 0, true);
  service.dispose();
});

test('the drawn frame-2 state carries Inside Out nine per cent in with a real resume action', async () => {
  const specimen = createDetailSpecimen('2');
  const service = new DetailService({api: specimen.api, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
    requestId: async () => crypto.randomUUID()});
  await service.select({libraryId: 'movies', itemId: 'inside-out'});
  const snapshot = service.getSnapshot();
  assert.equal(snapshot.phase, 'ready');
  const data = snapshot.data!;
  assert.equal(data.item.title, 'Inside Out');
  assert.equal(data.item.duration, 5_700);
  assert.equal(data.item.progressSeconds, 513);
  assert.equal(data.actions.find(action => action.id === 'play')?.playback?.startSeconds, 513);
  assert.ok(data.actions.some(action => action.id === 'start_over' && action.enabled));
  assert.ok(data.actions.some(action => action.id === 'rating' && action.enabled));
  assert.equal(data.collections, undefined);
  assert.deepEqual(data.metadata.genres.map(genre => genre.name), ['Animation', 'Family']);
  service.dispose();
});

test('the drawn frame-3 state carries the collection year bounds, position and full cast total', async () => {
  const specimen = createDetailSpecimen('3');
  const service = new DetailService({api: specimen.api, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
    requestId: async () => crypto.randomUUID()});
  await service.select({libraryId: 'movies', itemId: 'dune-part-two'});
  const data = service.getSnapshot().data!;
  assert.equal(data.item.progressSeconds, 0);
  assert.equal(data.collections?.[0]?.memberCount, 2);
  assert.equal(data.collections?.[0]?.position, 2);
  assert.equal(data.collections?.[0]?.yearFrom, 2021);
  assert.equal(data.collections?.[0]?.yearTo, 2024);
  assert.deepEqual(data.metadata.creditTotals, {cast: 40, crew: 24});
  assert.deepEqual(data.languages, {audio: ['en', 'fr', 'es'], subtitles: ['en', 'fr', 'es']});
  service.dispose();
});

test('the two drawn recommendation rows keep their catalogue cards and row subtitle facts', async () => {
  const specimen = createDetailSpecimen();
  const value = validateItemRecommendations(await specimen.api.request<unknown>('/v1/items/dune-part-two/recommendations?limit=12'), 'dune-part-two');
  assert.deepEqual(value.rows.map(row => row.title), ['More like Dune: Part Two', 'Viewers also watched']);
  assert.equal(value.rows[0].entries.length, 8);
  assert.equal(value.rows[1].entries.length, 8);
  assert.deepEqual(value.rows[0].entries.slice(0, 4).map(entry => entry.title), ['Dune', 'Blade Runner', 'Interstellar', 'Star Wars']);
  assert.deepEqual(value.rows[0].entries.slice(0, 2).map(entry => entry.year), [2021, 1982]);
  assert.equal(value.rows[1].subtitleText?.fallback, 'What people on this server played after it');
  assert.equal(value.rows[1].entries[4].title, 'Spider-Man: Across the Spider-Verse');
});

test('the drawn My List press is a real personal-state command with a canonical receipt', async () => {
  const specimen = createDetailSpecimen();
  const service = new DetailService({api: specimen.api, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
    requestId: async () => crypto.randomUUID()});
  await service.select({libraryId: 'movies', itemId: 'dune-part-two'});
  await service.mutate({action: 'watchlist', value: true});
  const data = service.getSnapshot().data!;
  assert.equal(data.personal.watchlisted, true);
  assert.equal(data.personal.revision, 2);
  const receipt = specimen.records.find(event => event.kind === 'personal-state');
  assert.deepEqual(receipt?.value, {watchlisted: true, favorite: false, watched: false, rating: null});
  assert.equal(specimen.records.some(event => event.kind === 'request' && (event.value as {method?: string}).method === 'PUT'), true);
  service.dispose();
});

test('the entry loads the registered production route through genuine providers on the development server only', () => {
  const entry = read('../src/dev/entries/detail.tsx'), html = read('../src/dev/entries/detail.html');
  assert.match(entry, /if \(!import\.meta\.env\.DEV\) throw new Error/);
  assert.match(entry, /createMemoryHistory\(\{initialEntries: \[`\/item\/\$\{specimen\.itemId\}`\]\}\)/);
  assert.match(entry, /<WebScreenProvider value=\{\{binding, context\}\}>/);
  assert.match(entry, /<ShellBindingsProvider value=\{binding\}>/);
  assert.match(entry, /<RouterProvider router=\{router\} \/>/);
  assert.match(entry, /import \{createDetailSpecimen, type SpecimenFrame\} from '\.\/detail-specimen'/);
  assert.match(html, /<script type="module" src="\.\/detail\.tsx"><\/script>/);
  assert.doesNotMatch(entry, /accounts\.html|ActivityReference/);
});

test('the public specimen carries no credentials, private frame artwork or production import', () => {
  const specimen = read('../src/dev/entries/detail-specimen.tsx');
  assert.doesNotMatch(specimen, /Bearer |Authorization|accessToken|refreshToken|private|password/i);
  assert.doesNotMatch(specimen, /assets\/pool|--bd\.jpg/);
  assert.doesNotMatch(specimen, /\/v1\/(?!items\/|admin\/activity)/);

  const root = new URL('../src/', import.meta.url).pathname;
  const files = (directory: string): string[] => readdirSync(directory).flatMap(name => {
    const path = join(directory, name);
    return statSync(path).isDirectory() ? files(path) : /\.(ts|tsx)$/.test(name) ? [path] : [];
  });
  const production = files(root).filter(path => !path.includes(`${join('src', 'dev')}`));
  const importers = production.filter(path => readFileSync(path, 'utf8').includes('detail-specimen'));
  assert.deepEqual(importers, []);
  const globals = production.filter(path => readFileSync(path, 'utf8').includes('__porticoDetailSpecimen'));
  assert.deepEqual(globals, []);
});
