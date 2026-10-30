// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import * as card from '../../../packages/client-core/src/invitation-card';
import * as issuer from '../../../packages/client-core/src/invitation-short-code-issuer';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.footer)] : [];
const texts = (node: any) => nodes(node).filter(item => item.type === 'Text').map(item => [item.props.children].flat().join(''));
const button = (tree: any, testID: string) => nodes(tree).find(node => node.type === 'Button' && node.props.testID === testID);
const t = defaultI18n.t;
const LINK = 'https://demo.example/join#code=' + 'c'.repeat(43);
// The card's wordmark is a bundled image; the harness has no asset loader.
const previousRequire = Object.getOwnPropertyDescriptor(globalThis, 'require');
test.before(() => {Object.defineProperty(globalThis, 'require', {configurable: true, value: (path: string) => {assert.equal(path, '../../../../assets/wordmark.png'); return 'wordmark';}});});
test.after(() => {if (previousRequire) Object.defineProperty(globalThis, 'require', previousRequire); else delete (globalThis as {require?: unknown}).require;});

async function mount(ready: 'created' | 'shared', options: {save?: boolean} = {}) {
  const calls: string[] = [], shared: unknown[] = [], copied: string[] = [], saved: string[] = [];
  const invitation = {id: 'inv', revision: 1, role: 'member', state: 'pending', createdAt: '2026-10-28T00:00:00Z', expiresAt: '2099-01-01T00:00:00Z', allowedLibraries: ['films'],
    notes: 'Nana’s iPad, private', access: {maxStreamsMode: 'inherit', maxStreams: 0, allowRemote: true, accessTokens: true, maxContentRating: ''}};
  const opening = {invitation, ready};
  let ops = 0;
  const snapshot = {invitationOpening: opening, libraries: [{id: 'films', name: 'Movies'}], busy: false};
  const source = {
    current: () => true, getSnapshot: () => snapshot, subscribe: () => () => {}, closeInvitation: () => calls.push('close'),
    loadLibraries: async () => {}, newOperationId: () => `op-${++ops}`,
    invitationShareLink: async () => {calls.push('link'); return LINK;},
    invitationShortCode: async (_: unknown, operationId: string) => {calls.push('code:' + operationId); return {serverId: 'srv', expiresAt: '2099-01-01T00:00:00Z', shortCode: '7QK29MXPABCD1234'};},
  };
  const {InvitationReadySheet} = await screenModule(new URL('../src-next/screens/settings-live/server/InvitationReadySheet.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Image: 'Image', Text: 'Text', View: 'View', Share: {share: async (value: unknown) => {shared.push(value);}}},
    'react-native-qrcode-svg': {default: 'QR'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', Path: 'Path', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop', Text: 'SvgText'},
    '../../../../../../packages/client-core/src/invitation-card': card,
    '../../../../../../packages/client-core/src/invitation-short-code-issuer': issuer,
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/controls': {Button: 'Button'},
    '../../../kit/qr-matrix': {qrMatrix: () => [[true, false], [false, true]]},
    '../../../kit/overlays': {BottomSheet: 'Sheet'},
    '../../../kit/states': {StateView: 'StateView'},
    '../../../kit/theme': {useTheme: () => createTheme({isTV: false, width: 390})},
    '../../../platform/clipboard': {copyText: async (value: string) => {copied.push(value);}},
    '../../../platform/invitation-picture-save': {invitationPictureSavePort: () => options.save ? async (png: string) => {saved.push(png); return 'saved';} : undefined},
  });
  const runtime = {text: {t}, error: (error: unknown) => ({title: String(error)})};
  let tree: any;
  const host = createFabricHookHost(() => {tree = InvitationReadySheet({source, opening, runtime, serverName: 'Portico demo', signIn: 'local'});});
  await act(() => host.mount());
  await act(async () => {await new Promise(resolve => setTimeout(resolve, 0));});
  return {calls, shared, copied, saved, tree: () => tree, host};
}

test('a new invitation\'s card shows the server, what they get, the QR of the long link and its one short code, never the notes', async () => {
  const app = await mount('created');
  try {
    assert.deepEqual(app.calls.filter(call => call !== 'close'), ['link', 'code:op-1'], 'the link is read and one code is issued with one operation');
    const shown = texts(app.tree());
    for (const expected of ['Portico demo', 'Movies', t('accounts.invitation.to'), t('accounts.invitation.scanHelp'), t('accounts.invitation.orEnterCode'), '7QK2-9MXP-ABCD-1234', LINK, t('accounts.invitation.copyAgainHelp')]) assert.ok(shown.includes(expected), expected);
    assert.equal(shown.some(text => text.includes('private')), false, 'private notes never reach the card');
    assert.equal(nodes(app.tree()).find(node => node.type === 'QR')?.props.value, LINK);
  } finally {await app.host.unmount();}
});

test('Share, Copy link and Save picture act on the link this card read', async () => {
  const app = await mount('created', {save: false});
  try {
    await act(() => button(app.tree(), 'invitation-share').props.onPress());
    await act(() => button(app.tree(), 'invitation-copy-link').props.onPress());
    assert.deepEqual(app.shared, [{message: LINK}]);
    assert.deepEqual(app.copied, [LINK]);
    assert.equal(button(app.tree(), 'invitation-save-picture'), undefined, 'no Save picture where the app has nowhere to save');
  } finally {await app.host.unmount();}
});

test('a waiting invitation opened to share issues no code until asked, and New code is a fresh operation', async () => {
  const app = await mount('shared');
  try {
    assert.deepEqual(app.calls, ['link']);
    assert.ok(texts(app.tree()).includes(t('web.access.shortCodeHelp')));
    await act(() => button(app.tree(), 'invitation-new-code').props.onPress());
    await act(async () => {await new Promise(resolve => setTimeout(resolve, 0));});
    await act(() => button(app.tree(), 'invitation-new-code').props.onPress());
    await act(async () => {await new Promise(resolve => setTimeout(resolve, 0));});
    assert.deepEqual(app.calls.filter(call => call.startsWith('code:')), ['code:op-1', 'code:op-2']);
  } finally {await app.host.unmount();}
});
