// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.footer)] : [];

test('Forget on the servers page asks once with the downloads choice, then forgets only the server it captured', async () => {
  const calls: unknown[][] = [];
  const saved = {connection: {pin: {serverId: 'srv_a', fingerprint: 'fp'}, session: {sessionFamilyId: 'family', viewer: {serverId: 'srv_a'}}}};
  const theme = {isTV: false, colors: {text: '#fff', textSecondary: '#ccc', danger: '#d33', lineStrong: '#678', accentStrong: '#4af', ink: '#071018'}, type: {body: {}, button: {}}};
  const {ServersRoutePage} = await screenModule(new URL('../src-next/pages/shared/accounts/ServersRoutePage.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Platform: {isTV: false, OS: 'android'}, Dimensions: {get: () => ({width: 412, height: 915})}, Text: 'Text', View: 'View'},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string, values?: Record<string, string>) => values?.server ? `${key}:${values.server}` : key}},
    '../../../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({body: error.message})},
    '../../../platform/accounts': {nativeAccounts: {
      snapshot: async () => ({selectedServerId: 'srv_a', servers: [{id: 'srv_a', name: 'Den', signIn: 'local'}]}),
      accountCredentials: () => ({subscribe: () => () => {}, getSnapshot: () => ({}), synchronize: async () => {}}),
      forgetServer: async (value: unknown) => {calls.push(['forget', value]);},
      signOutServer: async (...args: unknown[]) => {calls.push(['sign-out', ...args]);}}},
    '../../../platform/app-session-storage': {nativeAppSessions: {read: async () => ({servers: [saved]}), selected: () => saved}, subscribeNativeAppSessions: () => () => {}},
    '../../../../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({entries: []})}, offlineOwnerKey: () => 'owner'},
    '../../../kit/theme': {useTheme: () => theme}, '../../../kit/navigation/back': {registerBack: () => () => {}},
    '../../../screens/accounts/downloads-entry': {createAccountDownloadsEntry: () => ({dispose() {}})},
    '../../../screens/accounts/account-form-values': {completeTVLinkCode: () => false, formatTVLinkCode: (value: string) => value},
    '../../../screens/accounts/sign-out-downloads': {removeCapturedServerDownloads: async () => {calls.push(['remove-downloads']);}},
    '../../../kit/frames/TVAuthLayout': {TVAuthLayout: 'TVAuthLayout', TVAccountFields: 'TVAccountFields'},
    '../../../kit/states': {StateLayout: 'StateLayout'}, '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'TextField', FormError: 'FormError'},
    '../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../kit/Icon': {Icon: 'Icon'},
    '../../../screens/accounts/AccountEntry': {AccountEntry: 'AccountEntry'}, './TrustedCertificatesPage': {TrustedCertificatesPage: 'Certificates'},
    '../../touch/accounts/ServerChooserPage': {ServerChooserPage: 'Chooser'},
    '../../tv/accounts/ServerChooserPage': {TVServerChooserPage: 'TVChooser', TVServerSettingsPanel: 'TVPanel'},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = ServersRoutePage({onBack() {}, onComplete: () => calls.push(['complete'])});});
  await host.mount(); await host.flush();
  const settle = async () => {for (let index = 0; index < 4; index++) {await host.rerender(); await host.flush();}};
  const find = (predicate: (node: any) => boolean) => nodes(tree).find(predicate);
  find(node => node.type === 'Chooser').props.onForget({id: 'srv_a', familyId: 'family', fingerprint: 'fp', name: 'Den'});
  await settle();
  const sheet = find(node => node.type === 'BottomSheet');
  assert.equal(sheet.props.open, true);
  assert.equal(sheet.props.title, 'connection.identity.forgetTitle:Den');
  assert.ok(find(node => node.props?.forgetBody === 'accounts.servers.forgetBody:Den'), 'Forget says what it does first');
  find(node => node.props?.onToggle).props.onToggle(); await settle();
  assert.equal(find(node => node.props?.onToggle).props.checked, true, 'the selected server offers its downloads');
  find(node => node.props?.onConfirm).props.onConfirm(); await settle();
  assert.deepEqual(calls, [['remove-downloads'], ['forget', {serverId: 'srv_a', familyId: 'family', fingerprint: 'fp'}], ['complete']]);
  await host.unmount();
});
