import assert from 'node:assert/strict';
import test from 'node:test';
import type {CredentialSessionService} from '../../../packages/client-core/src';
import {NativeModules} from './react-native-stub.mjs';
import {nativeCertificatePlatform, nativeProofTLSLeaf, nativeHostedSource} from '../src-next/platform/server-connections';

const origin = 'https://synthetic-identity.test', leaf = 'A'.repeat(43), other = 'B'.repeat(43);
const path = origin + '/v1/networking/identity-proof';
const body = JSON.stringify({baseUrl: origin, nonce: 'A'.repeat(43)});
const code = (expected: string) => (error: unknown) => !!error && typeof error === 'object' && 'code' in error && error.code === expected;

test('native TLS metadata belongs only to the exact proof response, never a header or a response copy', async t => {
  const network = NativeModules.PorticoNetwork as Record<string, unknown>, before = network.proofRequest;
  t.after(() => {network.proofRequest = before;});
  const calls: unknown[][] = [];
  network.proofRequest = async (...args: unknown[]) => {calls.push(args); return {status: 200, body: '{}', headers: {'TLS-Leaf-Fingerprint': other}, tlsLeafFingerprint: leaf};};
  const fetcher = nativeCertificatePlatform.proofFetcher(origin, leaf);
  const response = await fetcher(path, {method: 'POST', body});
  assert.deepEqual(calls, [[origin, leaf, body]]);
  assert.equal(nativeProofTLSLeaf(response), leaf);
  assert.equal(response.headers.get('TLS-Leaf-Fingerprint'), other);
  assert.equal(nativeProofTLSLeaf(response.clone()), undefined);
  assert.equal(nativeProofTLSLeaf(new Response('{}', {headers: {'TLS-Leaf-Fingerprint': leaf}})), undefined);
  assert.equal(nativeCertificatePlatform.observedTLSLeaf?.(response), leaf);
});

test('missing or mismatched native leaf metadata cannot use an HTTP header as proof', async t => {
  const network = NativeModules.PorticoNetwork as Record<string, unknown>, before = network.proofRequest;
  t.after(() => {network.proofRequest = before;});
  for (const tlsLeafFingerprint of [undefined, other]) {
    network.proofRequest = async () => ({status: 200, body: '{}', headers: {'TLS-Leaf-Fingerprint': leaf}, tlsLeafFingerprint});
    await assert.rejects(nativeCertificatePlatform.proofFetcher(origin, leaf)(path, {method: 'POST', body}), code('certificate_unverified'));
  }
});

test('cancelled and non-proof requests perform no native call; cancellation retires a late proof response', async t => {
  const network = NativeModules.PorticoNetwork as Record<string, unknown>, before = network.proofRequest;
  t.after(() => {network.proofRequest = before;});
  let calls = 0;
  network.proofRequest = async () => {calls++; throw new Error('unexpected native call');};
  const fetcher = nativeCertificatePlatform.proofFetcher(origin, leaf), abort = new AbortController(); abort.abort();
  for (const [url, options] of [[path, {method: 'POST', body, signal: abort.signal}], [origin + '/v1/system', {method: 'GET'}], [path, {method: 'GET'}], ['https://other.test/v1/networking/identity-proof', {method: 'POST', body}]] as const)
    await assert.rejects(fetcher(url, options), code('cancelled'));
  assert.equal(calls, 0);
  let finish!: (value: unknown) => void;
  network.proofRequest = () => {calls++; return new Promise(resolve => {finish = resolve;});};
  const running = new AbortController(), pending = fetcher(path, {method: 'POST', body, signal: running.signal});
  running.abort(); finish({status: 200, body: '{}', headers: {}, tlsLeafFingerprint: leaf});
  await assert.rejects(pending, code('cancelled'));
  assert.equal(calls, 1);
});

test('a Portico route source opens its captured account only when requested and rejects a different account before sending its token', async t => {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>, before = runtime.networkRequest;
  t.after(() => {runtime.networkRequest = before;});
  let acquired = 0, synchronized = 0, requested = 0, accountId = 'captured-account';
  const snapshot = () => ({account: {id: accountId}, familyId: 'captured-family', accessToken: 'synthetic-hosted-access'});
  const credentials = {subscribe: () => () => {}, getSnapshot: () => ({session: snapshot()}),
    synchronize: async () => {synchronized++;}, accessSession: async () => snapshot()} as unknown as CredentialSessionService;
  runtime.networkRequest = async (_id: string, url: string, _method: string, headers: Record<string, string>) => {
    requested++; assert.equal(url, 'https://web.getportico.tv/v1/servers/synthetic/routes');
    assert.equal(new Headers(headers).get('Authorization'), 'Bearer synthetic-hosted-access');
    return {status: 200, headers: {'Content-Type': 'application/json'}, body: '{}'};
  };
  const source = nativeHostedSource('https://web.getportico.tv', () => {acquired++; return credentials;}, 'captured-account');
  assert.equal(acquired, 0); assert.equal(synchronized, 0); assert.equal(requested, 0);
  const cancelled = new AbortController(); cancelled.abort();
  await assert.rejects(source.request('/v1/servers/synthetic/routes', cancelled.signal), code('cancelled'));
  assert.equal(acquired, 0);
  await source.request('/v1/servers/synthetic/routes', new AbortController().signal);
  assert.equal(acquired, 1); assert.equal(synchronized, 2); assert.equal(requested, 1);
  accountId = 'different-account';
  await assert.rejects(source.request('/v1/servers/synthetic/routes', new AbortController().signal), code('account_changed'));
  assert.equal(requested, 1);
});
