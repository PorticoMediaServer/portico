import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import type {SettingsPersonRow} from '../../packages/client-core/src/settings-people.ts';
import {peopleTableRow} from '../src/screens/settings/people-table-row.ts';

const person: SettingsPersonRow = {accountId: 'actual-id', username: 'actual-user', role: 'member', revision: 3, limitsRevision: 4,
  disabled: false, status: 'active', hasMoveCode: false, allowedLibraries: [], libraryNames: ['Movies'], maxStreams: 0, maxStreamsMode: 'server',
  serverMaxStreams: 2, effectiveMaxStreams: 2, profileCount: 2, profileNames: ['Actual profile', 'Another profile'], liveState: 'idle'};
const actor = {accountId: 'other-owner', role: 'owner'} as const;
test('inherited zero never becomes unlimited; the actual effective stream fact governs the table', () => {
  assert.equal((peopleTableRow(person, actor).cells.access as {meta: string}).meta, defaultI18n.t('next.settings.streams', {count: 2}));
  assert.equal((peopleTableRow({...person, maxStreamsMode: 'unlimited', effectiveMaxStreams: null}, actor).cells.access as {meta: string}).meta, defaultI18n.t('next.settings.unlimitedStreams'));
});
test('unknown email and activity remain absent while the actual username drives identity/gradient', () => {
  const row = peopleTableRow(person, actor), identity = row.cells.person as {title: string; meta?: string; avatarKey: string};
  assert.equal(identity.title, person.username); assert.equal(identity.avatarKey, person.username); assert.equal(identity.meta, undefined);
  assert.deepEqual(row.cells.active, {title: ''}); assert.equal(row.cells.profiles, defaultI18n.list(person.profileNames));
});
test('selection keeps self/owner/admin authority fences, independently of current table pages', () => {
  assert.equal(peopleTableRow(person, {accountId: person.accountId, role: 'owner'}).selectable, false);
  assert.equal(peopleTableRow({...person, role: 'owner'}, actor).selectable, false);
  assert.equal(peopleTableRow({...person, role: 'admin'}, {accountId: 'other-admin', role: 'admin'}).selectable, false);
  assert.equal(peopleTableRow(person, {accountId: 'other-admin', role: 'admin'}).selectable, true);
});
