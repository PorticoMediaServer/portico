import assert from 'node:assert/strict';
import test from 'node:test';
import {createI18n} from '../../i18n/src/index.ts';
import {searchSettings, settingsStructure, type SettingsPage, type SettingsRow} from '../src/presentation/index.ts';
import {connectionLine, ownCertificateLine} from '../src/server-admin/status-words.ts';
import {withListEntry} from '../src/server-admin/server-forms.ts';

const i18n = createI18n();
const t = i18n.t;
const server = (platform: 'web' | 'phone' | 'tv') => settingsStructure({owner: true, platform, capabilities: {}}).headings.find(h => h.id === 'server')!.pages;
const page = (pages: readonly SettingsPage[], id: string) => pages.find(p => p.id === id);
const rowIds = (p: SettingsPage) => p.sections.flatMap(s => s.rows.map((r: SettingsRow) => (r.kind === 'server-setting' ? `${r.form}.${r.key}` : r.kind === 'custom' ? r.id : '')));

test('the web groups keys, webhooks and assistants without the retired member-token controls', () => {
  const integrations = page(server('web'), 'integrations')!;
  assert.deepEqual(integrations.tabs?.map(x => x.id), ['keys', 'webhooks', 'assistants']);
  assert.deepEqual(rowIds(integrations), ['server.apiKeys', 'runtime.apiRequestsPerMinute', 'server.webhooks', 'server.assistants']);
  // People keeps accounts and invitations; keys moved out.
  assert.deepEqual(page(server('web'), 'people')!.tabs?.map(x => x.id), ['accounts', 'invitations', 'devices']);
});

test('phones and tablets have the owner administration as on the web, in the web\'s order', () => {
  const pages = server('phone'), web = server('web');
  const order = web.map(p => p.id);
  // The phone carries the dedicated Remote sources page; the web still reaches the
  // same source editor from Storage, so neither platform loses the page.
  assert.deepEqual(pages.map(p => p.id), [...order.slice(0, order.indexOf('storage')), 'remote-sources', ...order.slice(order.indexOf('storage'))]);
  assert.ok(page(pages, 'remote-sources'));
  assert.equal(page(web, 'remote-sources'), undefined);
  assert.ok(rowIds(page(web, 'storage')!).includes('server.remoteSources'));
  assert.ok(rowIds(page(pages, 'storage')!).includes('server.remoteSources'));
  assert.ok(page(pages, 'integrations'));
  assert.ok(page(pages, 'metadata'));
  // The keys list has one home, Integrations, as on the web.
  assert.deepEqual(page(pages, 'people')!.tabs?.map(x => x.id), ['accounts', 'invitations', 'devices']);
  for (const id of ['server.resourceUsage', 'server.storageStatus', 'server.downloadUsage']) assert.ok(rowIds(page(pages, 'storage')!).includes(id), id);
  for (const id of ['server.setAsideTitles', 'server.clientReports']) assert.ok(rowIds(page(pages, 'troubleshooting')!).includes(id), id);
  assert.ok(rowIds(page(pages, 'general')!).includes('server.restart'));
  assert.ok(rowIds(page(pages, 'streaming')!).includes('server.playbackProblems'));
  // Television keeps the read-only Dashboard alone.
  assert.deepEqual(server('tv').map(p => p.id), ['dashboard']);
});

test('an administrator sees only what the server lets the administrator tier do', () => {
  const admin = (platform: 'web' | 'phone' | 'tv') => settingsStructure({owner: false, administrator: true, platform, capabilities: {}}).headings.find(h => h.id === 'server')?.pages ?? [];
  const web = admin('web');
  assert.deepEqual(web.map(p => p.id), ['dashboard', 'people', 'troubleshooting']);
  assert.deepEqual(rowIds(page(web, 'dashboard')!), ['server.nowPlaying']);
  assert.deepEqual(rowIds(page(web, 'people')!), ['server.accounts', 'server.invitations', 'server.devices']);
  assert.deepEqual(rowIds(page(web, 'troubleshooting')!), ['server.logs', 'server.setAsideTitles']);
  // No setting row: every owner form is the owner's.
  for (const p of [...web, ...admin('phone')]) for (const section of p.sections) for (const row of section.rows) assert.notEqual(row.kind, 'server-setting', row.kind);
  assert.deepEqual(admin('tv').map(p => p.id), ['dashboard']);
  assert.deepEqual(rowIds(admin('tv')[0]!), ['server.nowPlaying']);
  // A member has no Server heading at all.
  assert.equal(settingsStructure({owner: false, platform: 'web', capabilities: {}}).headings.some(h => h.id === 'server'), false);
});

test('remote access carries the home networks, the address to give out, proxies, the owner\'s certificate and the connection limit', () => {
  const remote = page(server('web'), 'remote')!;
  const ids = rowIds(remote);
  for (const id of ['server.connection', 'connectivity.lanNetworks', 'connectivity.advertiseUrl', 'connectivity.accessUrls', 'connectivity.trustedProxies', 'server.ownCertificateStatus', 'connectivity.customCertificateDomain', 'connectivity.customCertificatePath', 'connectivity.customCertificateKeyPath', 'connectivity.connectionLimit']) assert.ok(ids.includes(id), id);
  // Sub-path hosting is not offered, and the two retired choices are gone.
  for (const id of ids) assert.doesNotMatch(id, /subPath|basePath|secureConnectionsPolicy|treatWanAsLan/);
  const limit = remote.sections.flatMap(s => s.rows).find(r => r.kind === 'server-setting' && r.key === 'connectionLimit');
  assert.ok(limit && limit.kind === 'server-setting' && limit.control.type === 'number' && limit.control.empty?.stores === 0);
});

test('secure connection comes first on remote access: its status, a switch once there is a connection, then the owner\'s own certificate', () => {
  for (const platform of ['web', 'phone'] as const) {
    const remote = page(server(platform), 'remote')!;
    const secure = remote.sections.find(s => s.id === 'secure')!;
    assert.equal(secure.title, 'settings.server.secure.title');
    assert.deepEqual(secure.rows.map(r => (r.kind === 'server-setting' ? `${r.form}.${r.key}` : r.kind === 'custom' ? r.id : '')), ['server.secureConnection', 'certificate.enabled', 'server.certificateStatus']);
    const toggle = secure.rows.find(r => r.kind === 'server-setting')!;
    assert.ok(toggle.kind === 'server-setting' && toggle.when?.key === 'claimed' && toggle.when.equals === true);
    const order = remote.sections.map(s => s.id);
    assert.ok(order.indexOf('secure') < order.indexOf('ownCertificate') && order.indexOf('ownCertificate') < order.indexOf('reach'), order.join());
  }
});

test('background work speed is one named choice beside background priority', () => {
  const rows = page(server('web'), 'schedule')!.sections.flatMap(s => s.rows);
  const speed = rows.find(r => r.kind === 'server-setting' && r.key === 'backgroundSpeed');
  assert.ok(speed && speed.kind === 'server-setting' && speed.control.type === 'choice');
  assert.deepEqual(speed.control.choices.map(c => c.value), ['gentle', 'normal', 'fast']);
});

test('personal access tokens appear in a member\'s account only when the client says the member makes them', () => {
  const sections = (accessTokens: boolean, platform: 'web' | 'phone' | 'tv' = 'web') => settingsStructure({owner: false, platform, capabilities: {accessTokens}}).headings[0]!.pages.find(p => p.id === 'profile')!.sections.map(s => s.id);
  assert.ok(sections(true).includes('tokens'));
  assert.ok(!sections(false).includes('tokens'));
  assert.ok(sections(true, 'phone').includes('tokens'));
  assert.ok(!sections(true, 'tv').includes('tokens'));
});

test('the new pages are found by the settings search', () => {
  const structure = settingsStructure({owner: true, platform: 'web', capabilities: {}});
  assert.ok(searchSettings(structure, 'webhook', t).some(m => m.address === 'server-integrations'));
  assert.ok(searchSettings(structure, 'home networks', t).some(m => m.address === 'server-remote'));
  assert.ok(searchSettings(structure, 'metadata', t).some(m => m.address === 'server-metadata'));
});

test('every word the structure names exists in the catalogue', () => {
  for (const platform of ['web', 'phone', 'tv'] as const) {
    for (const p of server(platform)) {
      assert.ok(i18n.has(p.title), p.title);
      for (const tab of p.tabs ?? []) assert.ok(i18n.has(tab.title), tab.title);
      for (const section of p.sections) {
        if (section.title) assert.ok(i18n.has(section.title), section.title);
        if (section.description) assert.ok(i18n.has(section.description), section.description);
        for (const row of section.rows) {
          if ('label' in row && row.label) assert.ok(i18n.has(row.label), row.label);
          if ('help' in row && row.help) assert.ok(i18n.has(row.help), row.help);
        }
      }
    }
  }
});

test('the connection line says where the owner is and which side of the list that is', () => {
  const connection = {secure: false, remote: false, plainHttpOutsideHome: false, clientAddress: '192.168.2.14', accessUrls: []};
  assert.equal(connectionLine(t, connection)?.text, 'You are connected from 192.168.2.14, which counts as home.');
  assert.equal(connectionLine(t, {...connection, remote: true, clientAddress: '203.0.113.9'})?.text, 'You are connected from 203.0.113.9, which counts as away from home.');
  assert.equal(connectionLine(t, {...connection, clientAddress: ''}), undefined);
  assert.equal(connectionLine(t, undefined), undefined);
});

test('the owner\'s certificate says in use, or what is wrong and whether the last good one still serves', () => {
  const date = () => 'Jan 4, 2027';
  assert.equal(ownCertificateLine(t, undefined, date), undefined);
  assert.equal(ownCertificateLine(t, {serving: false, state: 'none'}, date), undefined);
  assert.deepEqual(ownCertificateLine(t, {serving: true, state: 'ready', notAfter: '2027-01-04T00:00:00Z'}, date), {tone: 'healthy', text: 'In use, valid until Jan 4, 2027.'});
  assert.deepEqual(ownCertificateLine(t, {serving: false, state: 'error', errorCode: 'custom_certificate_name_mismatch'}, date), {tone: 'danger', text: 'The certificate doesn’t cover this domain.'});
  const kept = ownCertificateLine(t, {serving: true, state: 'error', errorCode: 'custom_certificate_unreadable'}, date)!;
  assert.equal(kept.tone, 'warning');
  assert.match(kept.text, /can’t be read.*still in use\.$/);
  assert.equal(ownCertificateLine(t, {serving: false, state: 'error', errorCode: 'something_new'}, date)?.text, 'The certificate files can’t be used.');
});

test('a list setting takes an entry once, trimmed, and never an empty one', () => {
  assert.deepEqual(withListEntry(['10.0.0.0/8'], ' 100.64.0.0/10 '), ['10.0.0.0/8', '100.64.0.0/10']);
  assert.deepEqual(withListEntry(['10.0.0.0/8'], '10.0.0.0/8'), ['10.0.0.0/8']);
  assert.deepEqual(withListEntry(['10.0.0.0/8'], '   '), ['10.0.0.0/8']);
});

test('the second-pass rows are where an owner would look', () => {
  const web = server('web');
  assert.deepEqual(rowIds(page(web, 'people')!).filter(id => id.includes('evice')), ['server.devices']);
  // Phones and tablets retain the device list; the retired approval switch is absent on both surfaces.
  assert.deepEqual(rowIds(page(server('phone'), 'people')!).filter(id => id.includes('evice')), ['server.devices']);
  for (const id of ['runtime.sessionStorageMiB', 'runtime.ffmpegPath', 'runtime.temporaryDirectory', 'runtime.hardwareBackend']) assert.ok(rowIds(page(web, 'streaming')!).includes(id), id);
  for (const id of ['liveDefaults.importLimits.channels', 'liveDefaults.importLimits.programmes', 'liveDefaults.importLimits.bytes']) assert.ok(rowIds(page(web, 'live')!).includes(id), id);
  assert.ok(rowIds(page(web, 'general')!).includes('server.restart'));
  assert.ok(rowIds(page(web, 'troubleshooting')!).includes('server.clientReports'));
  const space = page(web, 'streaming')!.sections.flatMap(s => s.rows).find(r => r.kind === 'server-setting' && r.key === 'sessionStorageMiB');
  assert.ok(space && space.kind === 'server-setting' && space.control.type === 'number' && space.control.scale === 1024);
});

test('privacy opens with what the owner always sees, and the web never offers a player or passthrough it does not have', () => {
  const account = settingsStructure({owner: false, platform: 'web', capabilities: {audioPassthrough: false, hdr: true}}).headings[0]!.pages;
  assert.equal(account.find(p => p.id === 'privacy')!.sections[0]!.rows[0]!.kind === 'custom' && (account.find(p => p.id === 'privacy')!.sections[0]!.rows[0] as {id: string}).id, 'privacyNote');
  const quality = account.find(p => p.id === 'quality')!.sections.flatMap(s => s.rows).map(r => ('id' in r ? r.id : 'key' in r ? r.key : ''));
  assert.ok(quality.includes('forceStereo') && quality.includes('disableHdr'));
  assert.ok(!quality.includes('disablePassthrough') && !quality.includes('playerEngine'));
  const noHdr = settingsStructure({owner: false, platform: 'web', capabilities: {hdr: false}}).headings[0]!.pages.find(p => p.id === 'quality')!.sections.flatMap(s => s.rows).map(r => ('id' in r ? r.id : ''));
  assert.ok(!noHdr.includes('disableHdr'));
});

test('the third-pass panels sit beside the settings they explain', () => {
  const web = server('web');
  const storage = rowIds(page(web, 'storage')!);
  for (const id of ['server.resourceUsage', 'server.storageStatus', 'server.downloadUsage']) assert.ok(storage.includes(id), id);
  const schedule = rowIds(page(web, 'schedule')!);
  assert.ok(schedule.indexOf('server.backgroundPause') === schedule.indexOf('maintenance.backgroundSpeed') + 1, 'the pause follows the speed it interrupts');
  const playback = rowIds(page(web, 'streaming')!);
  assert.ok(playback.indexOf('server.ffmpegStatus') === playback.indexOf('runtime.ffmpegPath') + 1, 'the check result follows the path it checked');
  assert.ok(rowIds(page(web, 'troubleshooting')!).includes('server.setAsideTitles'));
  // What the server is avoiding after a playback failure is the last thing on the page about playing.
  assert.equal(playback.at(-1), 'server.playbackProblems');
  // None of the console's panels is offered on television, which draws the Dashboard alone.
  for (const id of ['server.resourceUsage', 'server.storageStatus', 'server.downloadUsage', 'server.setAsideTitles', 'server.playbackProblems']) for (const p of server('tv')) assert.ok(!rowIds(p).includes(id), id);
  const device = settingsStructure({owner: false, platform: 'web', capabilities: {}}).headings[0]!.pages.find(p => p.id === 'device')!;
  assert.ok(rowIds(device).includes('remoteControls'));
  // What this profile plays on its other devices can be paused or stopped from a phone or a television too.
  for (const platform of ['phone', 'tv'] as const) assert.ok(rowIds(settingsStructure({owner: false, platform, capabilities: {}}).headings[0]!.pages.find(p => p.id === 'device')!).includes('remoteControls'), platform);
});

test('remembered playback failures are shown a file at a time, newest first', async () => {
  const {groupPlaybackFailures, parsePlaybackFailureMemory} = await import('../src/playback-failure-memory.ts');
  const row = (sourceId: string, route: string, reportedAtMs: number, expiresAtMs: number) => ({sourceId, engine: 'native', route, code: 'decode_failed', detail: 'no decoder', reportedAtMs, expiresAtMs});
  const memory = parsePlaybackFailureMemory({items: [row('a', 'original', 10, 100), row('b', 'original', 30, 90), row('a', 'copy_remux', 20, 200), {sourceId: 'bad id!', engine: 'native', route: 'original', code: 'x', detail: '', reportedAtMs: 1, expiresAtMs: 2}], more: false});
  const groups = groupPlaybackFailures(memory.items);
  assert.deepEqual(groups.map(g => [g.sourceId, g.failures.map(f => f.route), g.reportedAtMs, g.expiresAtMs]), [['b', ['original'], 30, 90], ['a', ['copy_remux', 'original'], 20, 200]]);
  assert.deepEqual(groupPlaybackFailures([]), []);
});
