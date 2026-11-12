import assert from 'node:assert/strict';
import test from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';

// A screen's listener that throws must not stop a sign-out, a server switch or a Portico Account
// sign-out: every listener still hears the fence and the settle, and the step itself goes on.
async function accounts() {
  const modules: Record<string, unknown> = {
    'react-native': {NativeModules: {}, Platform: {OS: 'android'}},
    '../../../../packages/client-core/src/sign-in': {ServerSignInProbe: class {cancel() {} dispose() {}}},
  };
  const {NativeAccounts} = await screenModule(new URL('../src-next/platform/accounts.ts', import.meta.url), new Proxy(modules, {get: (all, key: string) => all[key] ?? {}}));
  return new NativeAccounts();
}

test('a throwing fence, cancel or settle listener is passed over and every other listener still runs', async t => {
  const warned = t.mock.method(console, 'warn', () => {});
  const native = await accounts(), heard: string[] = [];
  native.onCancel(() => {throw new Error('cancel listener')}); native.onCancel(() => heard.push('cancel'));
  native.onFence(() => {throw new Error('fence listener')}); native.onFence(() => heard.push('fence'));
  native.onSettled(() => {throw new Error('settle listener')}); native.onSettled(() => heard.push('settled'));
  assert.doesNotThrow(() => native.cancel());
  assert.deepEqual(heard, ['cancel', 'fence', 'settled']);
  assert.equal(warned.mock.callCount(), 3, 'each failure is reported, not swallowed silently');
});
