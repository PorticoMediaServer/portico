// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as React from 'react';
import * as joinLink from '../../../packages/client-core/src/join-link.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

const react = {...React, default: React};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.back)] : [];
const t = defaultI18n.t;

test('the empty directory joins only from the form as drawn: a join link fills and locks the address, and busy or a stale owner joins nothing', async () => {
  const joins: string[][] = [];
  let busy = false, current = true;
  const hook = await screenModule(new URL('../src-next/pages/shared/accounts/useNoServersJoin.ts', import.meta.url), {react,
    '../../../../../../packages/client-core/src/join-link': joinLink, '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../platform/qr-scanner': {qrScannerAvailable: () => false, scanQRCode: async () => undefined}});
  const {NoServersPage} = await screenModule(new URL('../src-next/pages/touch/accounts/NoServersPage.tsx', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/controls': {Button: 'Button', FormError: 'FormError', TextField: 'Field'},
    '../../../kit/accounts/NoServersParts': {NoServersFrame: 'Frame', NoServersCard: 'Card', NoServersButtons: 'Buttons', NoServersNote: 'Note'},
    '../../shared/accounts/useNoServersJoin': hook});
  let tree: any;
  const host = createFabricHookHost(() => {tree = NoServersPage({busy, isCurrent: () => current, onJoin: (invitation: string, address: string) => joins.push([invitation, address]),
    onFind() {}, onRefresh() {}, onShare() {}, onSignOut() {}});});
  await host.mount(); await host.flush();
  const render = async () => {await host.rerender(); await host.flush();};
  const field = (label: string) => nodes(tree).find(node => node.type === 'Field' && node.props.label === label);
  const button = (label: string) => nodes(tree).find(node => node.type === 'Button' && node.props.children === label);
  const invitation = () => field(t('services.start.invitation')), address = () => field(t('accounts.signIn.address'));
  assert.equal(button(t('action.continue')).props.disabled, true, 'nothing to join yet');
  address().props.onChangeText('https://den.example:32500'); await render();
  invitation().props.onChangeText('ABCD-EFGH'); await render();
  button(t('action.continue')).props.onPress();
  assert.deepEqual(joins, [['ABCD-EFGH', 'https://den.example:32500']]);
  button(t('action.continue')).props.onPress();
  assert.equal(joins.length, 1, 'a Continue already used retires the form as drawn');
  await render();
  const code = 'A'.repeat(40), link = `portico://join#code=${code}&server=${encodeURIComponent('https://cabin.example:32500')}`;
  invitation().props.onChangeText(link); await render();
  assert.equal(address().props.value, 'https://cabin.example:32500', 'the link names its server');
  assert.equal(address().props.editable, false, 'and the address is the link\'s');
  busy = true; await render();
  button(t('action.continue')).props.onPress();
  current = false; busy = false; await render();
  button(t('action.continue')).props.onPress();
  assert.equal(joins.length, 1, 'busy or a stale owner joins nothing');
  await host.unmount();
});
