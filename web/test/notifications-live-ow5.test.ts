import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

// OW-5: an open Notifications page or bell panel only refreshed the badge on `notification.changed`.
// The feed now hands each hint (and a stream resync) to InboxService.refresh, whose behaviour is
// covered in packages/client-core/test/inbox-refresh.test.ts.
const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('the web feed rereads its view on a change hint or a resync while its opening is current', () => {
  const source = read('../src/screens/account/Notifications.route.tsx');
  assert.match(source, /const refresh = \(revision\?: string\) => \{if \(opening\.current\(\)\) service\.refresh\(revision\);\};/);
  assert.match(source, /context\.events\.on\('notification\.changed', event => refresh\(event\.revision\)\)/);
  assert.match(source, /context\.events\.onResync\(\(\) => refresh\(\)\)/);
  assert.match(source, /return \(\) => \{for \(const off of offs\) off\(\);\};\n  \}, \[opening, service, context\.events\]\);/);
});

test('native follows the same hint without cancelling a write that is still out', () => {
  const source = read('../../apps/native/src-next/screens/notifications/NotificationsScreen.tsx');
  assert.match(source, /lease\.profile\.refresh\(event\.revision\);/);
  assert.doesNotMatch(source, /context\.events\.on\('notification\.changed', \(\) =>/);
});
