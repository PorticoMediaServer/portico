import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeReadingDiscovery} from '../src-next/screens/read/reading-discovery.ts';
import {NativeReadingLibrary} from '../src-next/screens/read/reading-library.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
type Page = {items: readonly unknown[]; nextCursor?: string};

function fixture(pages: readonly Page[]) {
  let disposed = 0;
  const calls: {libraryId: string; limit?: number; cursor?: string}[] = [];
  const client = {list: async (options: {libraryId: string; limit?: number; cursor?: string}) => {
    calls.push(options);
    return pages[calls.length - 1] ?? {items: []};
  }, dispose: () => {disposed++;}};
  const context: any = {
    api: {reading: () => client}, session: () => ({sessionFamilyId: 'family', viewer: {...authority, authority: 'local'}}),
    current: () => true, authority: () => authority,
    navigation: {open: () => true, back: () => true, getSnapshot: () => ({authority, revision: 1, route: {kind: 'shell', destination: {kind: 'library', libraryId: 'books'}}}), subscribe: () => () => {}},
  };
  return {context, calls, disposed: () => disposed};
}

test('a reading library reads the bounded reading list, not the unsupported content browse', async () => {
  const d = fixture([{items: []}]);
  const controller = new NativeReadingLibrary(d.context, 'books');
  try {
    await controller.load();
    assert.equal(d.calls.length, 1);
    assert.equal(d.calls[0]!.libraryId, 'books');
    const state = controller.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.deepEqual(state.items, [], 'an empty book library is a real empty state, not an error');
    assert.equal(await controller.open({id: 'missing', sourceId: 's', inventoryAvailable: true} as never), false, 'only resident items open');
  } finally {controller.dispose();}
  assert.equal(d.disposed(), 1);
});

test('resident inventory-available items are retained in the server order', async () => {
  const items = [{id: 'b1', libraryId: 'books', sourceId: 's1', format: 'epub', inventoryAvailable: true, title: 'Book one'},
    {id: 'b2', libraryId: 'books', sourceId: 's2', format: 'cbz', inventoryAvailable: false, title: 'Book two'}];
  const d = fixture([{items}]), controller = new NativeReadingLibrary(d.context, 'books');
  try {
    await controller.load();
    assert.deepEqual(controller.getSnapshot().items.map(item => item.id), ['b1', 'b2']);
    assert.equal(await controller.open(items[1] as never), false, 'unavailable inventory never opens');
  } finally {controller.dispose();}
});

test('the next page follows the opaque cursor and appends without dropping the first', async () => {
  const d = fixture([{items: [{id: 'b1', title: 'One'}], nextCursor: 'cursor-1'}, {items: [{id: 'b2', title: 'Two'}]}]);
  const controller = new NativeReadingLibrary(d.context, 'books');
  try {
    await controller.load();
    assert.equal(controller.getSnapshot().nextCursor, 'cursor-1');
    await controller.more();
    assert.equal(d.calls[1]!.cursor, 'cursor-1');
    const state = controller.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.deepEqual(state.items.map(item => item.id), ['b1', 'b2']);
    assert.equal(state.nextCursor, undefined);
  } finally {controller.dispose();}
});

test('a failed read is a real error state and retry reads again', async () => {
  let attempt = 0, disposed = 0;
  const context: any = {
    api: {reading: () => ({list: async () => {attempt++; if (attempt === 1) throw new Error('unreadable'); return {items: [{id: 'b1', title: 'One'}]};}, dispose: () => {disposed++;}})},
    session: () => ({sessionFamilyId: 'family', viewer: {...authority, authority: 'local'}}),
    current: () => true, authority: () => authority, navigation: {open: () => true, back: () => true, getSnapshot: () => ({authority, revision: 1}), subscribe: () => () => {}},
  };
  const controller = new NativeReadingLibrary(context, 'books');
  try {
    await controller.load();
    assert.equal(controller.getSnapshot().phase, 'error');
    await controller.retry();
    assert.equal(controller.getSnapshot().phase, 'ready');
    assert.deepEqual(controller.getSnapshot().items.map(item => item.id), ['b1']);
  } finally {controller.dispose();}
  assert.equal(disposed, 1);
});

function discoveryFixture(answers: {continue?: unknown; discover?: unknown}) {
  let disposed = 0;
  const calls: {method: string; libraryId?: string}[] = [];
  const client = {
    continue: async (options: {libraryId?: string}) => {calls.push({method: 'continue', libraryId: options.libraryId});
      if (answers.continue instanceof Error) throw answers.continue; return answers.continue ?? {items: []};},
    discover: async (options: {libraryId?: string}) => {calls.push({method: 'discover', libraryId: options.libraryId});
      if (answers.discover instanceof Error) throw answers.discover; return answers.discover ?? {rows: []};},
    dispose: () => {disposed++;},
  };
  const context: any = {
    api: {reading: () => client}, session: () => ({sessionFamilyId: 'family', viewer: {...authority, authority: 'local'}}),
    current: () => true, authority: () => authority,
    navigation: {open: () => true, back: () => true, getSnapshot: () => ({authority, revision: 1, route: {kind: 'shell', destination: {kind: 'library', libraryId: 'books'}}}), subscribe: () => () => {}},
  };
  return {context, calls, disposed: () => disposed};
}

test('Discover reads only the two real producers, scoped to this library', async () => {
  const d = discoveryFixture({
    continue: {items: [{item: {id: 'b1', title: 'One'}, progress: {location: {progression: 0.25, section: 'Chapter 2'}}}]},
    discover: {rows: [{key: 'recent', items: [{id: 'b1', title: 'One'}]}]},
  });
  const controller = new NativeReadingDiscovery(d.context, 'books');
  try {
    await controller.load();
    const state = controller.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.equal(state.continueItems.length, 1);
    assert.deepEqual(state.recent.map(item => item.id), ['b1']);
    assert.deepEqual(d.calls.map(call => [call.method, call.libraryId]), [['continue', 'books'], ['discover', 'books']]);
    assert.equal(await controller.open({id: 'elsewhere', inventoryAvailable: true} as never), false, 'a non-resident item never opens');
  } finally {controller.dispose();}
  assert.equal(d.disposed(), 1);
});

test('a failed producer preserves the successful row and exposes an error for retry', async () => {
  const d = discoveryFixture({continue: new Error('unreadable'), discover: {rows: [{key: 'recent', items: [{id: 'b2', title: 'Two'}]}]}});
  const controller = new NativeReadingDiscovery(d.context, 'books');
  try {
    await controller.load();
    const state = controller.getSnapshot();
    assert.equal(state.phase, 'error');
    assert.equal((state.error as Error).message, 'unreadable');
    assert.deepEqual(state.continueItems, []);
    assert.deepEqual(state.recent.map(item => item.id), ['b2']);
  } finally {controller.dispose();}
});

test('a partial failure with an empty successful row still allows retry', async () => {
  const answers = {continue: {items: []}, discover: new Error('discover down') as unknown};
  const d = discoveryFixture(answers), controller = new NativeReadingDiscovery(d.context, 'books');
  try {
    await controller.load();
    assert.equal(controller.getSnapshot().phase, 'error');
    assert.equal((controller.getSnapshot().error as Error).message, 'discover down');
    answers.discover = {rows: [{key: 'recent', items: [{id: 'b2', title: 'Two'}]}]};
    await controller.retry();
    assert.equal(controller.getSnapshot().phase, 'ready');
    assert.equal(controller.getSnapshot().error, undefined);
    assert.deepEqual(controller.getSnapshot().recent.map(item => item.id), ['b2']);
  } finally {controller.dispose();}
});

test('both failed producers are an error state and retry reads again', async () => {
  let fails = true, disposed = 0;
  const context: any = {
    api: {reading: () => ({
      continue: async () => {if (fails) throw new Error('continue down'); return {items: [{item: {id: 'b1', title: 'One'}, progress: {location: {progression: 0.5}}}]};},
      discover: async () => {if (fails) throw new Error('discover down'); return {rows: [{key: 'recent', items: []}]};},
      dispose: () => {disposed++;},
    })},
    session: () => ({sessionFamilyId: 'family', viewer: {...authority, authority: 'local'}}),
    current: () => true, authority: () => authority, navigation: {open: () => true, back: () => true, getSnapshot: () => ({authority, revision: 1}), subscribe: () => () => {}},
  };
  const controller = new NativeReadingDiscovery(context, 'books');
  try {
    await controller.load();
    assert.equal(controller.getSnapshot().phase, 'error');
    fails = false;
    await controller.retry();
    assert.equal(controller.getSnapshot().phase, 'ready');
    assert.equal(controller.getSnapshot().continueItems.length, 1);
  } finally {controller.dispose();}
  assert.equal(disposed, 1);
});

test('a Discover row continues alone from its own cursor and never repeats a drawn title', async () => {
  const d = discoveryFixture({discover: {rows: [{key: 'recent', items: [{id: 'b1', title: 'One'}], nextCursor: '20.1'}]}});
  const rows: {row: string; options: {libraryId?: string; cursor?: string}}[] = [];
  const pages = [{row: {key: 'recent', items: [{id: 'b1', title: 'One'}, {id: 'b2', title: 'Two'}], nextCursor: '10.2'}}, {row: {key: 'recent', items: [{id: 'b3', title: 'Three'}]}}];
  const client = d.context.api.reading();
  client.discoverRow = async (row: string, options: {libraryId?: string; cursor?: string}) => {rows.push({row, options}); return pages[rows.length - 1];};
  const controller = new NativeReadingDiscovery(d.context, 'books');
  try {
    await controller.load();
    await Promise.all([controller.more('recent'), controller.more('recent')]);
    assert.equal(rows.length, 1, 'one request per row at a time');
    assert.deepEqual(rows[0], {row: 'recent', options: {libraryId: 'books', cursor: '20.1', limit: 20}});
    await controller.more('recent');
    await controller.more('recent');
    assert.deepEqual(rows.map(call => call.options.cursor), ['20.1', '10.2'], 'the last page ends the row');
    assert.deepEqual(controller.getSnapshot().recent.map(item => item.id), ['b1', 'b2', 'b3']);
    await controller.more('short-reads');
    assert.equal(rows.length, 2, 'a row with no cursor asks for nothing');
  } finally {controller.dispose();}
});
