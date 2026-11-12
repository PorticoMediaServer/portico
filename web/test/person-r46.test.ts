import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync, statSync} from 'node:fs';
import {join} from 'node:path';
import {setImmediate} from 'node:timers/promises';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import {createPersonBindings, creditRole} from '../src/screens/detail/person-bindings.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

function setup(handler: (path: string) => Promise<unknown>, total = 3) {
  const response = (path: string) => path.startsWith('/v1/people/')
    ? Promise.resolve({id: 'rebecca', name: 'Rebecca Ferguson', images: {}, titleCount: total, crewTitleCount: 1,
      roles: [{role: 'actor', titleCount: total}, {role: 'producer', titleCount: 1}]}) : handler(path);
  let current = true, requests = 0;
  const context = withQueries({
    api: {request: <T,>(path: string) => { requests++; return response(path) as Promise<T>; },
      requestRaw: async (path: string) => ({status: 200, headers: {}, body: await response(path)})},
    scope: {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])}, isCurrent: () => current,
    preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry: () => {}, reportError: () => {}},
  }) as unknown as ScreenContext;
  return {context, requests: () => requests, retire: () => { current = false; }};
}

const media = (id: string, kind: 'movie' | 'show', title: string) => ({id, libraryId: kind === 'movie' ? 'movies' : 'tv', kind, title, available: true});
const credit = (id: string, kind: 'movie' | 'show', title: string, roles: readonly {role: string; department: string}[], creditKind: 'cast' | 'crew') => ({roles, media: media(id, kind, title), role: roles[0]?.role ?? '', character: roles[0]?.role ?? '', department: roles[0]?.department ?? '', creditKind});
const wireCredit = (row: ReturnType<typeof credit>) => ({...row.media,
  credits: row.roles.map(role => ({personId: 'rebecca', name: 'Rebecca Ferguson', role: role.department === 'Production' ? 'producer' : 'actor',
    ...(role.department === 'Acting' ? {character: role.role} : {})}))});
const page = (role: string | null, kind: string | null, limit: number) => {
  const all = [
    credit('dune-part-two', 'movie', 'Dune: Part Two', [{role: 'Lady Jessica', department: 'Acting'}], 'cast'),
    credit('dune', 'movie', 'Dune', [{role: 'Lady Jessica', department: 'Acting'}], 'cast'),
    credit('silo', 'show', 'Silo', [{role: 'Juliette Nichols', department: 'Acting'}, {role: 'Executive producer', department: 'Production'}], 'cast'),
  ];
  const selected = all.filter(row => (kind ? row.media.kind === kind : true) && (role === 'cast' ? row.roles.some(r => r.department === 'Acting') : role === 'crew' ? row.roles.some(r => r.department !== 'Acting') : true));
  return {items: selected.slice(0, limit).map(wireCredit), total: selected.length, nextCursor: selected.length > limit ? 'next' : ''};
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

test('the person page reads the server roles and pages requested kinds independently', async () => {
  const paths: string[] = [];
  const owner = setup(async path => { paths.push(path); const query = new URL('https://unit.invalid' + path).searchParams; return page(query.get('creditGroup') === 'acting' ? 'cast' : query.get('creditGroup'), query.get('kind'), Number(query.get('limit') ?? '40')); });
  const binding = createPersonBindings(owner.context);
  try {
    await binding.select('rebecca');
    const snapshot = binding.getSnapshot(), header = snapshot.page!;
    assert.equal(snapshot.phase, 'ready');
    assert.deepEqual(header.groups.map(group => [group.department, group.total]), [['Acting', 3], ['Production', 1]]);
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
  assert.doesNotMatch(entry, /\/v1\/(?!people\/|items\b|admin\/activity)/);
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

// A refused server cursor reads the person again once in place of the shown page; a second refusal shows.
function paged(continuation: (path: string) => unknown) {
  const paths: string[] = [];
  const many = Array.from({length: 60}, (_, index) => wireCredit(credit(`title-${index}`, 'movie', `Title ${index}`, [{role: 'Role', department: 'Acting'}], 'cast')));
  const owner = setup(async path => {
    paths.push(path);
    const query = new URL('https://unit.invalid' + path).searchParams, limit = Number(query.get('limit') ?? '40');
    if (query.get('cursor')) return continuation(path);
    const answer = {...page(null, null, 0), items: many.slice(0, limit), total: 60, nextCursor: 'p1'};
    return answer;
  }, 60);
  const later = (changes: object = {}) => ({...page(null, null, 0), items: many.slice(40), total: 60, nextCursor: '', ...changes});
  return {owner, paths, later, headers: () => paths.filter(path => path.includes('limit=1')).length};
}
const settled = async () => {for (let turn = 0; turn < 12; turn++) await setImmediate();};

test('step 7: a refused person credit cursor reads the person once in place of the shown page; a second refusal shows', async () => {
  const f = paged(() => Promise.reject(Object.assign(new Error('Expired'), {code: 'cursor_expired'})));
  const binding = createPersonBindings(f.owner.context);
  try {
    await binding.select('rebecca'); const header = binding.getSnapshot().page!;
    const phases: string[] = []; const off = binding.subscribe(() => phases.push(binding.getSnapshot().phase));
    // The collection reads ahead to page 1 on its own, so the refusal (and the re-read) can come with the first range.
    const all = binding.credits(header); all.ensureRange(0, 0); await settled(); all.ensureRange(40, 40); await settled(); off();
    const now = binding.getSnapshot();
    assert.notEqual(now.page, header); assert.equal(now.phase, 'ready'); assert.equal(now.page!.viewerFence, f.owner.context.scope.viewerId);
    assert.ok(!phases.includes('loading'), 'the shown page stays until the re-read answers');
    assert.equal(f.headers(), 2);
    // The screen takes the new page's collection; its refused cursor is shown, not read again.
    const again = binding.credits(now.page!); again.ensureRange(0, 0); await settled(); again.ensureRange(40, 40); await settled();
    assert.equal(again.getSnapshot().status, 'error'); assert.equal(binding.getSnapshot().page, now.page); assert.equal(f.headers(), 2);
  } finally { binding.dispose(); }
});

test('step 7: a later access change in the same visit reads the person again too, once a credit page has answered since the last re-read', async () => {
  // Three pages of credits; the continuation answers follow a script: refused, answered, refused, refused.
  const many = Array.from({length: 120}, (_, index) => wireCredit(credit(`title-${index}`, 'movie', `Title ${index}`, [{role: 'Role', department: 'Acting'}], 'cast')));
  const script = ['refuse', 'answer', 'refuse', 'refuse'], paths: string[] = [];
  let headers = 0;
  const owner = setup(async path => {
    paths.push(path);
    const query = new URL('https://unit.invalid' + path).searchParams, limit = Number(query.get('limit') ?? '40'), cursor = query.get('cursor');
    const base = page(null, null, 0);
    if (!cursor) {
      if (limit === 1) headers++;
      return {...base, items: many.slice(0, limit), total: 120, nextCursor: 'p40'};
    }
    if (script.shift() !== 'answer') throw Object.assign(new Error('Expired'), {code: 'invalid_cursor'});
    const start = Number(cursor.slice(1));
    return {...base, items: many.slice(start, start + 40), total: 120, nextCursor: `p${start + 40}`};
  }, 120);
  const binding = createPersonBindings(owner.context);
  const open = async (from: number) => {
    const all = binding.credits(binding.getSnapshot().page!);
    for (let start = 0; start <= from; start += 40) {all.ensureRange(start, start); await settled();}
    return all;
  };
  try {
    await binding.select('rebecca'); const first = binding.getSnapshot().page!;
    // First access change: page 1 is refused, and the person is read again.
    await open(40); const second = binding.getSnapshot().page!;
    assert.notEqual(second, first); assert.equal(headers, 2);
    // Paging goes on: page 1 answers, then a second access change refuses page 2, and the person is read again.
    await open(80); const third = binding.getSnapshot().page!;
    assert.notEqual(third, second); assert.equal(third.viewerFence, owner.context.scope.viewerId); assert.equal(headers, 3);
    assert.equal(binding.getSnapshot().phase, 'ready');
    // A refusal straight after that re-read, with no page answered in between, shows.
    const last = await open(40);
    assert.equal(last.getSnapshot().status, 'error'); assert.equal(binding.getSnapshot().page, third); assert.equal(headers, 3);
  } finally { binding.dispose(); }
});
