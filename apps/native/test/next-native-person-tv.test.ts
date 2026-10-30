// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';

const {react} = {react: {...React, default: React}};
const themed = {useTheme: () => ({isTV: true, colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', raised: '#111', line: '#222', accent: '#3af', accentStrong: '#4bf'}, type: {}})};
const {TVPerson} = await screenModule(new URL('../src-next/screens/people/PersonScreen.tsx', import.meta.url), {'../share/share-actions': {shareLinkActions: () => []}, 
  react,
  'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', VirtualizedList: 'VirtualizedList', useWindowDimensions: () => ({width: 1920, height: 1080})},
  'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
  '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', PersonCircle: 'PersonCircle', AmbientArt: 'AmbientArt', Artwork: 'Artwork'},
  '../../kit/cards': {PosterCard: 'PosterCard'}, '../../kit/controls': {Chip: 'Chip', Focusable: 'Focusable'},
  '../../kit/Icon': {Icon: 'Icon'}, '../../kit/states': {StateView: 'StateView'},
  '../../kit/browse/presentation': {browseType: (_theme: unknown, size: number, weight: number, tracking = 0) => ({fontSize: size, fontWeight: weight, tracking})},
  '../../kit/theme': themed, '../../kit/overlays': {Menu: 'Menu', MenuEntry: {}},
  '../../tv/layout/TVShelf': {TVShelf: 'TVShelf'},
});
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];
const flat = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flat)) : style ?? {};
const credits = [
  {media: {id: 'first', kind: 'movie', title: 'Saving Private Ryan', year: 1998, posterUrl: '/p1'}, creditKind: 'crew', role: 'Director'},
  {media: {id: 'second', kind: 'movie', title: 'Jurassic Park', year: 1993, posterUrl: '/p2'}, creditKind: 'crew', role: 'Director'},
];
const page = {person: {id: 'spielberg', name: 'Steven Spielberg', roles: ['Directing', 'Production'], birthDate: '1946-12-18',
  biography: 'Steven Allan Spielberg is an American filmmaker.', knownFor: []}, pageInfo: {total: 2}} as any;
const collection = {pageSize: 60, pageOf: () => 0, subscribe: () => () => {}, subscribePage: () => () => {},
  getSnapshot: () => ({generation: 1, total: 2, status: 'ready', refreshing: false, totalIsLowerBound: false}),
  getPageVersion: () => 'v1', itemAt: (index: number) => credits[index], ensureRange: () => {}, retry: () => {}} as any;
let backHandler: (() => boolean) | undefined;
const runtime = {artwork: () => undefined, ambient: () => undefined, text: {locale: 'en-US'}, registerTVPageBack: (handler: () => boolean) => {backHandler = handler; return () => {};}, error: () => ({})} as any;

test('TV person draws the ten-foot composition from the real person page', async () => {
  const opened: any[] = [], roles: string[] = [], backs: number[] = [];
  let tree: any;
  const host = createFabricHookHost(() => {tree = TVPerson({focusScope: 'person:spielberg', page, role: 'all', collection, runtime, actions: {open: (entry: unknown) => opened.push(entry), menuState: {subscribe: () => () => {}, getSnapshot: () => 0}, isMenuOpen: () => false} as any,
    onRole: (role: any) => roles.push(role), onBack: () => {backs.push(1);}});});
  await host.mount();
  try {
    const nodes = descendants(tree);
    assert.equal(nodes.find(node => node.type === 'PersonCircle').props.size, 280);
    const name = nodes.find(node => node.type === 'Text' && node.props.children === 'Steven Spielberg');
    assert.equal(flat(name.props.style).fontSize, 96, 'tv-detail#5 name at display size');
    const born = nodes.find(node => node.type === 'Text' && node.props.children === defaultI18n.t('web.person.born', {date: defaultI18n.date('1946-12-18', 'long')}));
    assert.ok(born);
    assert.ok(nodes.some(node => node.type === 'Text' && node.props.children === defaultI18n.t('web.person.titles', {count: 2})));
    const bio = nodes.find(node => node.props.testID === 'person-biography');
    assert.ok(descendants(bio).some(node => node.type === 'Text' && node.props.children === 'Steven Allan Spielberg is an American filmmaker.'));
    bio.props.onPress(); await host.rerender();
    const expanded = descendants(tree).find(node => node.props.testID === 'person-biography');
    assert.equal(expanded.props.accessibilityState.expanded, true);
    const menu = nodes.find(node => node.type === 'Menu');
    assert.deepEqual(menu.props.items.map((item: any) => item.id), ['all', 'cast', 'crew']);
    menu.props.items[2].onSelect(); assert.deepEqual(roles, ['crew']);
    const shelf = nodes.find(node => node.type === 'TVShelf');
    assert.ok(shelf, 'the credits are drawn by the shared TV shelf');
    assert.deepEqual(shelf.props.items, [0, 1], 'every real credit keeps its own shelf slot');
    const card = shelf.props.renderItem(0, {index: 0, id: '0', onFocus: () => {}, onBlur: () => {}});
    assert.equal(card.props.testID, 'person-credit-0');
    assert.equal(card.props.hasTVPreferredFocus, true, 'the first real credit is the page focus target');
    let poster: any; const cardHost = createFabricHookHost(() => {poster = card.type(card.props);}); await cardHost.mount();
    try {poster.props.onNavigate(); assert.deepEqual(opened, [credits[0]!.media]);} finally {await cardHost.unmount();}
    assert.equal(backHandler?.(), true);
    assert.deepEqual(backs, [1]);
  } finally {await host.unmount();}
});
