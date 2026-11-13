import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const theme = {colors: {ink: '#071018', text: '#edf5fa', textSecondary: '#a9b8c4'}, layers: {sticky: 20}};
async function topBar() {
  const h = hooks();
  const module = await screenModule(new URL('../src-next/kit/layout/TopBar.tsx', import.meta.url), {
    react: (() => {const react = {...h.react.default, forwardRef: (render: unknown) => render}; return {...react, default: react};})(), 'react-native': {StyleSheet: {absoluteFill: {position: 'absolute'}, create: (styles: object) => styles}, Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 59, bottom: 34, left: 0, right: 0})},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, '../../platform/native-blur': {BlurView: 'BlurView'}, '../../platform/glass': {glassRecipe: () => ({nativeBlur: true})},
    '../controls/Focusable': {Focusable: 'Focusable'}, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme},
    '../shell/visual': {alpha: (color: string, value: number) => `${color}/${value}`, shellType: (size: number, weight: number) => ({fontSize: size, fontWeight: weight})},
  });
  return {module, render: (node: () => any) => h.render(node)};
}
const flat = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.flat(Infinity).filter(Boolean)) : style ?? {};
const nodes = (tree: any): any[] => !tree || typeof tree !== 'object' ? [] : Array.isArray(tree) ? tree.flatMap(nodes) : [tree, ...nodes(tree.props?.children)];

// 12 Nov phone survey: about 30 screens drew their own back row and bar, 2 pt apart, and the library header drew
// its blur over its tint (a grey band). One bar: phone-app/phone-read `.bar` and `.ph-top.solid`.
test('the shared top bar is the drawn 44 pt row under the safe area: "‹ Label", a centred 17/700 title, trailing actions', async () => {
  const {module, render} = await topBar();
  let backs = 0;
  const tree = render(() => module.TopBar({back: {label: 'Saved', onPress: () => {backs++;}}, title: 'Favorites', trailing: 'PLUS'}));
  assert.equal(flat(tree.props.style).paddingTop, 59, 'the bar starts under the actual safe area');
  const all = nodes(tree), row = all.find(node => flat(node.props.style).height === 44);
  assert.deepEqual([flat(row.props.style).paddingLeft, flat(row.props.style).paddingRight], [12, 10]);
  const back = all.find(node => node.type === 'Focusable');
  assert.equal(back.props.accessibilityLabel, 'Saved'); back.props.onPress(); assert.equal(backs, 1);
  assert.deepEqual([flat(back.props.style).height, flat(back.props.style).marginLeft], [38, -4]);
  const label = all.find(node => node.type === 'Text' && node.props.children.includes('Saved'));
  assert.deepEqual([flat(label.props.style).fontSize, flat(label.props.style).fontWeight, flat(label.props.style).color], [16, 600, theme.colors.textSecondary]);
  const centre = all.find(node => flat(node.props.style).left === 118 && flat(node.props.style).right === 118);
  const title = nodes(centre).find(node => node.type === 'Text');
  assert.deepEqual([flat(title.props.style).fontSize, flat(title.props.style).fontWeight, title.props.children], [17, 700, ['Favorites']]);
  assert.ok(all.some(node => node.props?.children?.includes?.('PLUS')), 'trailing actions sit at the end of the row');
  assert.equal(all.some(node => node.type === 'BlurView'), false, 'no surface unless the page scrolls under the bar');
});

test('the bar surface draws the blur first and the .82 ink tint above it, with one hairline', async () => {
  const {module, render} = await topBar();
  const surface = render(() => module.BarSurface({}));
  const [blur, tint] = [surface.props.children].flat().filter(Boolean);
  assert.equal(blur.type, 'BlurView');
  assert.equal(flat(tint.props.style).backgroundColor, '#071018/0.82');
  assert.equal(flat(surface.props.style).borderBottomWidth, 1);
});

// 12 Nov: the Home header's gradient was an SVG 100% wide inside 20 pt padding, so it stopped 40 pt short of the
// right edge (a hard seam over the backdrop). The scrim is its own full-bleed layer, unpadded.
test('the bar scrim covers the whole bar: an unpadded full-bleed layer, ink .75 fading to clear', async () => {
  const {module, render} = await topBar();
  const scrim = render(() => module.BarScrim());
  assert.equal(flat(scrim.props.style).position, 'absolute');
  assert.equal(flat(scrim.props.style).paddingHorizontal, undefined);
  const svg = [scrim.props.children].flat()[0];
  assert.deepEqual([svg.props.width, svg.props.height], ['100%', '100%']);
  const stops = nodes(svg).filter(node => node.type === 'Stop').map(node => node.props.stopOpacity);
  assert.deepEqual(stops, [.75, 0]);
});
