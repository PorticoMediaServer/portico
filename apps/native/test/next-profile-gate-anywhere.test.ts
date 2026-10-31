import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

// Android walk, 31 Oct: a PIN changed elsewhere reached the PIN screen only from the shell's own
// reads; a screen's request (Saved) showed "This couldn't load". The shell now listens to its
// connection, so any request it sent that is refused with profile_pin_reentry asks for the PIN.

const GATED = '/v1/me/gated-screen-read';
const settle = async (ms = 0) => {await new Promise(resolve => setTimeout(resolve, ms));};

async function boot() {
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any) => request.path === GATED
    ? {status: 403, body: {error: {code: 'profile_pin_reentry', message: 'Enter the PIN again.'}}} : undefined});
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  const ended: unknown[] = [];
  let context: NativeShellBootstrapContext | undefined;
  const runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded(error) {ended.push(error);}});
  return {app, runtime, ended, context: context!};
}
async function close(app: any, runtime?: RestoredNativeShellRuntime) {
  try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
}

test('several screens refused at once end the shell through the PIN gate exactly once', async () => {
  const {app, runtime, ended, context} = await boot();
  try {
    assert.equal(ended.length, 0);
    const failures = await Promise.all([1, 2, 3].map(() => context.api.request(GATED).catch(error => error)));
    assert.ok(failures.every(error => error?.code === 'profile_pin_reentry'), 'each screen still gets its own error');
    await settle(20);
    assert.equal(ended.length, 1, 'one ending, not one per screen');
    assert.equal((ended[0] as {code?: unknown}).code, 'profile_pin_reentry', 'through the PIN gate, never the role probe');
  } finally {await close(app, runtime);}
});

// A request sent with another sign-in's token can't be made from a shell: its connection fills in
// the shell's live token at send time (exchange → auth.token), and every connect builds a new
// HttpLocalApi (client-core server-connections.ts restoreNativeSession). The token check below is
// the guard for a reply that arrives anyway; client-core's profile-gate-signal test covers the token.

test('the subscription is released with the shell, and renewals in place stay its own', () => {
  const source = readFileSync(new URL('../src-next/shell/bootstrap.ts', import.meta.url), 'utf8');
  assert.match(source, /cleanups\.push\(local\.subscribeProfileGate\(\(\{error, accessToken\}\) => \{if \(accessToken && ownTokens\.has\(accessToken\)\) queueMicrotask\(\(\) => refused\(error\)\);\}\)\);/);
  assert.match(source, /if \(!current\(\) \|\| !sameViewer\(session!, renewed\)\) \{end\(\); return;\}\n\s+ownTokens\.add\(renewed\.accessToken\);/,
    'only a renewal of the same viewer joins the shell\'s tokens');
});
