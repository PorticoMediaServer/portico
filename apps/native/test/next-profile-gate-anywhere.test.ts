import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

// Android walk, 31 Oct: a PIN changed elsewhere reached the PIN screen only from the shell's own
// reads; a screen's request (Saved) showed "This couldn't load". The shell now listens to its
// connection, so any request it sent that is refused with profile_pin_reentry asks for the PIN.

const GATED = '/v1/me/gated-screen-read';
const settle = async (ms = 0) => {await new Promise(resolve => setTimeout(resolve, ms));};

async function boot(nativeResponse?: (request: any, fixture: any) => Promise<any>) {
  const app = await standardHomeApp({
    mountUI: false,
    nativeResponse: async (request: any, fixture: any) => {
      if (nativeResponse) {
        const custom = await nativeResponse(request, fixture);
        if (custom) return custom;
      }
      if (request.path === GATED) {
        return {status: 403, body: {error: {code: 'profile_pin_reentry', message: 'Enter the PIN again.'}}};
      }
      return undefined;
    },
  });
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  const ended: unknown[] = [];
  let context: NativeShellBootstrapContext | undefined;
  const runtime = await restoreNativeShellRuntime(
    value => {context = value; return createNativeShellBindings(value);},
    {onAuthorityEnded(error) {ended.push(error);}},
  );
  return {app, runtime, ended, context: context!};
}

async function close(app: any, runtime?: RestoredNativeShellRuntime) {
  try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
}

test('several screens refused at once end the shell through the PIN gate exactly once', async () => {
  const {app, runtime, ended, context} = await boot();
  try {
    assert.equal(ended.length, 0);
    const failures = await Promise.all([1, 2, 3].map(() => context.api.request<unknown>(GATED).catch((error: unknown) => error) as Promise<{code?: string} | undefined>));
    assert.ok(failures.every(error => error?.code === 'profile_pin_reentry'), 'each screen still gets its own error');
    await settle(20);
    assert.equal(ended.length, 1, 'one ending, not one per screen');
    assert.equal((ended[0] as {code?: unknown}).code, 'profile_pin_reentry', 'through the PIN gate, never the role probe');
  } finally {await close(app, runtime);}
});

test('disposing the shell releases its profile-gate subscription', async () => {
  const {app, runtime, ended, context} = await boot();
  try {
    const api = context.api;
    runtime!.dispose();
    await api.request<unknown>(GATED).catch(() => {});
    await settle(20);
    assert.equal(ended.length, 0, 'a disposed shell ignores subsequent profile_pin_reentry events');
  } finally {await close(app, runtime);}
});

test('an in-place renewal for the same viewer keeps gating the shell, while a foreign credential is ignored', async () => {
  let rejectWith401 = false;
  const renewedCred = 'renewed_same_viewer_cred';
  const {app, runtime, ended, context} = await boot(async (request: any, fixture: any) => {
    if (request.path === '/v1/me/trigger-refresh' && rejectWith401) {
      rejectWith401 = false;
      return {status: 401, body: {error: {code: 'unauthorized', message: 'Expired'}}};
    }
    if (request.path === '/v1/me/trigger-refresh') return {status: 200, body: {ok: true}};
    if (request.path === '/v1/auth/refresh') {
      return {
        status: 200,
        body: {
          ...fixture.session,
          accessToken: renewedCred,
          refreshToken: 'renewed_refresh_cred',
          tokenGeneration: '2',
          expiresAt: new Date(Date.now() + 3600_000).toISOString(),
        },
      };
    }
    return undefined;
  });
  try {
    // 1) A refusal carrying another sign-in's credential never gates this shell.
    const gateListeners = (context.api as unknown as {profileGateListeners: Set<(r: {error: unknown; accessToken?: string}) => void>}).profileGateListeners;
    assert.equal(gateListeners.size, 1, 'shell holds one profile-gate listener');
    for (const listener of [...gateListeners]) {
      listener({error: Object.assign(new Error('Enter the PIN again.'), {code: 'profile_pin_reentry'}), accessToken: 'foreign_other_viewer_cred'});
    }
    await settle(20);
    assert.equal(ended.length, 0, 'foreign credential refusal does not end this shell');

    // 2) Trigger a real reactive 401 -> /v1/auth/refresh rotation for the same viewer.
    rejectWith401 = true;
    await context.api.request('/v1/me/trigger-refresh');
    await settle(20);
    assert.equal(context.session()?.accessToken, renewedCred, 'session rotated in place');
    assert.equal(ended.length, 0);

    // 3) A profile_pin_reentry on a request sent with the renewed credential STILL gates the shell.
    await context.api.request(GATED).catch(() => {});
    await settle(20);
    assert.equal(ended.length, 1, 'renewed same-viewer credential still triggers PIN gate');
    assert.equal((ended[0] as {code?: unknown}).code, 'profile_pin_reentry');
  } finally {await close(app, runtime);}
});

test('#3018: a renewal moving this sign-in to another profile ends the shell with NativeProfileMoved', async () => {
  let rejectWith401 = false;
  const {app, runtime, ended, context} = await boot(async (request: any, fixture: any) => {
    if (request.path === '/v1/me/trigger-move' && rejectWith401) {
      rejectWith401 = false;
      return {status: 401, body: {error: {code: 'unauthorized', message: 'Expired'}}};
    }
    if (request.path === '/v1/me/trigger-move') return {status: 200, body: {ok: true}};
    if (request.path === '/v1/auth/refresh') {
      return {
        status: 200,
        body: {
          ...fixture.session,
          accessToken: 'moved_profile_cred',
          refreshToken: 'moved_refresh_cred',
          tokenGeneration: '2',
          expiresAt: new Date(Date.now() + 3600_000).toISOString(),
          viewer: {...fixture.viewer, profileId: 'other_profile'},
        },
      };
    }
    return undefined;
  });
  const {NativeProfileMoved} = await import('../src-next/platform/session-lifecycle-native');
  try {
    rejectWith401 = true;
    await context.api.request('/v1/me/trigger-move').catch(() => {});
    await settle(30);
    assert.equal(ended.length, 1, 'shell ends once when renewal moves profile');
    assert.ok(ended[0] instanceof NativeProfileMoved, 'ended with NativeProfileMoved for Who\'s watching');
    assert.equal((ended[0] as InstanceType<typeof NativeProfileMoved>).viewer.profileId, 'other_profile');
  } finally {await close(app, runtime);}
});
