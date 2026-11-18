import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryAudience} from '../src/server-admin/library-document.ts';

// W2 F5 (16 Nov walk): a row the server had answers for and the web did not draw.
const member = (id: string, fields: Record<string, unknown>) => ({id, username: id, role: 'member', state: 'active', disabled: false, allLibraries: false, libraries: [], ...fields}) as never;

test('Who can see a library counts the accounts that can use the server and see it; the owner sees every library', () => {
  const members = [member('o', {role: 'owner'}), member('a', {allLibraries: true}), member('b', {libraries: ['lib_1']}), member('c', {libraries: ['lib_2']}),
    member('off', {allLibraries: true, disabled: true}), member('gone', {allLibraries: true, state: 'left'})];
  assert.deepEqual(libraryAudience(members, 'lib_1'), {everyone: false, accounts: 3});
  assert.deepEqual(libraryAudience(members.filter(m => (m as {id: string}).id !== 'c'), 'lib_1'), {everyone: true, accounts: 3});
});

