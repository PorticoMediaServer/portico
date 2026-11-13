import test from 'node:test';
import assert from 'node:assert/strict';
import {systemAnswer} from '../../../packages/client-core/test/next-wire.ts';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

// The TV's move hand-off shows a phone where to go (`inspectedOrigin()`): only the address of a
// finished lookup whose answer is the server held here, never one a lookup is still trying.
test('inspectedOrigin answers a finished lookup of the held server, and refuses while another lookup is in flight', async () => {
  let hold: Promise<void> | undefined, release!: () => void, asked!: () => void;
  const app = await standardHomeApp({setup: true, setupResponse: async (request: any, f: any) => {
    if (request.path === '/v1/system') {
      if (hold) {asked(); await hold;}
      return {status: 200, body: systemAnswer({id: f.pin.serverId, name: 'Synthetic local server', version: '1'})};
    }
    return undefined;
  }});
  const {NativeServerSignIn} = await import('../src-next/platform/server-sign-in.ts');
  const signIn = new NativeServerSignIn();
  try {
    await signIn.inspect('https://standard-home.test');
    assert.equal(signIn.inspectedOrigin(), 'https://standard-home.test');
    hold = new Promise<void>(resolve => {release = resolve;});
    const reached = new Promise<void>(resolve => {asked = resolve;});
    const again = signIn.inspect('https://standard-home.test:8443');
    await reached;
    assert.throws(() => signIn.inspectedOrigin(), {code: 'identity_mismatch'}, 'an address no server has answered from never reaches the QR');
    hold = undefined; release();
    await again.catch(() => undefined);
  } finally {signIn.dispose(); await app.settle();}
});
