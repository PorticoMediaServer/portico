import test from 'node:test';
import assert from 'node:assert/strict';
import {browserAccountOrigin} from '../src/platform/account-origin.ts';

test('development and explicitly configured account workspaces retain their own authority', () => {
  const page = new URL('http://127.0.0.1:19412');
  assert.equal(browserAccountOrigin({DEV: true}, page), 'http://127.0.0.1:19410');
  assert.equal(browserAccountOrigin({DEV: false, VITE_ACCOUNT_WORKSPACE: 'true'}, page), page.origin);
  assert.equal(browserAccountOrigin({DEV: false, VITE_HOSTED_URL: 'https://accounts.example'}, page), 'https://accounts.example');
  assert.equal(browserAccountOrigin({DEV: false}, new URL('https://web.getportico.tv')), 'https://web.getportico.tv');
});
