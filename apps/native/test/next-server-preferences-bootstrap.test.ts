import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import type {PreferenceSnapshot} from '../../../packages/client-core/src/preferences';

const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};

test('bootstrap and Settings share confirmed CAS results without rollback from an older concurrent refresh', async () => {
  const began = deferred(), release = deferred(), patchBegan = deferred(), releasePatch = deferred();
  let holdLibraries = false, holdPatch = false, initial: PreferenceSnapshot | undefined;
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any, fixture: any) => {
    if (holdLibraries && request.path === '/v1/libraries') {began.resolve(); await release.promise;}
    if (request.path === '/v1/preferences' && request.method === 'PATCH') {
      assert.ok(initial);
      const values = request.body.values;
      const next = {...initial, documents: initial.documents.map(document => document.scope === request.body.scope
        ? {...document, revision: document.revision + 1, activeRevision: document.activeRevision + 1, values: {...document.values, ...values}} : document),
        effective: {...initial.effective, ...values}};
      if (holdPatch) {patchBegan.resolve(); await releasePatch.promise;}
      return {status: 200, body: {scope: {serverId: fixture.pin.serverId, viewerFence: 'a'.repeat(64)}, data: next}};
    }
    return undefined;
  }});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {nativeServerPreferences} = await import('../src-next/platform/server-preferences');
    const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime');
    const {createBoundNativeSettingsController} = await import('../src-next/screens/settings-live/settings-controller');
    let captured: NativeShellBootstrapContext | undefined, ended = 0;
    runtime = await restoreNativeShellRuntime(context => {captured = context; return createNativeShellBindings(context);}, {onAuthorityEnded() {ended++;}});
    const context = captured; assert.ok(runtime); assert.ok(context);
    const source = nativeServerPreferences(context); initial = source.getSnapshot();
    assert.equal(source.getSnapshot(), context.preferences());
    let notifications = 0; const off = source.subscribe(() => {notifications++;});
    const borrower = createBoundNativeSettingsController(new NativeScreenLifetime(context), context);
    const revision = initial.documents.find(document => document.scope === 'profile-server')!.revision;
    holdLibraries = true; const refresh = runtime.refresh(); await began.promise;
    const confirmed = await source.applyPreferences('profile-server', 'mobile', revision, {'playback.defaultSpeed': 1.5});
    assert.equal(context.preferences(), confirmed, 'ACK publishes the same canonical object to root consumers immediately');
    assert.equal(confirmed.effective['playback.defaultSpeed'], 1.5);
    assert.equal(borrower.getSnapshot().preferences, confirmed);
    assert.equal(notifications, 1);
    release.resolve(); await refresh;
    assert.equal(context.preferences(), confirmed, 'a refresh that captured an older document cannot undo the acknowledged write');
    assert.equal(ended, 0);
    const patches = app.requests.filter(request => request.path === '/v1/preferences' && request.method === 'PATCH');
    assert.equal(patches[0]!.body.expectedRevision, revision);
    await assert.rejects(source.preferences('television'));
    await assert.rejects(source.applyPreferences('profile-server', 'mobile', revision, {'not-a-preference': true}));
    borrower.dispose(); assert.equal(source.getSnapshot(), confirmed, 'a screen borrower never closes the authority reader');
    assert.equal((await source.preferences('mobile')).effective['playback.defaultSpeed'], 1.5, 'the shared reader also withholds a later stale GET');
    holdPatch = true; const late = source.applyPreferences('profile-server', 'mobile', revision + 1, {'playback.defaultSpeed': 2});
    await patchBegan.promise; runtime.dispose(); releasePatch.resolve();
    await assert.rejects(late); assert.throws(source.getSnapshot, {name: 'AbortError'}); off();
  } finally {
    try {
      release.resolve();
      releasePatch.resolve();
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

test('a refresh completing during a preference PATCH does not cancel the authority-owned write', async () => {
  const began = deferred(), release = deferred(), readBegan = deferred(), releaseRead = deferred();
  let holdRead = false, initial: PreferenceSnapshot | undefined, patchSignal: AbortSignal | undefined;
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any, fixture: any) => {
    if (holdRead && request.path.startsWith('/v1/preferences?') && request.method === 'GET') {readBegan.resolve(); await releaseRead.promise;}
    if (request.path === '/v1/preferences' && request.method === 'PATCH') {
      patchSignal = request.signal; began.resolve(); await release.promise; assert.ok(initial);
      return {status: 200, body: {scope: {serverId: fixture.pin.serverId, viewerFence: 'a'.repeat(64)}, data: {...initial,
        documents: initial.documents.map(document => document.scope === 'profile-server' ? {...document, revision: document.revision + 1, activeRevision: document.activeRevision + 1} : document),
        effective: {...initial.effective, 'playback.defaultSpeed': 1.5}}}};
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
    const patch = source.applyPreferences('profile-server', 'mobile', initial.documents.find(document => document.scope === 'profile-server')!.revision, {'playback.defaultSpeed': 1.5});
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
