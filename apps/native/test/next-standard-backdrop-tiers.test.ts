import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// Justin, 29 Oct: the standard backdrop is drawn the same, all three glows (blue top-right,
// green bottom, deep blue-teal top-left), in every effects tier, including lite.
test('the standard backdrop draws all three glows in every effects tier', async () => {
  for (const tier of ['full', 'reduced', 'lite'] as const) for (const width of [390, 1280]) {
    const {react, render} = hooks();
    const reactModule = {...react, memo: (component: unknown) => component, useLayoutEffect: () => {}};
    const effects = {blur: tier !== 'lite', glow: tier !== 'lite', shadows: tier !== 'lite', drift: tier === 'full', enterFades: tier !== 'lite'};
    const {StandardBackdrop} = await screenModule(new URL('../src-next/kit/artwork/Surfaces.tsx', import.meta.url), {
      react: {...reactModule, default: reactModule},
      'react-native': {Animated: {}, StyleSheet: {absoluteFillObject: {}, flatten: (value: unknown) => value}, View: 'View'},
      'react-native-reanimated': {default: {}},
      'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop'},
      '../theme': {useTheme: () => ({colors: {ink: '#000', accentDeep: '#00f', generatedGreen: '#0f0', ambient: '#0aa'}, width, layout: {breakpoint: {phone: 600}}})},
      '../useMotion': {useEffectsPolicy: () => effects, useReducedMotion: () => false}, // the tier no longer reaches the backdrop
      '../dev-observation': {useNativeObservation: () => ({ref: undefined})},
      '../../platform/ArtworkAlphaMask': {ArtworkAlphaMask: 'ArtworkAlphaMask'},
      './Artwork': {Artwork: 'Artwork'},
      './model': {artworkArrivalDuration: () => 0},
      './types': {},
    });
    const tree = render(() => StandardBackdrop({})) as {props: {children: unknown[]}};
    const layer = (tree.props.children.flat(Infinity) as Array<{props: {children: unknown[]}}>)[0];
    const glows = (layer.props.children.flat(Infinity) as Array<{props: {glows: readonly unknown[]}}>)[0].props.glows;
    assert.equal(glows.length, 3, `${tier} at ${width} pt draws three glows`);
  }
});
