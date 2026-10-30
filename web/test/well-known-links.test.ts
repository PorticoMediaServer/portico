import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const read = (name: string) => JSON.parse(readFileSync(new URL(`../public/.well-known/${name}`, import.meta.url), 'utf8'));

test('O-162: the Hosted well-known files keep the existing app-link set, add /s/*, and hold real IDs or the agreed placeholders', () => {
  const apple = read('apple-app-site-association'), detail = apple.applinks.details[0];
  assert.deepEqual(detail.components.map((component: {'/': string}) => component['/']), ['/account', '/device', '/join', '/together/join', '/app/account-return', '/s/*']);
  for (const id of detail.appIDs) assert.match(id, /^(?:PORTICO_APPLE_TEAM_ID|[A-Z0-9]{10})\.tv\.getportico\.portico\.ios$/);
  const android = read('assetlinks.json')[0];
  assert.deepEqual(android.relation, ['delegate_permission/common.handle_all_urls']);
  assert.equal(android.target.package_name, 'tv.getportico.portico');
  for (const print of android.target.sha256_cert_fingerprints) assert.match(print, /^(?:PORTICO_PLAY_APP_SIGNING_SHA256|(?:[0-9A-F]{2}:){31}[0-9A-F]{2})$/);
});
