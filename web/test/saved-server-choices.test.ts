import test from 'node:test';
import assert from 'node:assert/strict';
import type {AppSessionRecord} from '@core/app-sessions.ts';
import {savedServerChoices} from '../src/screens/accounts/saved-server-choices.ts';

test('only a real saved sign-in and its pinned actual name can become a chooser row', () => {
  const record = {version: 1, revision: 4, selectedServerId: 'local', servers: [
    {signIn: 'portico', connection: {pin: {serverId: 'portico'}}},
    {signIn: 'local', connection: {pin: {serverId: 'local'}}},
    {signIn: 'local', connection: {pin: {serverId: 'unnamed'}, logicalOrigin: 'https://not-a-name.test'}},
  ]} as AppSessionRecord;
  assert.deepEqual(savedServerChoices(record, [{id: 'portico', label: 'A genuine server'}, {id: 'local', label: 'Local server'}, {id: 'not-saved', label: 'Cannot add a sign-in'}]), [
    {id: 'portico', label: 'A genuine server', signIn: 'portico', selected: false},
    {id: 'local', label: 'Local server', signIn: 'local', selected: true},
  ]);
});
test('missing names, status and ownership never become guessed labels or availability', () => {
  const record = {version: 1, revision: 0, servers: [{signIn: 'local', connection: {pin: {serverId: 'opaque'}, logicalOrigin: 'https://private-address.test'}}]} as AppSessionRecord;
  assert.deepEqual(savedServerChoices(record, []), []);
  const choices = savedServerChoices(record, [{id: 'opaque', label: 'Exact actual name'}]);
  assert.equal('online' in choices[0], false); assert.equal('owner' in choices[0], false);
  assert.doesNotMatch(JSON.stringify(choices), /private-address/);
});
