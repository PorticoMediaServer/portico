import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

// P2-61 HOME-RESILIENCE (native parity with web S:bf604da7e): a slow or failed library
// directory never fails sign-in or blanks Home; authority answers still stop.

const settle = async (ms = 0) => {await new Promise(resolve => setTimeout(resolve, ms));};
async function boot(libraries: () => {status: number; body: unknown} | undefined) {
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any) => request.path === '/v1/libraries' ? libraries() : undefined});
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  return {app, restore: () => restoreNativeShellRuntime(context => createNativeShellBindings(context), {onAuthorityEnded() {}})};
}
async function close(app: any, runtime?: RestoredNativeShellRuntime) {
  try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
}

test('a library directory that answers 503 at sign-in leaves the shell running: unknown, never empty, and Try again recovers', async () => {
  let failing = true, reads = 0;
  const {app, restore} = await boot(() => {reads++; return failing ? {status: 503, body: {error: {code: 'metadata_unavailable', message: 'busy'}}} : undefined;});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    runtime = await restore();
    assert.ok(runtime, 'sign-in completes despite the directory');
    assert.equal(runtime.getSnapshot().librariesUnavailable, true, 'the libraries are unknown');
    assert.deepEqual(runtime.getSnapshot().libraries, [], 'and not presented as a known empty list');
    assert.equal(typeof runtime.retryLibraries, 'function');
    failing = false;
    const before = reads;
    runtime.retryLibraries!();
    for (let i = 0; i < 50 && runtime.getSnapshot().librariesUnavailable; i++) await settle(10);
    assert.ok(reads > before, 'Try again reads the directory again');
    assert.equal(runtime.getSnapshot().librariesUnavailable, false, 'a later answer makes them known');
    assert.ok(runtime.getSnapshot().libraries.length > 0);
  } finally {await close(app, runtime);}
});

test('an authority answer from the library directory still stops sign-in', async () => {
  const {app, restore} = await boot(() => ({status: 401, body: {error: {code: 'unauthorized', message: 'signed out'}}}));
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    let failed: unknown;
    try {runtime = await restore();} catch (error) {failed = error;}
    assert.ok(failed !== undefined || runtime === undefined, 'a 401 is never tolerated as "unavailable"');
  } finally {await close(app, runtime);}
});

test('a refresh whose directory fails keeps the libraries already known', async () => {
  let failing = false;
  const {app, restore} = await boot(() => failing ? {status: 503, body: {error: {code: 'busy', message: 'busy'}}} : undefined);
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    runtime = await restore();
    assert.ok(runtime);
    const known = runtime.getSnapshot().libraries;
    assert.ok(known.length > 0);
    failing = true;
    await runtime.refresh?.();
    assert.deepEqual(runtime.getSnapshot().libraries, known, 'the known directory survives a failed re-read');
    assert.notEqual(runtime.getSnapshot().librariesUnavailable, true);
  } finally {await close(app, runtime);}
});

test('one automatic re-read about 2 s after sign-in brings the libraries back without Try again', async () => {
  let reads = 0;
  const {app, restore} = await boot(() => (++reads === 1 ? {status: 503, body: {error: {code: 'busy', message: 'busy'}}} : undefined));
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    runtime = await restore();
    assert.ok(runtime);
    assert.equal(runtime.getSnapshot().librariesUnavailable, true);
    await settle(1500);
    assert.equal(runtime.getSnapshot().librariesUnavailable, true, 'nothing re-reads before the short wait');
    for (let i = 0; i < 40 && runtime.getSnapshot().librariesUnavailable; i++) await settle(50);
    assert.equal(runtime.getSnapshot().librariesUnavailable, false, 'the automatic re-read answered');
    assert.ok(runtime.getSnapshot().libraries.length > 0);
  } finally {await close(app, runtime);}
});
