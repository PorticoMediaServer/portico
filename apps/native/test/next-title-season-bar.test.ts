import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const flat = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flat)) : style ?? {};

async function bar(platform: 'phone' | 'tablet' | 'tv') {
  const h = hooks();
  const {TitleSeasonBar} = await screenModule(new URL('../src-next/screens/watch/TitleSeasonBar.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../kit/controls': {Button: 'Button', Switch: 'Switch'},
    '../../kit/theme': {useTheme: () => ({isTV: platform === 'tv', platform, type: {caption: {}, bodyStrong: {}}, colors: {textTertiary: '#888', textSecondary: '#aaa'}})}});
  return (owner: boolean) => nodes(h.render(() => TitleSeasonBar({label: 'Unassigned absolute episodes', count: '3 episodes', onSeasons() {}, hideWatched: false, onHideWatched() {},
    settings: owner ? {label: 'More', onPress() {}} : undefined})));
}
const byID = (tree: any[], id: string) => tree.find(node => node.props?.testID === id);

for (const platform of ['phone', 'tablet'] as const) test(`W-10 ${platform}: a long season name truncates in its chip and Hide watched and ⋯ never leave the screen`, async () => {
  const tree = (await bar(platform))(true);
  const chip = flat(byID(tree, 'watch-seasons-open').props.style), count = flat(byID(tree, 'watch-season-count').props.style);
  assert.equal(chip.flexShrink, 1, 'the chip may shrink; its label ellipsizes'); assert.equal(chip.minWidth, 0);
  assert.ok(count.flexShrink > chip.flexShrink, 'the count gives way before the chip'); assert.equal(byID(tree, 'watch-season-count').props.numberOfLines, 1);
  assert.equal(flat(byID(tree, 'watch-season-controls').props.style).flexShrink, 0, 'Hide watched and its switch keep their width');
  assert.equal(flat(byID(tree, 'watch-show-settings-open').props.style).flexShrink, 0, 'the owner ⋯ keeps its width');
  assert.ok(byID(tree, 'watch-hide-watched'));
});

test('TV draws no Hide watched switch in the bar (its season list owns that) and a member gets no ⋯', async () => {
  const render = await bar('tv');
  assert.equal(byID(render(false), 'watch-season-controls'), undefined);
  assert.equal(byID(render(false), 'watch-show-settings-open'), undefined);
});
