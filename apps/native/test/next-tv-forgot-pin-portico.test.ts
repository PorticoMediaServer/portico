// A TV's Forgot PIN on a Portico Accounts server sends the person to web.getportico.tv, where the
// Portico Account's password can be checked by Portico Services; a local server's stays on its own page.
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

const serverId = 'srv_' + 'A'.repeat(43);
test('the TV\'s Forgot PIN address: a Portico Accounts server opens at web.getportico.tv, a local one at its own profile page', async () => {
  const {forgotPINHandoff} = await screenModule(new URL('../src-next/platform/forgot-pin-handoff.ts', import.meta.url), {
    react: {default: {}}, './account-authority': {hostedUrl: 'https://web.getportico.tv'},
    '../../../../packages/client-core/src/route-identity': await import('../../../packages/client-core/src/route-identity.ts'),
  });
  assert.deepEqual(forgotPINHandoff('https://a1.ns.portico.direct:32500', {id: serverId, signIn: 'portico'}),
    {url: `https://web.getportico.tv/servers?open=${serverId}`, body: 'next.auth.tv.forgotPINPorticoBody'});
  assert.deepEqual(forgotPINHandoff('http://192.168.1.20:32400', {id: serverId, signIn: 'local'}),
    {url: 'http://192.168.1.20:32400/settings/profile', body: 'next.auth.tv.forgotPINBody'});
});
