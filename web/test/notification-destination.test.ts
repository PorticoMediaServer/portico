import test from 'node:test';
import assert from 'node:assert/strict';
import {notificationDestination} from '../src/screens/account/notification-destination.ts';

test('notification navigation uses real visible library identity and never a guessed item alias', () => {
  assert.deepEqual(notificationDestination({kind: 'navigate', label: 'Open', target: {view: 'library', libraryId: 'actual'}}, [{id: 'actual'}], false), {view: 'library', id: 'actual'});
  assert.equal(notificationDestination({kind: 'navigate', label: 'Open', target: {view: 'library', libraryId: 'foreign'}}, [{id: 'actual'}], false), undefined);
  assert.deepEqual(notificationDestination({kind: 'navigate', label: 'Open', target: {view: 'item', entityId: 'actual'}}, [], false), {view: 'item', id: 'actual'});
});
test('an unavailable command or unknown destination has no fake page handler', () => {
  assert.equal(notificationDestination({kind: 'command', command: 'open-feedback', label: 'Open'}, [], true), undefined);
  assert.equal(notificationDestination({kind: 'navigate', label: 'Open', target: {view: 'new-server-view'}}, [], true), undefined);
});
test('server notifications cannot promote a viewer into the management route', () => {
  const action = {kind: 'navigate' as const, label: 'Open', target: {view: 'server'}};
  assert.equal(notificationDestination(action, [], false), undefined);
  assert.deepEqual(notificationDestination(action, [], true), {view: 'dashboard'});
});
