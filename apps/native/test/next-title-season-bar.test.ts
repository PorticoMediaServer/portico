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
  const render = (owner: boolean) => nodes(h.render(() => TitleSeasonBar({label: 'Unassigned absolute episodes', count: '3 episodes', onSeasons() {}, hideWatched: false, onHideWatched: (value: boolean) => changes.push(value),
    settings: owner ? {label: 'More', onPress: () => opened.push('settings')} : undefined})));
  return Object.assign(render, {changes, opened});
}
const byID = (tree: any[], id: string) => tree.find(node => node.props?.testID === id);

const layout = (node: any, width: number) => node.props.onLayout({nativeEvent: {layout: {width}}});

for (const platform of ['phone', 'tablet'] as const) test(`${platform}: the bar follows the frames by role; the chip's label is never cut`, async () => {
  const render = await bar(platform);
  // A viewer (phone-app ep-bar): chip · count · Hide watched.
  const viewer = render(false);
  const chip = byID(viewer, 'watch-seasons-open');
  assert.deepEqual([flat(chip.props.style).flexShrink, flat(chip.props.style).maxWidth, chip.props.labelLines], [0, '100%', 2], 'the chip keeps its label, wrapping to two lines before any cut');
  assert.equal(flat(byID(viewer, 'watch-season-bar').props.style).flexWrap, 'wrap', 'a short row wraps rather than squeezing the chip');
  assert.equal(flat(byID(viewer, 'watch-season-end').props.style).flexShrink, 0);
  assert.ok(byID(viewer, 'watch-season-controls')); assert.ok(byID(viewer, 'watch-hide-watched')); assert.equal(byID(viewer, 'watch-show-settings-open'), undefined);
  // Whoever has the ⋯ actions (phone-detail ep-bar): chip · count · ⋯, with Hide watched first inside ⋯.
  const owner = render(true);
  assert.equal(byID(owner, 'watch-season-controls'), undefined); assert.equal(byID(owner, 'watch-hide-watched'), undefined);
  assert.ok(byID(owner, 'watch-show-settings-open'));
  const items = byID(owner, 'watch-season-menu').props.items;
  assert.deepEqual(items.map((item: any) => [item.id, item.label]), [['hide-watched', 'next.controls.hideWatched'], ['show-settings', 'title.showSettings']]);
  assert.equal(items[0].selected, false);
  items[0].onSelect(); items[1].onSelect();
  assert.deepEqual(render.changes, [true]); assert.deepEqual(render.opened, ['settings']);
});

for (const owner of [false, true]) test(`${owner ? 'owner' : 'viewer'}: when the row is short the count drops first, still measured and folded into the chip's spoken name`, async () => {
  const render = await bar('phone');
  let tree = render(owner);
  const gap = 8, measured = (row: number) => {
    layout(byID(tree, 'watch-season-bar'), row); layout(byID(tree, 'watch-seasons-open'), 200);
    layout(byID(tree, 'watch-season-count'), 70); layout(byID(tree, 'watch-season-end'), 90);
    tree = render(owner);
    return byID(tree, 'watch-season-count');
  };
  let count = measured(200 + gap + 70 + gap + 90);
  assert.equal(flat(count.props.style).position, undefined, 'everything fits: the count shows');
  assert.equal(byID(tree, 'watch-seasons-open').props.accessibilityLabel, undefined);
  count = measured(200 + gap + 70 + gap + 89);
  assert.deepEqual([flat(count.props.style).position, flat(count.props.style).opacity, count.props.accessibilityElementsHidden], ['absolute', 0, true], 'one point short: the count drops out of the row');
  assert.equal(byID(tree, 'watch-seasons-open').props.accessibilityLabel, 'Unassigned absolute episodes, 3 episodes');
  count = measured(200 + gap + 70 + gap + 90);
  assert.equal(flat(count.props.style).position, undefined, 'room again: the count comes back');
});

test('TV draws no Hide watched switch in the bar (its season list owns that) and a member gets no ⋯', async () => {
  const render = await bar('tv');
  assert.equal(byID(render(false), 'watch-season-controls'), undefined);
  assert.equal(byID(render(false), 'watch-show-settings-open'), undefined);
});
