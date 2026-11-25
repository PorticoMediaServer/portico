import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import * as errorsCore from '@core/presentation/errors.ts';
import {folderPickerBreadcrumbs} from '../src/kit/settings/ServerFolderPicker.tsx';
import {Busiest} from '../src/screens/dashboard/Analytics.tsx';
import {Chart} from '../src/screens/dashboard/Chart.tsx';
import * as dashboardModel from '../src/screens/dashboard/model.ts';
import * as dashboardFormat from '../src/screens/dashboard/format.ts';
import * as peopleInvite from '../src/screens/settings/people-invite.ts';
import * as supportSourceMod from '../src/screens/settings/server/troubleshooting/support-source.ts';
import * as supportOptionsMod from '../src/screens/settings/server/troubleshooting/support-options.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, textOf, tree} from './helpers/interaction-harness.mjs';

const {SettingsSupportSource} = supportSourceMod;
const withReact = (h: ReturnType<typeof createRenderer>) => ({...h.react, useId: () => 'test-id'});

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
const flush = async (n = 6) => { for (let i = 0; i < n; i++) await Promise.resolve(); };

test('ServerDirectoryExplorer keeps browsed directory when parent re-renders with fresh inline callbacks', async () => {
  const h = createRenderer();
  const requests: (string | undefined)[] = [];
  const api = {};
  const browseServerFilesystem = async (_api: unknown, opts: {path?: string}) => {
    requests.push(opts.path);
    if (opts.path === '/media/movies') {
      return {path: '/media/movies', parent: '/media', separator: '/', roots: [{path: '/media', name: 'Media', kind: 'mount', description: '/media'}], entries: []};
    }
    return {
      path: '/media',
      parent: '',
      separator: '/',
      roots: [{path: '/media', name: 'Media', kind: 'mount', description: '/media'}],
      entries: [{path: '/media/movies', name: 'movies', kind: 'directory', readable: true}],
    };
  };
  const mod = await componentModule(new URL('../src/kit/settings/ServerDirectoryExplorer.tsx', import.meta.url), {
    react: withReact(h),
    '@i18n': {defaultI18n},
    '@core/library-management.ts': {browseServerFilesystem, classifyServerPath: () => 'local', typedServerPath: (v: string) => v},
    '../controls': {Button: 'Button', Switch: 'Switch', TextField: 'TextField'},
    '../Icon': {Icon: 'Icon'},
    '../overlays/BottomSheet': {BottomSheet: 'BottomSheet'},
    '../overlays/Dialog': {Dialog: (props: any) => h.react.createElement('Dialog', props, props.children, props.footer)},
    '../overlays/shared': {usePhone: () => false},
    '../states': {Skeleton: 'Skeleton'},
    './server-directory-explorer.module.css': {default: {}},
  });
  let chosenPath = '';
  const renderWithInlineCallbacks = () => tree(h.render(() => mod.ServerDirectoryExplorer({
    open: true,
    onOpenChange: () => {},
    api,
    initialPath: '/media',
    onChoose: (path: string) => { chosenPath = path; },
  })));
  try {
    renderWithInlineCallbacks();
    renderWithInlineCallbacks();
    await flush();
    const firstTree = renderWithInlineCallbacks();
    const moviesBtn = all(firstTree, 'button').find(node => textOf(node).includes('movies'));
    assert.ok(moviesBtn, 'movies folder button rendered');
    moviesBtn.props.onClick();
    await flush();
    renderWithInlineCallbacks();
    assert.deepEqual(requests, ['/media', '/media/movies']);

    // Re-render several times with new inline callbacks: must NOT reset back to /media.
    renderWithInlineCallbacks();
    renderWithInlineCallbacks();
    await flush();
    assert.deepEqual(requests, ['/media', '/media/movies'], 'inline callbacks must not re-run initialPath read');

    // Submitting invokes the latest onChoose callback with /media/movies.
    const useBtn = all(renderWithInlineCallbacks(), 'Button').find(node => textOf(node) === defaultI18n.t('next.ownerLibraries.explorer.useFolder'));
    assert.ok(useBtn && !useBtn.props.disabled);
    useBtn.props.onClick();
    assert.equal(chosenPath, '/media/movies');
  } finally {
    h.dispose();
  }
});

test('ServerFolderPicker preserves Windows UNC roots, Windows drive roots, and POSIX breadcrumbs', () => {
  assert.deepEqual(folderPickerBreadcrumbs('\\\\nas\\media\\movies\\4k', '\\'), [
    {label: '\\\\nas\\media', full: '\\\\nas\\media'},
    {label: 'movies', full: '\\\\nas\\media\\movies'},
    {label: '4k', full: '\\\\nas\\media\\movies\\4k'},
  ]);
  assert.deepEqual(folderPickerBreadcrumbs('\\\\nas\\media\\', '\\'), [
    {label: '\\\\nas\\media', full: '\\\\nas\\media'},
  ]);
  assert.deepEqual(folderPickerBreadcrumbs('C:\\Media\\Movies', '\\'), [
    {label: 'C:', full: 'C:\\'},
    {label: 'Media', full: 'C:\\Media'},
    {label: 'Movies', full: 'C:\\Media\\Movies'},
  ]);
  assert.deepEqual(folderPickerBreadcrumbs('/mnt/media/movies', '/'), [
    {label: 'mnt', full: '/mnt'},
    {label: 'media', full: '/mnt/media'},
    {label: 'movies', full: '/mnt/media/movies'},
  ]);
});

test('ServerIntros allows adding a marker on desktop even when an episode has 0 markers', async () => {
  const h = createRenderer();
  const entry = {itemId: 'ep-1', number: 1, title: 'Pilot', durationUS: String(2400 * 1e6), state: 'complete', markers: []};
  const snap = {
    phase: 'ready',
    status: {mode: 'standard', libraries: [{libraryId: 'lib-1', name: 'TV', itemKind: 'show', mode: 'standard', choice: 'server'}], files: {complete: 1, pending: 0, failed: 0, unsupported: 0}, showsWaiting: 0, catchUpComplete: true, lastFullPassMS: 0, failures: []},
    libraryId: 'lib-1',
    titles: [{id: 'show-1', title: 'Test Show', kind: 'show', files: {complete: 1, pending: 0, failed: 0, unsupported: 0}, markers: {suggested: 0, automatic: 0, approved: 0}}],
    titlesLoading: false,
    titleId: 'show-1',
    title: {id: 'show-1', title: 'Test Show', kind: 'show', libraryId: 'lib-1', mode: 'standard', seasons: [{number: 1, title: 'Season 1', entries: [entry]}]},
    season: 1,
    titleLoading: false,
  };
  const source = {
    subscribe: () => () => {},
    getSnapshot: () => snap,
    load: async () => {},
    openLibrary: async () => {},
    openTitle: async () => {},
    selectSeason() {},
    createMarker: async () => true,
  };
  const mod = await componentModule(new URL('../src/screens/settings/server-intros/ServerIntros.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n},
    '../../../kit/controls': {Button: 'Button', Select: 'Select'},
    '../../../kit/Icon': {Icon: 'Icon'},
    '../../../kit/artwork': {Artwork: 'Artwork'},
    '../../../kit/shell': {CreateMarkerSheet: 'CreateMarkerSheet', SegmentWindows: 'SegmentWindows', StatTiles: 'StatTiles'},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'},
    '../../../kit/overlays/Popover': {Menu: 'Menu'},
    '../../../kit/overlays': {ToastHost: 'ToastHost'},
    './server-intros.module.css': {default: {}},
  });
  try {
    const render = () => tree(h.render(() => mod.ServerIntros({source, modeRows: null, onPreview() {}})));
    const first = render();
    assert.ok(all(first, 'Menu').length >= 1, 'EpisodeMenu renders on desktop');
    const addButton = all(first, 'button').find(node => textOf(node) === defaultI18n.t('web.segments.addTitle'));
    assert.ok(addButton, 'inline Add a marker button renders when episode has 0 markers');
    addButton.props.onClick();
    const afterClick = render();
    assert.ok(one(afterClick, 'CreateMarkerSheet'), 'clicking Add a marker opens CreateMarkerSheet');
  } finally {
    h.dispose();
  }
});

test('People uses enable confirmation copy for disabled accounts and applies inviteExpiryHours/inviteRole', async () => {
  const h = createRenderer();
  const memberRow = {accountId: 'u1', username: 'alex', role: 'member', disabled: true, status: 'off', revision: 1, limitsRevision: 1};
  const memberDetail = {
    ...memberRow,
    allowedLibraries: [],
    accountLimits: false,
    limits: {allowRemote: true, conversion: 'original', schedule: {windows: [], timezone: 'UTC'}, maxContentRating: '', allowUnrated: true, accessTokens: true, tagPolicy: {deniedLabels: []}},
  };
  const client = {
    read: async () => ({items: [memberRow], nextCursor: '', totalCount: 1, matchedCount: 1, limit: 40}),
    refresh: async () => ({items: [memberRow], nextCursor: '', totalCount: 1, matchedCount: 1, limit: 40}),
    person: async () => memberDetail,
    bulk: async () => {},
    dispose() {},
  };
  let inviteProps: any;
  let capturedTableProps: any;
  let capturedSpecial: ((row: any) => any) | undefined;
  const mod = await componentModule(new URL('../src/screens/settings/People.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n},
    '../../platform/freshness': {useFreshness: () => ({hub: {note() {}}})},
    './quiet-refresh': {SettingsOpenContext: h.react.createContext(true), mountQuietInput: () => ({dispose() {}, setOpen() {}, invalidate() {}})},
    './people-refresh': {peopleQuietInputs: () => []},
    '../../kit/settings': {
      SettingsTable: (props: any) => { capturedTableProps = props; return h.react.createElement('SettingsTable', props); },
      SettingsGroup: (props: any) => h.react.createElement('SettingsGroup', props, props.actions, props.children),
      SettingsPanel: (props: any) => h.react.createElement('SettingsPanel', props, props.tabs?.map((tab: any) => tab.content)),
      SettingsRows: (props: any) => { capturedSpecial = props.renderSpecial; return h.react.createElement('SettingsRows', props); },
      SettingsAction: 'SettingsAction',
      SettingsHours: 'SettingsHours',
      SettingsSelectionList: 'SettingsSelectionList',
      SettingsStringList: 'SettingsStringList',
      SettingsPageFailure: 'SettingsPageFailure',
      MemberResetCodeSheet: 'MemberResetCodeSheet',
    },
    '../../kit/controls': {Button: 'Button', RichSelect: 'RichSelect', Tabs: 'Tabs'},
    '../../kit/overlays/shared': {usePhone: () => false},
    '../../kit/overlays/Dialog': {Dialog: (props: any) => h.react.createElement('Dialog', props, props.children, props.footer)},
    '../../kit/shell/SettingsSurfaces': {SettingsRow: 'Row'},
    '../../kit/accounts': {InvitationShortCodeDialog: 'InvitationShortCodeDialog'},
    './people-table-row': {peopleTableRow: (value: any) => ({id: value.accountId, label: value.username, cells: {}})},
    './member-reset-code-view': {useMemberResetCodeView: () => undefined},
    './PeopleMemberProfiles': {PeopleMemberProfiles: 'PeopleMemberProfiles'},
    './PeopleInvitations': {PeopleInvitations: 'PeopleInvitations'},
    './PeopleDevices': {PeopleDevices: 'PeopleDevices'},
    './PeopleInvitationCard': {PeopleInvitationCard: 'PeopleInvitationCard'},
    './people-add-account': {AddAccountDialog: 'AddAccountDialog'},
    './people-invite': peopleInvite,
    './people-invite-form': {PeopleInviteForm: (props: any) => { inviteProps = props; return h.react.createElement('PeopleInviteForm', props); }},
    './people-tabs.module.css': {default: {}},
  });
  const bindings = {
    client,
    serverName: 'Server',
    libraries: [],
    actor: {accountId: 'owner', role: 'owner'},
    authority: {scope: {serverId: 's', authority: 'local', accountId: 'owner', profileId: 'p', sessionFamilyId: 'f'}, isCurrent: () => true, subscribe: () => () => {}, onAuthorityChanged() {}},
    invitations: {list: async () => ({items: []}), create: async () => ({}), shortCode: async () => ({})},
    inviteExpiryHours: () => 72,
    inviteRole: () => 'admin' as const,
    openHome() {},
    reportError(error: unknown) { throw error; },
  };
  const render = () => tree(h.render(() => h.react.createElement(mod.PeopleScreen, {bindings})));
  const g = globalThis as {document?: unknown; HTMLElement?: unknown};
  const hadDoc = g.document, hadEl = g.HTMLElement;
  g.document = {activeElement: null};
  g.HTMLElement = class {};
  try {
    render();
    await new Promise(resolve => setTimeout(resolve, 10));
    await flush();
    render();

    // Open invite form and verify inviteExpiryHours & inviteRole took effect.
    const inviteButton = all(render(), 'Button').find(node => node.props.icon === 'i-plus');
    assert.ok(inviteButton);
    inviteButton.props.onClick({currentTarget: {}});
    render();
    assert.equal(inviteProps.open, true);
    assert.equal(inviteProps.choices.expiresInHours, 72);
    assert.equal(inviteProps.choices.role, 'admin');

    // Open disabled person and click Turn on -> confirmation must NOT show disable wording.
    capturedTableProps.rowAction.onAction({id: 'u1'}, null);
    render();
    await flush();
    render();
    assert.ok(capturedSpecial);
    const turnOnAction = capturedSpecial({id: 'people.turnOff', control: {type: 'action', label: 'next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.control.label'}});
    turnOnAction.props.onClick();
    const afterEnable = render();
    const dialog = one(afterEnable, 'Dialog');
    const body = textOf(dialog);
    assert.ok(body.includes(defaultI18n.t('next.settings.letBackInBody')), 'enable confirmation shows re-enable copy');
    assert.ok(!body.includes('Turn off the selected people'), 'enable confirmation must not show turn-off copy');
    assert.ok(body.includes(defaultI18n.t('next.settings.turnOn')), 'enable confirmation button says Turn on');
  } finally {
    g.document = hadDoc;
    g.HTMLElement = hadEl;
    h.dispose();
  }
});

test('RemoteSourcesPage uses string drafts for numeric inputs and aborts/clears errors in FolderStep', () => {
  const remoteSrc = read('../src/screens/settings/remote-sources/RemoteSourcesPage.tsx');
  assert.match(remoteSrc, /const \[connectionsDraft, setConnectionsDraft\] = useState\(String\(limits\.connections\)\)/);
  assert.match(remoteSrc, /const \[perSecondDraft, setPerSecondDraft\] = useState\(String\(limits\.perSecond\)\)/);
  assert.match(remoteSrc, /remoteSourceFolders\(api, source\.id, path, controller\.signal\)/);
  assert.match(remoteSrc, /setError\(''\);\s*void remoteSourceFolders/);
  assert.match(remoteSrc, /return \(\) => \{controller\.abort\(\);\};/);

  const channelSrc = read('../src/screens/settings/server/channels/OwnerChannelEditor.tsx');
  assert.match(channelSrc, /avoidRepeatsDraft,setAvoidRepeatsDraft\]=useState\(String\(avoidRepeatsValue\)\)/);
  assert.match(channelSrc, /maxKbpsDraft,setMaxKbpsDraft\]=useState\(String\(maxKbpsValue\)\)/);
});

test('Webhooks renders create/edit/confirm/log errors visibly inside open dialogs', async () => {
  const h = createRenderer();
  let snap: any = {phase: 'ready', list: {items: []}, busy: false, uncertain: false};
  const listeners = new Set<() => void>();
  const source = {
    subscribe: (fn: () => void) => { listeners.add(fn); return () => listeners.delete(fn); },
    getSnapshot: () => snap,
    load: async () => {},
    reconcile: async () => {},
    create: async () => {
      snap = {...snap, phase: 'error', uncertain: true, error: 'write'};
      for (const fn of listeners) fn();
      return undefined;
    },
    update: async () => undefined,
    remove: async () => undefined,
    rotate: async () => undefined,
    test: async () => undefined,
    select: async () => {},
    close() {},
  };
  const mod = await componentModule(new URL('../src/screens/settings/server/integrations/Webhooks.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n},
    '../../../../kit/controls': {Button: 'Button', Switch: 'Switch', TextField: 'TextField'},
    '../../../../kit/Icon': {Icon: 'Icon'},
    '../../../../kit/overlays/Dialog': {Dialog: (props: any) => props.open ? h.react.createElement('Dialog', props, props.children, props.footer) : null},
    '../../../../kit/overlays/shared': {usePhone: () => false},
    '../../../../kit/overlays/Popover': {Menu: 'Menu'},
    '../../../../kit/frames': {StateRow: 'StateRow'},
    './integrations.module.css': {default: {}},
  });
  const render = () => tree(h.render(() => mod.WebhooksPanel({source, isCurrent: () => true})));
  try {
    const first = render();
    const addBtn = all(first, 'Button').find(node => textOf(node) === defaultI18n.t('api.webhooks.add'));
    assert.ok(addBtn);
    addBtn.props.onClick();
    const openModal = one(render(), 'Dialog');
    // Trigger failed save via state becoming error/uncertain while modal is open.
    await source.create();
    const afterFail = one(render(), 'Dialog');
    assert.ok(openModal && afterFail);
    const alertInsideModal = all(tree(afterFail), 'p').find(node => node.props.role === 'alert');
    assert.ok(alertInsideModal, 'error alert is rendered inside the open create/edit Dialog');
  } finally {
    h.dispose();
  }
});

test('SupportPanel renders fixPermissions failure visibly via presentError + role="alert"', async () => {
  const h = createRenderer();
  const api = {
    request: async <T>(path: string) => {
      if (path === '/v1/admin/capabilities') return {items: []} as T;
      if (path === '/v1/admin/data-folder/permissions') return {path: '/config', owner: 'portico', account: 'portico', readableByOthers: true, fixable: true, checked: true} as T;
      if (path === '/v1/admin/data-folder/permissions/fix') throw Object.assign(new Error('chmod failed'), {status: 500, code: 'internal'});
      throw new Error('unexpected');
    },
    downloadSupportExport: async () => new Blob(),
  };
  const source = new SettingsSupportSource(api, () => true);
  const mod = await componentModule(new URL('../src/screens/settings/server/troubleshooting/Support.tsx', import.meta.url), {
    react: withReact(h),
    '@i18n': {defaultI18n},
    '@core/presentation/errors.ts': errorsCore,
    '../../../../kit/controls': {Button: 'Button', Select: 'Select', Switch: 'Switch'},
    '../../../../kit/settings': {SettingsAction: 'SettingsAction', SettingsAdvanced: 'SettingsAdvanced', SettingsGroup: 'SettingsGroup'},
    '../../../../kit/shell/SettingsSurfaces': {SettingsRow: 'SettingsRow'},
    '../../hardware/hardware-rows': {hardwareMethodName: () => undefined},
    './support-source': supportSourceMod,
    './support-options': supportOptionsMod,
    './troubleshooting.module.css': {default: {}},
  });
  try {
    await source.load();
    await source.fixPermissions();
    const rendered = tree(h.render(() => mod.SupportPanel({source, subscribe: source.subscribe, reportError() {}})));
    const alert = all(rendered, 'p').find(node => node.props.role === 'alert');
    assert.ok(alert, 'fixPermissions failure renders a role="alert" message');
    assert.equal(textOf(alert), errorsCore.presentError({status: 500, code: 'internal'}, 'server-console', {operation: 'action', i18n: defaultI18n}).body);
  } finally {
    source.dispose();
    h.dispose();
  }
});

test('Reports defers URL.revokeObjectURL by 60s and wires returnFocusRef to openers, not internal Done button', () => {
  const reportsSrc = read('../src/screens/settings/server/troubleshooting/Reports.tsx');
  assert.match(reportsSrc, /setTimeout\(\(\) => URL\.revokeObjectURL\(url\), 60_000\)/);
  assert.doesNotMatch(reportsSrc, /returnFocusRef=\{done\}/);
  assert.match(reportsSrc, /returnFocusRef=\{readOpener\}/);
  assert.match(reportsSrc, /returnFocusRef=\{deleteOpener\}/);
});

test('Dashboard keeps Now Playing visible when secondary reads fail and wires update alert action', async () => {
  const h = createRenderer();
  let openSettingsTarget = '';
  const sessionsData = {
    items: [{id: 's1', user: 'alex', profile: 'Alex', device: 'TV', title: 'Heat', kind: 'vod', state: 'playing', positionMs: 1000, location: 'local', startedAt: '2026-11-01T00:00:00Z', decision: {}}],
    nextCursor: '',
    scope: {serverId: 's', viewerFence: ''},
    totals: {streams: 1, atHome: 1, away: 0, paused: 0, converting: 0, people: 1, bandwidthKbps: 5000},
  };
  const statusData = {
    alerts: [{code: 'update_available', severity: 'info', major: false, params: {latest: '2.0.0'}, action: {kind: 'update', target: ''}}],
    scans: [],
    tasks: {count: 0, items: []},
  };
  let readCall = 0;
  const mod = await componentModule(new URL('../src/screens/dashboard/Dashboard.tsx', import.meta.url), {
    react: withReact(h),
    '@i18n': {defaultI18n},
    '../../kit/controls': {Button: 'Button'},
    '../../kit/overlays': {Dialog: 'Dialog', ToastHost: 'ToastHost'},
    '../../kit/overlays/shared': {usePhone: () => false},
    '../../kit/Icon': {Icon: 'Icon'},
    './model': dashboardModel,
    './format': dashboardFormat,
    './shared': {Range: 'Range', ReadFeedback: 'ReadFeedback', Section: (props: any) => h.react.createElement('Section', props, props.children), t: defaultI18n.t},
    './use-read': {
      useDashboardAuthority: () => true,
      useDashboardAccounts: () => ({loading: false, failed: true, items: [], refresh() {}}),
      useDashboardRead: () => {
        const index = readCall++;
        // 0: status (has update alert), 1: sessions (loaded), 2: live (FAILED!)
        if (index % 6 === 0) return {loading: false, failed: false, value: statusData, refresh() {}};
        if (index % 6 === 1) return {loading: false, failed: false, value: sessionsData, refresh() {}};
        return {loading: false, failed: true, value: undefined, refresh() {}};
      },
    },
    './Chart': {Chart: 'Chart'},
    './SessionCard': {SessionCard: 'SessionCard'},
    './BusySessions': {BusySessions: 'BusySessions'},
    './PhoneHealth': {PhoneHealth: 'PhoneHealth'},
    './DashboardQuiet': {DashboardQuiet: 'DashboardQuiet'},
    './Analytics': {Analytics: 'Analytics'},
    './pages': {readNowPlaying: async () => sessionsData},
    './phone': {PhonePage: 'PhonePage', PhoneStatistics: 'PhoneStatistics'},
    '../../kit/shell/Activity': {ProgressRing: 'ProgressRing'},
    '../../kit/layout/motion': {useLayoutMotion: () => ({reduced: true})},
    '../../kit/layout/presence': {useLayoutPresence: (items: any[]) => ({present: items, leaving: new Set(), root: {current: null}})},
    '../../kit/layout/use-list-removal-focus': {useListRemovalFocus: () => ({root: {current: null}})},
    './dashboard.module.css': {default: {}},
    './dashboard-quiet.module.css': {default: {}},
  });
  const bindings = {
    scope: {serverId: 's', viewerId: 'v'},
    role: 'owner',
    context: {timeZone: 'UTC', until: '2026-11-01T00:00:00Z'},
    isCurrent: () => true,
    isMainProfile: () => true,
    read: {status: async () => statusData, telemetry: async () => ({}), server: async () => ({facts: []})},
    effects: {openHome() {}, openHistory() {}, openSettings: (section: string) => { openSettingsTarget = section; }, reportError() {}},
    artwork: () => undefined,
  };
  try {
    const rendered = tree(h.render(() => mod.DashboardScreen({bindings})));
    assert.equal(all(rendered, 'SessionCard').length, 1, 'Now Playing card stays rendered even when live telemetry & accounts fail');
    const updateBtn = all(rendered, 'Button').find(node => textOf(node).includes(defaultI18n.t('next.dashboard.update')));
    assert.ok(updateBtn, 'Update alert action button renders');
    updateBtn.props.onClick();
    assert.equal(openSettingsTarget, 'general', 'clicking Update alert opens Server > General settings');
  } finally {
    h.dispose();
  }
});

test('Analytics Busiest heatmap uses single roving tabIndex and arrow-key navigation', () => {
  const summary = {
    window: 100,
    people: [],
    titles: [],
    busiest: Array.from({length: 7}, (_, d) => Array.from({length: 24}, (_, h) => d + h)),
  };
  const html = renderToStaticMarkup(createElement(Busiest, {summary}));
  const tab0 = [...html.matchAll(/tabIndex="0"|tabindex="0"/g)];
  const tabNeg1 = [...html.matchAll(/tabIndex="-1"|tabindex="-1"/g)];
  assert.equal(tab0.length, 1, 'exactly one roving tabIndex=0 cell');
  assert.equal(tabNeg1.length, 167, 'remaining 167 cells use tabIndex=-1');
  assert.doesNotMatch(html, /role="button"/, 'no 168 individual role="button" tab stops');
  assert.match(html, /role="grid"/);
  assert.match(html, /role="gridcell"/);
});

test('History does not reset pagination back to page 1 on session.updated while browsing older pages', async () => {
  const h = createRenderer();
  const listeners = new Map<string, Set<() => void>>();
  const reads: string[] = [];
  const mod = await componentModule(new URL('../src/screens/dashboard/History.tsx', import.meta.url), {
    react: withReact(h),
    '../../kit/settings': {
      SettingsGroup: (props: any) => h.react.createElement('SettingsGroup', props, props.children),
      SettingsTable: 'SettingsTable',
    },
    './model': dashboardModel,
    './format': dashboardFormat,
    './use-read': {
      useDashboardAuthority: () => true,
      useDashboardAccounts: () => ({loading: false, failed: false, items: [], refresh() {}}),
      useDashboardRead: (_b: any, load: any, _enabled: boolean, _everyMs: number, changes: readonly string[] = []) => {
        const key = changes.join(',');
        reads.push(key);
        return {
          loading: false,
          failed: false,
          value: {entries: [{id: 'play-1', title: 'Heat', kind: 'movie', user: 'alex', profile: 'alex', accountId: 'a1', device: 'TV', startedAt: '2026-11-01T00:00:00Z', completed: true, durationSeconds: 6000}], nextCursor: 'page-2'},
          refresh() { void load; },
        };
      },
    },
    './shared': {mediaLabel: (v: string) => v, ReadFeedback: 'ReadFeedback', t: defaultI18n.t},
    './dashboard.module.css': {default: {}},
  });
  const bindings = {
    scope: {serverId: 's', viewerId: 'v'},
    role: 'owner',
    context: {timeZone: 'UTC', until: '2026-11-01T00:00:00Z'},
    isCurrent: () => true,
    isMainProfile: () => true,
    events: {
      on: (type: string, fn: () => void) => {
        if (!listeners.has(type)) listeners.set(type, new Set());
        listeners.get(type)!.add(fn);
        return () => listeners.get(type)?.delete(fn);
      },
      onResync: () => () => {},
    },
    read: {plays: async () => ({entries: [], nextCursor: ''})},
    effects: {openHome() {}, openHistory() {}, openSettings() {}, reportError() {}},
    artwork: () => undefined,
  };
  const render = () => tree(h.render(() => mod.HistoryScreen({bindings})));
  try {
    const first = render();
    assert.equal(reads.at(-1), 'playback.history,stream.resync', 'page 1 subscribes only to playback.history and stream.resync, not session.updated');
    const table = one(first, 'SettingsTable');
    table.props.onLoadMore();
    render();
    assert.equal(reads.at(-1), '', 'older page disables background event churn');
    assert.equal(listeners.get('session.updated')?.size ?? 0, 0, 'HistoryTable does not attach a session.updated cursor-reset listener');
  } finally {
    h.dispose();
  }
});

test('Chart guards degenerate from === until ranges without producing NaN coordinates', () => {
  const stamp = Date.UTC(2026, 10, 12, 12, 0, 0);
  const iso = new Date(stamp).toISOString();
  const interval = {period: '24h', timeZone: 'UTC', from: iso, until: iso, buckets: [{start: iso, end: iso, partial: false}]};
  const series = [{key: 'cpu', label: 'Portico', points: [{t: stamp, v: 42}]}];
  const html = renderToStaticMarkup(createElement(Chart, {kind: 'processor', interval, title: 'Processor', series, format: (v: number) => `${v}%`}));
  assert.doesNotMatch(html, /NaN|Infinity/, 'degenerate interval produces no NaN or Infinity SVG coordinates');
  assert.match(html, /<circle cx="[\d.]+" cy="[\d.]+"/);
});
