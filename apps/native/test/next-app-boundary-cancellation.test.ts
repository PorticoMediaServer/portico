import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

// Policy (P2-74 E-tv-21): an expected cancellation (AbortError) must never reach the crash page.
let caught = 0;
const reported: string[] = [];
const {AppErrorBoundary} = await screenModule(new URL('../src-next/shell/AppErrorBoundary.tsx', import.meta.url), {
  react: {...React, default: React},
  'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}, Platform: {OS: 'android'}, Text: 'Text'},
  '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
  '../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: ''})},
  '../kit/states': {StateLayout: 'StateLayout'},
  '../kit/theme': {useTheme: () => ({isTV: false, type: {caption: {}}, colors: {danger: 'red'}})},
  '../platform/clipboard': {copyText: async () => {}},
  '../platform/tv-menu': {captureTVMenuKey: () => () => {}},
  '../platform/home-action-diagnostics': {homeActionDiagnostics: {error() {}, cancellationRemount(error: {name: string}, stack: string) {reported.push(`${error.name} ${stack.trim().split('\n')[0]}`);}, lifetime(name: string) {if (name === 'boundary-caught') caught++;}}},
});
const types = (nodes: any[]): string[] => nodes.flatMap(node => [node.type, ...types(node.children ?? [])]);
const abort = () => Object.assign(new Error('native_shell_cancelled'), {name: 'AbortError'});

async function mount(failures: Error[] | (() => Error | undefined)) {
  const host = await fabricHost();
  const Child = () => {const next = typeof failures === 'function' ? failures() : failures.shift(); if (next) throw next; return React.createElement('Text', null, 'ok');};
  try {await host.render(React.createElement(AppErrorBoundary, null, React.createElement(Child)));} catch {/* caught render errors are reported to the host */}
  try {await host.settle();} catch {/* as above */}
  return host;
}

test('an AbortError remounts the tree quietly instead of showing the crash page', async () => {
  // The child keeps failing until the boundary itself has caught it once (React's own replays
  // absorb a fixed number of throws), then renders normally.
  caught = 0; reported.length = 0;
  const host = await mount(() => caught === 0 ? abort() : undefined);
  try {
    const drawn = types(host.root);
    assert.ok(drawn.includes('Text'), 'the children are mounted again');
    assert.equal(drawn.includes('StateLayout'), false, 'no crash page for an expected cancellation');
    assert.equal(reported.length, 1, 'the quiet remount is never silent');
    assert.match(reported[0], /^AbortError (in|at) Child\b/);
  } finally {await host.unmount().catch(() => {});}
});

test('a repeating cancellation and any other error still show the crash page', async () => {
  const looping = await mount(() => abort());
  try {assert.ok(types(looping.root).includes('StateLayout'), 'a cancellation loop is a real failure');} finally {await looping.unmount().catch(() => {});}
  reported.length = 0;
  const real = await mount(() => new Error('boom'));
  try {assert.ok(types(real.root).includes('StateLayout')); assert.equal(reported.length, 0, 'only cancellations are reported as quiet remounts');} finally {await real.unmount().catch(() => {});}
});
