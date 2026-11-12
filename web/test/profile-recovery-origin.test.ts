import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';

async function fixture() {
  let reads = 0, proofs = 0;
  const source = {accountId: 'acct_ada', isCurrent: () => true};
  class Recovery {
    async porticoPassword(_password: string, _code: string, given: unknown) {
      proofs++; assert.equal((given as typeof source).accountId, source.accountId);
    }
  }
  const module = await componentModule(new URL('../src/platform/profile-recovery.ts', import.meta.url), {
    '@core/profile-recovery.ts': {ProfileRecoveryController: Recovery},
    '../services/account-session': {account: {}, accountSession: {signIns: () => {reads++; return 1;}, signedIn: () => true}},
    './portico-account-holder': {porticoAccountHolder: {who: async () => {reads++; return {id: source.accountId};}}},
  });
  const recovery = new module.BrowserProfileRecovery({}, {}, 'profile', 'portico', 'family', () => true);
  return {recovery, reads: () => reads, proofs: () => proofs};
}

test('the web password PIN path explicitly refuses every other origin before reading a signed-in account', async () => {
  const prior = Object.getOwnPropertyDescriptor(globalThis, 'location');
  try {
    const f = await fixture();
    for (const origin of ['http://web.getportico.tv', 'https://web.getportico.tv:4443',
      'https://web.getportico.tv.example', 'https://server.example', 'http://server.example']) {
      Object.defineProperty(globalThis, 'location', {configurable: true, value: {origin}});
      await assert.rejects(f.recovery.porticoPassword('just typed'), {code: 'portico_account_page_required'});
    }
    assert.equal(f.reads(), 0); assert.equal(f.proofs(), 0);
    Object.defineProperty(globalThis, 'location', {configurable: true, value: {origin: 'https://web.getportico.tv'}});
    await f.recovery.porticoPassword('just typed');
    assert.equal(f.reads(), 2); assert.equal(f.proofs(), 1);
  } finally {
    if (prior) Object.defineProperty(globalThis, 'location', prior);
    else Reflect.deleteProperty(globalThis, 'location');
  }
});
