/** Offline checkpoint preparation: notification route choices against current visible facts. */
import test from 'node:test';
import assert from 'node:assert/strict';
import type {NotificationAction} from '@core/notifications.ts';
import {notificationDestination} from '../src/screens/account/notification-destination.ts';
const navigate = (target: NonNullable<NotificationAction['target']>): NotificationAction => ({kind: 'navigate', label: 'Open', target});

test('retiring a visible library removes its destination without rewriting the received action', () => {
  const action = navigate({view: 'library', libraryId: 'library-A'});
  const original = structuredClone(action);
  assert.deepEqual(notificationDestination(action, [{id: 'library-A'}, {id: 'library-B'}], false), {view: 'library', id: 'library-A'});
  assert.equal(notificationDestination(action, [{id: 'library-B'}], false), undefined);
  assert.deepEqual(action, original);
});

test('library and item targets never borrow each other’s identifier', () => {
  assert.equal(notificationDestination(navigate({view: 'library', entityId: 'visible'}), [{id: 'visible'}], true), undefined);
  assert.equal(notificationDestination(navigate({view: 'item', libraryId: 'visible'}), [{id: 'visible'}], true), undefined);
  assert.equal(notificationDestination(navigate({view: 'library', libraryId: ''}), [{id: ''}], true), undefined);
  assert.equal(notificationDestination(navigate({view: 'item', entityId: ''}), [], true), undefined);
});

test('explicit home and settings targets retain their route independent of extra IDs', () => {
  for (const view of ['home', 'settings']) {
    const action = navigate({view, entityId: 'unrelated-item', libraryId: 'unrelated-library'});
    assert.deepEqual(notificationDestination(action, [], false), {view});
    assert.deepEqual(notificationDestination(action, [{id: 'another-library'}], true), {view});
  }
});

test('management route resolution follows the current capability on every lookup', () => {
  const action = navigate({view: 'server'});
  assert.deepEqual(notificationDestination(action, [], true), {view: 'dashboard'});
  assert.equal(notificationDestination(action, [], false), undefined);
  assert.deepEqual(notificationDestination(action, [], true), {view: 'dashboard'});
});

test('commands and missing targets remain unavailable rather than becoming navigations', () => {
  const actions: NotificationAction[] = [
    {kind: 'command', command: 'open-feedback', label: 'Feedback', target: {view: 'settings'}},
    {kind: 'navigate', label: 'Open'},
    navigate({view: 'unsupported', entityId: 'item'}),
  ];
  for (const action of actions) {
    assert.equal(notificationDestination(action, [{id: 'item'}], true), undefined);
  }
});
