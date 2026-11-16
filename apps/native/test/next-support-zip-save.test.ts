import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// The rebuilt server's support export is a ZIP; without this save the owner's Export does nothing on a phone.
// React Native reads a Blob through FileReader; Node has none, so a faithful stand-in reads its bytes.
(globalThis as any).FileReader ??= class {
  result: string | null = null; onload?: () => void; onerror?: () => void;
  readAsDataURL(blob: Blob) {void blob.arrayBuffer().then(bytes => {this.result = `data:${blob.type};base64,${Buffer.from(bytes).toString('base64')}`; this.onload?.();}, () => this.onerror?.());}
};
const zip = (bytes: number[]) => new Blob([new Uint8Array([...bytes, ...new Array(30).fill(0)])], {type: 'application/zip'});

async function port(sink: Record<string, unknown>) {
  const module = await screenModule(new URL('../src-next/platform/diagnostics-save.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoHighlightsSave: sink}, Platform: {OS: 'android', isTV: false}},
    '../../../../packages/client-core/src/route-identity': {routeBase64: () => 'B'.repeat(32)},
    './server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24)}},
  });
  return module.supportZipSavePort();
}

test('only a ZIP is handed to the save sheet, as base64 with today\'s date, and a retired page never publishes', async () => {
  const calls: unknown[][] = [];
  const save = (await port({saveSupportZip: async (...args: unknown[]) => {calls.push(args); return 'saved';}, cancelDiagnostics: () => {}}))!;
  await assert.rejects(save(zip([0x25, 0x50, 0x44, 0x46]), new AbortController().signal, () => true), /invalid_support_export/, 'a PDF is not a ZIP');
  const archive = zip([0x50, 0x4b, 0x03, 0x04]);
  assert.equal(await save(archive, new AbortController().signal, () => true), 'saved');
  assert.equal(calls.length, 1);
  assert.equal(calls[0]![0], 'B'.repeat(32));
  assert.equal(calls[0]![1], Buffer.from(await archive.arrayBuffer()).toString('base64'));
  assert.match(String(calls[0]![2]), /^\d{4}-\d{2}-\d{2}$/);
  const retired = new AbortController(); retired.abort();
  await assert.rejects(save(archive, retired.signal, () => true), {name: 'AbortError'});
  assert.equal(calls.length, 1);
});
