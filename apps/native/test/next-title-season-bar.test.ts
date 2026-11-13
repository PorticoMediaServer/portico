import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const flat = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flat)) : style ?? {};

async function bar(platform: 'phone' | 'tablet' | 'tv') {
  const h = hooks();
  const {TitleSeasonBar} = await screenModule(new URL('../src-next/screens/watch/TitleSeasonBar.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../kit/controls': {Button: 'Button', Switch: 'Switch'}, '../../kit/overlays': {Menu: 'Menu'},
    '../../kit/theme': {useTheme: () => ({isTV: platform === 'tv', platform, type: {caption: {}, bodyStrong: {}}, colors: {textTertiary: '#888', textSecondary: '#aaa'}})}});
  const changes: boolean[] = [], opened: string[] = [];
  const render = (owner: boolean) => nodes(h.render(() => TitleSeasonBar({label: 'Unassigned absolute episodes', count: '3 episodes', onSeasons() {}, hideWatched: false, onHideWatched: value => changes.push(value),
    settings: owner ? {label: 'More', onPress: () => opened.push('settings')} : undefined})));
  return Object.assign(render, {changes, opened});
}
const byID = (tree: any[], id: string) => tree.find(node => node.props?.testID === id);

for (const platform of ['phone', 'tablet'] as const) test(`${platform}: the bar follows the frames by role; the count never gives way and a long season name truncates in its chip`, async () => {
  const render = await bar(platform);
  // A viewer (phone-app ep-bar): chip · count · Hide watched.
  const viewer = render(false);
  const chip = flat(byID(viewer, 'watch-seasons-open').props.style), count = flat(byID(viewer, 'watch-season-count').props.style);
  assert.equal(chip.flexShrink, 1, 'the chip may shrink; its label ellipsizes'); assert.equal(chip.minWidth, 0);
  assert.equal(count.flexShrink, 0, 'the count never gives way');
  assert.equal(flat(byID(viewer, 'watch-season-controls').props.style).flexShrink, 0);
  assert.ok(byID(viewer, 'watch-hide-watched')); assert.equal(byID(viewer, 'watch-show-settings-open'), undefined);
  // Whoever has the ⋯ actions (phone-detail ep-bar): chip · count · ⋯, with Hide watched first inside ⋯.
  const owner = render(true);
  assert.equal(byID(owner, 'watch-season-controls'), undefined); assert.equal(byID(owner, 'watch-hide-watched'), undefined);
  assert.equal(flat(byID(owner, 'watch-show-settings-open').props.style).flexShrink, 0);
  const items = byID(owner, 'watch-season-menu').props.items;
  assert.deepEqual(items.map((item: any) => [item.id, item.label]), [['hide-watched', 'next.controls.hideWatched'], ['show-settings', 'title.showSettings']]);
  assert.equal(items[0].selected, false);
  items[0].onSelect(); items[1].onSelect();
  assert.deepEqual(render.changes, [true]); assert.deepEqual(render.opened, ['settings']);
});

test('TV draws no Hide watched switch in the bar (its season list owns that) and a member gets no ⋯', async () => {
  const render = await bar('tv');
  assert.equal(byID(render(false), 'watch-season-controls'), undefined);
  assert.equal(byID(render(false), 'watch-show-settings-open'), undefined);
});
