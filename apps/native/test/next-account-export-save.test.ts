// The phone's OS save for the Portico Account export (#3431): the existing JSON-only saver under the `account` kind,
// which takes only that export document and nothing else.
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

async function port(saved: unknown[][]) {
  const sink = {saveDiagnostics: async (...args: unknown[]) => {saved.push(args); return 'saved';}, cancelDiagnostics: () => {}};
  const {diagnosticsSavePort} = await screenModule(new URL('../src-next/platform/diagnostics-save.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoHighlightsSave: sink}, Platform: {OS: 'ios', isTV: false}},
    '../../../../packages/client-core/src/route-identity': {routeBase64: () => 'a'.repeat(32)},
    './server-connections': {nativeRouteCrypto: {random: (n: number) => new Uint8Array(n)}},
  });
  return diagnosticsSavePort();
}

test('the account export is saved under the account kind as its exact JSON', async () => {
  const saved: unknown[][] = [], save = await port(saved);
  const document = {format: 'portico.account-export.v2', account: {id: 'acct_1'}, profiles: []};
  assert.equal(await save('account', document, new AbortController().signal, () => true), 'saved');
  assert.equal(saved.length, 1);
  assert.equal(saved[0]![1], 'account');
  assert.deepEqual(JSON.parse(saved[0]![2] as string), document);
});

test('the account kind refuses anything that is not that export, before native custody', async () => {
  for (const document of [{id: 'report_1', body: 'x'}, {format: 'portico.account-export.v1', account: {id: 'a'}}, {format: 'portico.account-export.v2'},
    {format: 'portico.account-export.v2', account: {}}, {format: 'portico.account-export.v2', account: {id: ''}}]) {
    const saved: unknown[][] = [], save = await port(saved);
    await assert.rejects(save('account', document as never, new AbortController().signal, () => true), /invalid_diagnostics_document/, JSON.stringify(document));
    assert.equal(saved.length, 0);
  }
});

test('the diagnostics kinds keep their own rules', async () => {
  const saved: unknown[][] = [], save = await port(saved);
  await assert.rejects(save('report', {format: 'portico.account-export.v2', account: {id: 'a'}} as never, new AbortController().signal, () => true), /invalid_diagnostics_document/);
  assert.equal(await save('report', {id: 'report_1', body: 'text'} as never, new AbortController().signal, () => true), 'saved');
});
