// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const react = {...React, default: React};

/** The TV no-server page (connect 35, 36, 38) on its hook, with the kit's parts as stubs. */
async function page() {
  const entries: any[] = [], backs = new Set<() => boolean>();
  const hook = await screenModule(new URL('../src-next/pages/shared/accounts/useTVNoServers.ts', import.meta.url), {react,
    '../../../tv/auth/entry-revision': {useTVBusyEntryRevision: () => 0},
    '../../../kit/navigation/back': {registerBack: (fn: () => boolean) => {backs.add(fn); return () => backs.delete(fn);}},
    '../../../screens/destinations/useEntryFocus': {useDestinationEntryFocus: (...args: any[]) => entries.push(args)}});
  const module = await screenModule(new URL('../src-next/pages/tv/accounts/NoServersPage.tsx', import.meta.url), {react,
    'react-native-qrcode-svg': {default: 'QR'}, '@portico/design': {tvAccountSpec: {wide: {door: {code: 220, codePad: 16}, form: {keyColumns: 9, width: 860}}}},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/accounts/TVNoServerParts': {TVAccountNotice: 'Notice', TVAccountWidePage: 'Page', TVDoor: 'Door', TVDoors: 'Doors', TVInvitationForm: 'Form'},
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField'}, '../../../kit/focus': {FocusProvider: 'FocusProvider'},
    '../../../screens/search/TVKeyboard': {NativeTVKeyboard: 'Keyboard'}, '../../shared/accounts/useTVNoServers': hook});
  return {module, entries, backs};
}

test('the doors ask for Join once per drawing, never on a busy turn, and again after the code entry closes; Set up runs nothing', async () => {
  const {module, entries, backs} = await page();
  let tree: any, busy = false; const isCurrent = () => true;
  const host = createFabricHookHost(() => {tree = module.NoServersPage({busy, isCurrent, onJoin() {}, onFind() {}, onRefresh() {}, onSignOut() {}, onBack() {}});});
  await host.mount();
  try {
    const doorEntry = () => entries.filter(args => args[1]?.regionId === 'accounts.empty.door').at(-1);
    assert.deepEqual(doorEntry(), [{visible: true, intent: 'enter', revision: 0}, {regionId: 'accounts.empty.door', childId: 'join'}]);
    busy = true; await act(() => host.rerender()); busy = false; await act(() => host.rerender());
    assert.equal(doorEntry()[0].revision, 0, 'a busy turn does not ask again');
    const door = (id: string) => nodes(tree).find(node => node.type === 'Door' && node.props.focusId === id);
    assert.equal(door('setup').props.onPress, undefined, 'Set up only shows its code');
    await act(() => door('join').props.onPress());
    assert.ok(nodes(tree).some(node => node.type === 'Form'), 'Join opens the code entry');
    assert.equal(doorEntry()[0].visible, false);
    await act(() => {[...backs][0]!();});
    assert.ok(nodes(tree).some(node => node.type === 'Doors'), 'Back closes the code entry first');
    assert.deepEqual(doorEntry()[0], {visible: true, intent: 'enter', revision: 1}, 'the doors drawn again ask for Join again');
  } finally {await host.unmount();}
});

test('Join sends the typed code and address; busy and stale callers send nothing; Back from the doors leaves', async () => {
  const {module} = await page();
  let tree: any, busy = false, current = true; const calls: unknown[] = [], isCurrent = () => current;
  const host = createFabricHookHost(() => {tree = module.NoServersPage({busy, isCurrent, onJoin: (...value: string[]) => calls.push(value),
    onFind: () => calls.push('find'), onRefresh() {}, onSignOut: () => calls.push('sign-out'), onBack: () => calls.push('back')});});
  await host.mount();
  try {
    // The doors' foot (Find, Sign out) is the page frame's own prop.
    const all = () => {const found = nodes(tree), frame = found.find(node => node.type === 'Page'); return [...found, ...nodes(frame?.props.foot)];};
    const find = (type: string, id: string) => all().find(node => node.type === type && node.props.focusId === id);
    find('Button', 'find').props.onPress(); find('Button', 'sign-out').props.onPress();
    await act(() => find('Door', 'join').props.onPress());
    const form = () => nodes(tree).find(node => node.type === 'Form');
    const field = (id: string) => nodes(form().props.fields).find((node: any) => node.type === 'TextField' && node.props.focusId === id);
    await act(() => {field('address').props.onChangeText('https://original.example'); field('invitation').props.onChangeText('ORIGINAL-CODE');});
    const action = (id: string) => nodes(form().props.actions).find((node: any) => node.props?.focusId === id);
    busy = true; await act(() => host.rerender());
    assert.equal(action('join').props.disabled, true);
    action('join').props.onPress(); assert.deepEqual(calls, ['find', 'sign-out'], 'busy sends nothing');
    busy = false; await act(() => host.rerender());
    await act(() => action('join').props.onPress());
    assert.deepEqual(calls, ['find', 'sign-out', ['ORIGINAL-CODE', 'https://original.example']]);
    current = false; await act(() => action('back').props.onPress()); assert.ok(form(), 'a stale caller cannot close it');
    current = true; await act(() => action('back').props.onPress());
    find('Button', 'find').props.onPress(); assert.equal(calls.at(-1), 'find');
    assert.equal(calls.includes('back'), false, 'Back from the code entry stays on the page');
  } finally {await host.unmount();}
});
void defaultI18n;
