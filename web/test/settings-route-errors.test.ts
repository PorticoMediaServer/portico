import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {PreferencesReader} from '@core/preferences-reader.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {readOwnerSignInConfiguration} from '@core/sign-in-policy.ts';
import {settingsRowIndex, settingsSpec} from '@core/presentation/settings-spec.ts';
import {defaultI18n} from '@i18n';
import {
  isFatalSettingsError,
  resolveSettingsLocation,
  SettingsController,
  settingsFatalReason,
  settingsSectionPermitted,
  type SettingsSectionAccess,
} from '../src/screens/settings/controller.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, all} from './helpers/interaction-harness.mjs';
import * as tableModel from '../src/kit/settings/table-model.ts';
import {FreshnessHub} from '@core/freshness-hub.ts';
import {mountQuietInput, SettingsOpenContext} from '../src/screens/settings/quiet-refresh.ts';
import {peopleQuietInputs} from '../src/screens/settings/people-refresh.ts';
import {quietClock, tick} from './settings-quiet-clock.ts';

const preferences: PreferenceSnapshot = {
  deviceClass: 'web',
  registry: {revision: 'test', fields: []},
  documents: [],
  effective: {},
  effectiveSource: {},
  clampedFields: [],
  registryRevision: 'test',
};

function access(overrides: Partial<SettingsSectionAccess> = {}): SettingsSectionAccess {
  const role = overrides.role ?? 'owner';
  return {
    role,
    mainProfile: overrides.mainProfile ?? true,
    capabilities: overrides.capabilities ?? {
      manageServer: role !== 'member',
      managePeople: role !== 'member',
      manageSignIn: role === 'owner',
    },
    environment: overrides.environment ?? {platform: 'web', os: 'browser', role, signIn: 'local'},
  };
}

test('resolveSettingsLocation matches sectionVisible and flags unauthorized server-scope navigation cleanly', () => {
  const owner = access({role: 'owner'});
  const admin = access({role: 'admin'});
  const peopleOnlyAdmin = access({role: 'admin', capabilities: {manageServer: false, managePeople: true, manageSignIn: false}});
  const member = access({role: 'member'});
  const secondaryOwner = access({role: 'owner', mainProfile: false});
  const porticoOwner = access({role: 'owner', environment: {platform: 'web', os: 'browser', role: 'owner', signIn: 'portico'}});

  // Member and secondary profile cannot open /dashboard or /settings?scope=server.
  for (const denied of [member, secondaryOwner]) {
    assert.deepEqual(resolveSettingsLocation({pathname: '/dashboard', searchStr: ''}, denied), {
      unauthorized: true, rawScope: 'server', rawSection: 'dashboard',
    });
    assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: '?scope=server'}, denied), {
      unauthorized: true, rawScope: 'server',
    });
    assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: '?scope=server&section=libraries'}, denied), {
      unauthorized: true, rawScope: 'server', rawSection: 'libraries',
    });
    assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: ''}, denied), {
      unauthorized: false, route: {scope: 'account', section: 'profile'},
    });
  }

  // Non-owner admin gets dashboard on /settings?scope=server (never general), and is blocked from owner-only sections like libraries/general.
  assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: '?scope=server'}, admin), {
    unauthorized: false, route: {scope: 'server', section: 'dashboard'},
  });
  assert.deepEqual(resolveSettingsLocation({pathname: '/dashboard', searchStr: ''}, admin), {
    unauthorized: false, route: {scope: 'server', section: 'dashboard'},
  });
  assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: '?scope=server&section=libraries'}, admin), {
    unauthorized: true, rawScope: 'server', rawSection: 'libraries',
  });
  assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: '?scope=server&section=general'}, admin), {
    unauthorized: true, rawScope: 'server', rawSection: 'general',
  });

  // People-only admin visiting /settings?scope=server resolves straight to people so the People source is instantiated.
  assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: '?scope=server'}, peopleOnlyAdmin), {
    unauthorized: false, route: {scope: 'server', section: 'people'},
  });
  assert.deepEqual(resolveSettingsLocation({pathname: '/dashboard', searchStr: ''}, peopleOnlyAdmin), {
    unauthorized: true, rawScope: 'server', rawSection: 'dashboard',
  });

  // Owner accesses libraries and general; on Portico sign-in, local-only remote-sources falls back to general rather than owner-only refusal.
  assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: '?scope=server&section=libraries'}, owner), {
    unauthorized: false, route: {scope: 'server', section: 'libraries'},
  });
  assert.deepEqual(resolveSettingsLocation({pathname: '/settings', searchStr: '?scope=server&section=remote-sources'}, porticoOwner), {
    unauthorized: false, route: {scope: 'server', section: 'general'},
  });

  // Every resolved authorized route is permitted by settingsSectionPermitted.
  for (const viewer of [owner, admin, peopleOnlyAdmin, member, secondaryOwner, porticoOwner]) {
    for (const loc of [
      {pathname: '/settings', searchStr: ''},
      {pathname: '/settings', searchStr: '?scope=server'},
      {pathname: '/dashboard', searchStr: ''},
      {pathname: '/settings', searchStr: '?scope=server&section=libraries'},
      {pathname: '/settings', searchStr: '?scope=server&section=people'},
    ]) {
      const resolved = resolveSettingsLocation(loc, viewer);
      if (!resolved.unauthorized) {
        const section = settingsSpec.find(s => s.scope === resolved.route.scope && s.id === resolved.route.section)!;
        assert.equal(settingsSectionPermitted(section, viewer), true);
      }
    }
  }
});

test('Settings.route gates source instantiation on resolveSettingsLocation and renders SettingsOwnerOnly when unauthorized', () => {
  const routeFile = readFileSync(new URL('../src/screens/settings/Settings.route.tsx', import.meta.url), 'utf8');
  assert.match(routeFile, /resolveSettingsLocation\(location, access\)/);
  assert.match(routeFile, /if \(!context \|\| !reader \|\| !snapshot \|\| unauthorized \|\| !route/);
  assert.match(routeFile, /if \(unauthorized\) \{[\s\S]*?<SettingsOwnerOnly/);
  assert.match(routeFile, /const libraries = route\.section === 'libraries' && identity\.primaryProfile === true && identity\.capabilities\?\.manageServer === true && role === 'owner'/);
});

test('SettingsController keeps factor_setup_required and 500 row write errors inline without calling reportError', async () => {
  const row = settingsRowIndex.get('signin.requireTwoStep')!.row;
  const section = settingsRowIndex.get(row.id)!.section;
  let nextError: unknown = {status: 403, code: 'factor_setup_required'};
  const reports: unknown[] = [];
  const doc = {
    name: 'sign-in',
    etag: '"1"',
    values: {staySignedInDays: 90, pinRememberDays: 30, requireTwoStep: 'nobody'},
    defaults: {staySignedInDays: 90, pinRememberDays: 30, requireTwoStep: 'nobody'},
  };
  const api = {
    request: async <T>(path: string): Promise<T> => {
      if (path === '/v1/admin/settings/sign-in') return doc as T;
      throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
    },
    requestRaw: async () => {
      if (nextError && typeof nextError === 'object' && 'status' in nextError && 'code' in nextError) {
        const {status, code} = nextError as {status: number; code: string};
        return {status, headers: {}, body: {error: {code, message: code}}};
      }
      throw nextError;
    },
  };
  const reader = new PreferencesReader(api);
  const controller = new SettingsController({
    api,
    serverId: 'server',
    viewerId: 'owner',
    role: 'owner',
    mainProfile: true,
    capabilities: {manageServer: true, managePeople: true, manageSignIn: true},
    preferences,
    reader,
    readConfiguration: signal => readOwnerSignInConfiguration(api, signal),
    isCurrent: () => true,
    reportError: error => reports.push(error),
    environment: {platform: 'web', os: 'browser', signIn: 'local'},
    eligibility: {hasEngine: () => true, library: () => undefined},
    reading: {read: () => undefined as never, patch: () => {throw new Error('unexpected');}},
  });
  const writeAndWait = (value: string) => new Promise<void>(resolve => {
    const off = controller.subscribe(() => {
      if (controller.rowState(row).error && !controller.rowState(row).saving) {off(); resolve();}
    });
    controller.change(row, value);
  });
  try {
    await controller.load(section);
    // 403 factor_setup_required must render inline on the row and NOT escalate to fatal page failure.
    await writeAndWait('administrators');
    assert.equal(controller.rowState(row).error, defaultI18n.t('error.factorSetupRequired'));
    assert.deepEqual(reports, [], 'factor_setup_required must stay inline');

    // Non-fatal 500 error also stays inline on the row.
    nextError = Object.assign(new Error('boom'), {status: 500, code: 'internal'});
    await writeAndWait('everyone');
    assert.equal(controller.rowState(row).error, defaultI18n.t('next.states.pageFailedBody'));
    assert.deepEqual(reports, [], 'non-fatal save failure must stay inline');

    // Fatal 403 forbidden DOES escalate to reportError.
    nextError = {status: 403, code: 'forbidden'};
    await writeAndWait('administrators');
    assert.equal(reports.length, 1);
    assert.equal(isFatalSettingsError(reports[0]), true);
  } finally {controller.dispose(); reader.dispose();}
});

test('PeopleScreen keeps factor_setup_required and 500 mutations inline without calling reportError or SettingsPageFailure', async t => {
  const c = quietClock(), hub = new FreshnessHub(c.env);
  const h = createRenderer({element: {scrollTop: 0, clientHeight: 440, querySelectorAll: () => []}});
  const resize = Object.getOwnPropertyDescriptor(globalThis, 'ResizeObserver');
  Object.defineProperty(globalThis, 'ResizeObserver', {configurable: true, value: class {observe() {} disconnect() {}}});
  const react = {...h.react, useId: () => 'people-err', useImperativeHandle: (ref: any, make: () => unknown) => {if (ref) ref.current = make();}};
  react.default = react;
  let table: any;
  let notesError: unknown = Object.assign(new Error('need mfa'), {status: 403, code: 'factor_setup_required'});
  const person = {
    accountId: 'person-A', username: 'Alex', role: 'member', revision: 1, limitsRevision: 1, allowedLibraries: [],
    limits: {allowRemote: true, conversion: 'original', schedule: {windows: [], timezone: 'UTC'}, maxContentRating: '', allowUnrated: true, accessTokens: true, tagPolicy: {deniedLabels: []}},
  };
  const page = {items: [{accountId: 'person-A', username: 'Alex', role: 'member', revision: 1, limitsRevision: 1}], nextCursor: '', totalCount: 1, matchedCount: 1, limit: 40};
  const client = {
    read: async () => page,
    refresh: async () => page,
    person: async () => person,
    notes: async () => {throw notesError;},
    dispose() {},
  };
  const tableModule = await componentModule(new URL('../src/kit/settings/SettingsTable.tsx', import.meta.url), {
    react, '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../controls': {Button: 'Button', Select: 'Select', TextField: 'TextField'}, '../Icon': {Icon: 'Icon'},
    '../artwork/Artwork': {Artwork: 'Artwork'}, './table-model': tableModel, './settings.module.css': {default: {}},
  });
  function MountedTable(props: any) {table = props; return react.createElement(tableModule.SettingsTable, props);}
  const kit = {SettingsTable: MountedTable, SettingsGroup: 'SettingsGroup', SettingsPanel: 'SettingsPanel', SettingsRows: 'SettingsRows', SettingsPageFailure: 'SettingsPageFailure', MemberResetCodeSheet: 'MemberResetCodeSheet'};
  const app = await componentModule(new URL('../src/screens/settings/People.tsx', import.meta.url), {
    react, '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../../platform/freshness': {useFreshness: () => ({hub})},
    './quiet-refresh': {SettingsOpenContext, mountQuietInput},
    './people-refresh': {peopleQuietInputs},
    '../../kit/settings': kit,
    '../../kit/controls': {Button: 'Button', RichSelect: 'RichSelect', Tabs: 'Tabs'},
    '../../kit/overlays/Dialog': {Dialog: 'Dialog'},
    '../../kit/shell/SettingsSurfaces': {SettingsRow: 'Row'},
    '../../kit/accounts': {InvitationShortCodeDialog: 'InvitationShortCodeDialog'},
    './people-table-row': {peopleTableRow: (value: any) => ({id: value.accountId, label: value.username, cells: {name: value.username}})},
    './member-reset-code-view': {useMemberResetCodeView: () => undefined},
    './controller': {isFatalSettingsError, settingsFatalReason},
  });
  const reported: unknown[] = [];
  let authorityChanged = 0;
  const bindings = {
    client, actor: {accountId: 'owner', role: 'owner'}, serverName: 'Server', libraries: [],
    authority: {scope: {serverId: 'server', authority: 'local', accountId: 'owner', profileId: 'main', sessionFamilyId: 'fam'}, isCurrent: () => true, subscribe: () => () => {}, onAuthorityChanged: () => {authorityChanged++;}},
    openHome: () => {},
    reportError: (error: unknown) => {reported.push(error);},
  };
  const render = () => tree(h.render(() => react.createElement(app.PeopleScreen, {bindings})));
  t.after(() => {
    h.dispose(); hub.dispose();
    if (resize) Object.defineProperty(globalThis, 'ResizeObserver', resize);
    else Reflect.deleteProperty(globalThis, 'ResizeObserver');
  });
  render(); await new Promise(r => setTimeout(r, 0)); await tick(); render();
  table.rowAction.onAction({id: 'person-A'}, null); render(); await tick();
  let nodes = render();
  let panel = all(nodes, 'SettingsPanel').find((node: any) => node.props.open);
  assert.ok(panel, 'person panel opened');
  const rows = all(tree(panel.props.tabs[0].content), 'SettingsRows')[0];
  rows.props.change({id: 'people.notes'}, 'updated');
  await tick();
  nodes = render();
  panel = all(nodes, 'SettingsPanel').find((node: any) => node.props.open);
  assert.equal(panel?.props.error, 'error.factorSetupRequired');
  assert.deepEqual(reported, [], 'factor_setup_required must not call reportError');
  assert.equal(authorityChanged, 0, 'factor_setup_required must not retire authority');
  assert.equal(all(nodes, 'SettingsPageFailure').length, 0);

  notesError = Object.assign(new Error('server down'), {status: 500});
  const rows2 = all(tree(panel.props.tabs[0].content), 'SettingsRows')[0];
  rows2.props.change({id: 'people.notes'}, 'again');
  await tick();
  nodes = render();
  panel = all(nodes, 'SettingsPanel').find((node: any) => node.props.open);
  assert.equal(panel?.props.error, 'next.states.pageFailedBody');
  assert.deepEqual(reported, [], '500 mutation error must stay inline');
  assert.equal(all(nodes, 'SettingsPageFailure').length, 0);

  // Fatal management PIN refusal escalates to reportError without retiring authority.
  notesError = Object.assign(new Error('pin'), {status: 403, code: 'profile_pin_manage'});
  all(tree(panel.props.tabs[0].content), 'SettingsRows')[0].props.change({id: 'people.notes'}, 'pin');
  await tick();
  assert.equal(reported.length, 1);
  assert.equal(authorityChanged, 0);

  // Fatal 403 forbidden retires authority.
  notesError = Object.assign(new Error('no'), {status: 403, code: 'forbidden'});
  all(tree(panel.props.tabs[0].content), 'SettingsRows')[0].props.change({id: 'people.notes'}, 'denied');
  await tick();
  assert.equal(authorityChanged, 1);
});

test('GeneralTools and GeneralPause keep non-fatal mutation failures inline and escalate only fatal errors', async t => {
  const h = createRenderer();
  const react = {...h.react}; react.default = react;
  const tools = await componentModule(new URL('../src/screens/settings/server/GeneralTools.tsx', import.meta.url), {
    react,
    '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../../kit/controls/Choices': {Segmented: 'Segmented'},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'},
    '../../../kit/settings': {SettingsAction: 'SettingsAction'},
    '../controller': {isFatalSettingsError},
    './server.module.css': {default: {generalTool: 'generalTool', storageNote: 'storageNote', backupFailure: 'backupFailure', composerFields: 'composerFields', composerLabel: 'composerLabel', composerBody: 'composerBody', composerProblem: 'composerProblem'}},
  });
  t.after(() => h.dispose());

  // GeneralMessage: 500 stays inline, 403 forbidden escalates.
  const hm = createRenderer();
  t.after(() => hm.dispose());
  const reactM = {...hm.react}; reactM.default = reactM;
  const toolsM = await componentModule(new URL('../src/screens/settings/server/GeneralTools.tsx', import.meta.url), {
    react: reactM,
    '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../../kit/controls/Choices': {Segmented: 'Segmented'},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'},
    '../../../kit/settings': {SettingsAction: 'SettingsAction'},
    '../controller': {isFatalSettingsError},
    './server.module.css': {default: {generalTool: 'generalTool', storageNote: 'storageNote', backupFailure: 'backupFailure', composerFields: 'composerFields', composerLabel: 'composerLabel', composerBody: 'composerBody', composerProblem: 'composerProblem'}},
  });
  let messageError: unknown = Object.assign(new Error('fail'), {status: 500});
  const messageReports: unknown[] = [];
  const messageApi = {request: async () => {throw messageError;}};
  const renderMessage = () => tree(hm.render(() => reactM.createElement(toolsM.GeneralMessage, {
    api: messageApi, isCurrent: () => true, reportError: (e: unknown) => messageReports.push(e), label: 'Message everyone',
  })));
  let mNodes = renderMessage();
  all(mNodes, 'SettingsAction')[0].props.onClick();
  mNodes = renderMessage();
  all(mNodes, 'TextField')[0].props.onChange({currentTarget: {value: 'Heads up'}});
  all(mNodes, 'textarea')[0].props.onChange({currentTarget: {value: 'Maintenance soon'}});
  mNodes = renderMessage();
  const mDialog = all(mNodes, 'Dialog')[0];
  all(tree(mDialog.props.footer), 'Button').find((b: any) => b.props.variant === 'primary').props.onClick();
  await tick(); await tick();
  mNodes = renderMessage();
  assert.deepEqual(messageReports, [], '500 broadcast failure must not call reportError');
  assert.ok(all(mNodes, 'p').some((p: any) => p.props.role === 'alert' && [p.props.children].flat().includes('error.unavailable')));

  messageError = {status: 403, code: 'forbidden'};
  const mDialog2 = all(mNodes, 'Dialog')[0];
  all(tree(mDialog2.props.footer), 'Button').find((b: any) => b.props.variant === 'primary').props.onClick();
  await tick(); await tick();
  assert.equal(messageReports.length, 1);

  // GeneralRestart: 500 stays inline, 403 forbidden escalates.
  let restartError: unknown = Object.assign(new Error('fail'), {status: 500});
  const restartReports: unknown[] = [];
  const restartApi = {request: async () => {throw restartError;}};
  const renderRestart = () => tree(h.render(() => react.createElement(tools.GeneralRestart, {
    api: restartApi, isCurrent: () => true, reportError: (e: unknown) => restartReports.push(e), label: 'Restart',
  })));
  let nodes = renderRestart();
  all(nodes, 'SettingsAction')[0].props.onClick();
  nodes = renderRestart();
  const dialog = all(nodes, 'Dialog')[0];
  const confirmButton = all(tree(dialog.props.footer), 'Button').find((b: any) => b.props.variant === 'primary');
  confirmButton.props.onClick();
  await tick();
  nodes = renderRestart();
  assert.deepEqual(restartReports, [], '500 restart failure must not call reportError');
  assert.ok(all(nodes, 'p').some((p: any) => p.props.role === 'alert' && [p.props.children].flat().includes('server.restart.failed')));

  restartError = {status: 403, code: 'forbidden'};
  const dialog2 = all(nodes, 'Dialog')[0];
  all(tree(dialog2.props.footer), 'Button').find((b: any) => b.props.variant === 'primary').props.onClick();
  await tick();
  assert.equal(restartReports.length, 1);

  // GeneralPause: 500 stays inline, 403 forbidden escalates.
  const h2 = createRenderer();
  t.after(() => h2.dispose());
  const react2 = {...h2.react}; react2.default = react2;
  const pause2 = await componentModule(new URL('../src/screens/settings/server/GeneralPause.tsx', import.meta.url), {
    react: react2,
    '@i18n': {defaultI18n: {t: (key: string) => key, time: (value: string) => value}},
    '../../../kit/controls': {Button: 'Button'},
    '../controller': {isFatalSettingsError},
  });
  let pauseError: unknown = Object.assign(new Error('fail'), {status: 500});
  const pauseReports: unknown[] = [];
  const pauseApi = {
    request: async (_path: string, method?: string) => {
      if (!method || method === 'GET') return {paused: false};
      throw pauseError;
    },
  };
  const renderPause = () => tree(h2.render(() => react2.createElement(pause2.GeneralPause, {
    api: pauseApi, isCurrent: () => true, reportError: (e: unknown) => pauseReports.push(e), label: 'Pause',
  })));
  renderPause(); await tick();
  nodes = renderPause();
  all(nodes, 'Button')[0].props.onClick();
  await tick();
  nodes = renderPause();
  assert.deepEqual(pauseReports, [], '500 pause failure must not call reportError');
  assert.ok(all(nodes, 'p').some((p: any) => p.props.role === 'alert' && [p.props.children].flat().includes('next.states.pageFailedBody')));

  pauseError = {status: 403, code: 'forbidden'};
  all(nodes, 'Button')[0].props.onClick();
  await tick();
  assert.equal(pauseReports.length, 1);
});
