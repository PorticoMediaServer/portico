import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

const PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAwS2OUAAAAABJRU5ErkJggg==';

async function port(platform: {OS: string; isTV: boolean}, sink: Record<string, unknown> | undefined) {
  const module = await screenModule(new URL('../src-next/platform/invitation-picture-save.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: sink ? {PorticoHighlightsSave: sink} : {}, Platform: platform},
    '../../../../packages/client-core/src/route-identity': {routeBase64: () => 'A'.repeat(32)},
    './server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24)}},
  });
  return module.invitationPictureSavePort();
}

test('the invitation picture sink exists only on a phone app with the fixed save method', async () => {
  const sink = {saveInvitationPicture: async () => 'saved', cancelInvitationPicture: () => {}};
  assert.equal(await port({OS: 'ios', isTV: true}, sink), undefined, 'not on TV');
  assert.equal(await port({OS: 'web', isTV: false}, sink), undefined);
  assert.equal(await port({OS: 'android', isTV: false}, {saveDiagnostics: () => {}}), undefined, 'no picture method');
  assert.equal(typeof await port({OS: 'android', isTV: false}, sink), 'function');
});

test('only a PNG is handed over, once, and a retired card never publishes a result', async () => {
  const calls: unknown[][] = [];
  const save = (await port({OS: 'ios', isTV: false}, {saveInvitationPicture: async (...args: unknown[]) => {calls.push(['save', ...args]); return 'saved';}, cancelInvitationPicture: (id: string) => {calls.push(['cancel', id]);}}))!;
  await assert.rejects(save('R0lGODlhAQABAAAAACw=', new AbortController().signal, () => true), /invalid_invitation_picture/, 'a GIF is not a PNG');
  await assert.rejects(save(PNG + '<script>', new AbortController().signal, () => true), /invalid_invitation_picture/);
  assert.equal(await save(PNG, new AbortController().signal, () => true), 'saved');
  assert.deepEqual(calls[0], ['save', 'A'.repeat(32), PNG]);
  const retired = new AbortController(); retired.abort();
  await assert.rejects(save(PNG, retired.signal, () => true), {name: 'AbortError'});
});
