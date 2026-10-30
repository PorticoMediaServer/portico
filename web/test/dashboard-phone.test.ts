import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

// The drawn phone Dashboard is its own page: this suite renders the composition, not a browser.
const state = {authority: true, value: undefined as unknown};
Object.assign(globalThis, {__dashboardPhoneTest: state});
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === './use-read' && (context.parentURL?.endsWith('/src/screens/dashboard/Dashboard.tsx') || context.parentURL?.endsWith('/src/screens/dashboard/use-report.ts'))) return {url: 'dashboard-phone:read', shortCircuit: true};
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'dashboard-phone:read') return {format: 'module', source: `
      export const useDashboardAuthority = () => globalThis.__dashboardPhoneTest.authority;
      export const useDashboardRead = () => ({loading: false, failed: false, value: globalThis.__dashboardPhoneTest.value, refresh: () => {}});
      export const useDashboardAccounts = () => ({loading: false, failed: false, refresh: () => {}, items: []});
    `, shortCircuit: true};
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.svg')) return {format: 'module', source: 'export default "/asset.svg";', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  },
});

const {DashboardScreen} = await import('../src/screens/dashboard/Dashboard.tsx');
const {SessionCard} = await import('../src/screens/dashboard/SessionCard.tsx');
const {PhonePage} = await import('../src/screens/dashboard/phone.tsx');
const {captureReportContext} = await import('../../packages/client-core/src/report-context.ts');
const context = captureReportContext('UTC');
const scope = {serverId: 'server', viewerId: 'viewer'};
const page = {scope: {serverId: 'server', viewerFence: 'a'.repeat(64)}, observedAt: '2026-10-23T00:00:00Z', items: [], page: {limit: 10, start: 0, total: 0, revision: '1'}, totals: {streams: 0, atHome: 0, away: 0, paused: 0, converting: 0, people: 0, bandwidthKbps: 16_000}};
const readValue = {status: {}, series: {}, summary: {plays: 46, people: 4, watchedMs: 111_600_000, converted: 7, mostPlayed: [{name: 'The Last of Us', plays: 12}, {name: 'Severance', plays: 9}], mostActive: [{name: 'sam_k', plays: 18}, {name: 'demo', plays: 14}], period: '7d', buckets: []}, alerts: [{code: 'storage_nearly_full', severity: 'warning', major: false, action: {kind: 'storage', target: '/settings?scope=server&section=storage'}, params: {}}], scans: [{libraryId: 'library-1', libraryName: 'Movies', analyzed: 51, total: 80, discovered: 80, current: 'Practical Magic 2 (2026).mkv', kind: 'scan', phase: 'scanning'}], tasks: {count: 0, items: []}, observedAt: '2026-10-23T00:00:00Z'};
function bindings(role: 'owner' | 'admin' | 'member' = 'owner') {
  return {
    scope, role, context, isCurrent: () => true, isMainProfile: () => true,
    read: {sessions: async () => page as never, status: async () => readValue as never, telemetry: async () => ({} as never), report: async () => ({} as never), people: async () => ({} as never), plays: async () => ({} as never), server: async () => ({facts: []})},
    effects: {transport: async () => {}, stop: async () => {}, openHistory() {}, openHome() {}, openSettings() {}, reportError() {}},
    artwork: () => undefined, dispose() {},
  };
}
function session(overrides: Record<string, unknown> = {}) {
  return {id: 'session-1', user: 'sam_k', profile: 'Sam', device: 'Living room TV', title: 'Long, Long Time', kind: 'vod', state: 'playing', positionMs: 1_915_000, durationMs: 4_560_000,
    decision: {video: {action: 'original', reasons: []}}, media: {video: {id: 'video-1', codec: 'h264', height: 1080}}, location: 'local', startedAt: '2026-10-23T19:00:00Z', currentBitrateKbps: 8000, itemId: 'item-1',
    item: {id: 'item-1', title: 'Long, Long Time', kind: 'episode', showTitle: 'The Last of Us', seasonNumber: 1, episodeNumber: 3, art: []}, ...overrides};
}
function card(overrides: Record<string, unknown> = {}, role: 'owner' | 'admin' = 'owner') {
  return renderToStaticMarkup(createElement(SessionCard, {session: session(overrides) as never, bindings: bindings(role) as never, refresh: () => {}, layout: 'phone'}));
}
const headings = (html: string) => [...html.matchAll(/<h[23][^>]*>([^<]*)<\/h[23]>/g)].map(match => '>' + match[1] + '<');

test('the phone Dashboard is a page: its own bar and server header over the standard backdrop', () => {
  Object.assign(state, {authority: true, value: {...readValue, ...page, facts: [{id: 'uptime', role: 'value', text: 'Up for 6 days'}]}});
  const html = renderToStaticMarkup(createElement(DashboardScreen, {bindings: bindings() as never, phone: true, chrome: {serverName: 'Portico demo', audience: 'owner'}}));
  assert.match(html, /data-dashboard-page/);
  assert.match(html, /class="phoneBar"(?: [^>]*)?>.*<h1>Dashboard<\/h1>/s);
  // Justin, 29 Oct: no per-screen glow; the page shows the one standard backdrop.
  assert.doesNotMatch(html, /phoneBand/);
  assert.match(html, /Only you and administrators see this page/);
  assert.match(html, /Portico demo/);
  assert.match(html, />Updated a few seconds ago</);
  assert.match(html, />Refresh<\/button>/);
  assert.deepEqual(headings(html).slice(0, 5), ['>Now playing<', '>Alerts<', '>Background activity<', '>Health<', '>Viewing statistics<']);
  assert.doesNotMatch(html, /class="dashboard"/);
  for (const label of ['Plays', 'People', 'Hours watched', 'Converted', '15% of plays', 'Most played', 'Most active', 'Play history']) assert.match(html, new RegExp('>' + label + '<'), label);
  assert.match(html, />31</);
  assert.match(html, />The Last of Us</);
  assert.match(html, />sam_k</);
});

test('the desktop Dashboard keeps its Settings composition and its own section order', () => {
  Object.assign(state, {authority: true, value: {...readValue, ...page}});
  const html = renderToStaticMarkup(createElement(DashboardScreen, {bindings: bindings() as never, embedded: true}));
  assert.doesNotMatch(html, /data-dashboard-page|class="phoneBar"|>Updated a few seconds ago</);
  assert.deepEqual(headings(html).slice(0, 5), ['>Now playing<', '>Server load<', '>Top people<', '>Play history<', '>Top played<']);
  assert.doesNotMatch(html, /class="phoneSession"|data-dashboard-page/);
});

test('a member or a non-main profile gets no phone page at all', () => {
  Object.assign(state, {authority: true, value: {...readValue, ...page}});
  for (const role of ['member', 'admin'] as const) {
    state.authority = role === 'admin';
    const html = renderToStaticMarkup(createElement(DashboardScreen, {bindings: bindings(role) as never, phone: true, chrome: {serverName: 'Portico demo', audience: 'administrators'}}));
    if (role === 'member') {assert.equal(html, ''); continue;}
    assert.match(html, /Only administrators and the owner see this page/);
    assert.deepEqual(headings(html), ['>Now playing<']);
    assert.doesNotMatch(html, /Server load|Top people|Background activity/);
  }
  state.authority = false;
  assert.equal(renderToStaticMarkup(createElement(DashboardScreen, {bindings: bindings() as never, phone: true})), '');
  state.authority = true;
});

test('a phone stream card draws the drawn lines: artwork shape, username first, facts, Stop and its menu', () => {
  const html = card();
  assert.match(html, /class="phoneSession"/);
  assert.match(html, /<h3>The Last of Us<\/h3>/);
  assert.match(html, /<b>sam_k<\/b><span>Sam<\/span><span>Living room TV<\/span>/);
  assert.match(html, />Home network</);
  assert.match(html, />Original</);
  assert.match(html, />At 31:55 of 1:16:00</);
  assert.match(html, /class="button phoneStop"[^>]*>.*buttonLabel">Stop<\/span>/s);
  assert.match(html, /aria-label="Options for sam_k&#x27;s stream"/);
  assert.match(html, /class="phoneProgress"[^>]*><i style="width:41\.99/);
  for (const desktopOnly of ['class="session"', 'class="sessionArt"', 'class="sessionHover"']) assert.doesNotMatch(html, new RegExp(desktopOnly.replace(/["/]/g, '\\$&')));
  const desktop = renderToStaticMarkup(createElement(SessionCard, {session: session() as never, bindings: bindings() as never, refresh: () => {}}));
  assert.match(desktop, /class="session"/);
  assert.match(desktop, /class="sessionArt"/);
  assert.match(desktop, /class="sessionHover"/);
  assert.doesNotMatch(desktop, /phoneSession|phoneProgress/);
});

test('a paused phone stream says Paused, and only an owner sees Stop on the card', () => {
  const paused = card({state: 'paused'});
  assert.match(paused, /data-state="paused"/);
  assert.match(paused, /class="phoneState"[^>]*><svg[^>]*>[\s\S]*?<\/svg>Paused/);
  assert.doesNotMatch(card({}, 'admin'), /phoneStop|buttonLabel">Stop</);
});

test('the phone page header draws only the facts the server returned and offers Refresh, never a pull ring', () => {
  const html = renderToStaticMarkup(createElement(PhonePage, {
    chrome: {serverName: 'Portico demo', audience: 'owner'}, server: {health: 'attention', facts: [{id: 'uptime', role: 'value', text: 'Up for 6 days'}, {id: 'free', role: 'value', text: '1.2 TB free'}]},
    updatedAt: Date.now() - 120_000, onBack() {}, onRefresh() {}, children: createElement('p', {}, 'body'),
  }));
  assert.match(html, /Needs attention/);
  assert.match(html, />Up for 6 days</);
  assert.match(html, />1.2 TB free</);
  assert.match(html, />Updated 120 seconds ago</);
  assert.match(html, /aria-label="Back"[^>]*><svg/);
  assert.doesNotMatch(html, /Database/);
});
