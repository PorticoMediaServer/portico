import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {readsAdminInbox} from '../src/notifications.ts';

// N5-2: the server lets a local owner or administrator read the account-admin inbox.
test('owners and administrators read the account-admin inbox; members never ask for it', () => {
  assert.equal(readsAdminInbox('owner'), true);
  assert.equal(readsAdminInbox('admin'), true);
  assert.equal(readsAdminInbox('member'), false);
  assert.equal(readsAdminInbox(''), false);
});

test('native Notifications gates the administrators\' feed on that rule, not on the owner alone', () => {
  const source = readFileSync(new URL('../../../apps/native/src-next/screens/notifications/NotificationsScreen.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /role [!=]== 'owner'/);
  assert.equal(source.match(/readsAdminInbox\(context\.account\.account\.role\)/g)?.length, 4);
  const services = readFileSync(new URL('../../../apps/native/src-next/shell/bootstrap-services.ts', import.meta.url), 'utf8');
  assert.match(services, /await this\.inbox\.load\('profile'\)/, 'the badge discovers both audiences from the server');
});
