import test from 'node:test';
import assert from 'node:assert/strict';
import {porticoAccount, type HeldSignIn, type Tokens} from '../src/portico-account.ts';

// A tab left open past the access token's life, then Sign out pressed: the call renews first,
// so the sign-in held is no longer the one that was held when Sign out was pressed. Sign-out
// must still leave this browser holding nothing.
test('sign-out leaves nothing held when the access token had to be renewed to send it', async () => {
  let current: Tokens | undefined = {accessToken: 'old', expiresAt: new Date(Date.now() - 1000).toISOString()};
  let flagged = true;
  const held: HeldSignIn = {
    current: () => current,
    keep: tokens => { current = tokens; flagged = true; },
    forget: tokens => { if (current === tokens) { current = undefined; flagged = false; } },
    kept: () => flagged,
  };
  const sent: string[] = [];
  const saved = globalThis.fetch;
  globalThis.fetch = (async (path: string, init: RequestInit) => {
    sent.push(`${init.method} ${path}`);
    const renewal = String(path).endsWith('/sessions/refresh');
    const answer = renewal ? {accessToken: 'new', expiresAt: new Date(Date.now() + 3600_000).toISOString()} : undefined;
    return {ok: true, status: renewal ? 200 : 204, headers: {get: () => null}, text: async () => answer ? JSON.stringify(answer) : ''};
  }) as never;
  try {
    await porticoAccount(held, '', {cookie: true}).signOut();
  } finally { globalThis.fetch = saved; }
  assert.deepEqual(sent, ['POST /v1/sessions/refresh', 'DELETE /v1/sessions/current']);
  assert.equal(current, undefined, 'the page still holds an access token after signing out');
  assert.equal(flagged, false, 'the browser is still marked as signed in after signing out');
});
