import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {readFileSync} from 'node:fs';
import {NativeModules} from './react-native-stub.mjs';
import {nativeNetworkFetch} from '../src-next/platform/server-connections.ts';
import {screenModule} from './helpers/screen-harness.mjs';

// Exercise RN's actual descriptor production, including its Blob and string coercion behavior.
const require = createRequire(import.meta.url);
const {transformSync, parseSync} = require('@babel/core');
const formDataSource = readFileSync(require.resolve('react-native/Libraries/Network/FormData'), 'utf8');
const compiled = transformSync(formDataSource, {babelrc: false, configFile: false,
  plugins: ['@babel/plugin-transform-flow-strip-types', '@babel/plugin-transform-modules-commonjs']}).code;
const module = {exports: {} as {default: typeof FormData & {prototype: FormData & {getParts: () => unknown}}}};
new Function('module', 'exports', compiled)(module, module.exports);
const RNFormData = module.exports.default;
const url = 'https://server.test/v1/items/item_1/subtitles';
const metadata = '{"language":"en","scope":"personal"}';
const file = {uri: 'file:///private/portico-subtitle-uploads/id/subtitle.idx', name: 'subtitle.idx', type: 'application/octet-stream'};
const companion = {...file, uri: 'file:///private/portico-subtitle-uploads/id/subtitle.sub', name: 'subtitle.sub'};
const cancelled = (error: {code?: string}) => error.code === 'cancelled';
function install(t: TestContext) {
  const original = globalThis.FormData;
  globalThis.FormData = RNFormData;
  t.after(() => {globalThis.FormData = original;});
  t.mock.method(NativeModules.PorticoRuntime, 'playbackRequestId', () => 'subtitle-request');
  t.mock.method(NativeModules.PorticoRuntime, 'cancelQueueRequest', () => {});
  const calls: unknown[][] = [], strings: unknown[][] = [];
  // The new method is absent from the generic stub until this test installs it.
  const before = NativeModules.PorticoRuntime.networkUploadRequest;
  NativeModules.PorticoRuntime.networkUploadRequest = async (...args: unknown[]) => {calls.push(args); return {status: 201, headers: {'x-upload': 'saved'}, body: '{"saved":true}'};};
  t.after(() => {if (before === undefined) delete NativeModules.PorticoRuntime.networkUploadRequest; else NativeModules.PorticoRuntime.networkUploadRequest = before;});
  t.mock.method(NativeModules.PorticoRuntime, 'networkRequest', async (...args: unknown[]) => {strings.push(args); return {status: 204, headers: {}, body: ''};});
  return {calls, strings};
}
function body(withCompanion = false) {
  const form = new FormData();
  form.append('metadata', metadata); form.append('file', file as unknown as Blob);
  if (withCompanion) form.append('companion', companion as unknown as Blob);
  return form;
}

test('RN subtitle FormData bridges only descriptors with exact arguments and shared response handling', async t => {
  const {calls, strings} = install(t);
  for (const paired of [false, true]) {
    const method = paired ? 'PUT' : 'POST', target = paired ? url + '/subtitle_1' : url;
    const response = await nativeNetworkFetch(target, {method, headers: {Accept: 'application/json', Authorization: 'Bearer synthetic'}, body: body(paired)});
    assert.deepEqual(calls.at(-1), ['subtitle-request', target, method, {accept: 'application/json', authorization: 'Bearer synthetic'}, metadata, file.uri, file.name, paired ? companion.uri : null, paired ? companion.name : null]);
    assert.equal(response.status, 201); assert.equal(response.headers.get('x-upload'), 'saved');
    assert.deepEqual(await response.json(), {saved: true});
  }
  assert.equal(strings.length, 0);
});

test('extra parts, wrong order, Blob, non-file descriptors, arbitrary objects and broken getParts stay refused', async t => {
  const {calls, strings} = install(t);
  const extra = body(); extra.append('extra', 'no');
  const reversed = new FormData(); reversed.append('file', file as unknown as Blob); reversed.append('metadata', metadata);
  const blob = new FormData(); blob.append('metadata', metadata); blob.append('file', new Blob(['not a staged file']));
  const text = new FormData(); text.append('metadata', metadata); text.append('file', 'not a file');
  const missingName = new FormData(); missingName.append('metadata', metadata); missingName.append('file', {uri: file.uri, type: file.type} as unknown as Blob);
  const remote = new FormData(); remote.append('metadata', metadata); remote.append('file', {...file, uri: 'https://other.test/subtitle'} as unknown as Blob);
  const badCompanion = body(); badCompanion.append('companion', 'not a file');
  const objectMetadata = new FormData(); objectMetadata.append('metadata', file as unknown as Blob); objectMetadata.append('file', file as unknown as Blob);
  for (const value of [extra, reversed, blob, text, missingName, remote, badCompanion, objectMetadata, new Blob(['raw']), {getParts: () => []}, new Uint8Array([1])])
    await assert.rejects(nativeNetworkFetch(url, {method: 'POST', body: value as BodyInit}), cancelled);
  const getParts = RNFormData.prototype.getParts;
  try {
    for (const read of [() => {throw Error('broken');}, () => null, () => [null, null]]) {
      RNFormData.prototype.getParts = read;
      await assert.rejects(nativeNetworkFetch(url, {method: 'POST', body: body()}), cancelled);
    }
  } finally {RNFormData.prototype.getParts = getParts;}
  assert.deepEqual(calls, []); assert.deepEqual(strings, []);
});

test('string bodies still use networkRequest; aborts share the same request ID and reject late upload responses', async t => {
  const {calls, strings} = install(t);
  const response = await nativeNetworkFetch(url, {method: 'POST', body: '{}'});
  assert.equal(response.status, 204); assert.equal(response.body, null);
  assert.deepEqual(strings, [['subtitle-request', url, 'POST', {}, '{}']]);
  const already = new AbortController(); already.abort();
  await assert.rejects(nativeNetworkFetch(url, {method: 'POST', body: body(), signal: already.signal}), cancelled);
  assert.deepEqual(calls, []);
  let finish!: (value: unknown) => void;
  NativeModules.PorticoRuntime.networkUploadRequest = () => new Promise(resolve => {finish = resolve;});
  const cancelledIds: string[] = [];
  NativeModules.PorticoRuntime.cancelQueueRequest = (id: string) => cancelledIds.push(id);
  const controller = new AbortController();
  const pending = nativeNetworkFetch(url, {method: 'POST', body: body(true), signal: controller.signal});
  controller.abort(); finish({status: 201, headers: {}, body: '{}'});
  await assert.rejects(pending, cancelled); assert.deepEqual(cancelledIds, ['subtitle-request']);
});

test('certificate settlement retries the same RN descriptors once through networkUploadRequest', async t => {
  install(t);
  // Keep the actual transport/retry source, replacing only the certificate decision boundary.
  const source = new URL('../src-next/platform/server-connections.ts', import.meta.url);
  const parsed = parseSync(readFileSync(source, 'utf8'), {babelrc: false, configFile: false, parserOpts: {plugins: ['typescript']}});
  const imports: string[] = parsed.program.body.filter((node: {type: string}) => node.type === 'ImportDeclaration').map((node: {source: {value: string}}) => node.source.value);
  const boundaries: Record<string, unknown> = {react: {default: {}}};
  await Promise.all(imports.map(async name => {
    boundaries[name] = name === 'react-native' ? await import('./react-native-stub.mjs') : await import(new URL(name.endsWith('.ts') ? name : name + '.ts', source).href);
  }));
  const settlements: unknown[][] = [];
  boundaries['./certificate-trust'] = {...boundaries['./certificate-trust'] as object,
    settleCertificate: async (...args: unknown[]) => {settlements.push(args);}};
  // The test stub has no certificate monitor; this run supplies its one refusal read.
  const network = NativeModules.PorticoNetwork as typeof NativeModules.PorticoNetwork & {refusedCertificate?: () => Promise<unknown>};
  const before = network.refusedCertificate;
  network.refusedCertificate = async () => undefined;
  t.after(() => {if (before === undefined) delete network.refusedCertificate; else network.refusedCertificate = before;});
  const calls: unknown[][] = [];
  NativeModules.PorticoRuntime.networkUploadRequest = async (...args: unknown[]) => {
    calls.push(args);
    if (calls.length === 1) throw Object.assign(Error('Untrusted'), {code: 'certificate_untrusted'});
    return {status: 204, headers: {}, body: ''};
  };
  const transport = await screenModule(source, boundaries);
  const response = await transport.nativeNetworkFetch(url, {method: 'POST', body: body(true)});
  assert.equal(response.status, 204); assert.equal(response.body, null);
  assert.equal(settlements.length, 1); assert.equal(settlements[0]![0], 'https://server.test');
  assert.equal(calls.length, 2); assert.deepEqual(calls[0], calls[1]);
  assert.deepEqual(calls[1]!.slice(4), [metadata, file.uri, file.name, companion.uri, companion.name]);
});
