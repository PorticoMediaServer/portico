import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync, statSync} from 'node:fs';
import {join} from 'node:path';
import {setImmediate} from 'node:timers/promises';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import {createPersonBindings, creditRole} from '../src/screens/detail/person-bindings.ts';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

function setup(handler: (path: string) => Promise<unknown>) {
  let current = true, requests = 0;
  const context = {
    api: {request: <T,>(path: string) => { requests++; return handler(path) as Promise<T>; },
      requestRaw: async (path: string) => ({status: 200, headers: {}, body: await handler(path)})},
    scope: {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])}, isCurrent: () => current,
    preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry: () => {}, reportError: () => {}},
  } as unknown as ScreenContext;
  return {context, requests: () => requests, retire: () => { current = false; }};
}

const media = (id: string, kind: 'movie' | 'show', title: string) => ({id, libraryId: kind === 'movie' ? 'movies' : 'tv', kind, title, available: true});
const credit = (id: string, kind: 'movie' | 'show', title: string, roles: readonly {role: string; department: string}[], creditKind: 'cast' | 'crew') => ({roles, media: media(id, kind, title), role: roles[0]?.role ?? '', character: roles[0]?.role ?? '', department: roles[0]?.department ?? '', creditKind});
const page = (role: string | null, kind: string | null, limit: number) => {
  const all = [
    credit('dune-part-two', 'movie', 'Dune: Part Two', [{role: 'Lady Jessica', department: 'Acting'}], 'cast'),
    credit('dune', 'movie', 'Dune', [{role: 'Lady Jessica', department: 'Acting'}], 'cast'),
    credit('silo', 'show', 'Silo', [{role: 'Juliette Nichols', department: 'Acting'}, {role: 'Executive producer', department: 'Production'}], 'cast'),
  ];
  const selected = all.filter(row => (kind ? row.media.kind === kind : true) && (role === 'cast' ? row.creditKind === 'cast' : role === 'crew' ? row.creditKind === 'crew' : true));
  return {serverId: 'server', viewerFence: 'fence', revision: {catalog: 1, viewer: 1},
    person: {id: 'rebecca', name: 'Rebecca Ferguson', revision: 1, biography: '', birthDate: '', deathDate: '', birthPlace: '', portraitUrl: '', roles: ['Acting'], knownFor: [], providerIds: {}},
    groups: [{id: 'movie', kind: 'movie', total: 2}, {id: 'show', kind: 'show', total: 1}], credits: selected.slice(0, limit),
    pageInfo: {total: selected.length, nextCursor: selected.length > limit ? 'next' : ''}};
};

test('acting and crew credit rows keep the drawn catalogue wording and separated emphasis', () => {
  const acting = creditRole({role: 'Lady Jessica', department: 'Acting'}, 'role-0');
  assert.equal(acting.part.text, 'as Lady Jessica');
  assert.deepEqual(acting.presentation?.['role-0'], [{text: 'as '}, {text: 'Lady Jessica', emphasis: 'secondary'}]);
  const crew = creditRole({role: 'Executive producer', department: 'Production'}, 'role-1');
  assert.equal(crew.part.text, 'Executive producer');
  assert.equal(crew.presentation, undefined);
  assert.equal(creditRole({role: 'Director', department: 'Directing'}, 'role-2').part.text, 'Director');
});

test('the person page groups credits by the server kind and pages each group on its own', async () => {
  const paths: string[] = [];
  const owner = setup(async path => { paths.push(path); const query = new URL('https://unit.invalid' + path).searchParams; return page(query.get('role'), query.get('kind'), Number(query.get('limit') ?? '40')); });
  const binding = createPersonBindings(owner.context);
  try {
    await binding.select('rebecca');
    const snapshot = binding.getSnapshot(), header = snapshot.page!;
    assert.equal(snapshot.phase, 'ready');
    assert.deepEqual(header.groups.map(group => [group.kind, group.total]), [['movie', 2], ['show', 1]]);
    assert.equal(header.pageInfo.total, 3);
    const movies = binding.credits(header, 'movie'); movies.ensureRange(0, 1);
    const shows = binding.credits(header, 'show'); shows.ensureRange(0, 1);
    for (let turn = 0; turn < 8; turn++) await setImmediate();
    assert.deepEqual(movies.residentItems().map(item => item.media.title), ['Dune: Part Two', 'Dune']);
    assert.deepEqual(shows.residentItems().map(item => item.media.title), ['Silo']);
    assert.ok(paths.some(path => path.includes('kind=movie') && path.includes('limit=40')));
    assert.ok(paths.some(path => path.includes('kind=show') && path.includes('limit=40')));
    assert.equal(binding.credits(header, 'movie'), movies, 'the same group keeps one bounded collection');
  } finally { binding.dispose(); }
});

test('the DEV entry mounts the registered Person route on the public frame facts and fails closed outside development', () => {
  const entry = read('../src/dev/entries/person.tsx'), html = read('../src/dev/entries/person.html');
  assert.match(entry, /if \(!import\.meta\.env\.DEV\) throw new Error/);
  assert.match(entry, /createMemoryHistory\(\{initialEntries: \[`\/person\/\$\{specimen\.personId\}`\]\}\)/);
  assert.match(entry, /<WebScreenProvider value=\{\{binding, context: contextBase\}\}>/);
  assert.match(entry, /<ShellBindingsProvider value=\{binding\}>/);
  assert.match(entry, /<RouterProvider router=\{router\} \/>/);
  assert.match(entry, /const personId = 'rebecca-ferguson'/);
  assert.match(entry, /name: 'Rebecca Ferguson'/);
  assert.match(entry, /birthPlace: 'Stockholm, Sweden'/);
  assert.match(entry, /biographySource: 'TMDB'/);
  assert.match(entry, /\{id: 'movie', kind: 'movie', total: 2\}/);
  assert.match(entry, /\{id: 'show', kind: 'show', total: 1\}/);
  assert.match(entry, /role: 'Executive producer', department: 'Production'/);
  assert.match(entry, /const tomHanksId = 'tom-hanks'/);
  assert.match(entry, /name: 'Tom Hanks'/);
  assert.match(entry, /requested === 'phone' \? 'phone' : 'desktop'/);
  assert.match(entry, /\{id: 'movie', kind: 'movie', total: 6\}/);
  assert.match(entry, /creditFor\(toyStory3, 'Woody'\)/);
  assert.match(html, /<script type="module" src="\.\/person\.tsx"><\/script>/);
  assert.doesNotMatch(entry, /Bearer |Authorization:|assets\/pool|--bd\.jpg/i);
  assert.doesNotMatch(entry, /\/v1\/(?!people\/|admin\/activity)/);
});

test('no production module imports the person specimen', () => {
  const root = new URL('../src/', import.meta.url).pathname;
  const files = (directory: string): string[] => readdirSync(directory).flatMap(name => {
    const path = join(directory, name);
    return statSync(path).isDirectory() ? files(path) : /\.(ts|tsx)$/.test(name) ? [path] : [];
  });
  const production = files(root).filter(path => !path.includes(`${join('src', 'dev')}`));
  assert.deepEqual(production.filter(path => /dev\/entries\/person|__porticoPersonSpecimen/.test(readFileSync(path, 'utf8'))), []);
});
