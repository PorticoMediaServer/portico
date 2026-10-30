import {createRoot} from 'react-dom/client';
import {createI18n, deviceRegion, resolveRegion, formatMessage} from '@i18n';
import {troubleshootingFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/troubleshooting-fixtures';
import {setServiceI18n} from '@core/presentation/service-text.ts';
import {IconSprite} from '../../kit/Icon';
import {TroubleshootingPage} from '../../screens/settings/server/troubleshooting';
import {SettingsFeedbackSource} from '../../screens/settings/server/troubleshooting/feedback-source';
import {SettingsReportsSource} from '../../screens/settings/server/troubleshooting/reports-source';
import {SettingsMessagesSource} from '../../screens/settings/server/troubleshooting/messages-source';
import {SettingsSupportSource} from '../../screens/settings/server/troubleshooting/support-source';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// Drawn demo facts exist only in this DEV specimen, never in runtime adapters.
// It mounts the production Reports screen; every row is owner-server.html frame 5's own state.
if (!import.meta.env.DEV) throw new Error('Settings specimens require development.');

const fixture = (key: keyof typeof troubleshootingFixtureMessages) => formatMessage(troubleshootingFixtureMessages[key], {}, {locale: 'en-US'});
const params = new URLSearchParams(location.search);
if (params.get('clock') === 'fixed') {
  const fixed = Date.parse('2026-10-22T19:00:00Z'), RealDate = Date;
  const Construct = RealDate as unknown as new (...args: unknown[]) => Date;
  const FakeDate = function (...args: unknown[]) { return args.length ? new Construct(...args) : new Construct(fixed); } as unknown as DateConstructor;
  FakeDate.now = () => fixed;
  FakeDate.parse = RealDate.parse.bind(RealDate);
  FakeDate.UTC = RealDate.UTC.bind(RealDate);
  window.Date = FakeDate;
}
if (params.get('capture') === '1') {
  const style = document.createElement('style');
  style.textContent = '*,*::before,*::after{animation:none!important;transition:none!important}';
  document.head.append(style);
}

setServiceI18n(createI18n(resolveRegion(undefined, deviceRegion())));
const serverId = 'specimen-server', fence = 'a'.repeat(64);
const envelope = (result: unknown) => ({protocolVersion: '1.0', serverId, result});
const report = (id: string, platform: string, deviceClass: string, appVersion: string, receivedAt: string, bytes: number) =>
  ({id, accountId: 'account', profileId: 'profile', deviceId: `device-${id}`, platform, deviceClass, appVersion, receivedAt, bytes});
// Messages are the redacted sample lines drawn in owner-server frame 1.
const messageRecords = [
  {
    "sequence": "1",
    "at": "2026-10-22T18:58:02Z",
    "level": "info",
    "category": "server",
    "message": fixture('next.troubleshooting.fixture.message1')
  },
  {
    "sequence": "2",
    "at": "2026-10-22T18:58:40Z",
    "level": "info",
    "category": "scan",
    "message": fixture('next.troubleshooting.fixture.message2')
  },
  {
    "sequence": "3",
    "at": "2026-10-22T19:01:17Z",
    "level": "info",
    "category": "scan",
    "message": fixture('next.troubleshooting.fixture.message3')
  },
  {
    "sequence": "4",
    "at": "2026-10-22T19:03:05Z",
    "level": "info",
    "category": "playback",
    "message": fixture('next.troubleshooting.fixture.message4')
  },
  {
    "sequence": "5",
    "at": "2026-10-22T19:03:22Z",
    "level": "warn",
    "category": "network",
    "message": fixture('next.troubleshooting.fixture.message5')
  },
  {
    "sequence": "6",
    "at": "2026-10-22T19:05:48Z",
    "level": "info",
    "category": "playback",
    "message": fixture('next.troubleshooting.fixture.message6')
  },
  {
    "sequence": "7",
    "at": "2026-10-22T19:07:31Z",
    "level": "error",
    "category": "playback",
    "message": fixture('next.troubleshooting.fixture.message7')
  },
  {
    "sequence": "8",
    "at": "2026-10-22T19:07:32Z",
    "level": "info",
    "category": "playback",
    "message": fixture('next.troubleshooting.fixture.message8')
  },
  {
    "sequence": "9",
    "at": "2026-10-22T19:09:10Z",
    "level": "info",
    "category": "client",
    "message": fixture('next.troubleshooting.fixture.message9')
  },
  {
    "sequence": "10",
    "at": "2026-10-22T19:11:02Z",
    "level": "warn",
    "category": "server",
    "message": fixture('next.troubleshooting.fixture.message10')
  },
  {
    "sequence": "11",
    "at": "2026-10-22T19:12:40Z",
    "level": "info",
    "category": "playback",
    "message": fixture('next.troubleshooting.fixture.message11')
  },
  {
    "sequence": "12",
    "at": "2026-10-22T19:14:03Z",
    "level": "info",
    "category": "network",
    "message": fixture('next.troubleshooting.fixture.message12')
  },
  {
    "sequence": "13",
    "at": "2026-10-22T19:14:55Z",
    "level": "info",
    "category": "scan",
    "message": fixture('next.troubleshooting.fixture.message13')
  },
  {
    "sequence": "14",
    "at": "2026-10-22T19:15:00Z",
    "level": "info",
    "category": "scan",
    "message": fixture('next.troubleshooting.fixture.message14')
  }
];
const records = [
  {sequence: 1842, at: '2026-10-22T18:58:02Z', lane: 'runtime', severity: 'info', component: 'backups', code: 'backup_finished', fields: {kept: 7}},
  {sequence: 1841, at: '2026-10-22T19:03:00Z', lane: 'runtime', severity: 'warn', component: 'remote_access', code: 'port_mapping_failed'},
];
const feedbackReport = (id: string, createdAt: string, updatedAt: string, revision: number, status: string, kind: string, category: string, message: string, itemId?: string,
  client?: {platform: string; appVersion: string; online?: boolean}) => ({id, cursor: id, createdAt, updatedAt, expiresAt: '2027-04-20T18:20:00Z', revision, status, kind, category, message,
  ...(itemId ? {itemId} : {}), reporter: {name: 'Alex', authority: 'local', role: 'member', self: false}, diagnostics: {decision: 'not-requested'}, ...(client ? {client} : {}), duplicates: 0, thread: []});
const feedback = [
  feedbackReport('feedback-1', '2026-10-22T18:20:00Z', '2026-10-22T18:20:00Z', 1, 'open', 'playback', 'buffering', fixture('next.troubleshooting.fixture.message15'), 'item-1', {platform: 'ios', appVersion: '1.4.2', online: true}),
  feedbackReport('feedback-2', '2026-10-21T23:05:00Z', '2026-10-22T17:00:00Z', 2, 'in-progress', 'playback', 'subtitles', fixture('next.troubleshooting.fixture.message16'), undefined, {platform: 'tvos', appVersion: '1.4.2', online: false}),
  feedbackReport('feedback-3', '2026-10-20T20:15:00Z', '2026-10-21T09:00:00Z', 3, 'resolved', 'library', 'wrong-details', fixture('next.troubleshooting.fixture.message17'), 'item-2'),
  feedbackReport('feedback-4', '2026-10-19T18:40:00Z', '2026-10-20T10:00:00Z', 2, 'closed', 'other', 'other', fixture('next.troubleshooting.fixture.message18')),
];
const setAside = {failures: [
  {domain: 1, title: fixture('next.troubleshooting.fixture.title1'), reason: fixture('next.troubleshooting.fixture.reason1'), attempts: 3, failedAt: '2026-10-22T17:10:00Z'},
  {domain: 1, title: fixture('next.troubleshooting.fixture.title2'), reason: fixture('next.troubleshooting.fixture.reason2'), attempts: 2, failedAt: '2026-10-22T17:10:00Z'},
], more: false};
const capabilities = {items: [
  {capability: 'live_tv', available: true, checkedAt: '2026-10-22T18:00:00Z'},
  {capability: 'recording', available: true, checkedAt: '2026-10-22T18:00:00Z'},
  {capability: 'prepared_media', available: true, checkedAt: '2026-10-22T18:00:00Z'},
  {capability: 'hardware_encoding', available: false, code: 'hardware_unavailable', detail: fixture('next.troubleshooting.fixture.detail1'), checkedAt: '2026-10-22T18:00:00Z'},
  {capability: 'decoder_sandbox', available: false, code: 'decoder_confinement_unavailable', checkedAt: '2026-10-22T18:00:00Z'},
  {capability: 'dolby_vision_conversion', available: true, checkedAt: '2026-10-22T18:00:00Z'},
]};
const alerts = [{id: 'permissions', code: 'state-permissions', status: 'open', severity: 'warning', firstAt: '2026-10-22T17:00:00Z', lastAt: '2026-10-22T18:00:00Z', occurrences: 3, revision: 1}];
const request = async (path: string, method = 'GET'): Promise<unknown> => {
  if (path.includes('/console/diagnostics/')) return {scope: {serverId, viewerFence: fence}, data: {items: records, nextCursor: ''}};
  if (path.includes('/catalogue/failures')) return setAside;
  if (method === 'DELETE') return undefined;
  if (path.includes('/logs/settings')) return {revision: 1, logLevel: 'info', effectiveLevel: 'info', retention: [], levels: ['error','warn','info','debug'], categories: ['server','playback','scan','network','client']};
  if (path.includes('/admin/logs')) return {items: [...messageRecords].reverse(), limit: 200, nextCursor: '', level: 'info', effectiveLevel: 'info'};
  if (path.includes('/diagnostics/capabilities')) return capabilities;
  if (path.includes('/console/registry')) return {scope: {serverId, viewerFence: fence}, data: {fields: []}};
  if (path.includes('/console/alerts')) return {scope: {serverId, viewerFence: fence}, data: alerts};
  if (path.includes('/admin/feedback/reports/')) {
    const id = path.split('/').pop();
    return {scope: {serverId, viewerFence: fence}, data: feedback.find(item => item.id === id) ?? feedback[0]};
  }
  if (path.includes('/admin/feedback/reports')) return {scope: {serverId, viewerFence: fence}, data: {items: feedback, nextCursor: '', statusCounts: {}, total: feedback.length, filter: {}, observedAt: '2026-10-22T19:00:00Z'}};
  if (path.includes('client-logs/')) return envelope({...report('one', 'iPhone', 'phone', '1.4.2', '2026-10-21T22:44:00Z', 38912), body: fixture('next.troubleshooting.fixture.body1')});
  return envelope({items: [
    report('one', 'iPhone', 'phone', '1.4.2', '2026-10-21T22:44:00Z', 38912),
    report('two', 'Android TV', 'tv', '1.4.1', '2026-10-20T21:10:00Z', 114688),
    report('three', 'Chrome', 'desktop', '1.4.2', '2026-10-18T23:33:00Z', 21504),
  ], limit: 25, nextCursor: ''});
};
const transport = {request: <T,>(path: string, method?: string) => request(path, method) as Promise<T>};
const messages = new SettingsMessagesSource(transport, undefined, () => true);
if (params.get('live') === '1') messages.setLive(true);
const reports = new SettingsReportsSource(transport, serverId, 'specimen-viewer', () => true);
const feedbackSource = new SettingsFeedbackSource(transport, serverId, 'specimen-viewer', () => true);
const supportSource = new SettingsSupportSource(transport, serverId, 'specimen-viewer', () => true);
const subscribe = (listener: () => void) => {const offs = [reports.subscribe(listener), feedbackSource.subscribe(listener), supportSource.subscribe(listener)];return () => {for (const off of offs) off();};};

createRoot(document.getElementById('root')!).render(<div style={{minHeight: '100vh', background: 'var(--color-ink)', padding: '40px 32px'}}>
  <div style={{maxWidth: 952, margin: '0 auto'}}><IconSprite />
    <TroubleshootingPage messages={messages} reports={reports} feedback={feedbackSource} support={supportSource} subscribe={subscribe} isCurrent={() => true} downloadBase="" reportError={() => {}}
      initialView={params.get('view') === 'messages' ? 'messages' : params.get('view') === 'reports' ? 'reports' : params.get('view') === 'support' ? 'support' : 'feedback'} />
  </div>
</div>);
