import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {ApiError} from '@portico/client-core';
import type {ShellIdentity} from '../src/shell/Shell.tsx';
import {createManageGate} from '../src/shell/manage-gate.ts';
import {profileDialogText} from '../src/shell/profile-pin-enter.ts';
import * as profileOffer from '../src/shell/profile-offer.ts';
import * as profilePinEnter from '../src/shell/profile-pin-enter.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, one, all} from './helpers/interaction-harness.mjs';
import {defaultI18n} from '@i18n';

// PR-1: a remembered main profile past its management hour is refused with profile_pin_manage on every management
// request. The shell asks for the open profile's PIN in its profile dialog; choosing that profile again with the PIN
// opens management for the hour. PR-3: the same dialog says why a switch failed (wrong PIN, lockout, anything else).

const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'main', role: 'owner'};
const identity = (over: Partial<ShellIdentity> = {}): ShellIdentity => ({viewer, username: 'owner', profileName: 'Main', serverName: 'Server', primaryProfile: true,
  profiles: [{id: 'main', name: 'Main', initial: 'M', pinRequired: true, primary: true, revision: 1, pinRevision: 1, trustRevision: 1},
    {id: 'kids', name: 'Kids', initial: 'K', revision: 2, pinRevision: 1, trustRevision: 1}],
  capabilities: {manageServer: true, manageAccount: true, managePeople: true, manageSignIn: true}, canManageProfiles: true, accountFacts: [],
  libraries: [], liveAvailable: false, unread: 0, showNotificationDot: false, ...over});

async function mounted(value: ShellIdentity, onSwitchProfile: (id: string, pin: string, signal: AbortSignal) => Promise<void>, gate = createManageGate(), lockSeconds = 0) {
  const h = createRenderer(), location = {pathname: '/', search: {}};
  let subscribed: (() => void) | undefined;
  // The harness's store hook reads the snapshot each render; the test redraws after a refusal.
  h.react.useSyncExternalStore = (subscribe: (changed: () => void) => () => void, read: () => unknown) => {
    h.react.useEffect(() => subscribe(() => {subscribed?.();}), [subscribe]); return read();
  };
  const module = await componentModule(new URL('../src/shell/Shell.tsx', import.meta.url), {react: h.react, '@i18n': {defaultI18n}, './profile-offer': profileOffer,
    '@tanstack/react-router': {useNavigate: () => () => {}, useRouterState: ({select}: any) => select({location}), Link: 'Link'},
    '../kit/theme': {useSizeClass: () => 'expanded', useCoarsePointer: () => false, useWindowScrollAnchor() {}}, './viewport': {useShellScrolled: () => false}, './window-chrome': {usePageWindowChrome: () => ({})},
    '../kit/shell': {AccountMenu: 'AccountMenu', ProfileAvatar: 'ProfileAvatar', ProfileAvatarFace: 'ProfileAvatarFace'},
    '../kit/selection': {PhoneSelectionProvider: ({children}: any) => children, usePhoneSelectionPresent: () => false}, '../kit/overlays': {Dialog: 'Dialog', MenuItem: 'MenuItem'}, '../kit/controls': {Button: 'Button', TextField: 'TextField'},
    './SwitchProfileServerHost': {SwitchProfileServerHost: 'SwitchProfileServerHost', HostedServerOpening: 'HostedServerOpening'},
    './model': {activeLibraryId: () => undefined, isLibraryLocation: () => false, rememberLibrary() {}, rememberedLibrary: () => undefined, showOwnerActivity: () => true},
    './profile-pin-enter': profilePinEnter, '../kit/accounts/pin-feedback': {usePINLockSeconds: () => lockSeconds}});
  const draw = (next = value) => tree(h.render(() => module.Shell({identity: next, OwnerActivity: 'OwnerActivity', dockVisible: false, onSignOut() {}, onSwitchProfile, manageGate: gate})));
  return {h, gate, draw};
}

const withWindow = async (run: () => Promise<void>) => {
  const prior = globalThis.window;
  globalThis.window = {localStorage: {getItem: () => null, setItem() {}}} as never;
  try {await run();} finally {globalThis.window = prior;}
};
const flush = async () => {for (let i = 0; i < 4; i++) await new Promise(resolve => setImmediate(resolve));};

test('a management refusal asks for the open profile\'s PIN, and Continue chooses that profile again with it', () => withWindow(async () => {
  const switches: {id: string; pin: string}[] = [];
  const {h, gate, draw} = await mounted(identity(), async (id, pin) => {switches.push({id, pin});});
  try {
    let ui = draw(); ui = draw();
    assert.equal(all(ui, 'Dialog').some(dialog => dialog.props.open), false, 'nothing is asked before a refusal');
    gate.refused(); await flush(); draw(); ui = draw();
    const dialog = one(ui, 'Dialog');
    assert.equal(dialog.props.open, true);
    assert.equal(dialog.props.title, 'Main');
    assert.equal(dialog.props.description, defaultI18n.t('error.profilePinManage'));
    one(ui, 'TextField').props.onChange({currentTarget: {value: '4321'}}); ui = draw();
    all(tree(one(ui, 'Dialog').props.footer), 'Button')[1].props.onClick(); await flush();
    assert.deepEqual(switches, [{id: 'main', pin: '4321'}]);
  } finally {h.dispose();}
}));

test('while management is locked the owner activity is not mounted, so nothing reads it to be refused', () => withWindow(async () => {
  const locked = await mounted(identity({manageLocked: true}), async () => {});
  try {locked.draw(); assert.equal(all(locked.draw(), 'OwnerActivity').length, 0);} finally {locked.h.dispose();}
  const open = await mounted(identity(), async () => {});
  try {open.draw(); assert.equal(all(open.draw(), 'OwnerActivity').length, 1);} finally {open.h.dispose();}
}));

test('a failed switch to a profile with no PIN says why instead of an empty dialog (PR-3)', () => withWindow(async () => {
  const {h, draw} = await mounted(identity(), async () => {throw new ApiError(409, 'profile_changed', 'changed', false);});
  try {
    let ui = draw(); ui = draw();
    one(ui, 'AccountMenu').props.actions.switchProfile(identity().profiles[1]); await flush(); draw(); ui = draw();
    const dialog = one(ui, 'Dialog');
    assert.equal(dialog.props.open, true);
    assert.equal(dialog.props.description, undefined, 'not the management sentence for another profile');
    const alert = all(ui, 'p').find(node => node.props.role === 'alert');
    assert.ok(alert, 'the dialog has a sentence');
    assert.ok(String(alert.props.children).length > 0);
  } finally {h.dispose();}
}));

test('a locked-out PIN shows the countdown and Continue waits for it (PR-3)', () => withWindow(async () => {
  const locked = Object.assign(new ApiError(429, 'profile_pin_locked', 'locked', false), {retryAfterSeconds: 30});
  const {h, draw} = await mounted(identity({viewer: {...viewer, profileId: 'kids'}, primaryProfile: false}), async () => {throw locked;}, createManageGate(), 30);
  try {
    let ui = draw(); ui = draw();
    one(ui, 'AccountMenu').props.actions.switchProfile(identity().profiles[0]); ui = draw();
    one(ui, 'TextField').props.onChange({currentTarget: {value: '1111'}}); ui = draw();
    all(tree(one(ui, 'Dialog').props.footer), 'Button')[1].props.onClick(); await flush(); draw(); ui = draw();
    assert.equal(one(ui, 'TextField').props.error, defaultI18n.t('accounts.profiles.pinLocked', {seconds: 30}));
    assert.equal(all(tree(one(ui, 'Dialog').props.footer), 'Button')[1].props.disabled, true);
  } finally {h.dispose();}
}));

test('the dialog text: wrong PIN and lockout under the field, anything else as a sentence, a cancel says nothing', () => {
  assert.deepEqual(profileDialogText(undefined, 0), {});
  assert.deepEqual(profileDialogText(new ApiError(403, 'profile_pin_invalid', 'no', false), 0), {pin: defaultI18n.t('accounts.profiles.pinFailed')});
  assert.deepEqual(profileDialogText(new ApiError(429, 'profile_pin_locked', 'wait', false), 12), {pin: defaultI18n.t('accounts.profiles.pinLocked', {seconds: 12})});
  assert.deepEqual(profileDialogText(new ApiError(429, 'profile_pin_locked', 'wait', false), 0), {}, 'the countdown ended: try again');
  assert.ok(profileDialogText(new ApiError(503, 'server_busy', 'busy', true), 0).failure);
  assert.ok(profileDialogText(new TypeError('Failed to fetch'), 0).failure);
  assert.deepEqual(profileDialogText(Object.assign(new Error('aborted'), {name: 'AbortError'}), 0), {});
});

test('the management refusal has its own sentence, not the generic permission one', async () => {
  const {presentError} = await import('@core/presentation/errors.ts');
  const presented = presentError(new ApiError(403, 'profile_pin_manage', 'Enter this profile\'s PIN to manage this account.', false), 'settings', {operation: 'save'});
  assert.equal(presented.body, defaultI18n.t('error.profilePinManage'));
});

test('the web shell hears only its own tokens\' management refusals, and unlocking keeps the profile remembered', () => {
  const source = readFileSync(new URL('../src/shell/bootstrap.ts', import.meta.url), 'utf8');
  assert.match(source, /api\.subscribeManageGate\(\(\{accessToken\}\) => \{\n\s*if \(!accessToken \|\| !ownTokens\.has\(accessToken\)\) return;/);
  assert.match(source, /switchProfile\(api,session,profileId,pin,signal,isCurrent,profileId===session\.viewer\.profileId\)/);
});
