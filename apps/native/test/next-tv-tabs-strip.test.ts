import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// W-17: a horizontal ScrollView is focusable on Android TV; with passive tabs it took D-pad focus
// itself (no visible focus, Right went nowhere). On TV the strip is a plain, unfocusable row.
async function tabs(isTV: boolean) {
  const h = hooks();
  const {Tabs} = await screenModule(new URL('../src-next/kit/controls/Tabs.tsx', import.meta.url), {
    react: {...h.react, useId: () => 'id', default: {...h.react.default, useId: () => 'id'}}, 'react-native': {ScrollView: 'ScrollView', StyleSheet: {create: (value: unknown) => value, absoluteFill: {}}, Text: 'Text', View: 'View'},
    '@portico/design': {tabsSpec: {gap: {tv: 40, phone: 20}, paddingTop: {tv: 0, phone: 0}, paddingBottom: {tv: 0, phone: 0}, underline: {tv: 3, phone: 2}}},
    '../focus': {focusTestID: (a: string, b: string) => `${a}.${b}`},
    '../theme': {useTheme: () => ({isTV, platform: isTV ? 'tv' : 'phone', gutter: 0, textScale: 1, fontScale: 1, type: {tab: {}}, colors: {line: 'l', text: 't', textTertiary: 't3', accent: 'a', raisedBright: 'r', textSecondary: 's'}})},
    './Focusable': {Focusable: 'Focusable'}, './control-model': {colorAlpha: (c: string) => c, enabledChoice: (items: any[]) => items[0]?.value, minimumHitSlop: () => 0},
    './useControlValue': {useControlValue: (value: string) => [value, () => {}]},
  });
  return (passive: boolean) => h.render(() => Tabs({testID: 'strip', value: 'related', passive, items: [{value: 'related', label: 'More like this'}, {value: 'details', label: 'Details'}]})) as any;
}
const kids = (node: any): any[] => Array.isArray(node) ? node.flatMap(kids) : node?.props ? [node, ...kids(node.props.children)] : [];

test('W-17: on TV the tab strip is a plain unfocusable row whose tabs are the focus stops', async () => {
  const render = await tabs(true);
  const tree = render(false), all = kids(tree);
  assert.equal(all.some(node => node.type === 'ScrollView'), false, 'no focusable scroll container on TV');
  const row = all.find(node => node.props.accessibilityRole === 'tablist');
  assert.equal(row.type, 'View'); assert.equal(row.props.focusable, false);
  assert.deepEqual(all.filter(node => node.type === 'Focusable').map(node => node.props.focusId), ['strip.related', 'strip.details'], 'More like this, then Details to its right');
  assert.equal(kids(render(true)).filter(node => node.type === 'Focusable').length, 0, 'a passive marker copy offers no stops');
});

test('touch keeps its scrolling strip', async () => {
  const render = await tabs(false);
  assert.ok(kids(render(false)).some(node => node.type === 'ScrollView'));
});
