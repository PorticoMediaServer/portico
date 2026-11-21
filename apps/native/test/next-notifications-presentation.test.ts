// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const react = {...React, default: React};
const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all)
  // The phone sheet frame receives its authored header as a prop (SUP-001).
  : React.isValidElement<any>(node) ? [node, ...all(node.props.header), ...all(node.props.children)] : [];
const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style || {};
const native = {View: 'View', Text: 'Text', ScrollView: 'ScrollView', FlatList: 'FlatList', StyleSheet: {absoluteFill: {}, create: (value: unknown) => value}};
const theme = {platform: 'phone', isTV: false, fontScale: 1, textScale: 1, gutter: 24,
  type: {tab: {}, button: {}}, colors: {ink: '#071018', raised: '#10212d', raisedBright: '#1d3848', text: '#edf5fa', textSecondary: '#bdccd6', lineStrong: '#3b5668'}};
const i18n = {t: (key: string) => key, has: () => true, number: String, relativeTime: () => 'Yesterday'};
const shellType = (fontSize: number, weight = 600) => ({fontSize, fontWeight: String(weight)});

test('Notifications is a Settings page: Unread with its count, Mark all read, and the notices as one card with rules between them', async () => {
  const hook = await screenModule(new URL('../src-next/pages/shared/notifications/useNotifications.ts', import.meta.url), {
    react, '../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../../../../packages/client-core/src/notifications': {noticeWords: (notice: any) => ({title: notice.id, body: 'Notice'})},
    '../../../platform/event-resync': {recoverEventState: () => () => {}},
    '../../../shell/account': {nativeMenuRouteAvailable: () => true},
    '../../../shell/model': {sameAuthority: (a: any, b: any) => a.serverId === b.serverId},
    '../../../screens/notifications/notification-route': await import('../src-next/screens/notifications/notification-route.ts'),
  });
  const {NotificationsPage} = await screenModule(new URL('../src-next/pages/touch/notifications/NotificationsPage.tsx', import.meta.url), {
    react, '../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../kit/controls': {Button: 'Button', Tabs: 'Tabs'},
    '../../../kit/frames': {SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame', SheetFrame: 'SheetFrame', StateRow: 'StateRow'},
    '../../../kit/overlays': {Menu: 'Menu'},
    '../../../kit/shell': {NotificationItem: 'NotificationItem', NotificationSubject: 'NotificationSubject'},
    '../../../kit/shell/OwnFeedbackReport': {OwnFeedbackReport: 'OwnFeedbackReport'},
    '../../../kit/theme': {useTheme: () => theme},
    '../../shared/notifications/useNotifications': hook,
  });
  const item = (id: string) => ({id, audience: 'profile', read: false, archived: false, severity: 'info', revision: '1', source: 'downloads', actions: [], createdAt: '2026-10-01T00:00:00Z'});
  const state = {phase: 'ready', audiences: ['profile'], counts: {unread: 3}, items: [item('one'), item('two'), item('three')], view: 'unread', more: false, busy: false};
  const profileService = {subscribe: () => () => {}, getSnapshot: () => state, load: async () => {}, readAll: async () => {}};
  const authority = {serverId: 'server'}, route = {kind: 'notifications'};
  const navigation = {route, revision: 1, destination: {kind: 'settings'}};
  const context = {authority: () => authority, account: {account: {role: 'owner'}},
    navigation: {getSnapshot: () => navigation}, events: {on: () => () => {}, onResync: () => () => {}}};
  const runtime = {getSnapshot: () => ({hasLiveTV: false}), text: i18n};
  const retirement = {subscribe: () => () => {}, getSnapshot: () => undefined};
  let tree: any;
  const host = createFabricHookHost(() => {tree = NotificationsPage({context, route, runtime, profileService, retirement});});
  await host.mount(); await host.flush();
  try {
    const sheet = all(tree).find(node => node.type === 'SheetFrame');
    assert.equal(sheet.props.kind, 'full', 'a phone shows Notifications as a page in the full sheet');
    assert.equal(sheet.props.open, true);
    const page = all(tree).find(node => node.type === 'SettingsPageFrame');
    assert.equal(page.props.back.label, 'next.shell.settings');
    assert.equal(page.props.action.testID, 'notifications-mark-all');
    const tabs = all(page.props.tabs).find(node => node.type === 'Tabs');
    assert.equal(tabs.props.items[0].count, '3');
    const cards = all(page.props.children).filter(node => node.type === 'SectionCard');
    assert.equal(cards.length, 1);
    const rows = all(cards[0].props.children).filter(node => node.type === 'NotificationItem');
    assert.deepEqual(rows.map(node => [node.props.notification.id, node.props.separator]), [['one', false], ['two', true], ['three', true]]);
  } finally {await host.unmount();}
});

test('a notification row omits its top rule unless the caller supplies a preceding row', async () => {
  const {NotificationItem} = await screenModule(new URL('../src-next/kit/shell/AccountShell.tsx', import.meta.url), {
    react, 'react-native': native, '../../../../../packages/i18n/src/index': {defaultI18n: i18n},
    '../artwork/Profiles': {ProfileFace: 'ProfileFace'}, '../controls/Focusable': {Focusable: 'Focusable'},
    '../overlays/Menu': {Popover: 'Popover'}, '../frames/SheetFrame': {SheetFrame: 'SheetFrame'}, '../frames/metrics': {useFrameMetrics: () => ({form: 'expanded'})}, '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: false})},
    '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme}, '../focus/registry': {focusTestID: (...parts: string[]) => parts.join(':')},
    './visual': {shellType, alpha: (color: string, opacity: number) => `${color}:${opacity}`},
  });
  const props = {notification: {id: 'one', read: false, archived: false, severity: 'info'}, title: 'Title', body: 'Notice', when: 'Yesterday',
    subject: null, actions: [], onMenu() {}, menuLabel: 'More'};
  assert.equal(flatten(NotificationItem(props).props.style).borderTopWidth, 0);
  assert.equal(flatten(NotificationItem({...props, separator: true}).props.style).borderTopWidth, 1);
});

test('Tabs draws the count as a distinct pill while retaining tab semantics and large-text growth', async () => {
  let fontScale = 1;
  const {Tabs, Segmented} = await screenModule(new URL('../src-next/kit/controls/Tabs.tsx', import.meta.url), {
    react, 'react-native': native,
    '@portico/design': {tabsSpec: {gap: {phone: 34}, insetX: {phone: 0}, paddingTop: {phone: 16}, paddingBottom: {phone: 14}, underline: {phone: 3}}},
    '../focus': {useCovered: () => false, focusTestID: (...parts: string[]) => parts.join(':')}, '../theme': {useTheme: () => ({...theme, fontScale})},
    './Focusable': {Focusable: 'Focusable'}, './control-model': {colorAlpha: () => 'transparent', enabledChoice: (items: any[]) => items[0]?.value, minimumHitSlop: () => 0},
    './useControlValue': {useControlValue: (value: unknown, initial: unknown, change: unknown) => [value ?? initial, change]},
    '../Icon': {Icon: 'Icon'},
  });
  const items = [{value: 'unread', label: 'Unread', count: '12'}, {value: 'all', label: 'All'}];
  let tree: any;
  const host = createFabricHookHost(() => {tree = Tabs({items, value: 'unread', onValueChange() {}, testID: 'tabs'});});
  await host.mount();
  try {
    const tab = all(tree).find(node => node.props.testID === 'tabs-item-unread');
    assert.equal(tab.props.accessibilityRole, 'tab');
    assert.equal(tab.props.accessibilityLabel, 'Unread, 12');
    const choice = all(tab).find(node => typeof node.type === 'function');
    const label = choice.type(choice.props), texts = all(label).filter(node => node.type === 'Text');
    assert.deepEqual(texts.map(node => node.props.children), ['Unread', '12']);
    const pill = flatten(texts[1].props.style);
    assert.equal(pill.fontSize, 12); assert.equal(pill.borderRadius, 9); assert.equal(pill.marginLeft, 6);
    assert.equal(pill.paddingHorizontal, 7); assert.equal(pill.paddingVertical, 1);
    fontScale = 2; await host.rerender();
    const enlarged = all(tree).find(node => node.props.testID === 'tabs-item-unread');
    const enlargedChoice = all(enlarged).find(node => typeof node.type === 'function');
    assert.equal(all(enlargedChoice.type(enlargedChoice.props)).find(node => node.type === 'Text').props.numberOfLines, undefined);
  } finally {await host.unmount();}
  const segmented = createFabricHookHost(() => {tree = Segmented({items, value: 'unread', onValueChange() {}, variant: 'phone'});});
  await segmented.mount();
  try {
    const choice = all(tree).find(node => node.type === 'Focusable'), style = flatten(choice.props.style({focused: false}));
    assert.equal(style.minHeight, 28); assert.equal(style.height, undefined);
  } finally {await segmented.unmount();}
});
