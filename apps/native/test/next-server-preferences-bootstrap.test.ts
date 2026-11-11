import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import type {PreferenceSnapshot} from '../../../packages/client-core/src/preferences';

const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};

test('a refresh completing during a preference PATCH does not cancel the authority-owned write', async () => {
  const began = deferred(), release = deferred(), readBegan = deferred(), releaseRead = deferred();
  let holdRead = false, initial: PreferenceSnapshot | undefined, patchSignal: AbortSignal | undefined;
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any, fixture: any) => {
    if (holdRead && request.path.startsWith('/v1/preferences?') && request.method === 'GET') {readBegan.resolve(); await releaseRead.promise;}
    if (request.path === '/v1/preferences' && request.method === 'PATCH') {
      patchSignal = request.signal; began.resolve(); await release.promise; assert.ok(initial);
      return {status: 200, body: {...initial, effective: {...initial.effective, 'playback.defaultSpeed': 1.5}}};
    }
    return undefined;
  }});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {nativeServerPreferences} = await import('../src-next/platform/server-preferences');
    let captured: NativeShellBootstrapContext | undefined;
    runtime = await restoreNativeShellRuntime(context => {captured = context; return createNativeShellBindings(context);}, {onAuthorityEnded() {throw new Error('unexpected_retirement');}});
    assert.ok(runtime); assert.ok(captured); const source = nativeServerPreferences(captured); initial = source.getSnapshot();
    holdRead = true; const refresh = runtime.refresh(); await readBegan.promise;
    const patch = source.applyPreferences('profile-server', 'mobile', {'playback.defaultSpeed': 1.5});
    await began.promise; releaseRead.resolve(); await refresh;
    assert.equal(patchSignal?.aborted, false, 'refresh batch completion does not abort a concurrent preference request');
    release.resolve(); assert.equal((await patch).effective['playback.defaultSpeed'], 1.5);
  } finally {
    try {
      release.resolve();
      releaseRead.resolve();
    } finally {
      try {
        runtime?.dispose();
      } finally {
        try {
          await app.unmount();
        } finally {
          app.disposeLoader();
        }
      }
    }
  }
});
