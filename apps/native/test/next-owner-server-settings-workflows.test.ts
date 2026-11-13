import {routePath} from '../../../packages/contracts/src/route-paths.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {bindingModule, formsFixture as untypedFormsFixture, lifecycleHooks, maintenance, nodes, content, specification, t, i18n, theme} from './helpers/owner-server-settings.mjs';
import * as values from '../src-next/screens/settings-live/server/settings-values.ts';
import {secureConnectionView} from '../../../packages/client-core/src/server-admin/secure-connection.ts';
import {certificateProblemMessage, retryCertificate} from '../../../packages/client-core/src/certificates.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';

import type {DraftForm} from '../../../packages/client-core/src/server-admin/draft-form.ts';
// The JavaScript fixture builds this dictionary from the supplied form IDs.
type Fixture = Awaited<ReturnType<typeof untypedFormsFixture>> & {group: {forms: Record<string, DraftForm<any, any>>}};
const formsFixture = (...args: Parameters<typeof untypedFormsFixture>) => untypedFormsFixture(...args) as Promise<Fixture>;

const bindings = await bindingModule();
const settle = () => new Promise(resolve => setImmediate(resolve));
const runtime = {text: {t}, reportError: (error: unknown) => {throw error;}, error: () => ({title: 'Error'})};
const chrome = {'./NetworkInterfaceRow': {NativeNetworkInterfaceRow: 'NetworkInterfaceRow'}, 'react-native': {ScrollView: 'ScrollView', View: 'View', Text: 'Text'}, './RefusedAddresses': {NativeRefusedAddresses: 'NativeRefusedAddresses'}, './SecuritySummary': {NativeSecuritySummary: 'NativeSecuritySummary'}, '../../../kit/shell/ServerIdentity': {ServerHeader: 'ServerHeader'}, './page-layout': {settingsPageInset: () => ({}), settingsPageHeading: () => ({})}, '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => theme}, '../../../kit/states': {StateView: 'StateView'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/controls': {Button: 'Button', Select: 'Select', Switch: 'Switch', TextField: 'TextField'}, './AdvancedSection': {NativeAdvancedSection: 'Advanced'}, './SettingControl': {NativeSettingControl: 'Setting'}, './settings-values': values};

test('General exposes backed Advanced controls and one Background speed setting, with draft-aware update checks', async () => {
  const feed = 'https://example.invalid/releases';
  const fixture = await formsFixture({runtime: {name: 'Server', 'updates.feedUrl': feed}, maintenance: maintenance(), alerts: {storageWarningPercent: 15, storageCriticalPercent: 5, certificateWarningDays: 14, serverNoticesTo: 'owner'}}), host = lifecycleHooks();
  const configuration = {path: '/config/portico.toml', fields: [{id: 'general.updateFeed', current: feed, effective: feed, default: feed, configOnly: false}]};
  const api = {request: async (path: string) => path === routePath('get_settings_configuration') ? configuration : {version: '0.0-test', newer: false, install: 'manual', checkedAt: '2026-10-01T00:00:00Z'}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/GeneralPage.tsx', import.meta.url), {
    ...chrome, react: host.react, '../use-settings-request-scope': {useSettingsRequestScope: () => ({api, lifetime: {current: fixture.group.context.current}})},
    '../../../../../../packages/client-core/src/server-admin/server-forms': {pauseBackgroundWork: assert.fail}, '../../../../../../packages/client-core/src/backups': {restartServer: assert.fail},
    '../../../../../../packages/contracts/src/routes': {routePath}, '../../../../../../packages/client-core/src/settings-configuration': {parseSettingsConfiguration: (value: unknown) => value}, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings, '../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    '../../../../../../packages/i18n/src': {defaultI18n: i18n}, '../../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'Error'})}, '../../../../../../packages/client-core/src/server-admin/console-words': {when: String},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/overlays/BottomSheet': {BottomSheet: 'BottomSheet'}, './HeavyWorkControls': {HeavyWorkControls: 'Heavy'}, './BroadcastSheet': {NativeBroadcastSheet: 'Broadcast'}, './storage/forms': fixture.hooks,
  });
  const render = () => host.render(() => {const outer = module.NativeServerGeneral({ports: {context: fixture.group.context}, runtime});return outer.type(outer.props);});
  render();host.flush();await settle();
  const controls = () => nodes(render()).filter((node: any) => node.type === 'Setting');
  for (const id of ['general.updateChecks', 'general.backgroundSpeed', 'general.updateFeed']) assert.ok(controls().some((node: any) => node.props.row.id === id), id);
  // The alert thresholds (critical 5 below warning 15 is valid) and who sees each notice are Advanced rows.
  for (const id of ['general.alertStorageWarn', 'general.alertStorageCritical', 'general.alertCertificateDays', 'general.serverNoticesTo']) assert.ok(controls().some((node: any) => node.props.row.id === id), id);
  controls().find((node: any) => node.props.row.id === 'general.serverNoticesTo').props.onChange('owner-and-admins');
  assert.equal(fixture.group.forms.alerts.getSnapshot().draft.serverNoticesTo, 'owner-and-admins');
  assert.equal(fixture.group.forms.alerts.getSnapshot().problem, undefined);
  assert.equal(nodes(render()).filter((node: any) => node.type === 'Advanced').length, 1);
  controls().find((node: any) => node.props.row.id === 'general.backgroundSpeed').props.onChange('fast');assert.equal(fixture.group.forms.maintenance.getSnapshot().draft.backgroundSpeed, 'fast');
  controls().find((node: any) => node.props.row.id === 'general.updateChecks').props.onChange('never');
  assert.equal(controls().find((node: any) => node.props.row.id === 'general.updateChecks').props.value, 'never');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft['updates.feedUrl'], '');
  host.dispose();fixture.stop();
});

test('Storage custom limits open an exact-value editor and a rejected edit never changes the draft', async () => {
  const fixture = await formsFixture({runtime: {analysisStorageGiB: 40, playHistoryDays: 0, jobDays: 7, diagnosticDays: 7}, maintenance: maintenance(), logs: {retention: []}}, {maintenance: {storageCategories: [], retentionMaxima: {}}}), host = hooks();
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/storage/LimitsTab.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../../../packages/client-core/src/presentation/settings-bindings': bindings, '../../../../../../../packages/client-core/src/presentation/settings-spec': specification, '../../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../../kit/controls': {Button: 'Button', Select: 'Select', TextField: 'TextField'}, '../../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../../kit/shell/visual': {shellType: () => ({})}, '../../../../kit/theme': {useTheme: () => theme}, '../SettingControl': {NativeSettingControl: 'Setting'}, '../settings-values': values, './space-source': {nativeStorageOwner: (context: any) => context.current()}, './forms': fixture.hooks,
  });
  const opened: string[] = [];
  const render = () => host.render(() => module.LimitsTab({group: fixture.group, t, onOpenPage: (page: string) => opened.push(page)})), find = (id: string) => nodes(render()).find((node: any) => node.props.testID === id);
  assert.equal(find('storage.analysisSpace').props.value, 'custom');assert.ok(find('storage.analysisSpace').props.options.some((option: any) => option.value === 'custom-edit'));
  find('storage.analysisSpace').props.onValueChange('custom-edit');
  const field = () => nodes(render()).find((node: any) => node.type === 'TextField');field().props.onChangeText('0');
  const save = () => nodes(find('server-storage-number').props.footer).find((node: any) => node.type === 'Button' && content(node) === 'action.save');
  assert.equal(save().props.disabled, true);save().props.onPress();assert.equal(fixture.group.forms.runtime.getSnapshot().draft.analysisStorageGiB, 40);
  field().props.onChangeText('48');assert.equal(save().props.disabled, false);save().props.onPress();assert.equal(fixture.group.forms.runtime.getSnapshot().draft.analysisStorageGiB, 48);
  const links = nodes(render()).filter((node: any) => node.type === 'Button' && ['next.settingsSpec.server.storage.advanced.groups.0.rows.0.control.label', 'next.settingsSpec.server.storage.advanced.groups.0.rows.2.control.label'].includes(content(node)));
  assert.equal(links.length, 2); links.forEach((node: any) => node.props.onPress()); assert.deepEqual(opened, ['history', 'troubleshooting']);
  assert.ok(!nodes(render()).some((node: any) => node.props.row?.id === 'troubleshooting.keepLogs'));
  fixture.stop();
});

test('Remote access edits the certificate switch, keeps own address on main, and retries renewal failure once', async () => {
  const certificate = {configured: true, authorityId: 'a'.repeat(64), config: {enabled: true, publicAddress: '', publicPort: 32500, revision: '1'}, state: 'renewing', errorCode: 'renewal_failed', environment: 'production', tlsReady: true, publiclyTrusted: true, listenerBound: true, listenPort: 32500, reachability: 'probe_required'};
  const connectivity = {ownAddressEnabled: false, customCertificateDomain: '', customCertificatePath: '', customCertificateKeyPath: '', remoteSignInPolicy: 'allow', lanDiscoveryEnabled: true, dlnaEnabled: false, accessUrls: [], trustedProxies: [], advertiseUrl: '', lanNetworks: [], connectionLimit: 0};
  const fixture = await formsFixture({remote: {enabled: true, publicPort: 32500, gateway: '', mapping: true, pcp: true, natpmp: true, upnp: true}, connectivity, certificate: {...certificate.config, claimed: true}}, {connectivity: {tls: {}}, certificate}), host = lifecycleHooks();
  let retries = 0, finish: ((value: unknown) => void) | undefined;
  const api = {request: async (path: string) => {assert.equal(path, routePath('post_networking_certificate_retry'));retries++;return new Promise(resolve => {finish = resolve;});}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/RemoteAccessPage.tsx', import.meta.url), {
    ...chrome, react: host.react, '../../../../../../packages/client-core/src/server-admin/secure-connection': {secureConnectionView}, '../../../../../../packages/client-core/src/server-admin/status-words': {remoteStatusLine: () => ({tone: 'neutral', text: 'Route status'}), ownCertificateLine: () => undefined},
    '../../../../../../packages/client-core/src/remote-access': {remoteWarningLines: () => []},
    '../../../../../../packages/client-core/src/certificates': {certificateProblemMessage, retryCertificate}, '../../../../../../packages/contracts/src/routes': {routePath}, '../use-settings-request-scope': {useSettingsRequestScope: () => ({api, lifetime: {current: fixture.group.context.current}})},
    '../../../../../../packages/client-core/src/presentation/settings-spec': specification, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings, './storage/forms': fixture.hooks,
  });
  const render = () => host.render(() => {const outer = module.NativeRemoteAccessPage({ports: {context: fixture.group.context}, runtime});return outer.type(outer.props);});
  assert.ok(!nodes(render()).some((node: any) => node.props.testID === 'remote-services-enabled'), 'local-only server has no Services control');
  assert.ok(nodes(render()).some((node: any) => node.type === 'TextField' && node.props.value === connectivity.customCertificateDomain));
  (fixture.group.context.account.account as any).hostedAccountId = 'connected-owner';
  const retry = nodes(render()).find((node: any) => node.props.testID === 'remote-services-retry');assert.ok(retry, 'renewal errors remain actionable even while previous TLS is serving');
  retry.props.onPress();retry.props.onPress();assert.equal(retries, 1);finish!(certificate);await settle();
  nodes(render()).find((node: any) => node.props.testID === 'remote-services-enabled').props.onCheckedChange(false);
  assert.equal(fixture.group.forms.certificate.getSnapshot().draft.enabled, false);assert.equal(fixture.group.forms.remote.getSnapshot().draft.enabled, true);
  host.dispose();fixture.stop();
});

// Storage › Backups on the rebuilt server (ServerBackupSource, as the web's Backups.tsx).
async function backupsFixture() {
  const host = lifecycleHooks(), real = await import('../../../packages/client-core/src/server-admin/server-backups.ts');
  // The tab's own parts (restore, passphrase, waiting restore) each render on a hook host of their own.
  let active = host;
  const children = new Map<string, ReturnType<typeof lifecycleHooks>>();
  const react: any = Object.fromEntries(Object.keys(host.react).filter(key => key !== 'default').map(key => [key, (...args: any[]) => (active.react as any)[key](...args)]));
  react.default = react;
  const expand = (value: any): any => Array.isArray(value) ? value.map(expand) : !value || typeof value !== 'object' || !value.props ? value
    : typeof value.type === 'function' ? part(value) : {...value, props: Object.fromEntries(Object.entries(value.props).map(([key, child]) => [key, typeof child === 'object' ? expand(child) : child]))};
  const part = (element: any) => {
    let child = children.get(element.type.name);
    if (!child) {child = lifecycleHooks(); children.set(element.type.name, child);}
    const previous = active; active = child;
    try {const out = child.render(() => element.type(element.props)); child.flush(); return expand(out);} finally {active = previous;}
  };
  const item = {id: 'backup-1', createdAt: '2026-10-01T00:00:00Z', scheduled: true, version: '0.1-test', size: 1024, protected: true};
  const state: any = {busy: false, list: [real.backupSummary(item as any)], settings: {days: [], hour: 3, keep: 7, folder: '/backups', includeArtwork: false},
    backups: {items: [item], folder: '/backups', folderWritable: true, protected: false, readable: true, readableMeans: []}};
  const calls: any[] = [];
  let inspectAnswer: (target: unknown, phrase?: string) => Promise<unknown> = async () => ({madeHere: true, restoring: ['server_restarts']});
  class Source {
    constructor(_api: unknown, _current: unknown, private notify: () => void) {}
    getSnapshot = () => state; load = async () => {}; dispose() {}
    backupNow = async () => {calls.push(['backupNow']);};
    remove = async (id: string) => {calls.push(['remove', id]);};
    setFolder = async (folder: string) => {calls.push(['setFolder', folder]);};
    saveSettings = async (change: unknown) => {calls.push(['saveSettings', change]);};
    setPassphrase = async (phrase: string) => {calls.push(['setPassphrase', phrase]);};
    inspect = (target: unknown, _signal?: AbortSignal, phrase?: string) => {calls.push(['inspect', target, phrase]); return inspectAnswer(target, phrase);};
    restore = async (target: unknown, proof: unknown) => {calls.push(['restore', target, proof]); return {};};
    restartAsks = async () => {calls.push(['restartAsks']); return {playing: 2, canRestart: true};};
    restart = async () => {calls.push(['restart']); return true;};
    cancelRestore = async () => {calls.push(['cancelRestore']);};
  }
  const schedule = await screenModule(new URL('../src-next/screens/settings-live/server/storage/BackupSchedule.tsx', import.meta.url), {
    react, 'react-native': {View: 'View'},
    '../../../../../../../packages/client-core/src/presentation/settings-bindings': await import('../../../packages/client-core/src/presentation/settings-bindings.ts'),
    '../../../../../../../packages/client-core/src/presentation/settings-spec': await import('../../../packages/client-core/src/presentation/settings-spec.ts'),
    '../../../../kit/controls': {Button: 'Button'}, '../SettingControl': {NativeSettingControl: 'SettingControl'},
  });
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/storage/BackupsTab.tsx', import.meta.url), {
    './BackupSchedule': schedule,
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../../packages/client-core/src/server-admin/server-backups': {...real, ServerBackupSource: Source},
    '../../../../../../../packages/client-core/src/server-admin/restore-gate': await import('../../../packages/client-core/src/server-admin/restore-gate.ts'),
    '../../../../../../../packages/client-core/src/server-admin/console-words': {when: String},
    '../../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch', TextField: 'TextField'}, '../../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'},
    '../../owner-libraries/FolderExplorer': {FolderExplorer: 'FolderExplorer'}, '../AdvancedSection': {NativeAdvancedSection: 'Advanced'},
    '../../../../kit/shell/visual': {shellType: () => ({})}, '../../../../kit/theme': {useTheme: () => theme}, './space-source': {nativeStorageOwner: () => true},
  });
  const ports = {context: {api: {request: async () => ({})}}};
  const render = () => expand(host.render(() => {const outer = module.BackupsTab({ports, runtime, t, advancedContent: {type: 'Limits', props: {}}}); return outer ? outer.type(outer.props) : null;}));
  render(); host.flush(); render(); host.flush();
  const sheet = (title: string) => nodes(render()).find((node: any) => node.type === 'BottomSheet' && node.props.title === title);
  const footerButton = (title: string, label: string) => nodes(sheet(title).props.footer).find((node: any) => node.type === 'Button' && content(node) === label);
  const field = (title: string, label: string) => nodes(sheet(title)).find((node: any) => node.type === 'TextField' && node.props.label === label);
  return {host, state, render, calls, sheet, footerButton, field, answer: (next: typeof inspectAnswer) => {inspectAnswer = next;}, stop: () => {for (const child of children.values()) child.dispose(); host.dispose();}};
}

test('Backups lists what the rebuilt server says and keeps typed deletion behind the row menu', async () => {
  const f = await backupsFixture(), copy = content(nodes(f.render()));
  for (const text of ['/backups', 'web.maintenance.backupKindScheduled', 'server.backups.madeBy', 'server.backups.protected']) assert.ok(copy.includes(text), text);
  assert.equal(nodes(f.render()).filter((node: any) => node.type === 'Advanced').length, 1);
  nodes(f.render()).find((node: any) => node.props.testID === 'server-backup-backup-1').props.onPress();
  nodes(f.render()).find((node: any) => node.type === 'Menu').props.items.find((item: any) => item.id === 'delete').onSelect();
  const erase = () => f.footerButton('web.maintenance.deleteBackupTitle', 'action.delete');
  assert.equal(erase().props.disabled, true);
  nodes(f.sheet('web.maintenance.deleteBackupTitle')).find((node: any) => node.type === 'TextField').props.onChangeText('delete');
  erase().props.onPress(); await settle();
  assert.deepEqual(f.calls.filter(call => call[0] === 'remove'), [['remove', 'backup-1']]);
  f.stop();
});

test('A protected backup opens with its passphrase, then restores with the word, password and phrase, which are not kept', async () => {
  const f = await backupsFixture();
  nodes(f.render()).find((node: any) => node.props.testID === 'server-backup-backup-1').props.onPress();
  nodes(f.render()).find((node: any) => node.type === 'Menu').props.items.find((item: any) => item.id === 'restore').onSelect();
  f.render(); f.host.flush();
  const title = 'web.maintenance.restoreTitle', phrase = 'next.settings.backup.passphrase';
  assert.equal(f.calls.some(call => call[0] === 'inspect'), false, 'a listed protected backup asks for its phrase before any check');
  f.field(title, phrase).props.onChangeText('test-only-phrase');
  nodes(f.sheet(title)).find((node: any) => node.type === 'Button' && content(node) === 'server.backups.openBackup').props.onPress(); await settle();
  assert.deepEqual(f.calls.find(call => call[0] === 'inspect'), ['inspect', {id: 'backup-1'}, 'test-only-phrase']);
  const confirm = () => f.footerButton(title, 'web.maintenance.restoreConfirm');
  assert.equal(confirm().props.disabled, true);
  f.field(title, 'web.deleteAccount.typeLabel').props.onChangeText('server.backups.restoreWord');
  f.field(title, 'server.backups.password').props.onChangeText('test-only-password');
  assert.equal(confirm().props.disabled, false);
  confirm().props.onPress(); await settle();
  assert.deepEqual(f.calls.find(call => call[0] === 'restore'), ['restore', {id: 'backup-1'}, {password: 'test-only-password', code: undefined, notMadeHereConfirmed: false, passphrase: 'test-only-phrase'}]);
  assert.equal(f.sheet(title).props.open, false);
  f.stop();
});

test('A restore waiting for a restart asks the server first, then restarts; Cancel drops it', async () => {
  const f = await backupsFixture();
  f.state.backups = {...f.state.backups, pendingRestore: {backup: 'one.ptb', stagedAt: '2026-10-02T00:00:00Z'}};
  f.render(); f.host.flush(); await settle();
  const pending = () => nodes(f.render()).find((node: any) => node.props.testID === 'server-backup-pending');
  nodes(pending()).find((node: any) => node.type === 'Button' && content(node) === 'server.backups.restartNow').props.onPress(); await settle();
  f.footerButton('server.backups.restartTitle', 'server.backups.restartNow').props.onPress(); await settle();
  assert.ok(f.calls.some(call => call[0] === 'restart'));
  nodes(pending()).find((node: any) => node.type === 'Button' && content(node) === 'server.backups.cancelRestore').props.onPress(); await settle();
  assert.ok(f.calls.some(call => call[0] === 'cancelRestore'));
  assert.equal(nodes(f.render()).find((node: any) => node.props.testID === 'server-backup-now').props.disabled, true, 'no new backup while a restore waits');
  f.stop();
});

test('The passphrase is asked twice before it protects new backups, and turning it off asks first', async () => {
  const f = await backupsFixture(), protect = 'next.settings.backup.protectTitle';
  nodes(f.render()).find((node: any) => node.props.testID === 'server-backup-passphrase').props.onCheckedChange(true);
  const save = () => f.footerButton(protect, 'next.settings.backup.protect');
  f.field(protect, 'next.settings.backup.passphrase').props.onChangeText('test-only-phrase');
  f.field(protect, 'next.settings.backup.repeat').props.onChangeText('test-only-other');
  assert.equal(save().props.disabled, true);
  f.field(protect, 'next.settings.backup.repeat').props.onChangeText('test-only-phrase');
  save().props.onPress(); await settle();
  assert.deepEqual(f.calls.filter(call => call[0] === 'setPassphrase'), [['setPassphrase', 'test-only-phrase']]);
  f.state.backups = {...f.state.backups, protected: true};
  nodes(f.render()).find((node: any) => node.props.testID === 'server-backup-passphrase').props.onCheckedChange(false);
  f.footerButton('server.backups.passphraseOffTitle', 'server.backups.passphraseOff').props.onPress(); await settle();
  assert.deepEqual(f.calls.filter(call => call[0] === 'setPassphrase').at(-1), ['setPassphrase', '']);
  f.stop();
});

test('Streaming edits its folder and server-wide tone-map method', async () => {
  const memoryAvailable = false;
  const fixture = await formsFixture({runtime: {conversionMode: 'all', hardwareSelection: 'auto', hardwareDecode: true, hdrToneMapping: true, hdrToneMappingAlgorithm: 'hable', x264Preset: 'veryfast', planningPolicy: 'minimize_server_work', directStreamRemux: true, outputFormat: 'auto', maxConcurrentSessions: 0, maxHardwareSessions: 0, maxSoftwareSessions: 0, maxBackgroundSessions: 0, serverCap: 0, throttleBufferMs: 60000, playedRetentionMs: 180000, sessionStorageMiB: 4096, memoryCap: 'quarter', transcodeLocation: 'disk', ffmpegPath: '', temporaryDirectory: '/transcode'}, connectivity: {uploadCapacityKbps: 0, remoteBitrateLimitKbps: 8000, pausedSessionTimeoutMinutes: 0}}), host = lifecycleHooks();
  const capacity = {hardware: {effective: 'vaapi', devices: [{id: 'drm-0a1b', name: 'Intel UHD 770', inUse: true}]}, temporaryDirectory: {path: '/tmp/transcode', ready: true, freeBytes: 12000, memoryPath: '/dev/shm', memoryAvailable}};
  const section = specification.settingsSpec.find((value: any) => value.scope === 'server' && value.id === 'streaming');
  const allRows = [...section!.groups!, ...section!.advanced!.groups!].flatMap((group: any) => values.settingRows(group.rows));
  const configuration = {fields: allRows.map((row: any) => ({id: row.id, configOnly: false}))};
  let hardwareTests = 0, finish: ((value: unknown) => void) | undefined;
  class Lifetime {authority = fixture.group.context.authority(); current = fixture.group.context.current; assert() {assert.ok(this.current());} request = (run: any) => run(new AbortController().signal);dispose() {}}
  class Console {transcodeCapacity = async () => capacity;dispose() {}}
  class Streaming {form = (id: string) => fixture.group.forms[id];readConfiguration = async () => configuration;}
  const {FormSet} = await import('../../../packages/client-core/src/server-admin/draft-form.ts');
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/StreamingPage.tsx', import.meta.url), {
    ...chrome, react: host.react, '../../../../../../packages/contracts/src/routes': {routePath}, '../../../../../../packages/client-core/src/settings-configuration': {parseSettingsConfiguration: (value: unknown) => value}, '../../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24)}},
    '../../../../../../packages/client-core/src/hardware': {testHardware: () => {hardwareTests++;return new Promise(resolve => {finish = resolve;});}}, '../../../../../../packages/i18n/src': {defaultI18n: i18n}, '../../../../../../packages/client-core/src/console': {ConsoleClient: Console}, '../../../../../../packages/client-core/src/server-admin/draft-form': {FormSet},
    '../../../../../../packages/client-core/src/server-admin/server-forms': {createServerForm: (id: string) => fixture.group.forms[id], withFormValue: (draft: object, key: string, value: unknown) => ({...draft, [key]: value})}, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings, '../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    '../../../../../../packages/client-core/src/server-admin/streaming-settings': {OwnerStreamingSettings: Streaming},
    '../../../../../../packages/client-core/src/home-selection/list-destinations': {withRequestRaw: (api: unknown) => api},
    './StreamingHardware': {NativeStreamingHardware: 'Hardware'}, '../lifetime': {NativeScreenLifetime: Lifetime},
    '../../../../../../packages/client-core/src/presentation/hardware-selection': await import('../../../packages/client-core/src/presentation/hardware-selection.ts'),
  });
  (fixture.group.context as any).api = {request: async () => configuration};
  const render = () => host.render(() => module.NativeServerStreaming({ports: {context: fixture.group.context}, runtime}));
  render();host.flush();await settle();
  const tree = render(), controls = nodes(tree).filter((node: any) => node.type === 'Setting');
  assert.equal(nodes(tree).filter((node: any) => node.type === 'Advanced').length, 1);
  for (const id of ['streaming.toneMappingMethod', 'streaming.encoderSpeed', 'streaming.whenCantPlay', 'streaming.repackage', 'streaming.maxConversions', 'streaming.awayExact', 'streaming.ffmpegPath']) assert.ok(controls.some((node: any) => node.props.row.id === id), id);
  // Device lists Automatic, Processor only and each adapter the hardware report found; the
  // memory limit is drawn, and usable only while the location is memory.
  const device = controls.find((node: any) => node.props.row.id === 'streaming.hardware');
  assert.deepEqual(device.props.row.control.options.map((option: any) => option.value), ['auto', 'software', 'drm-0a1b']);
  assert.ok(controls.some((node: any) => node.props.row.id === 'streaming.hardwareDecode'));
  assert.equal(controls.some((node: any) => ['streaming.memoryCap', 'streaming.transcodeLocation', 'streaming.toneMapping'].includes(node.props.row.id)), false);
  device.props.onChange('drm-0a1b');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.hardwareSelection, 'drm-0a1b');
  const folder = controls.find((node: any) => node.props.row.id === 'streaming.transcodeFolder');
  assert.equal(folder.props.disabled, false); assert.equal(folder.props.value, '/transcode');
  folder.props.onChange('/mnt/disk');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.temporaryDirectory, '/mnt/disk');
  controls.find((node: any) => node.props.row.id === 'streaming.repackage').props.onChange(false);assert.equal(fixture.group.forms.runtime.getSnapshot().draft.directStreamRemux, false);
  assert.equal(hardwareTests, 0);const hardware = nodes(tree).find((node: any) => node.type === 'Hardware');hardware.props.onTest();hardware.props.onTest();assert.equal(hardwareTests, 1);finish!(capacity);await settle();host.dispose();fixture.stop();
});

test('History mounts real list/filter wiring and its named retention choices write numeric days', async () => {
  const fixture = await formsFixture({runtime: {playHistoryDays: 46}}), host = lifecycleHooks();
  const history = await import('../src-next/screens/settings-live/server/history-source.ts'), reports = await import('../../../packages/client-core/src/report-context.ts');
  const calls: any[] = [], entry = {id: 'play', user: 'sam_k', profile: 'Kids', accountId: 'a1', title: 'Film', startedAt: '2026-10-28T11:00:00Z', watchedMs: 42000, completed: false, location: '192.0.2.5'};
  class Lifetime {signal = new AbortController().signal;authority = {serverId: 'server', accountId: 'a0', profileId: 'p0'};current = fixture.group.context.current;request = (run: any) => run(this.signal);dispose() {}}
  class Dashboard {people = async () => ({items: [{accountId: 'a1', username: 'sam_k'}], nextCursor: ''});}
  // The first page is read through the viewer's cache (client data step 6); every page still reaches this fake.
  const page = async (_api: unknown, filters: unknown) => {calls.push(filters);return {entries: [entry], nextCursor: '', total: 1};};
  const adminDocuments = await import('../../../packages/client-core/src/query/admin-documents.ts'), shellFreshness = await import('../src-next/shell/freshness.ts');
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/HistoryPage.tsx', import.meta.url), {
    ...chrome, react: host.react, 'react-native': {Text: 'Text', View: 'View', FlatList: 'FlatList'}, '../../../../../../packages/client-core/src/play-history': {fetchPlayHistory: page, playHistoryFirstPageQuery: (api: unknown, filters: unknown) => ({queryKey: ['admin', 'play-history', filters], queryFn: () => page(api, filters)}), playHistoryPlatform: () => '', playHistoryTitle: (value: any) => value.title},
    '../../../../../../packages/client-core/src/query/admin-documents': adminDocuments, '../../../shell/freshness': shellFreshness,
    '../../../../../../packages/client-core/src/admin/dashboard': {DashboardDataClient: Dashboard}, '../../../../../../packages/client-core/src/report-context': reports, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings, '../../../../../../packages/client-core/src/presentation/settings-spec': specification, '../../../../../../packages/client-core/src/server-admin/console-words': {duration: (seconds: number) => `${seconds}s`}, '../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../lifetime': {NativeScreenLifetime: Lifetime, screenScopeEnded: () => new Error('ended')}, './history-source': history, './storage/space-source': {nativeStorageOwner: (context: any) => context.current()}, './storage/forms': fixture.hooks,
  });
  (fixture.group.context as any).api = {request: assert.fail};
  const render = () => host.render(() => {const outer = module.NativeServerHistory({ports: {context: fixture.group.context}, runtime});return outer ? outer.type(outer.props) : null;});
  render();host.flush();await settle();render();host.flush();
  const tree = render();assert.equal(tree.type, 'FlatList');assert.equal(tree.props.data[0], entry);
  const row = tree.props.renderItem({item: entry});assert.match(content(row), /sam_k.*Kids/);assert.doesNotMatch(content(row), /192\.0\.2\.5/);
  nodes(tree).find((node: any) => node.props.testID === 'server-history-period').props.onValueChange('7d');await settle();assert.equal(calls.at(-1).period, '7d');
  const keep = () => nodes(render()).find((node: any) => node.props.testID === 'history.keepHistory');assert.equal(keep().props.value, 'custom');keep().props.onValueChange('forever');assert.equal(fixture.group.forms.runtime.getSnapshot().draft.playHistoryDays, 0);keep().props.onValueChange('3y');assert.equal(fixture.group.forms.runtime.getSnapshot().draft.playHistoryDays, 1095);
  host.dispose();fixture.stop();
});

test('TV Streaming saves its chosen conversion folder', async () => {
  const memoryAvailable = false;
  const fixture = await formsFixture({runtime: {conversionMode: 'all', transcodeLocation: 'disk', temporaryDirectory: '/disk', memoryCap: 'quarter'}, connectivity: {}}), host = lifecycleHooks();
  const section = specification.settingsSpec.find((value: any) => value.scope === 'server' && value.id === 'streaming')!;
  const allRows = [...section.groups!, ...section.advanced!.groups!].flatMap((group: any) => values.settingRows(group.rows));
  const configuration = {fields: allRows.map((row: any) => ({id: row.id, configOnly: false}))};
  const capacity = {hardware: {devices: []}, temporaryDirectory: {path: '/disk', memoryPath: '/dev/shm', memoryAvailable}};
  const api = {request: async (path: string) => path === routePath('get_settings_configuration') ? configuration : capacity};
  const model = await import('../src-next/screens/settings-live/server/tv-server-settings-model.ts');
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/TVServerSettingsPage.tsx', import.meta.url), {
    ...chrome, react: host.react,
    '../../../../../../packages/contracts/src/routes': {routePath},
    '../../../../../../packages/client-core/src/settings-configuration': {parseSettingsConfiguration: (value: unknown) => value},
    '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings,
    '../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    '../../../../../../packages/client-core/src/telemetry': {parseTranscodeCapacity: (value: unknown) => value},
    '../use-settings-request-scope': {useSettingsRequestScope: () => ({api, lifetime: {current: fixture.group.context.current}})},
    './storage/forms': fixture.hooks, './TVSettingRows': {TVChoiceRow: 'TVChoiceRow', TVEntryRow: 'TVEntryRow', TVSwitchRow: 'TVSwitchRow'},
    './TVFolderPicker': {TVFolderRow: 'TVFolderRow'}, './tv-server-settings-model': model,
  });
  const render = () => host.render(() => {const outer = module.TVServerSettingsPage({page: 'streaming', ports: {context: fixture.group.context}, runtime});return outer.type(outer.props);});
  const row = (id: string) => nodes(render()).find((node: any) => node.props.testID === id);
  render();host.flush();await settle();
  assert.equal(row('streaming.transcodeLocation'), undefined);
  row('streaming.transcodeFolder').props.onChoose('/fast'); await settle();
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.temporaryDirectory, '/fast');
  host.dispose();fixture.stop();
});
