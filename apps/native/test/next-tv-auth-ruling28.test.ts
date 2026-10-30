// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {createTVKeyAdapter} from '../src-next/platform/tv-key-adapter';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const theme = createTheme({isTV: true, width: 1920});
const remoteKeys = (accept: (handler: (event: any) => void) => void) => {
  const adapter = createTVKeyAdapter();
  return {useTVKeyEvent: (handler: (event: any) => void) => accept(raw => {const event = adapter.read(raw); if (event) handler(event);})};
};
const inScope = (component: (props: any) => any, props: any) => {
  const scope = component(props);
  assert.equal(scope.type, 'Provider', 'pre-sign-in entry supplies its own focus scope');
  const child = [scope.props.children].flat()[0];
  return child.type(child.props);
};
/** The stage's own real entry revision: its source runs, on the React this case supplies
 * (real hooks under the driver, or the harness stand-in for a single shallow render). */
const entryRevision = (react: unknown) => screenModule(new URL('../src-next/tv/auth/entry-revision.ts', import.meta.url), {react});
const common = async (react: unknown) => ({react, '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/theme': {nativeType, useTheme: () => theme},
  '../../platform/tv-keys': {useTVKeyEvent() {}},
  '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'Field'}, './TVAuthLayout': {TVAuthLayout: 'Layout'},
  '../../screens/destinations/useEntryFocus': {useDestinationEntryFocus() {}}, './entry-revision': await entryRevision(react)});
// The existing caller vectors render the pad directly; give its real entry hooks
// a separate controlled host. Native region/Focusable boundaries remain stubs.
const numericModule = async (_react: unknown) => {
  const h = hooks(), registry = createFocusRegistry();
  const numeric = await screenModule(new URL('../src-next/tv/auth/TVNumericPad.tsx', import.meta.url), {
    ...await common(h.react), 'react-native': {Text: 'Text', View: 'View'},
    '../../kit/focus': {FocusRegion: 'Region', FocusItem: 'Item', useFocusRegistry: () => registry},
    '../../kit/focus/initial-entry': {createInitialRegionEntry}, '../../kit/Icon': {Icon: 'Icon'},
  });
  return {...numeric, TVNumericPad: (props: unknown) => h.render(() => numeric.TVNumericPad(props))};
};

for (const kind of ['portico', 'local'] as const) test(`${kind} credentials use secure platform fields/autofill and only the owner's Submit/Back callbacks`, async () => {
  const focused: unknown[] = [], events: string[] = [];
  const {TVCredentialForm} = await screenModule(new URL('../src-next/tv/auth/TVCredentialForm.tsx', import.meta.url), {
    ...await common({...React, default: React}), 'react-native': {Text: 'Text', View: 'View'},
    '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region',
      useFocusRegistry: () => ({focus: (address: unknown) => focused.push(address)})},
  });
  const props = {kind, serverName: 'Actual local server', username: 'actual-user', password: 'in-memory-only', busy: false,
    usernamePlaceholder: 'auth.email', onUsernameChange() {}, onPasswordChange() {}, onSubmit: () => events.push('submit'), onBack: () => events.push('back')};
  let tree: any, username: any, password: any;
  const root = createFabricHookHost(() => {tree = inScope(TVCredentialForm, props);});
  await act(() => root.mount());
  let fields = nodes(tree).filter(node => typeof node.type === 'function');
  const usernameHost = createFabricHookHost(() => {username = fields[0].type(fields[0].props);});
  const passwordHost = createFabricHookHost(() => {password = fields[1].type(fields[1].props);});
  await act(async () => {await usernameHost.mount(); await passwordHost.mount();});
  try {
    const field = (node: unknown) => nodes(node).find(value => value.type === 'Field');
    assert.equal(field(username).props.focusId, 'username');
    assert.equal(field(username).props.textContentType, 'username');
    assert.equal(field(username).props.autoComplete, 'username');
    assert.equal(field(password).props.focusId, 'password');
    assert.equal(field(password).props.secureTextEntry, true);
    assert.equal(field(password).props.textContentType, 'password');
    assert.equal(field(password).props.autoComplete, 'current-password');
    assert.equal(field(password).props.autoCorrect, false);
    assert.equal(field(password).props.editable, true);
    assert.equal(field(password).props.showSoftInputOnFocus, undefined, 'the platform keyboard is not suppressed');
    assert.ok(nodes(username).some(node => node.type === 'Text' && node.props.children === defaultI18n.t(kind === 'portico' ? 'auth.usernameOrEmail' : 'auth.username')));
    await act(() => {field(username).props.onSubmitEditing(); field(password).props.onSubmitEditing();});
    assert.deepEqual(focused, [{regionId: `tv.auth.${kind}.credentials`, childId: 'password'}, {regionId: `tv.auth.${kind}.credentials`, childId: 'submit'}]);
    const buttons = nodes(tree).filter(node => node.type === 'Button');
    assert.equal(buttons.length, 2, 'no provider/signup/forgot-password controls are added');
    buttons[0].props.onPress(); buttons[1].props.onPress();
    assert.deepEqual(events, ['submit', 'back']);
    props.password = '';
    await act(() => root.rerender());
    const empty = nodes(tree).filter(node => node.type === 'Button');
    empty[0].props.onPress();
    assert.deepEqual(events, ['submit', 'back'], 'an empty password still refuses Submit');
    props.password = 'in-memory-only';
    await act(() => root.rerender());
    assert.equal(tree.props.portico, kind === 'portico', 'only Portico Account renders legal terms');
    assert.equal(tree.props.body, defaultI18n.t(kind === 'local' ? 'auth.direct.lead' : 'auth.accountLead'));
    props.busy = true;
    await act(() => root.rerender());
    fields = nodes(tree).filter(node => typeof node.type === 'function');
    await act(async () => {await usernameHost.rerender(); await passwordHost.rerender();});
    assert.equal(field(password).props.editable, false, 'busy disables the direct field');
    nodes(tree).find(node => node.type === 'Button' && node.props.focusId === 'submit').props.onPress();
    assert.deepEqual(events, ['submit', 'back']);
  } finally {await passwordHost.unmount(); await usernameHost.unmount(); await root.unmount();}
});

test('both actual TV code modes expose the drawn credential route without starting a transaction', async () => {
  const h = hooks(), calls: string[] = [];
  const {TVCodePresenter} = await screenModule(new URL('../src-next/tv/auth/TVCodePresenter.tsx', import.meta.url), {
    ...await common(h.react), 'react-native': {Text: 'Text', View: 'View'}, 'react-native-qrcode-svg': {default: 'QR'}, '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'},
    '../../platform/native-blur': {BlurView: 'Blur', glassRecipe: () => ({nativeBlur: true})},
  });
  for (const mode of ['portico', 'local'] as const) {
    const tree = h.render(() => inScope(TVCodePresenter, {mode, serverName: 'Actual server', body: 'actual owner copy', userCode: 'ACTUAL-CODE',
      verificationUri: 'https://account.example/device', verificationUriComplete: 'https://account.example/device?user_code=ACTUAL-CODE', busy: false,
      onTypeCredentials: () => calls.push(mode), onBack: () => calls.push('back')}));
    const buttons = nodes(tree).filter(node => node.type === 'Button');
    assert.equal(buttons[0].props.children.flat().join(''), defaultI18n.t(mode === 'portico' ? 'accounts.signIn.onTV' : 'quickConnect.usePassword'));
    buttons[0].props.onPress(); if (mode === 'portico') buttons[1].props.onPress();
    else assert.equal(buttons.length, 1, 'local code frame9 has one drawn password button; hardware Back is owned by AccountEntry');
    assert.equal(nodes(tree.props.aside).find(node => node.type === 'QR').props.value, 'https://account.example/device?user_code=ACTUAL-CODE');
    assert.equal(tree.props.portico, mode === 'portico');
  }
  assert.deepEqual(calls, ['portico', 'back', 'local']);
});

test('sixth MFA digit submits once, rejected controlled state can retry, and Play/Pause deletes without a system input', async () => {
  let tree: any, remote: (event: any) => void = () => {};
  const submitted: string[] = [];
  const props = {kind: 'local', code: '', recovery: false, recoveryCode: '', busy: false,
    onCodeChange: (value: string) => {props.code = value;}, onRecoveryCodeChange() {}, onRecoveryChange() {},
    onSubmit: (value: string) => submitted.push(value), onBack() {}};
  const numeric = await numericModule({...React, default: React});
  const {TVVerificationForm} = await screenModule(new URL('../src-next/tv/auth/TVVerificationForm.tsx', import.meta.url), {
    ...await common({...React, default: React}), 'react-native': {Text: 'Text', View: 'View', useTVEventHandler: (fn: typeof remote) => {remote = fn;}},
    '../../platform/tv-keys': remoteKeys(handler => {remote = handler;}),
    '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'}, '../../kit/Icon': {Icon: 'Icon'},
    './TVNumericPad': numeric,
  });
  const host = createFabricHookHost(() => {tree = inScope(TVVerificationForm, props);});
  const press = (digit: string) => nodes(numeric.TVNumericPad(tree.props.aside.props)).find(node => node.props.testID === `tv.auth.local.factor-pad-${digit}`).props.onPress();
  try {
    await act(() => host.mount());
    for (const digit of '123456') await act(async () => {press(digit); await host.rerender();});
    assert.deepEqual(submitted, ['123456']);
    await act(async () => {press('6'); await host.rerender();});
    assert.deepEqual(submitted, ['123456']);
    await act(async () => {props.code = ''; await host.rerender();});
    for (const digit of '123456') await act(async () => {press(digit); await host.rerender();});
    assert.deepEqual(submitted, ['123456', '123456']);
    await act(async () => {remote({eventType: 'playPause', eventKeyAction: 0}); await host.rerender();});
    assert.equal(props.code, '12345');
    remote({eventType: 'playPause', eventKeyAction: 1});
    assert.equal(props.code, '12345', 'key release does not delete twice');
    await act(async () => {remote({eventType: 'playPause', eventKeyAction: 1}); await host.rerender();});
    assert.equal(props.code, '1234', 'a release-only native tap deletes one digit');
    await act(async () => {props.busy = true; await host.rerender(); remote({eventType: 'playPause', eventKeyAction: 0}); press('9');});
    assert.equal(props.code, '1234');
    assert.ok(!nodes(tree).some(node => node.type === 'TextInput'));
  } finally {await host.unmount();}
});

test('recovery uses the authored uppercase/digit/dash keyboard and submits the exact controlled value', async () => {
  const h = hooks(), values: string[] = [], submits: string[] = [];
  const {TVVerificationForm} = await screenModule(new URL('../src-next/tv/auth/TVVerificationForm.tsx', import.meta.url), {
    ...await common(h.react), 'react-native': {Text: 'Text', View: 'View', useTVEventHandler() {}}, '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'}, '../../kit/Icon': {Icon: 'Icon'},
    './TVNumericPad': {TVNumericPad: 'Pad', TVCodeBoxes: 'Boxes'},
  });
  const props = {kind: 'portico', code: '', recovery: true, recoveryCode: 'R7-', busy: false, onCodeChange() {}, onRecoveryChange() {},
    onRecoveryCodeChange: (value: string) => values.push(value), onSubmit: (value: string) => submits.push(value), onBack() {}};
  const tree = h.render(() => inScope(TVVerificationForm, props));
  assert.ok(!nodes(tree).some(node => node.type === 'TextInput'), 'recovery does not invoke the platform keyboard');
  for (const key of ['A', '0', '-']) nodes(tree).find(node => node.props.testID === `tv.auth.portico.factor-keyboard-${key}`).props.onPress();
  assert.deepEqual(values, ['R7-A', 'R7-0', 'R7--']);
  nodes(tree).find(node => node.type === 'Button' && node.props.focusId === 'submit').props.onPress();
  assert.deepEqual(submits, ['R7-']);
});

test('profile PIN shares the actual pad, submits four digits once and exposes only masked boxes', async () => {
  let tree: any, remote: (event: any) => void = () => {};
  const submitted: string[] = [], actions: string[] = [], react = {...React, default: React};
  const numeric = await numericModule(react);
  const {TVProfilePINForm} = await screenModule(new URL('../src-next/tv/auth/TVProfilePINForm.tsx', import.meta.url), {
    ...await common(react), 'react-native': {Text: 'Text', View: 'View', useTVEventHandler: (fn: typeof remote) => {remote = fn;}},
    '../../platform/tv-keys': remoteKeys(handler => {remote = handler;}),
    '../../kit/focus': {FocusProvider: 'Provider'}, './TVAuthLayout': {TVProfilePINLayout: 'PINLayout'}, './TVNumericPad': numeric,
  });
  const profile = {id: 'actual-profile', name: 'Actual name', initial: 'A', color: 'blue'};
  const props = {profile, code: '', busy: false, onCodeChange: (value: string) => {props.code = value;},
    onSubmit: (value: string) => submitted.push(value), onBack: () => actions.push('back'), onForgotPIN: () => actions.push('forgot')};
  const host = createFabricHookHost(() => {tree = inScope(TVProfilePINForm, props);});
  const pad = () => numeric.TVNumericPad(tree.props.pad.props);
  const press = (value: string) => nodes(pad()).find(node => node.type === 'Focusable' && node.props.focusId === value).props.onPress();
  try {
    await act(() => host.mount());
    assert.equal(tree.props.profile, profile);
    const hints = nodes(tree.props.hints.type(tree.props.hints.props));
    assert.deepEqual(hints.filter(node => node.type === 'Text').map(node => node.props.children), [
      defaultI18n.t('next.auth.tv.playPause'), defaultI18n.t('next.auth.tv.deleteDigit'), defaultI18n.t('action.back'), defaultI18n.t('web.direct.otherProfiles'),
    ]);
    assert.ok(!hints.some(node => node.type === 'TextInput' || node.type === 'Button' || node.type === 'Focusable'), 'remote hints add no controls or native-input path');
    for (const digit of '1234') await act(async () => {press(digit); await host.rerender();});
    await act(async () => {press('5'); await host.rerender();});
    assert.deepEqual(submitted, ['1234']);
    const boxes = nodes(tree).find(node => node.type === numeric.TVCodeBoxes);
    assert.equal(boxes.props.length, 4); assert.equal(boxes.props.masked, true);
    assert.equal(nodes(numeric.TVCodeBoxes(boxes.props)).filter(node => node.type === 'Text').length, 0, 'entered PIN characters are never drawn as text');
    await act(async () => {remote({eventType: 'playPause', eventKeyAction: 0}); await host.rerender();});
    assert.equal(props.code, '123');
    await act(async () => {props.code = ''; await host.rerender();});
    for (const digit of '1234') await act(async () => {press(digit); await host.rerender();});
    assert.deepEqual(submitted, ['1234', '1234'], 'a rejected/reset controlled PIN can be retried');
    nodes(pad()).find(node => node.type === 'Button').props.onPress(); tree.props.onBack();
    assert.deepEqual(actions, ['forgot', 'back']);
    await act(async () => {props.busy = true; await host.rerender(); remote({eventType: 'playPause', eventKeyAction: 0}); press('9');});
    assert.equal(props.code, '1234');
    assert.ok(!nodes(tree).some(node => node.type === 'TextInput'));
  } finally {await host.unmount();}
});
