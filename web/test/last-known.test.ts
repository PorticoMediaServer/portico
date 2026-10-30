import test from 'node:test';
import assert from 'node:assert/strict';

const store = new Map<string, string>();
Object.defineProperty(globalThis, 'localStorage', {configurable: true, value: {
  get length() { return store.size; },
  key: (index: number) => [...store.keys()][index] ?? null,
  getItem: (key: string) => store.get(key) ?? null,
  setItem: (key: string, value: string) => {store.set(key, value);},
  removeItem: (key: string) => {store.delete(key);},
  clear: () => {store.clear();},
}});

const {forgetAllShells, forgetShell, lastKnownShell, rememberShell} = await import('../src/shell/last-known.ts');

test('the launch record keeps the server name and never a library name', () => {
  store.clear();
  rememberShell('family-a', 'Portico demo');
  assert.deepEqual(lastKnownShell('family-a'), {serverName: 'Portico demo'});
  assert.equal(JSON.stringify([...store.values()]).includes('libraries'), false);
  // Another family, and a cleared family, have no record.
  assert.equal(lastKnownShell('family-b'), undefined);
  forgetShell('family-a');
  assert.equal(lastKnownShell('family-a'), undefined);
});

test('a legacy record carrying library names is read for its name only', () => {
  store.clear();
  store.set('portico.shell.lastKnown.v2:legacy', JSON.stringify({serverName: 'Old server', libraries: ['Movies', 'Anime', 'Music']}));
  const known = lastKnownShell('legacy');
  assert.deepEqual(known, {serverName: 'Old server'});
  assert.equal('libraries' in (known ?? {}), false);
  assert.equal(JSON.stringify(known).includes('Movies'), false);
});

test('a hostile or malformed record is ignored rather than partly trusted', () => {
  store.clear();
  store.set('portico.shell.lastKnown.v2:bad', '{not json');
  store.set('portico.shell.lastKnown.v2:empty', '');
  store.set('portico.shell.lastKnown.v2:shape', JSON.stringify({libraries: ['Movies']}));
  store.set('portico.shell.lastKnown.v2:huge', JSON.stringify({serverName: 'x'.repeat(20000)}));
  for (const family of ['bad', 'empty', 'shape', 'huge']) assert.equal(lastKnownShell(family), undefined, family);
});

test('signing out of the server drops every recorded family', () => {
  store.clear();
  rememberShell('one', 'First');
  rememberShell('two', 'Second');
  forgetAllShells();
  assert.equal(lastKnownShell('one'), undefined);
  assert.equal(lastKnownShell('two'), undefined);
});
