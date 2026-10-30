import test from 'node:test';
import assert from 'node:assert/strict';
import {browserAccountOrigin} from '../src/platform/account-origin.ts';
import {safeApprovalUrl} from '@core/claim-web.ts';

test('packaged web accepts the production claim page on local, LAN and custom server addresses', () => {
  for (const origin of ['http://localhost:32500', 'http://192.168.1.20:32500', 'https://media.example']) {
    const hosted = browserAccountOrigin({DEV: false}, new URL(origin));
    assert.equal(hosted, 'https://web.getportico.tv');
    assert.equal(safeApprovalUrl('https://web.getportico.tv/claim#code=approval', hosted), 'https://web.getportico.tv/claim#code=approval');
    assert.equal(safeApprovalUrl('https://attacker.example/claim#code=approval', hosted), undefined);
  }
});

test('development and explicitly configured account workspaces retain their own authority', () => {
  const page = new URL('http://127.0.0.1:19412');
  assert.equal(browserAccountOrigin({DEV: true}, page), 'http://127.0.0.1:19410');
  assert.equal(browserAccountOrigin({DEV: false, VITE_ACCOUNT_WORKSPACE: 'true'}, page), page.origin);
  assert.equal(browserAccountOrigin({DEV: false, VITE_HOSTED_URL: 'https://accounts.example'}, page), 'https://accounts.example');
  assert.equal(browserAccountOrigin({DEV: false}, new URL('https://web.getportico.tv')), 'https://web.getportico.tv');
});
