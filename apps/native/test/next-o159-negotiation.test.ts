import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {parseServerSignIn} from '../../../packages/client-core/src/sign-in.ts';
import {negotiateNativeServer, negotiationRefusal} from '../src-next/platform/negotiation.ts';

const system = (extra: Record<string, unknown> = {}) => parseServerSignIn({id: 'server', name: 'Living room', version: '1.0.0', signIn: 'local', move: null,
  setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, ...extra});
const source = (path: string) => readFileSync(new URL(`../src-next/${path}`, import.meta.url), 'utf8');

test('O-159: a pre-release server (no negotiation facts) is refused by name before sign-in', () => {
  let refused: unknown;
  try {negotiateNativeServer(system());} catch (error) {refused = error;}
  assert.deepEqual(negotiationRefusal(refused), {kind: 'server', serverName: 'Living room'});
  // A malformed block is the same refusal, never a degraded session.
  assert.throws(() => negotiateNativeServer(system({apiVersion: '1', apiMajors: [1], capabilities: []})), {code: 'api_level_mismatch'});
});

test('O-159: a 1.0 server is accepted on major 1, and nothing at 1.0 is gated', () => {
  const api = negotiateNativeServer(system({apiVersion: '1.0', apiMajors: [1], capabilities: []}));
  assert.equal(api.major, 1); assert.equal(api.version, '1.0');
  assert.equal(api.can('file-info.windows'), false, 'baseline features are never advertised or checked');
  assert.equal(negotiateNativeServer(system({apiVersion: '1.7', apiMajors: [1], capabilities: ['future.feature']})).can('future.feature'), true);
});

test('O-159: a server that only speaks a newer major asks for a newer app', () => {
  let refused: unknown;
  try {negotiateNativeServer(system({apiVersion: '2.0', apiMajors: [2], capabilities: []}));} catch (error) {refused = error;}
  assert.deepEqual(negotiationRefusal(refused), {kind: 'app', serverName: 'Living room'});
  assert.equal(negotiationRefusal(new Error('other')), undefined);
});

test('O-159 native wiring: every first contact negotiates before credentials, refusals have their page, About names both builds', () => {
  const signIn = source('platform/server-sign-in.ts');
  const inspect = signIn.slice(signIn.indexOf('  inspect(address: string) {'), signIn.indexOf('  prepareSetup() {'));
  assert.match(inspect, /if \(!server\) throw this\.probe\.getSnapshot\(\)\.error \?\? mismatch\(\);\n\s+\/\/[^\n]*\n\s+negotiateNativeServer\(server\);/);
  assert.doesNotMatch(signIn, /assertSameApiLevel/);
  assert.match(source('platform/server-display.ts'), /const server = parseServerSignIn\([^\n]*\n\s+\/\/[^\n]*\n\s+negotiateNativeServer\(server\);/);
  assert.match(source('screens/accounts/AccountEntry.tsx'), /if \(refusal\) return <ServerNeedsUpdate refusal=\{refusal\}/);
  assert.match(source('shell/RestoredShell.tsx'), /if \(refusal\) return withDownloads\(<ServerNeedsUpdate refusal=\{refusal\}/);
  const about = source('screens/settings-live/DeviceAboutPanel.tsx');
  assert.match(about, /testID="settings-app-build" label=\{t\('web\.about\.build'\)\} value=\{BUILD_ID\}/);
  assert.match(about, /testID="settings-server-api" label=\{t\('web\.about\.serverApi'\)\} value=\{serverApi\}/);
});
