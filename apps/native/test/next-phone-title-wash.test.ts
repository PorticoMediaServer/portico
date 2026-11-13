// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createFabricHookHost, act} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {createTheme} from '../src-next/kit/theme.ts';

// 13 Nov art-library walk (W1): a phone title page took no colour from its art below the hero; the blurred layer sat only
// under the full-width backdrop and faded to ink with it. phone-detail `.th` and `.amb-l` are the target.
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : React.isValidElement<any>(node) ? [node, ...nodes(node.props.children)] : [];
const flat = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.flat(Infinity).filter(Boolean)) : style ?? {};

async function render(width: number, treatment: string, backdrop = true) {
  let tree: any;
  const theme = createTheme({isTV: false, width});
  const {AmbientArt} = await screenModule(new URL('../src-next/kit/artwork/Surfaces.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', StyleSheet: {absoluteFillObject: {}, create: (value: any) => value},
      Animated: {View: 'AnimatedView', Value: class {constructor(public value: number) {} stopAnimation() {} setValue(next: number) {this.value = next;}}, timing: () => ({start() {}})}},
    'react-native-reanimated': {default: {View: 'ReanimatedView'}},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop'},
    '../theme': {useTheme: () => theme}, '../useMotion': {useReducedMotion: () => false}, '../dev-observation': {useNativeObservation() {}},
    '../../platform/ArtworkAlphaMask': {ArtworkAlphaMask: 'ArtworkAlphaMask'}, './Artwork': {Artwork: 'Artwork'},
    './model': {artworkArrivalDuration: () => 180},
  });
  const art = {uri: '/art', cacheKey: 'art'};
  const host = createFabricHookHost(() => {tree = AmbientArt({source: art, foreground: backdrop ? art : undefined, treatment, height: 516, testID: 'hero'});});
  await host.mount(); await act(() => {});
  const all = nodes(tree); await host.unmount();
  return {all, theme};
}

test('a phone title page washes the top 760 pt with its blurred art and fades the backdrop out by alpha, not to ink', async () => {
  const {all, theme} = await render(402, 'title'), spec = theme.spec.hero.phoneTitle;
  const wash = all.find(node => node.props.testID === 'hero-wash');
  assert.equal(flat(wash.props.style).height, spec.wash.height, 'the wash runs past the hero to the frames\' 760 pt');
  const masks = all.filter(node => node.type === 'ArtworkAlphaMask').map(node => node.props.bottomOpaqueAt);
  assert.deepEqual(masks, [1 - spec.wash.opaqueAt, 1 - spec.artOpaqueAt], 'the wash fades out from 25 %, the backdrop from 48 %');
  const blurred = nodes(wash).find(node => node.type === 'Artwork' && node.props.sized);
  assert.ok(blurred, 'the blurred art is inside the wash');
  assert.ok(nodes(wash).some(node => node.type === 'View' && flat(node.props.style).opacity === spec.wash.alpha), 'at the frames\' .42');
  const overlay = all.find(node => node.props?.id === 'phone-title-overlay');
  assert.deepEqual(overlay.props.stops, spec.overlay); assert.equal(spec.overlay.at(-1)!.alpha, 0, 'the overlay ends clear, so no ink edge shows below the hero');
  assert.equal(all.some(node => node.type === 'LinearGradient' && node.props.id === 'art-bottom'), false, 'no ink bottom scrim on a phone title');
});

test("Home's phone hero and title washes with no backdrop (music, books, Now Playing) keep their own treatment", async () => {
  for (const [treatment, backdrop] of [['phone', true], ['title', false]] as const) {
    const {all} = await render(402, treatment, backdrop);
    assert.equal(all.some(node => node.props?.testID === 'hero-wash'), false, `${treatment}, backdrop ${backdrop}`);
  }
});

// W2 (lead's ruling 13 Nov): the phone album and artist pages drew a private copy of `.amb-l` with an extra ink layer at
// .45 over it, which turned the cover's colour grey. They now draw the kit's wash, `page`: the same 760 pt .42 wash, nothing over it.
test('a phone page with no backdrop of its own (album, artist) draws only the kit wash', async () => {
  const {all, theme} = await render(402, 'page', false), spec = theme.spec.hero.phoneTitle;
  const wash = all.find(node => node.props.testID === 'hero-wash');
  assert.equal(flat(wash.props.style).height, spec.wash.height);
  assert.deepEqual(all.filter(node => node.type === 'ArtworkAlphaMask').map(node => node.props.bottomOpaqueAt), [1 - spec.wash.opaqueAt], 'one mask: the wash fading out');
  assert.equal(all.some(node => node.props?.id === 'phone-title-overlay'), false, 'no ink overlay');
  assert.ok(nodes(wash).some(node => node.type === 'View' && flat(node.props.style).opacity === spec.wash.alpha));
});

// Desk B on #3428: the real theme lays a tablet out as handheld too (layoutMode 'phone', platform 'tablet'). The phone washes
// are the phone frames', so a tablet keeps its own: tested with the real theme builder.
test('a tablet (handheld layout, tablet platform) keeps its own title and page treatment', async () => {
  for (const [treatment, backdrop] of [['title', true], ['page', false]] as const) {
    const {all, theme} = await render(820, treatment, backdrop);
    assert.deepEqual([theme.layoutMode, theme.platform], ['phone', 'tablet']);
    assert.equal(all.some(node => node.props?.testID === 'hero-wash'), false, treatment);
  }
});
