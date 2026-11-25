import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';

const source = (path: string) => readFile(new URL(path, import.meta.url), 'utf8');

// Both Touch and TV entries register all routes explicitly via shell/register-routes.ts,
// which registers profiles and servers from shell/account-route on both platforms.
test('the app and TV entries invoke explicit route registration including accounts routes', async () => {
  const app = await source('../src-next/App.tsx');
  assert.match(app, /registerNativeRoutes\(Platform\.isTV\)/);
  const tv = await source('../src-next/tv/register.ts');
  assert.match(tv, /registerNativeRoutes\(true\)/);
  const table = await source('../src-next/shell/register-routes.ts');
  assert.match(table, /import \{createAccountRoute\} from '\.\/account-route';/);
  assert.match(table, /kind: 'profiles', factory: createAccountRoute/);
  assert.match(table, /kind: 'servers', factory: createAccountRoute/);
});
