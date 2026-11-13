import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {PinsController} from '../src/screens/pins/pins-controller.ts';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('Edit pins pins a library in the profile preference, preserving the saved pins', async () => {
  let pins = ['collection:kept'];
  const calls: string[] = [];
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    calls.push(`${method} ${path}`);
    if (path.startsWith('/v1/preferences')) {
      if (method === 'PATCH') {const next = body as {scope: string; values: {pins: string[]}}; assert.equal(next.scope, 'profile-server'); pins = next.values.pins;}
      return {effective: {pins}} as T;
    }
    if (path.startsWith('/v1/lists?')) return {items: [{id: 'kept', kind: 'collection', name: 'Kept'}]} as T;
    throw new Error('unexpected ' + path);
  }};
  const controller = new PinsController({api, serverId: 'server', current: () => true, libraryIds: () => ['lib_a', 'lib_b'], requestId: async () => 'op'});
  await controller.load();
  await controller.pinLibrary('lib_b', true);
  assert.deepEqual(pins, ['collection:kept', 'library:lib_b']);
  assert.deepEqual(controller.getSnapshot().libraries?.pinnedLibraryIds, ['lib_b']);
  assert.ok(calls.includes('PATCH /v1/preferences'));
  // A library that isn't this viewer's is never pinned.
  await controller.pinLibrary('lib_other', true);
  assert.deepEqual(pins, ['collection:kept', 'library:lib_b']);
  controller.dispose();
});

test('Edit pins sits at the right of the Libraries title (4d) and in every library\'s ⋯ (4c)', () => {
  const route = read('../src/screens/browse/Libraries.route.tsx'), library = read('../src/screens/browse/Library.route.tsx');
  assert.match(route, /<LibrariesScreen libraries=\{shown\} notice=\{notice\} artwork=\{captured\.context\.artwork\} action=\{editPins\}/);
  assert.match(route, /\{defaultI18n\.t\('pins\.edit'\)\}/);
  // Beside the title on a phone only; desktop keeps Edit pins in the library ⋯ (owner-metadata).
  assert.match(route, /const editPins = width < 600 && <Button/);
  assert.match(library, /\{\.\.\.\(editPins \? \{onEditPins: editPins\} : \{\}\)\}/);
  assert.equal(library.match(/const pins = usePinEditor\(context\);/g)?.length, 2);
  assert.equal(library.match(/copyLibraryLink, pins\.open\)/g)?.length, 2);
});
