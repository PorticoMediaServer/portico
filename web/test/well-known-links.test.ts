import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const read = (name: string) => JSON.parse(readFileSync(new URL(`../public/.well-known/${name}`, import.meta.url), 'utf8'));

test('O-162: the Hosted well-known files keep the existing app-link set, add /s/*, and name the Portico app and its signing key', () => {
  const apple = read('apple-app-site-association'), detail = apple.applinks.details[0];
  assert.deepEqual(detail.components.map((component: {'/': string}) => component['/']), ['/account', '/device', '/join', '/together/join', '/app/account-return', '/s/*']);
  for (const id of detail.appIDs) assert.equal(id, 'WB7BUK5Z7C.tv.getportico.app');
  const android = read('assetlinks.json')[0];
  assert.deepEqual(android.relation, ['delegate_permission/common.handle_all_urls']);
  assert.equal(android.target.package_name, 'tv.getportico.app');
  // Our own app signing key, also uploaded to Play App Signing, so Play and GitHub installs update each other.
  assert.deepEqual(android.target.sha256_cert_fingerprints, ['DB:88:A3:17:8D:49:86:03:FE:B6:63:B9:67:99:0C:76:39:2E:37:CD:43:E1:B9:53:44:AE:2E:A9:87:EF:56:FA']);
});

test('getportico.tv serves the same app links as web.getportico.tv', () => {
  const site = (name: string) => JSON.parse(readFileSync(new URL(`../../website/public/.well-known/${name}`, import.meta.url), 'utf8'));
  assert.deepEqual(site('assetlinks.json'), read('assetlinks.json'));
  const apple = site('apple-app-site-association');
  assert.deepEqual(apple.applinks.details.flatMap((d: {appIDs: string[]}) => d.appIDs), ['WB7BUK5Z7C.tv.getportico.app']);
  assert.deepEqual(apple.activitycontinuation.apps, ['WB7BUK5Z7C.tv.getportico.app']);
});
