import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

// PR-1 (review sweep 2 Nov): a remembered main profile with a PIN, past its one-hour management window, is refused with
// profile_pin_manage on every management request. Nothing asked for the PIN, so every write failed until sign-out.
// The shell now hears those refusals on its own sign-in, keeps running (they are not a re-entry, a sign-out or a role
// change) and asks for the PIN, which chooses the same profile again.

const MANAGED = '/v1/direct/profiles';
const settle = async (ms = 0) => {await new Promise(resolve => setTimeout(resolve, ms));};

async function boot() {
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any) => request.path === MANAGED && request.method === 'POST'
    ? {status: 403, body: {error: {code: 'profile_pin_manage', message: 'Enter this profile\'s PIN to manage this account.'}}} : undefined});
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  const {nativeManageGate} = await import('../src-next/platform/manage-gate');
  const ended: unknown[] = [];
  let context: NativeShellBootstrapContext | undefined;
  const runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded(error) {ended.push(error);}});
  return {app, runtime, ended, context: context!, gate: nativeManageGate};
}
async function close(app: any, runtime?: RestoredNativeShellRuntime) {
  try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
}

test('a management refusal keeps the shell and asks for the PIN once per refusal', async () => {
  const {app, runtime, ended, context, gate} = await boot();
  try {
    const before = gate.getSnapshot();
    const failure: {code?: unknown} = await context.api.request(MANAGED, 'POST', {name: 'Kid'}).catch((error: unknown) => error as {code?: unknown});
    assert.equal(failure?.code, 'profile_pin_manage', 'the screen still gets its own error');
    await settle(20);
    assert.deepEqual(ended, [], 'not a sign-out, a re-entry or a role probe');
    assert.equal(gate.getSnapshot(), before + 1, 'the PIN is asked for');
  } finally {await close(app, runtime);}
});

test('only the shell\'s own sign-in counts, and the phone and tablet shell mounts the PIN prompt (TV manages elsewhere)', () => {
  const source = readFileSync(new URL('../src-next/shell/bootstrap.ts', import.meta.url), 'utf8');
  assert.match(source, /local\.subscribeManageGate\(\(\{accessToken\}\) => \{if \(accessToken && ownTokens\.has\(accessToken\)\)/);
  assert.match(source, /const refused = \(cause\?: unknown\) => \{if \(nativeManageRefused\(cause\)\) return;/);
  const root = readFileSync(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), 'utf8');
  assert.match(root, /\{!theme\.isTV && <ManageUnlockHost\/>\}/);
});

test('the lifecycle takes the lock from the server and the unlock chooses the open profile again', () => {
  const ports = readFileSync(new URL('../src-next/platform/session-lifecycle-native.ts', import.meta.url), 'utf8');
  assert.match(ports, /manageLocked: snapshot\.managePinRequired === true/);
  const lifecycle = readFileSync(new URL('../src-next/platform/native-session-lifecycle.ts', import.meta.url), 'utf8');
  assert.match(lifecycle, /await settle\(lifecycle\.actions\.unlockManagement, pin\);/);
  const host = readFileSync(new URL('../src-next/screens/accounts/ManageUnlockHost.tsx', import.meta.url), 'utf8');
  assert.match(host, /lifecycle\.report\(\{type: 'manageRefused'\}\);/);
});
