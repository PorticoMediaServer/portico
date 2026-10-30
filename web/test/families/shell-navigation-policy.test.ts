import assert from 'node:assert/strict';
import test from 'node:test';
import {registerHooks} from 'node:module';
import {Children, createElement, isValidElement, type ReactElement, type ReactNode} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {SettingsNav, ShortcutTiles} = await import('../../src/kit/shell/SettingsSurfaces.tsx');
type Element = ReactElement<Record<string, any>>;
function nodes(node: ReactNode): Element[] {
  return Children.toArray(node).flatMap(child => isValidElement<Record<string, any>>(child) ? [child, ...nodes(child.props.children)] : []);
}
const summaries = [
  {id: 'dashboard', label: 'Dashboard', icon: 'i-pulse' as const, count: 2, warning: true, trailing: '2 playing'},
  {id: 'libraries', label: 'Libraries', icon: 'i-grid' as const, count: 3, warning: true, trailing: '5'},
  {id: 'people', label: 'People', icon: 'i-people' as const, count: 1, warning: true},
  {id: 'troubleshooting', label: 'Troubleshooting', icon: 'i-wrench' as const, count: 2},
  {id: 'segments', label: 'Intros and credits', icon: 'i-next' as const, count: 7},
  {id: 'live', label: 'Live TV & Channels', icon: 'i-tv' as const, trailing: '14 channels'},
];
const states = [
  {id: 'remote', label: 'Remote access', icon: 'i-link' as const, stateValue: 'On'},
  {id: 'state-with-digits', label: 'Configured state', icon: 'i-cog' as const, stateValue: 'HTTP/2'},
];
const props = {accountName: 'actual_user', serverName: 'Actual server', viewerRole: 'owner' as const, account: [{id: 'playback', label: 'Playback', icon: 'i-play' as const}], server: [...summaries, ...states], selectedId: 'libraries', query: '', onQueryChange() {}, onNavigate(_id: string) {}, onSearchResult(_page: string, _row: string) {}};
for (const layout of ['desktop', 'phone'] as const) {
  test(`${layout}: actual Settings navigation drops summaries, preserves explicit states, and only marks a known major row`, () => {
    const ordinary = renderToStaticMarkup(createElement(SettingsNav, {...props, layout}));
    assert.doesNotMatch(ordinary, /2 playing|14 channels|class="count"|data-warning|role="img"|>\d+<\/span>/);
    for (const value of ['On', 'HTTP/2']) assert.ok(ordinary.includes(`>${value}</span>`));
    const major = renderToStaticMarkup(createElement(SettingsNav, {...props, layout, server: [...props.server, {id: 'storage', label: 'Storage & backups', icon: 'i-disk', majorIssue: true}]}));
    assert.equal((major.match(/class="majorIssue"/g) ?? []).length, 1);
    assert.match(major, /Storage &amp; backups<\/span><span class="majorIssue" role="img" aria-label="Needs attention"><\/span>/);
    assert.doesNotMatch(major, /data-warning|class="count"/);
  });
}
test('real row selection invokes the original callback without changing the admitted role or selection', () => {
  const navigated: string[] = [];
  const tree = SettingsNav({...props, onNavigate: id => navigated.push(id)});
  const buttons = nodes(tree).filter(node => node.type === 'button');
  const library = buttons.find(node => nodes(node).some(child => child.props.children === 'Libraries'))!;
  assert.equal(library.props['aria-current'], 'page'); library.props.onClick();
  assert.deepEqual(navigated, ['libraries']);
  const member = renderToStaticMarkup(createElement(SettingsNav, {...props, viewerRole: 'member'}));
  assert.match(member, />Playback<\/span>/); assert.doesNotMatch(member, /Actual server|>Dashboard<\/span>|majorIssue/);
  const admin = renderToStaticMarkup(createElement(SettingsNav, {...props, viewerRole: 'administrator'}));
  assert.match(admin, /Administrator/); assert.doesNotMatch(admin, /Owner only/);
});
test('Settings shortcuts omit incoming summary counts while keeping real action callbacks', () => {
  const calls: string[] = [];
  const items = [{id: 'notifications', label: 'Notifications', icon: 'i-bell' as const, count: 7, onAction: () => calls.push('notifications')}];
  const html = renderToStaticMarkup(createElement(ShortcutTiles, {items}));
  assert.match(html, /Notifications/); assert.doesNotMatch(html, /shortcutBadge|>7</);
  nodes(ShortcutTiles({items})).find(node => node.type === 'button')!.props.onClick();
  assert.deepEqual(calls, ['notifications']);
});
test('Settings search retains real match counts and admitted content rows', () => {
  const matches = [{id: 'playback', label: 'Playback', icon: 'i-play' as const, area: 'account' as const, rows: [{id: 'one', label: 'Skip intro'}, {id: 'two', label: 'Skip credits'}]}, {id: 'libraries', label: 'Libraries', icon: 'i-grid' as const, area: 'server' as const, rows: [{id: 'private', label: 'Skip files'}]}];
  const html = renderToStaticMarkup(createElement(SettingsNav, {...props, viewerRole: 'member', query: 'Skip', matches}));
  assert.match(html, /2 settings match/); assert.doesNotMatch(html, /Skip files/);
});

test('real owner Activity glyph retains its actual total stream number outside Settings navigation', async () => {
  const {TopBarActivity} = await import('../../src/kit/shell/Activity.tsx');
  const view = {serverName: 'Actual server', streamCount: 27, taskCount: 0, streams: [], scans: [], tasks: [], phase: 'ready' as const, onRetry() {}, onDashboard() {}};
  const html = renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: 'owner', view, open: false, onOpenChange() {}}));
  assert.match(html, /class="streamCount">27<\/span>/);
});
