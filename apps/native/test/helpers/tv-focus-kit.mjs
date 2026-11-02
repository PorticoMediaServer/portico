// The real focus kit (registry, FocusRegion, CoveredLayer, FocusGate, Focusable) bound to a fake
// native focus engine (tv-focus-engine.mjs). Only the platform boundary is replaced: the theme
// is a TV theme, motion is instant, and the dev layout observer records nothing.
import {focusSpec} from '@portico/design';
import * as registryModule from '../../src-next/kit/focus/registry.ts';
import * as layerStack from '../../src-next/kit/focus/layer-stack.ts';
import * as controlModel from '../../src-next/kit/controls/control-model.ts';
import {liveOverlayPaint, screenModule} from './screen-harness.mjs';
import {tvFocusEngine} from './tv-focus-engine.mjs';

const src = path => new URL(`../../src-next/${path}`, import.meta.url);

export async function tvFocusKit(options = {}) {
  const engine = await tvFocusEngine(options);
  const React = engine.React, react = {...React, default: React};
  const theme = {isTV: true, platform: 'tv', colors: {ink: '#000', focus: '#fff', accentStrong: '#f80', text: '#fff'}, layers: {tvFocus: 5}};
  const common = {react, 'react-native': engine.native, 'react-native-reanimated': engine.animated, '../theme': {useTheme: () => theme}};
  const {useHostRef} = await screenModule(src('kit/focus/useHostRef.ts'), {react});
  const regions = await screenModule(src('kit/focus/FocusRegion.tsx'), {...common, './registry': registryModule, './cell-navigation': {}, './useHostRef': {useHostRef}});
  const covered = await screenModule(src('kit/focus/CoveredLayer.tsx'), common);
  const gate = await screenModule(src('kit/focus/FocusGate.tsx'), {...common, './FocusRegion': regions});
  const focus = {...regions, ...covered, ...gate, createFocusRegistry: registryModule.createFocusRegistry, focusTestID: registryModule.focusTestID, requestNativeFocus: registryModule.requestNativeFocus,
    createTVLayerStack: layerStack.createTVLayerStack};
  const {Focusable} = await screenModule(src('kit/controls/Focusable.tsx'), {...common,
    '@portico/design': {focusSpec}, '../useMotion': {useMotion: () => ({reduce: true, transforms: false, timing: () => ({duration: 0})})},
    './control-model': controlModel, './useFocusedMenu': {useFocusedMenu() {}}, '../overlays/OverlayPaintContext': liveOverlayPaint,
    '../focus': focus, '../dev-observation': {useNativeObservation: (_facts, ref) => ({ref, recordFocus() {}, recordDeclaredStyle() {}})}});
  const {TVRow} = await screenModule(src('kit/layout/TVRow.tsx'), {react, '../theme': {useTheme: () => theme}, '../focus': focus});
  const e = React.createElement;
  /** A focus stop that records its own drawn ring (from Focusable's real render-prop state) and its presses. */
  const presses = [];
  const rings = new Map();
  const Stop = ({id, w = 200, h = 100, onPress, ...props}) => e(Focusable, {testID: id, focusId: id, style: {width: w, height: h}, onPress: onPress ?? (() => presses.push(id)), ...props},
    state => {rings.set(id, state.focused); return null;});
  return {engine, React, e, focus, Focusable, Stop, TVRow, presses, ring: id => rings.get(id) === true, theme};
}
