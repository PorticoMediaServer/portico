import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {ApiError} from '@portico/client-core';
import {defaultI18n} from '@i18n';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {handleProfilePinManage, isProfilePinManageError} from '../src/screens/settings/pin-manage.ts';
import {SignInSettingsSource} from '../src/screens/settings/signin-source.ts';
import {activeServerSection, canManageServerSection, serverSectionRoutes, serverSectionTierAllowed} from '../src/screens/settings/route-sections/access-gate.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, all, one} from './helpers/interaction-harness.mjs';

const flush = async () => {for (let i = 0; i < 4; i++) await new Promise(resolve => setImmediate(resolve));};

test('pin-manage detects 403 profile_pin_manage uniformly and ignores unrelated 403/500 errors', () => {
  const pinApi = new ApiError(403, 'profile_pin_manage', 'Enter PIN', false);
  const pinPlain = {status: 403, code: 'profile_pin_manage'};
  const pinStatusCode = {statusCode: 403, code: 'profile_pin_manage'};
  const pinNoStatus = {code: 'profile_pin_manage'};
  for (const err of [pinApi, pinPlain, pinStatusCode, pinNoStatus]) {
    assert.equal(isProfilePinManageError(err), true);
    const seen: unknown[] = [];
    assert.equal(handleProfilePinManage(err, e => seen.push(e)), true);
    assert.deepEqual(seen, [err]);
  }
  for (const err of [{status: 403, code: 'forbidden'}, {status: 401, code: 'unauthorized'}, {status: 500, code: 'profile_pin_manage'}, new Error('offline'), null, undefined]) {
    assert.equal(isProfilePinManageError(err), false);
    const seen: unknown[] = [];
    assert.equal(handleProfilePinManage(err, e => seen.push(e)), false);
    assert.deepEqual(seen, []);
  }
});

test('access-gate maps every server Settings section to published routes and enforces role/capability gates', () => {
  const api = JSON.parse(readFileSync(new URL('../../server-next/api/openapi.json', import.meta.url), 'utf8'));
  const published = new Set(Object.entries(api.paths).flatMap(([path, methods]) => Object.keys(methods as object).map(method => `${method.toUpperCase()} ${path}`)));
  for (const [section, routeIds] of Object.entries(serverSectionRoutes)) {
    for (const id of routeIds) assert.ok(published.has(id), `${section}: ${id}`);
  }
  assert.equal(serverSectionTierAllowed('libraries', 'owner'), true);
  assert.equal(serverSectionTierAllowed('libraries', 'admin'), false);
  assert.equal(serverSectionTierAllowed('people', 'admin'), true);
  assert.equal(serverSectionTierAllowed('dashboard', 'admin'), true);
  assert.equal(serverSectionTierAllowed('troubleshooting', 'admin'), true);
  assert.equal(serverSectionTierAllowed('dashboard', 'member'), false);

  const ownerGate = {role: 'owner' as const, primaryProfile: true, capabilities: {manageServer: true, managePeople: true, manageSignIn: true}, signIn: 'local', authority: 'local'};
  const adminGate = {role: 'admin' as const, primaryProfile: true, capabilities: {manageServer: true, managePeople: true, manageSignIn: false}, signIn: 'local', authority: 'local'};
  assert.equal(activeServerSection({scope: 'server', section: 'libraries'}, 'libraries', ownerGate), true);
  assert.equal(activeServerSection({scope: 'server', section: 'libraries'}, 'libraries', adminGate), false);
  assert.equal(activeServerSection({scope: 'server', section: 'people'}, 'people', adminGate), true);
  assert.equal(canManageServerSection('remote-sources', {...ownerGate, signIn: 'portico'}), false);
  assert.equal(canManageServerSection('general', {...ownerGate, primaryProfile: false}), false);
});

test('SignInSettingsSource routes 403 profile_pin_manage to onPinManage without retiring authority', async () => {
  let nextError: unknown = new ApiError(403, 'profile_pin_manage', 'Enter PIN', false);
  let authorityChanged = 0;
  const pinErrors: unknown[] = [];
  const source = new SignInSettingsSource({
    request: async <T,>() => {
      if (nextError) throw nextError;
      return {kind: 'local', owner: {id: 'owner', username: 'owner'}, waiting: []} as T;
    },
  }, {
    serverId: 'server', signIn: 'local', isCurrent: () => true,
    capabilities: () => ({manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true}),
    onAuthorityChanged: () => {authorityChanged++;},
    onPinManage: error => {pinErrors.push(error);},
  });
  try {
    await source.load();
    assert.equal(authorityChanged, 0, 'profile_pin_manage must not retire authority on load');
    assert.equal(pinErrors.length, 1);
    assert.equal(source.getSnapshot().error, nextError);

    // Succeeding afterwards works because the source was not disposed.
    nextError = undefined;
    await source.load();
    assert.equal(source.rowState('signin.ownerLocal')?.displayValue, 'owner');

    // Refresh hitting profile_pin_manage also keeps authority and invokes onPinManage.
    nextError = {status: 403, code: 'profile_pin_manage'};
    const refresh = await source.refresh();
    assert.equal(refresh.kind, 'unreadable');
    assert.equal(authorityChanged, 0, 'profile_pin_manage must not retire authority on refresh');
    assert.equal(pinErrors.length, 2);

    // Real 403 forbidden still retires authority.
    nextError = {status: 403, code: 'forbidden'};
    await source.refresh();
    assert.equal(authorityChanged, 1);
  } finally {source.dispose();}
});

test('MetadataSettingsPage guards unsaved changes with useBlocker and handles 403 profile_pin_manage without wiping draft', async t => {
  const h = createRenderer();
  t.after(() => h.dispose());
  const react = {...h.react}; react.default = react;
  let blockerOpts: any;
  let blockerState: any = {status: 'idle', proceed: undefined, reset: undefined};
  const doc = {
    revision: 3, onlineLookups: true, includeAdult: false, defaultLanguage: 'en', defaultRegion: 'US',
    tmdbCredentialConfigured: false, openSubtitlesUsername: '', openSubtitlesKeyConfigured: false,
    openSubtitlesPasswordConfigured: false, openSubtitles: {keySource: 'none', status: 'unconfigured'},
    librariesComplete: false,
  };
  let saveError: unknown;
  const reported: unknown[] = [];
  const api = {
    request: async <T,>(_path: string, method?: string) => {
      if (method === 'PUT') {if (saveError) throw saveError; return {...doc, revision: 4, includeAdult: true} as T;}
      return doc as T;
    },
  };
  const mod = await componentModule(new URL('../src/screens/settings/metadata/MetadataSettingsPage.tsx', import.meta.url), {
    react,
    '@tanstack/react-router': {useBlocker: (opts: any) => {blockerOpts = opts; return blockerState;}},
    '@i18n': {defaultI18n: {t: (key: string) => key, bytes: (n: number) => `${n}B`}},
    '../../../kit/controls': {Button: 'Button', Select: 'Select', Switch: 'Switch', TextField: 'TextField'},
    '../../../kit/settings': {SettingsAdvanced: 'SettingsAdvanced', SettingsGroup: 'SettingsGroup'},
    '../../../kit/overlays': {Dialog: 'Dialog'},
    '../../../kit/states': {Skeleton: 'Skeleton'},
    '../../../kit/shell/SettingsSurfaces': {SettingsRow: 'SettingsRow'},
    '../pin-manage': {handleProfilePinManage, isProfilePinManageError},
    './MetadataLibraries': {MetadataLibraries: 'MetadataLibraries'},
    './metadata-settings.module.css': {default: {}},
  });
  const isCurrent = () => true, onLibraries = () => {}, reportError = (err: unknown) => reported.push(err);
  const render = () => tree(h.render(() => react.createElement(mod.MetadataSettingsPage, {
    api, isCurrent, onLibraries, reportError,
  })));

  render(); await flush();
  let nodes = render();
  assert.equal(blockerOpts.disabled, true, 'clean page does not block navigation');
  assert.equal(blockerOpts.enableBeforeUnload, false);

  // Toggle includeAdult to make draft dirty.
  const adultSwitch = all(nodes, 'Switch').find((s: any) => s.props['aria-label'] === 'metadata.settings.includeAdult')!;
  adultSwitch.props.onCheckedChange(true);
  nodes = render();
  assert.equal(blockerOpts.disabled, false, 'dirty page enables navigation guard');
  assert.equal(blockerOpts.enableBeforeUnload, true);
  assert.equal(blockerOpts.shouldBlockFn(), true);

  // Blocked navigation renders confirmation dialog with Cancel -> reset and Discard -> proceed.
  let resets = 0, proceeds = 0;
  blockerState = {status: 'blocked', reset: () => {resets++;}, proceed: () => {proceeds++;}};
  nodes = render();
  const leaveDialog = all(nodes, 'Dialog').find((d: any) => d.props.title === 'settings.save.unsaved');
  assert.equal(leaveDialog?.props.open, true);
  const footerButtons = all(tree(leaveDialog.props.footer), 'Button');
  footerButtons[0].props.onClick();
  footerButtons[1].props.onClick();
  assert.equal(resets, 1);
  assert.equal(proceeds, 1);
  blockerState = {status: 'idle'};

  // 403 profile_pin_manage on save reports via reportError and keeps draft alive (not denied/forbidden).
  saveError = new ApiError(403, 'profile_pin_manage', 'Enter PIN', false);
  const saveButton = all(nodes, 'Button').find((b: any) => [b.props.children].flat().includes('web.metadata.saveChanges'))!;
  saveButton.props.onClick();
  await flush();
  nodes = render();
  assert.equal(reported.length, 1);
  assert.equal(reported[0], saveError);
  assert.ok(all(nodes, 'div').some((d: any) => d.props['data-testid'] === 'server-metadata-settings'), 'draft page stays mounted instead of collapsing to forbidden');
  assert.equal(blockerOpts.disabled, false, 'unsaved edit is preserved after profile_pin_manage');
});

test('ProfilePINDialog, ProfileDelete, and ProfileDevices pass 403 profile_pin_manage to onFailure uniformly', async t => {
  const pinError = new ApiError(403, 'profile_pin_manage', 'Enter PIN', false);

  // 1. ProfilePINDialog
  const hPin = createRenderer(); t.after(() => hPin.dispose());
  const reactPin = {...hPin.react}; reactPin.default = reactPin;
  const pinMod = await componentModule(new URL('../src/screens/settings/account-profile/ProfilePINControl.tsx', import.meta.url), {
    react: reactPin, '@i18n': {defaultI18n: {t: (k: string) => k}},
    '../../../kit/accounts/AuthStage': {AuthCodeBoxes: 'AuthCodeBoxes'},
    '../../../kit/controls': {Button: 'Button'},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'},
    '../pin-manage': {handleProfilePinManage, isProfilePinManageError},
    './profile.module.css': {default: {}},
  });
  const pinFailures: unknown[] = [];
  let pinClosed = 0;
  const pinSource = {
    subscribe: () => () => {},
    getSnapshot: () => ({loading: false, busy: false, document: {canManage: true, capabilities: {manageAccount: true}}, error: undefined}),
    setPIN: async () => {throw pinError;},
  };
  const pinNodes = tree(hPin.render(() => reactPin.createElement(pinMod.ProfilePINDialog, {
    request: {source: pinSource, target: {id: 'p1', name: 'Kid'}, remove: true, trigger: null},
    isCurrent: () => true, onClose: () => {pinClosed++;}, onFailure: (e: unknown) => pinFailures.push(e),
  })));
  const removeBtn = all(tree(one(pinNodes, 'Dialog').props.footer), 'Button')[1];
  removeBtn.props.onClick();
  await flush();
  assert.equal(pinClosed, 1);
  assert.deepEqual(pinFailures, [pinError]);

  // 2. ProfileDelete (previewDelete + deleteProfile)
  const hDel = createRenderer(); t.after(() => hDel.dispose());
  const reactDel = {...hDel.react}; reactDel.default = reactDel;
  const delMod = await componentModule(new URL('../src/screens/settings/account-profile/ProfileDelete.tsx', import.meta.url), {
    react: reactDel, '@i18n': {defaultI18n: {t: (k: string) => k}},
    '../../../kit/controls': {Button: 'Button'},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'},
    '../pin-manage': {handleProfilePinManage, isProfilePinManageError},
    './profile.module.css': {default: {}},
  });
  const delFailures: unknown[] = [];
  const delSource = {
    subscribe: () => () => {},
    getSnapshot: () => ({loading: false, busy: false, document: {canManage: true, capabilities: {manageAccount: true}}, error: undefined}),
    selected: () => ({id: 'kid', name: 'Kid', primary: false}),
    previewDelete: async () => {throw pinError;},
    deleteProfile: async () => {throw pinError;},
  };
  const delNodes = tree(hDel.render(() => reactDel.createElement(delMod.ProfileDelete, {
    source: delSource, isCurrent: () => true, onFailure: (e: unknown) => delFailures.push(e),
  })));
  one(delNodes, 'Button').props.onClick();
  await flush();
  assert.deepEqual(delFailures, [pinError]);

  // 3. ProfileDevices (signOut hitting profile_pin_manage invokes onFailure even when source stores snapshot.error)
  const hDev = createRenderer(); t.after(() => hDev.dispose());
  const reactDev = {...hDev.react}; reactDev.default = reactDev;
  const devMod = await componentModule(new URL('../src/screens/settings/account-profile/ProfileDevices.tsx', import.meta.url), {
    react: reactDev, '@i18n': {defaultI18n: {t: (k: string) => k}},
    '../../../kit/controls': {Button: 'Button'},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'},
    '../../../kit/Icon': {Icon: 'Icon'},
    '../pin-manage': {handleProfilePinManage, isProfilePinManageError},
    './profile.module.css': {default: {}},
    './profile-devices.module.css': {default: {}},
    './device-icon': {profileDeviceIcon: () => 'i-device'},
  });
  const devFailures: unknown[] = [];
  const device = {id: 'd1', name: 'Phone', app: 'Web', platform: 'web', sessions: 1, current: false};
  let devSnapshot: any = {loading: false, busy: false, devices: [device], error: undefined};
  const devSource = {
    subscribe: () => () => {},
    getSnapshot: () => devSnapshot,
    load: async () => {},
    signOut: async () => {devSnapshot = {loading: false, busy: false, error: pinError}; throw pinError;},
  };
  const drawDev = () => tree(hDev.render(() => reactDev.createElement(devMod.ProfileDevices, {
    source: devSource, isCurrent: () => true, onFailure: (e: unknown) => devFailures.push(e), signOut: () => {},
  })));
  let devNodes = drawDev();
  one(devNodes, 'Button').props.onClick({currentTarget: {}});
  devNodes = drawDev();
  all(tree(one(devNodes, 'Dialog').props.footer), 'Button')[1].props.onClick();
  await flush();
  assert.deepEqual(devFailures, [pinError]);
});

test('ProfileSignIn defers URL.revokeObjectURL on recovery code download and wires returnFocusRef on Dialog', () => {
  const source = readFileSync(new URL('../src/screens/settings/account-profile/ProfileSignIn.tsx', import.meta.url), 'utf8');
  assert.match(source, /const dialogTrigger=useRef<HTMLElement\|null>\(null\);/);
  assert.match(source, /returnFocusRef=\{dialogTrigger\}/);
  assert.match(source, /setTimeout\(\(\)=>URL\.revokeObjectURL\(created\),60_000\)/);
  assert.doesNotMatch(source, /finally\{if\(url\)URL\.revokeObjectURL\(url\);\}/);
});

test('DeviceRowControl attaches linkReturn ref to the Link a TV trigger button and passes it to LinkTvDialog', async t => {
  const h = createRenderer(); t.after(() => h.dispose());
  const react = {...h.react}; react.default = react;
  const mod = await componentModule(new URL('../src/screens/settings/device-rows.tsx', import.meta.url), {
    react,
    '@i18n': {defaultI18n},
    '../../router': {router: {navigate: async () => {}}},
    '../../kit/states/Licences': {Licences: 'Licences'},
    '../../kit/controls': {Button: 'Button'},
    '../../kit/Icon': {Icon: 'Icon'},
    './LinkTvDialog': {LinkTvDialog: 'LinkTvDialog'},
  });
  const row = settingsRowIndex.get('device.linkTv')!.row;
  const nodes = tree(h.render(() => react.createElement(mod.DeviceRowControl, {
    row, onSwitchServer: () => {}, api: {request: async () => ({}), requestRaw: async () => ({status: 200, headers: {}, body: {}})},
  })));
  const button = one(nodes, 'Button');
  const dialog = one(nodes, 'LinkTvDialog');
  assert.ok(button.props.ref, 'Link a TV button receives ref');
  assert.equal(button.props.ref, dialog.props.returnFocusRef, 'trigger button ref matches LinkTvDialog returnFocusRef');
});
