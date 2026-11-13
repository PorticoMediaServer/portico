import assert from 'node:assert/strict';
import test from 'node:test';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {createNativePersonController, personDepartments} from '../src-next/screens/people/controller';
import {createNativePersonCreditWindow} from '../src-next/screens/people/credit-window';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const film = (id: string): ContentEntry => ({id, kind: 'movie', libraryId: 'films', title: id, navigation: {view: 'item', entityId: id}});
const show = (id: string): ContentEntry => ({id, kind: 'show', libraryId: 'shows', title: id, navigation: {view: 'item', entityId: id}});
const facts = {id: 'person-without-library', name: 'Stored Person', biography: 'Actual biography', images: {}, titleCount: 5, crewTitleCount: 5,
  roles: [{role: 'director', titleCount: 3}, {role: 'producer', titleCount: 2}]};
function answer(path: string) {
  const role = new URL(path, 'https://fixture.invalid').searchParams.get('role');
  const items = [...[film('movie-1'), film('movie-2'), film('movie-3')].map(media => ({...media, credits: [{personId: facts.id, name: facts.name, role: 'director'}]})),
    ...[show('series-1'), show('series-2')].map(media => ({...media, credits: [{personId: facts.id, name: facts.name, role: 'producer'}]}))]
    .filter(item => !role || item.credits[0].role === role);
  return {items, total: items.length};
}

test('a department window reads only that department of the arrived person page, with its own total', async () => {
  const reads: string[] = [];
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: {path: string}, f: {pin: {serverId: string}}) => {
    if (request.path.startsWith('/v1/people/')) return {status: 200, body: facts};
    if (!request.path.startsWith('/v1/items?person=')) return;
    reads.push(request.path);
    return {status: 200, body: answer(request.path)};
  }});
  let runtime: RestoredNativeShellRuntime | undefined, source: ReturnType<typeof createNativePersonController> | undefined;
  const windows: ReturnType<typeof createNativePersonCreditWindow>[] = [];
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    let context!: NativeShellBootstrapContext;
    runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    const route = {kind: 'person', personId: 'person-without-library'} as const;
    assert.equal(context.navigation.open(route, context.authority()), true);
    source = createNativePersonController(context, route);
    await source.load();
    assert.equal(reads.length, 1, 'the page read the person once');
    const page = source.getSnapshot().page!;
    const rows = page.groups.flatMap(group => group.department && group.total > 0 ? [{id: group.id, department: group.department, total: group.total}] : []);
    assert.deepEqual(rows.map(row => row.department), ['Directing', 'Production']);
    for (const row of rows) windows.push(createNativePersonCreditWindow(context, route, source, row));
    const [movies, shows] = windows;
    for (const window of windows) {
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error('group credits did not arrive')), 10000);
        const done = () => {if (window.collection.itemAt(0)) {clearTimeout(timer); resolve();}};
        window.collection.subscribePage(0, done); window.collection.ensureRange(0, 4); done();
      });
    }
    const departments = reads.slice(1).map(path => new URL(path, 'https://fixture.invalid').searchParams.get('role'));
    assert.deepEqual(departments.sort(), ['director', 'producer'], 'one department-scoped read per row; the seeded all-credits page is not reused');
    assert.equal(movies!.collection.getSnapshot().total, 3);
    assert.equal(shows!.collection.getSnapshot().total, 2);
    assert.deepEqual([0, 1, 2].map(index => movies!.collection.itemAt(index)?.media.id), ['movie-1', 'movie-2', 'movie-3']);
    assert.deepEqual([0, 1].map(index => shows!.collection.itemAt(index)?.media.id), ['series-1', 'series-2']);
    assert.equal(movies!.positionOf(movies!.collection.itemAt(2)!), 2, 'selection positions are the row own offsets');
    assert.equal(movies!.contains(movies!.collection.itemAt(1)!.media), true);
    assert.equal(movies!.contains(shows!.collection.itemAt(0)!.media), false, 'a row only claims its own credits');
  } finally {
    for (const window of windows) window.dispose();
    source?.dispose(); runtime?.dispose(); await app.unmount(); app.disposeLoader();
  }
});

test('on TV a re-read of all credits reads each department first page with it, and its window holds it from the start', async () => {
  const reads: string[] = [];
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: {path: string}, f: {pin: {serverId: string}}) => {
    if (request.path.startsWith('/v1/people/')) return {status: 200, body: facts};
    if (!request.path.startsWith('/v1/items?person=')) return;
    reads.push(request.path);
    return {status: 200, body: answer(request.path)};
  }});
  let runtime: RestoredNativeShellRuntime | undefined, source: ReturnType<typeof createNativePersonController> | undefined;
  const windows: ReturnType<typeof createNativePersonCreditWindow>[] = [];
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {personDepartments} = await import('../src-next/screens/people/controller');
    let context!: NativeShellBootstrapContext;
    runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    const route = {kind: 'person', personId: 'person-without-library'} as const;
    assert.equal(context.navigation.open(route, context.authority()), true);
    source = createNativePersonController(context, route, {departmentPages: true});
    await source.load();
    assert.equal(source.getSnapshot().departmentPages, undefined, 'the first read is the page alone (each row reads its own)');
    assert.equal(await source.reread(), true);
    const snapshot = source.getSnapshot(), pages = snapshot.departmentPages!;
    assert.deepEqual([...pages.keys()].sort(), ['role:director', 'role:producer']);
    const departments = reads.slice(1).map(path => new URL(path, 'https://fixture.invalid').searchParams.get('role'));
    assert.deepEqual(departments, [null, 'director', 'producer'], 'the page, then one read per department');
    const before = reads.length;
    for (const group of personDepartments(snapshot.page)) windows.push(createNativePersonCreditWindow(context, route, source, {...group, first: pages.get(group.id)}));
    const [movies, shows] = windows;
    assert.deepEqual([0, 1, 2].map(index => movies!.collection.itemAt(index)?.media.id), ['movie-1', 'movie-2', 'movie-3'], 'resident before any read');
    assert.deepEqual([0, 1].map(index => shows!.collection.itemAt(index)?.media.id), ['series-1', 'series-2']);
    assert.equal(movies!.collection.getSnapshot().total, 3);
    assert.equal(shows!.positionOf(shows!.collection.itemAt(1)!), 1);
    movies!.collection.ensureRange(0, 4); shows!.collection.ensureRange(0, 4);
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(reads.length, before, 'a seeded row reads nothing more for its first page');
  } finally {
    for (const window of windows) window.dispose();
    source?.dispose(); runtime?.dispose(); await app.unmount(); app.disposeLoader();
  }
});

test('Filters apply to every department row: Cast keeps the acting row, Crew the others, All every one', () => {
  const page = {groups: [{id: 'department:Acting', department: 'Acting', total: 4}, {id: 'department:Directing', department: 'Directing', total: 3},
    {id: 'department:Production', department: 'Production', total: 2}, {id: 'department:Empty', department: 'Empty', total: 0}, {id: 'kind:movie', kind: 'movie', total: 9}]} as any;
  const ids = (role?: 'all' | 'cast' | 'crew') => personDepartments(page, role).map(group => group.id);
  assert.deepEqual(ids(), ['department:Acting', 'department:Directing', 'department:Production'], 'empty and kind groups are never rows');
  assert.deepEqual(ids('all'), ids());
  assert.deepEqual(ids('cast'), ['department:Acting']);
  assert.deepEqual(ids('crew'), ['department:Directing', 'department:Production']);
  assert.deepEqual(personDepartments({groups: [{id: 'department:Cast', department: 'Cast', total: 1}]} as any, 'cast').map(group => group.id), ['department:Cast'], 'the server counts "Cast" as cast too');
  // Guest-star credits are their own TV row, named Guest stars, and belong to the Cast filter.
  const guests = {groups: [{id: 'role:actor', department: 'Acting', total: 2}, {id: 'role:guest', department: 'Guest stars', total: 1}, {id: 'role:director', department: 'Directing', total: 1}]} as any;
  assert.deepEqual(personDepartments(guests, 'cast').map(group => group.department), ['Acting', 'Guest stars']);
  assert.deepEqual(personDepartments(guests, 'crew').map(group => group.department), ['Directing']);
});

test('a department page whose total is not the department total never seeds its window (a server that ignored department)', async () => {
  const reads: string[] = [];
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: {path: string}, f: {pin: {serverId: string}}) => {
    if (request.path.startsWith('/v1/people/')) return {status: 200, body: facts};
    if (!request.path.startsWith('/v1/items?person=')) return;
    reads.push(request.path);
    return {status: 200, body: answer(request.path)};
  }});
  let runtime: RestoredNativeShellRuntime | undefined, source: ReturnType<typeof createNativePersonController> | undefined, window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    let context!: NativeShellBootstrapContext;
    runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    const route = {kind: 'person', personId: 'person-without-library'} as const;
    assert.equal(context.navigation.open(route, context.authority()), true);
    source = createNativePersonController(context, route);
    await source.load();
    const all = source.getSnapshot().page!;
    // All's page (five credits) offered as Directing's (three): not a seed.
    window = createNativePersonCreditWindow(context, route, source, {department: 'Directing', total: 3, first: all});
    assert.equal(window.collection.itemAt(0), undefined, 'nothing resident before its own read');
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('department credits did not arrive')), 10000);
      const done = () => {if (window!.collection.itemAt(0)) {clearTimeout(timer); resolve();}};
      window!.collection.subscribePage(0, done); window!.collection.ensureRange(0, 4); done();
    });
    assert.deepEqual([0, 1, 2].map(index => window!.collection.itemAt(index)?.media.id), ['movie-1', 'movie-2', 'movie-3'], 'its own department page');
    assert.equal(new URL(reads.at(-1)!, 'https://fixture.invalid').searchParams.get('role'), 'director');
  } finally {
    window?.dispose(); source?.dispose(); runtime?.dispose(); await app.unmount(); app.disposeLoader();
  }
});
