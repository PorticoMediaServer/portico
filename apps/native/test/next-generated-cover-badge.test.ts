import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Walk W-1 (2 Nov): on artwork-free New cards the New badge covered the title drawn inside the generated
// cover. As phone-read.html "Recently added" draws it, the badge takes the place of the cover's small rule and
// the title starts below the badge.
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};

async function cover(clearance: number) {
  const context = {value: clearance};
  const react = {
    createElement: (type: any, props: any, ...children: any[]) => ({type, props: {...props, ...(children.length ? {children: children.length === 1 ? children[0] : children} : {})}}),
    createContext: () => context, useContext: (c: typeof context) => c.value, useState: (value: unknown) => [value, () => {}],
  };
  const theme = {colors: {text: '#edf5fa', ink: '#071018', textSecondary: '#a9b8c3'}};
  const {GeneratedCover} = await screenModule(new URL('../src-next/kit/artwork/Generated.tsx', import.meta.url), {
    react: {...react, default: react},
    'react-native': {View: 'View', Text: 'Text', StyleSheet: {absoluteFillObject: {position: 'absolute', inset: 0}}},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop'},
    '../theme': {useTheme: () => theme, fontFamilies: {700: 'Manrope-Bold', 800: 'Manrope-ExtraBold'}},
    './Artwork': {BookSpine: 'BookSpine'}, './model': {mixColour: () => '#0a1620'}, './Surfaces': {PaletteWash: 'PaletteWash'},
  });
  const tree = GeneratedCover({title: 'Wire Tap', glyph: 'W', tint: {first: '#2f86bb', second: '#1e5b52'}, width: 120, height: 180});
  const all = nodes(tree);
  const titleText = all.find(node => node.type === 'Text' && node.props.children === 'Wire Tap');
  const block = all.find(node => node.type === 'View' && nodes(node.props.children).includes(titleText) && flatten(node.props.style).paddingTop !== undefined);
  const rule = all.find(node => node.type === 'View' && flatten(node.props.style).width === 120 * 0.09);
  return {paddingTop: flatten(block.props.style).paddingTop as number, rule};
}

test('W-1: with no badge the cover keeps its rule and its usual top', async () => {
  const plain = await cover(0);
  assert.ok(plain.rule, 'the small rule is drawn');
  assert.equal(plain.paddingTop, 120 * 0.12);
});

test('W-1: under a New badge the rule gives way and the title starts below the badge', async () => {
  const badged = await cover(40);
  assert.equal(badged.rule, undefined, 'the badge takes the rule\'s place');
  assert.equal(badged.paddingTop, 40, 'the title clears the badge');
  // A badge shorter than the rule's slot never pulls the title up.
  const small = await cover(5);
  assert.equal(small.paddingTop, 120 * 0.12 + 120 * 0.012 + 120 * 0.065);
});
