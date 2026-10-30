import test from 'node:test';
import assert from 'node:assert/strict';
import {CertificateFormSource} from '../src/screens/settings/server-remote/certificate-form-source.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import type {CertificateStatus} from '@core/certificates.ts';
const row = (id: string) => settingsRowIndex.get(id)!.row;
const status = (patch: Partial<CertificateStatus> = {}): CertificateStatus => ({configured: true, authorityId: 'a'.repeat(64),
  config: {enabled: true, publicPort: 443, publicAddress: '192.0.2.1', revision: '3'}, state: 'ready', environment: 'production',
  tlsReady: true, publiclyTrusted: true, listenerBound: true, listenPort: 443, reachability: 'probe_required', ...patch});
const turn = () => new Promise<void>(resolve => setImmediate(resolve));
const held = <T>() => {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};};
test.beforeEach(t => t.mock.timers.enable({apis: ['setTimeout']}));
function fixture(initial = status()) {
  let stored = initial, current = true, failSave = false;const errors: unknown[] = [];
  const calls: {path: string; method?: string; body?: any; signal?: AbortSignal}[] = [];
  const source = new CertificateFormSource({async request<T>(path: string, method?: string, body?: any, signal?: AbortSignal) {
    calls.push({path, method, body, signal});
    if (path.endsWith('/config')) {
      if (failSave) throw Object.assign(new Error('Refused'), {status: 503});
      assert.equal(body.authorityId, stored.authorityId);assert.equal(body.config.revision, stored.config.revision);
      stored = {...stored, config: {...body.config, revision: String(Number(stored.config.revision) + 1)}, tlsReady: body.config.enabled, publiclyTrusted: body.config.enabled};
    }
    return stored as T;
  }}, () => current, error => errors.push(error));
  return {source, calls, errors, retire: () => {current = false;}, failSave: (value: boolean) => {failSave = value;}};
}

test('claimed secure switch saves canonical authority/revision and preserves other certificate fields', async () => {
  const f = fixture();try {
    await f.source.load();assert.equal(f.source.rowState(row('remote.secure')).value, true);
    f.source.change('remote.secure', false);await turn();
    assert.deepEqual(f.calls[1].body, {authorityId: 'a'.repeat(64), config: {enabled: false, publicPort: 443, publicAddress: '192.0.2.1', revision: '3'}});
    assert.equal(f.source.rowState(row('remote.secureStatus')).present, false);
    assert.equal(f.source.rowState(row('remote.secure')).changed, true);
    f.source.reset('remote.secure');await turn();assert.equal(f.calls[2].body.config.revision, '4');
    assert.equal(f.source.rowState(row('remote.secure')).changed, false);assert.deepEqual(f.errors, []);
  } finally {f.source.dispose();}
});

test('unclaimed states or absent authority do not expose or configure the service', async () => {
  for (const state of ['claim_required', 'claim_pending', 'authority_unavailable', 'unconfigured', 'ready']) {
    const f = fixture(status({state, ...(state === 'ready' ? {authorityId: ''} : {})}));try {
      await f.source.load();assert.equal(f.source.rowState(row('remote.secure')).present, false);
      f.source.change('remote.secure', false);await f.source.retry();await turn();assert.equal(f.calls.length, 1);
    } finally {f.source.dispose();}
  }
});

test('refused switch save remains inline and retries the same original configuration', async () => {
  const f = fixture();try {
    await f.source.load();f.failSave(true);f.source.change('remote.secure', false);await turn();
    assert.ok(f.source.getSnapshot().saveError);assert.equal(f.source.getSnapshot().dirty, true);assert.deepEqual(f.errors, []);
    await turn();assert.equal(f.calls.length, 2);f.failSave(false);await f.source.save();
    assert.deepEqual(f.calls[1].body, f.calls[2].body);assert.equal(f.source.getSnapshot().dirty, false);
  } finally {f.source.dispose();}
});

test('certificate retry is single flight, disables writes, and rereads before unlocking', async () => {
  const retry = held<CertificateStatus>(), refresh = held<CertificateStatus>();let calls = 0;
  const source = new CertificateFormSource({async request<T>(path: string, method?: string, body?: unknown) {
    calls++;if (path.endsWith('/retry')) {assert.equal(method, 'POST');assert.deepEqual(body, {});return await retry.promise as T;}
    return await (calls === 1 ? status({errorCode: 'renewal_failed'}) : refresh.promise) as T;
  }}, () => true, assert.fail);
  try {
    await source.load();const task = source.retry();assert.equal(source.retry(), task);assert.equal(source.getSnapshot().retrying, true);
    source.change('remote.secure', false);await turn();assert.equal(calls, 2);assert.equal(source.rowState(row('remote.secure')).disabled, true);
    retry.resolve(status());await turn();source.change('remote.secure', false);assert.equal(calls, 3);
    refresh.resolve(status());await task;assert.equal(source.getSnapshot().retrying, false);assert.equal(source.getSnapshot().draft!.enabled, true);
  } finally {source.dispose();}
});

test('retirement aborts a retry and rejects even when the transport ignores its signal', async () => {
  const pending = held<CertificateStatus>();let calls = 0;let signal: AbortSignal | undefined;
  const source = new CertificateFormSource({async request<T>(_path: string, _method?: string, _body?: unknown, sent?: AbortSignal) {
    calls++;signal = sent;return await (calls === 1 ? status() : pending.promise) as T;
  }}, () => true, assert.fail);
  await source.load();const retry = source.retry();await turn();source.dispose();assert.equal(signal?.aborted, true);
  await assert.rejects(retry, {name: 'AbortError'});pending.resolve(status());await turn();assert.equal(calls, 2);
});

test('certificate reads time out and a fresh deliberate load may retry', async t => {
  let calls = 0;const source = new CertificateFormSource({request: async <T>() => {calls++;return new Promise<T>(() => {});}}, () => true, assert.fail);
  try {
    const loading = source.load();assert.equal(source.load(), loading);await Promise.resolve();t.mock.timers.tick(15000);await loading;
    assert.ok(source.getSnapshot().loadError);assert.equal(source.getSnapshot().loading, false);
    const retry = source.load();await Promise.resolve();t.mock.timers.tick(15000);await retry;assert.equal(calls, 2);
  } finally {source.dispose();}
});
