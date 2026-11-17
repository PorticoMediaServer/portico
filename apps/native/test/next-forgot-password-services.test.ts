import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {CodedError} from '../../../packages/client-core/src/server-messages.ts';

async function fixture() {
  const urls: string[] = []; let live = true, opening: () => Promise<void> = async () => {};
  const {NativePorticoSignIn} = await screenModule(new URL('../src-next/platform/portico-sign-in.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {}, Linking: {openURL: async (url: string) => {urls.push(url); await opening();}}},
    '../../../../packages/client-core/src/server-clock': {}, '../../../../packages/client-core/src/hosted-security': {},
    '../../../../packages/client-core/src/server-messages': {CodedError}, '../../../../packages/client-core/src': {},
    './accounts': {nativeAccounts: {onCancel: () => () => {}, porticoClient: () => {throw new Error('reset must stay in browser');}}},
    './abort-signals': {}, './account-authority': {hostedUrl: 'https://services.example.test'}, './continuation-queue': {},
  });
  return {source: new NativePorticoSignIn(() => live), urls, retire: () => {live = false;}, open: (work: () => Promise<void>) => {opening = work;}};
}

test('Forgot password hands the whole code/new-password flow to the existing hosted page', async () => {
  const f = await fixture();
  await f.source.forgot();
  assert.deepEqual(f.urls, ['https://services.example.test/reset']);
  // No email in the URL, no refresh-token adoption, and no provider callback to invent.
});

test('Forgot password refuses a stale form before opening and after an outstanding browser launch', async () => {
  const f = await fixture();
  await assert.rejects(f.source.forgot(() => false)); assert.equal(f.urls.length, 0);
  f.open(async () => {f.retire();});
  await assert.rejects(f.source.forgot()); assert.equal(f.urls.length, 1);
});

test('Forgot password reports a browser launch failure so the existing form can retry', async () => {
  const f = await fixture(); f.open(async () => {throw new Error('browser unavailable');});
  await assert.rejects(f.source.forgot(), /browser unavailable/);
  f.open(async () => {}); await f.source.forgot(); assert.equal(f.urls.length, 2);
});
