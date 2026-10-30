import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {bindingModule, formsFixture as untypedFormsFixture, lifecycleHooks, maintenance, nodes, content, specification, t, i18n, theme} from './helpers/owner-server-settings.mjs';
import * as values from '../src-next/screens/settings-live/server/settings-values.ts';
import {secureConnectionView} from '../../../packages/client-core/src/server-admin/secure-connection.ts';
import {certificateProblemMessage, retryCertificate} from '../../../packages/client-core/src/certificates.ts';
import {restoreRecoveryDetails, restoreRecoveryNotice} from '../../../packages/client-core/src/backups.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';

import type {DraftForm} from '../../../packages/client-core/src/server-admin/draft-form.ts';
import * as transcodeLocation from '../../../packages/client-core/src/presentation/transcode-location.ts';
// The JavaScript fixture builds this dictionary from the supplied form IDs.
type Fixture = Awaited<ReturnType<typeof untypedFormsFixture>> & {group: {forms: Record<string, DraftForm<any, any>>}};
const formsFixture = (...args: Parameters<typeof untypedFormsFixture>) => untypedFormsFixture(...args) as Promise<Fixture>;

const bindings = await bindingModule();
const settle = () => new Promise(resolve => setImmediate(resolve));
const runtime = {text: {t}, reportError: (error: unknown) => {throw error;}, error: () => ({title: 'Error'})};
const chrome = {'react-native': {ScrollView: 'ScrollView', View: 'View', Text: 'Text'}, '../../../kit/shell/ServerIdentity': {ServerHeader: 'ServerHeader'}, './page-layout': {settingsPageInset: () => ({}), settingsPageHeading: () => ({})}, '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => theme}, '../../../kit/states': {StateView: 'StateView'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/controls': {Button: 'Button', Select: 'Select', Switch: 'Switch', TextField: 'TextField'}, './AdvancedSection': {NativeAdvancedSection: 'Advanced'}, './SettingControl': {NativeSettingControl: 'Setting'}, './settings-values': values};

test('General exposes backed Advanced controls and one Background speed setting, with draft-aware update checks', async () => {
  const feed = 'https://example.invalid/releases';
  const fixture = await formsFixture({runtime: {name: 'Server', 'updates.feedUrl': feed}, maintenance: maintenance(), alerts: {storageWarningPercent: 15, storageCriticalPercent: 5, certificateWarningDays: 14, noticeAudience: {scan: 'both', storage: 'both', certificate: 'both', recording: 'off', feedback: 'both'}}}), host = lifecycleHooks();
  const configuration = {path: '/config/portico.toml', fields: [{id: 'general.updateFeed', current: feed, effective: feed, default: feed, configOnly: false}]};
  const api = {request: async (path: string) => path === 'get_settings_configuration' ? configuration : {current: {version: '0.0-test'}, updateAvailable: false, message: 'Current', checkedAt: '2026-10-01T00:00:00Z'}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/GeneralPage.tsx', import.meta.url), {
    ...chrome, react: host.react, '../use-settings-request-scope': {useSettingsRequestScope: () => ({api, lifetime: {current: fixture.group.context.current}})},
    '../../../../../../packages/client-core/src/administration': {parseUpdateReport: (value: unknown) => value}, '../../../../../../packages/client-core/src/server-admin/server-forms': {pauseBackgroundWork: assert.fail}, '../../../../../../packages/client-core/src/backups': {restartServer: assert.fail},
    '../../../../../../packages/contracts/src/routes': {routePath: (id: string) => id}, '../../../../../../packages/client-core/src/settings-configuration': {parseSettingsConfiguration: (value: unknown) => value}, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings, '../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    '../../../../../../packages/i18n/src': {defaultI18n: i18n}, '../../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'Error'})}, '../../../../../../packages/client-core/src/server-admin/console-words': {when: String},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/overlays/BottomSheet': {BottomSheet: 'BottomSheet'}, './HeavyWorkControls': {HeavyWorkControls: 'Heavy'}, './BroadcastSheet': {NativeBroadcastSheet: 'Broadcast'}, './storage/forms': fixture.hooks,
  });
  const render = () => host.render(() => {const outer = module.NativeServerGeneral({ports: {context: fixture.group.context}, runtime});return outer.type(outer.props);});
  render();host.flush();await settle();
  const controls = () => nodes(render()).filter((node: any) => node.type === 'Setting');
  for (const id of ['general.updateChecks', 'general.backgroundSpeed', 'general.updateFeed']) assert.ok(controls().some((node: any) => node.props.row.id === id), id);
  // The alert thresholds (critical 5 below warning 15 is valid) and who sees each notice are Advanced rows.
  for (const id of ['general.alertStorageWarn', 'general.alertStorageCritical', 'general.alertCertificateDays', 'general.noticeAudience']) assert.ok(controls().some((node: any) => node.props.row.id === id), id);
  controls().find((node: any) => node.props.row.id === 'general.noticeAudience').props.onChange({scan: 'owner', storage: 'both', certificate: 'both', recording: 'off', feedback: 'both'});
  assert.equal(fixture.group.forms.alerts.getSnapshot().draft.noticeAudience.scan, 'owner');
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
  const api = {request: async (path: string) => {assert.equal(path, 'post_networking_certificate_retry');retries++;return new Promise(resolve => {finish = resolve;});}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/RemoteAccessPage.tsx', import.meta.url), {
    ...chrome, react: host.react, '../../../../../../packages/client-core/src/server-admin/secure-connection': {secureConnectionView}, '../../../../../../packages/client-core/src/server-admin/status-words': {remoteStatusLine: () => ({tone: 'neutral', text: 'Route status'}), ownCertificateLine: () => undefined},
    '../../../../../../packages/client-core/src/remote-access': {remoteWarningLines: () => []},
    '../../../../../../packages/client-core/src/certificates': {certificateProblemMessage, retryCertificate}, '../../../../../../packages/contracts/src/routes': {routePath: (id: string) => id}, '../use-settings-request-scope': {useSettingsRequestScope: () => ({api, lifetime: {current: fixture.group.context.current}})},
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

async function backupsFixture(translate = t) {
  const host = lifecycleHooks(), backup = {id: 'backup-1', createdAt: '2026-10-01T00:00:00Z', kind: 'scheduled', schemaVersion: 4, serverVersion: '0.1-test', bytes: 1024, path: '/backups/one.ptb'};
  let active = true, prepared: unknown, restored: unknown, deleted: unknown, cancelled = 0;
  const state: any = {busy: false, document: {backups: [backup], running: {jobId: 'j', phase: 'copying_database', bytesDone: 500, bytesTotal: 1000}}};
  const selection = {encrypted: true, isCurrent: () => active};
  class Source {
    getSnapshot = () => state;load = async () => {};dispose() {}cancelRestoreSelection() {cancelled++;}
    async prepareRestore(value: unknown) {prepared = value;return selection;}
    async restore(value: unknown, secret: unknown) {restored = {value, secret};return true;}
    async remove(id: string) {deleted = id;return true;}
  }
  class Lifetime {current = () => active;request = (run: any) => run(new AbortController().signal);dispose() {active = false;}}
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/storage/BackupsTab.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../../../packages/client-core/src/server-admin/backup-source': {SettingsBackupSource: Source}, '../../../../../../../packages/client-core/src/backups': {isAuthenticationFailure: () => false, restoreRecoveryDetails, restoreRecoveryNotice}, '../../../../../../../packages/contracts/src/routes': {routePath: (id: string) => id}, '../../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    '../../../../../../../packages/client-core/src/administration': {backupProgressFraction: (value: any) => value.bytesTotal > 0 ? value.bytesDone / value.bytesTotal : undefined}, '../../../../../../../packages/client-core/src/server-admin/console-words': {sentence: (value: string) => value.replaceAll('_', ' '), when: String}, '../../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'TextField'}, '../../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'}, '../../owner-libraries/FolderExplorer': {FolderExplorer: 'FolderExplorer'}, '../AdvancedSection': {NativeAdvancedSection: 'Advanced'}, '../../../../kit/shell/visual': {shellType: () => ({})}, '../../../../kit/theme': {useTheme: () => theme}, '../../lifetime': {NativeScreenLifetime: Lifetime, screenScopeEnded: () => new Error('ended')}, './space-source': {nativeStorageOwner: () => active}, './BackupFolder': {NativeBackupFolder: 'BackupFolder'}, './BackupProtection': {NativeBackupProtection: 'Protection'},
  });
  const ports = {context: {authority: () => ({serverId: 'server'}), api: {request: async () => ({})}}, requestId: () => 'operation'};
  const render = () => host.render(() => {const outer = module.BackupsTab({ports, runtime, t: translate, advancedContent: {type: 'Limits', props: {}}});return outer ? outer.type(outer.props) : null;});
  render();host.flush();render();host.flush();
  return {host, state, render, selection, backup, snapshot: () => ({prepared, restored, deleted, cancelled}), stop: () => {active = false;host.dispose();}};
}

test('Backups paints actual progress/metadata and keeps typed deletion behind the row menu', async () => {
  const fixture = await backupsFixture(), tree = fixture.render();
  assert.match(content(nodes(tree)), /50%/);assert.match(content(nodes(tree)), /web\.maintenance\.backupSchema/);assert.match(content(nodes(tree)), /Scheduled/);
  assert.equal(nodes(tree).filter((node: any) => node.type === 'Advanced').length, 1);
  nodes(tree).find((node: any) => node.props.testID === 'server-backup-backup-1').props.onPress();
  const menu = nodes(fixture.render()).find((node: any) => node.type === 'Menu');menu.props.items.find((item: any) => item.id === 'delete').onSelect();
  const deletion = () => nodes(fixture.render()).find((node: any) => node.type === 'BottomSheet' && node.props.title === 'web.maintenance.deleteBackupTitle');
  const erase = () => nodes(deletion().props.footer).find((node: any) => node.props.variant === 'danger');
  assert.equal(erase().props.disabled, true);erase().props.onPress();await settle();assert.equal(fixture.snapshot().deleted, undefined);
  nodes(deletion()).find((node: any) => node.type === 'TextField').props.onChangeText('delete');assert.equal(erase().props.disabled, false);erase().props.onPress();await settle();assert.equal(fixture.snapshot().deleted, 'backup-1');fixture.stop();
});

test('Backups shows the manual pause warning, pressure failure and recovery outcome', async () => {
  const fixture = await backupsFixture();
  fixture.state.document.running.kind = 'manual';
  assert.match(content(nodes(fixture.render())), /web\.maintenance\.backupMayPause/);
  fixture.state.document.running.kind = 'scheduled';
  assert.doesNotMatch(content(nodes(fixture.render())), /web\.maintenance\.backupMayPause/);
  fixture.state.document.running = undefined;
  fixture.state.document.lastBackup = {jobId: 'j', outcome: 'failed', errorCode: 'backup_busy'};
  assert.match(content(nodes(fixture.render())), /web\.maintenance\.backupBusy/);
  for (const [recovery, message] of [
    [{indexesRebuilt: true}, 'restoreRepaired'],
    [{salvaged: true}, 'restoreRecovered'],
    [{salvaged: true, damagedTables: ['libraries']}, 'restorePartial'],
  ] as const) {
    fixture.state.document.lastRestore = {outcome: 'restored', recovery};
    assert.ok(content(nodes(fixture.render())).includes(`web.maintenance.${message}`));
  }
  fixture.stop();
});

test('Backups exposes bounded precise manifest damage in completed and restarting restores', async () => {
  const fixture = await backupsFixture(defaultI18n.t);
  const recovery = {indexesRebuilt: true, manifestDamage: {count: 25, missingFiles: 2, regionsTruncated: true, unverifiedDatabase: true,
    regions: Array.from({length: 20}, () => ({firstLine: 4, lastLine: 4, startByte: 500, endByte: 600, reason: 'payload_hash_mismatch'}))}};
  fixture.state.document.running = undefined;
  fixture.state.document.lastRestore = {outcome: 'restored', recovery};
  const assertDamage = () => {
    const tree = fixture.render(), copy = content(nodes(tree));
    assert.match(copy, /Portico restored the readable parts of this backup/);
    assert.match(copy, /Backup damage: 25 regions; 2 files could not be restored/);
    assert.equal(nodes(tree).filter((node: any) => node.type === 'Text' && content(node) === 'Lines 4–4, bytes [500, 600): Payload hash mismatch.').length, 20);
    assert.match(copy, /Showing 20 of 25 damaged regions/);
    assert.match(copy, /could not be verified against the backup manifest/);
    assert.doesNotMatch(copy, /Nothing was lost|recovered all of it/);
  };
  assertDamage();
  fixture.state.restorePhase = 'restarting';fixture.state.staged = {validation: {recovery}};
  fixture.state.document.lastRestore = undefined;assertDamage();
  fixture.state.restorePhase = undefined;fixture.state.document.lastRestore = {outcome: 'rolled_back', recovery};
  assert.doesNotMatch(content(nodes(fixture.render())), /Backup damage|readable parts/);
  fixture.stop();
});

test('Restore picker retains its candidate through the encrypted step and closing fences stale choices', async () => {
  const fixture = await backupsFixture();fixture.state.document.running = undefined;
  const restore = () => nodes(fixture.render()).find((node: any) => node.type === 'Button' && content(node) === 'web.maintenance.restoreFromFile');
  restore().props.onPress();
  const picker = nodes(fixture.render()).find((node: any) => node.type === 'FolderExplorer');assert.equal(picker.props.selectionMode, 'file-or-folder');assert.equal(picker.props.multiple, false);
  picker.props.onUse([{path: '/backups/chosen.ptb', name: 'chosen.ptb'}]);picker.props.onOpenChange(false);
  const sheet = () => nodes(fixture.render()).find((node: any) => node.type === 'BottomSheet' && node.props.title === 'web.maintenance.restoreTitle');
  assert.equal(sheet().props.open, true);
  const confirm = () => nodes(sheet().props.footer).find((node: any) => node.type === 'Button' && content(node) === 'web.maintenance.restoreConfirm');
  confirm().props.onPress();await settle();assert.deepEqual(fixture.snapshot().prepared, {path: '/backups/chosen.ptb'});assert.equal(fixture.snapshot().restored, undefined);
  nodes(sheet()).find((node: any) => node.type === 'TextField').props.onChangeText('test-only-passphrase');confirm().props.onPress();await settle();assert.deepEqual(fixture.snapshot().restored, {value: fixture.selection, secret: 'test-only-passphrase'});
  restore().props.onPress();const stale = nodes(fixture.render()).find((node: any) => node.type === 'FolderExplorer');stale.props.onOpenChange(false);stale.props.onUse([{path: '/wrong.ptb', name: 'wrong.ptb'}]);assert.equal(sheet().props.open, false);fixture.stop();
});

for (const memoryAvailable of [true, false]) test(`Streaming keeps memory preference selectable with host availability (${memoryAvailable})`, async () => {
  const fixture = await formsFixture({runtime: {conversionMode: 'all', hardwareSelection: 'auto', hardwareDecode: true, hdrToneMapping: true, hdrToneMappingAlgorithm: 'hable', x264Preset: 'veryfast', planningPolicy: 'minimize_server_work', directStreamRemux: true, outputFormat: 'auto', maxConcurrentSessions: 0, maxHardwareSessions: 0, maxSoftwareSessions: 0, maxBackgroundSessions: 0, serverCap: 0, throttleBufferMs: 60000, playedRetentionMs: 180000, sessionStorageMiB: 4096, memoryCap: 'quarter', transcodeLocation: 'disk', ffmpegPath: '', temporaryDirectory: '/transcode'}, connectivity: {uploadCapacityKbps: 0, remoteBitrateLimitKbps: 8000, pausedSessionTimeoutMinutes: 0}}), host = lifecycleHooks();
  const capacity = {hardware: {effective: 'vaapi', devices: [{id: 'drm-0a1b', name: 'Intel UHD 770', inUse: true}]}, temporaryDirectory: {path: '/tmp/transcode', ready: true, freeBytes: 12000, memoryPath: '/dev/shm', memoryAvailable}};
  const section = specification.settingsSpec.find((value: any) => value.scope === 'server' && value.id === 'streaming');
  const allRows = [...section!.groups!, ...section!.advanced!.groups!].flatMap((group: any) => values.settingRows(group.rows));
  const configuration = {fields: allRows.map((row: any) => ({id: row.id, configOnly: false}))};
  let hardwareTests = 0, finish: ((value: unknown) => void) | undefined;
  class Lifetime {authority = fixture.group.context.authority(); current = fixture.group.context.current; assert() {assert.ok(this.current());} request = (run: any) => run(new AbortController().signal);dispose() {}}
  class Console {transcodeCapacity = async () => capacity;dispose() {}}
  const {FormSet} = await import('../../../packages/client-core/src/server-admin/draft-form.ts');
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/StreamingPage.tsx', import.meta.url), {
    ...chrome, react: host.react, '../../../../../../packages/contracts/src/routes': {routePath: (id: string) => id}, '../../../../../../packages/client-core/src/settings-configuration': {parseSettingsConfiguration: (value: unknown) => value}, '../../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24)}},
    '../../../../../../packages/client-core/src/hardware': {testHardware: () => {hardwareTests++;return new Promise(resolve => {finish = resolve;});}}, '../../../../../../packages/i18n/src': {defaultI18n: i18n}, '../../../../../../packages/client-core/src/console': {ConsoleClient: Console}, '../../../../../../packages/client-core/src/server-admin/draft-form': {FormSet},
    '../../../../../../packages/client-core/src/server-admin/server-forms': {createServerForm: (id: string) => fixture.group.forms[id], withFormValue: (draft: object, key: string, value: unknown) => ({...draft, [key]: value})}, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings, '../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    './StreamingHardware': {NativeStreamingHardware: 'Hardware'}, '../lifetime': {NativeScreenLifetime: Lifetime},
    '../../../../../../packages/client-core/src/presentation/transcode-location': transcodeLocation,
    '../../../../../../packages/client-core/src/presentation/hardware-selection': await import('../../../packages/client-core/src/presentation/hardware-selection.ts'),
  });
  (fixture.group.context as any).api = {request: async () => configuration};
  const render = () => host.render(() => module.NativeServerStreaming({ports: {context: fixture.group.context}, runtime}));
  render();host.flush();await settle();
  const tree = render(), controls = nodes(tree).filter((node: any) => node.type === 'Setting');
  assert.equal(nodes(tree).filter((node: any) => node.type === 'Advanced').length, 1);
  for (const id of ['streaming.toneMapping', 'streaming.toneMappingMethod', 'streaming.encoderSpeed', 'streaming.whenCantPlay', 'streaming.repackage', 'streaming.maxConversions', 'streaming.awayExact', 'streaming.ffmpegPath']) assert.ok(controls.some((node: any) => node.props.row.id === id), id);
  // Device lists Automatic, Processor only and each adapter the hardware report found; the
  // memory limit is drawn, and usable only while the location is memory.
  const device = controls.find((node: any) => node.props.row.id === 'streaming.hardware');
  assert.deepEqual(device.props.row.control.options.map((option: any) => option.value), ['auto', 'software', 'drm-0a1b']);
  assert.ok(controls.some((node: any) => node.props.row.id === 'streaming.hardwareDecode'));
  const memoryCap = () => nodes(render()).find((node: any) => node.type === 'Setting' && node.props.row.id === 'streaming.memoryCap');
  assert.equal(memoryCap()?.props.value, 'quarter');assert.equal(memoryCap()?.props.disabled, true, 'the memory limit waits for the memory location');
  device.props.onChange('drm-0a1b');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.hardwareSelection, 'drm-0a1b');
  const location = controls.find((node: any) => node.props.row.id === 'streaming.transcodeLocation');
  assert.ok(location);assert.equal(location.props.value, 'disk');
  assert.deepEqual(location.props.row.control.options.map((option: any) => option.value), ['memory', 'disk']);
  assert.ok(location.props.row.control.options.every((option: any) => !option.locked));
  assert.equal(location.props.row.help, memoryAvailable ? section!.groups![2].rows[1].help : 'next.settings.streaming.memoryUnavailable');
  const folder = () => nodes(render()).find((node: any) => node.type === 'Setting' && node.props.row.id === 'streaming.transcodeFolder');
  assert.equal(folder().props.disabled, false);assert.equal(folder().props.value, '/transcode');
  location.props.onChange('memory');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.transcodeLocation, 'memory');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.temporaryDirectory, '/transcode');
  assert.equal(memoryCap()?.props.disabled, false, 'the memory preference enables its limit even when this host uses disk');
  memoryCap()!.props.onChange('2gb');assert.equal(fixture.group.forms.runtime.getSnapshot().draft.memoryCap, '2gb');
  assert.equal(folder().props.disabled, false, 'the disk folder remains accessible under memory');
  folder().props.onChange('/mnt/disk');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.temporaryDirectory, '/mnt/disk');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.transcodeLocation, 'memory');
  location.props.onChange('disk');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.transcodeLocation, 'disk');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.temporaryDirectory, '/mnt/disk');
  assert.equal(memoryCap()?.props.disabled, true);
  assert.equal(folder().props.disabled, false);
  controls.find((node: any) => node.props.row.id === 'streaming.repackage').props.onChange(false);assert.equal(fixture.group.forms.runtime.getSnapshot().draft.directStreamRemux, false);
  assert.equal(hardwareTests, 0);const hardware = nodes(tree).find((node: any) => node.type === 'Hardware');hardware.props.onTest();hardware.props.onTest();assert.equal(hardwareTests, 1);finish!(capacity);await settle();host.dispose();fixture.stop();
});

test('History mounts real list/filter wiring and its named retention choices write numeric days', async () => {
  const fixture = await formsFixture({runtime: {playHistoryDays: 46}}), host = lifecycleHooks();
  const history = await import('../src-next/screens/settings-live/server/history-source.ts'), reports = await import('../../../packages/client-core/src/report-context.ts');
  const calls: any[] = [], entry = {id: 'play', user: 'sam_k', profile: 'Kids', accountId: 'a1', title: 'Film', startedAt: '2026-10-28T11:00:00Z', watchedMs: 42000, completed: false, location: '192.0.2.5'};
  class Lifetime {signal = new AbortController().signal;current = fixture.group.context.current;request = (run: any) => run(this.signal);dispose() {}}
  class Dashboard {people = async () => ({items: [{accountId: 'a1', username: 'sam_k'}], nextCursor: ''});}
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/HistoryPage.tsx', import.meta.url), {
    ...chrome, react: host.react, 'react-native': {Text: 'Text', View: 'View', FlatList: 'FlatList'}, '../../../../../../packages/client-core/src/play-history': {fetchPlayHistory: async (_api: unknown, filters: unknown) => {calls.push(filters);return {entries: [entry], nextCursor: '', total: 1};}, playHistoryPlatform: () => '', playHistoryTitle: (value: any) => value.title},
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

for (const memoryAvailable of [true, false]) test(`TV Streaming saves its explicit preference and independent disk folder (${memoryAvailable})`, async () => {
  const fixture = await formsFixture({runtime: {conversionMode: 'all', transcodeLocation: 'disk', temporaryDirectory: '/disk', memoryCap: 'quarter'}, connectivity: {}}), host = lifecycleHooks();
  const section = specification.settingsSpec.find((value: any) => value.scope === 'server' && value.id === 'streaming')!;
  const allRows = [...section.groups!, ...section.advanced!.groups!].flatMap((group: any) => values.settingRows(group.rows));
  const configuration = {fields: allRows.map((row: any) => ({id: row.id, configOnly: false}))};
  const capacity = {hardware: {devices: []}, temporaryDirectory: {path: '/disk', memoryPath: '/dev/shm', memoryAvailable}};
  const api = {request: async (path: string) => path === 'get_settings_configuration' ? configuration : capacity};
  const model = await import('../src-next/screens/settings-live/server/tv-server-settings-model.ts');
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/TVServerSettingsPage.tsx', import.meta.url), {
    ...chrome, react: host.react,
    '../../../../../../packages/contracts/src/routes': {routePath: (id: string) => id},
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
  assert.equal(row('streaming.transcodeLocation').props.value, 'disk');
  assert.equal(row('streaming.transcodeLocation').props.description, memoryAvailable ? section.groups![2].rows[1].help : 'next.settings.streaming.memoryUnavailable');
  assert.ok(row('streaming.transcodeLocation').props.options.every((option: any) => !option.disabled));
  row('streaming.transcodeLocation').props.onChange('memory');await settle();
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.transcodeLocation, 'memory');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.temporaryDirectory, '/disk');
  assert.equal(row('streaming.transcodeLocation').props.value, 'memory');
  row('streaming.transcodeFolder').props.onChoose('/fast');await settle();
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.temporaryDirectory, '/fast');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.transcodeLocation, 'memory');
  row('streaming.transcodeLocation').props.onChange('disk');await settle();
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.transcodeLocation, 'disk');
  assert.equal(fixture.group.forms.runtime.getSnapshot().draft.temporaryDirectory, '/fast');
  host.dispose();fixture.stop();
});
