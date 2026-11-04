// The real kit overlays that draw a native Modal on TV (CenteredDialog, BottomSheet, the modal Popover, a modal
// TVPanelTransition and the live mini guide), with their real overlay lifecycle and native dismissal
// (useNativeModalPresence), on a tvFocusKit's fake engine (whose Modal is a window, D12-D16). Only drawing is
// replaced: icons, art, blur, gradients and the design canvas draw nothing, and a Button is a plain focus stop.
// Motion is reduced; withTiming reports its end on the next timer, as reanimated does on the JS thread.
import * as registryModule from '../../src-next/kit/focus/registry.ts';
import * as geometry from '../../src-next/kit/overlays/geometry.ts';
import * as overlayStack from '../../src-next/kit/overlays/overlay-stack.ts';
import * as tvCanvas from '../../src-next/platform/layout/tv-canvas.ts';
import {screenModule} from './screen-harness.mjs';

const src = path => new URL(`../../src-next/${path}`, import.meta.url);

/** `wait` is the dismissal fallback (LAYER_FOCUS_WAIT_MS in the app). */
export async function tvKitOverlays(kit, {wait = 60} = {}) {
  const {engine, React, e, focus, Focusable} = kit, react = {...React, default: React};
  const native = {...engine.native, useWindowDimensions: () => ({width: 1920, height: 1080, scale: 1, fontScale: 1}), TextInput: engine.native.View,
    Keyboard: {addListener: () => ({remove() {}})}};
  const animated = {...engine.animated, runOnJS: fn => fn, cancelAnimation() {}, withSpring: value => value,
    withTiming: (value, _config, done) => {if (done) setTimeout(() => done(true), 0); return value;}};
  const none = () => null;
  const svg = {default: none, Defs: none, LinearGradient: none, RadialGradient: none, Rect: none, Stop: none};
  const theme = {...kit.theme, platform: 'tv', textScale: 1,
    colors: {...kit.theme.colors, accent: '#08f', raised: '#123', raisedHigh: '#234', raisedBright: '#345', line: '#456', lineStrong: '#567', textSecondary: '#ccc', textTertiary: '#999', record: '#f00'},
    layers: {...kit.theme.layers, menu: 10}, type: {caption: {}, captionStrong: {}}, layout: {designWidth: {tv: 1920}, designHeight: {tv: 1080}},
    spec: {overlay: {width: {lg: {tv: 1100}}, maxHeight: {tv: .9}}}};
  const themed = {useTheme: () => theme, nativeType: () => ({})};
  const motion = {useMotion: () => ({reduce: true, transforms: false, effects: {}, timing: () => ({duration: 0}), spring: () => ({}), duration: () => 0})};
  const i18n = {defaultI18n: {t: key => key, locale: 'en'}};
  const Button = ({testID, focusId, ref, onPress}) => e(Focusable, {testID, focusId, ref, onPress, style: {width: 120, height: 48}}, () => null);
  const paint = await screenModule(src('kit/overlays/OverlayPaintContext.tsx'), {react});
  const back = await screenModule(src('kit/navigation/back.ts'), {react, 'react-native': native});
  const host = await screenModule(src('kit/overlays/OverlayHost.tsx'), {react, 'react-native': native,
    '../../platform/overlay-keys': {registerOverlayKeys: () => () => {}}, '../../platform/tv-menu': {captureTVMenuKey: () => () => {}},
    './OverlayPaintContext': paint, '../theme': themed, '../focus': {...focus, requestNativeFocus: registryModule.requestNativeFocus},
    './geometry': geometry, './overlay-stack': overlayStack, '../navigation/back': back});
  const dismissal = await screenModule(src('kit/overlays/native-dismissal.ts'), {react, 'react-native': native, '../focus/layer-stack': {LAYER_FOCUS_WAIT_MS: wait}});
  const common = {react, 'react-native': native, 'react-native-reanimated': animated, 'react-native-svg': svg,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, left: 0, right: 0, bottom: 0})},
    '../../../../../packages/i18n/src/index': i18n, '../controls/Button': {Button}, '../controls/Focusable': {Focusable}, '../focus': focus,
    '../Icon': {Icon: none}, '../theme': themed, '../useMotion': motion, './OverlayHost': host, './OverlayPaintContext': paint,
    './TVDesignCanvas': {TVDesignCanvas: ({children}) => children}, './native-dismissal': dismissal, './geometry': geometry};
  const surface = await screenModule(src('kit/overlays/surface.tsx'), {...common, '../../platform/layout/tv-canvas': tvCanvas, './BottomSheet': {BottomSheet: none}});
  const pan = new Proxy({}, {get: () => () => pan});
  const sheet = await screenModule(src('kit/overlays/BottomSheet.tsx'), {...common, './surface': surface,
    '../../platform/keyboard': {useKeyboardHeight: () => 0}, 'react-native-gesture-handler': {Gesture: {Pan: () => pan}, GestureDetector: ({children}) => children}});
  const menu = await screenModule(src('kit/overlays/Menu.tsx'), {...common, './surface': surface, './BottomSheet': sheet,
    '../../platform/native-blur': {glassRecipe: () => ({nativeBlur: false}), BlurView: none}, './anchored-menu-geometry': {rightEndMenuRect: () => ({x: 0, y: 0, width: 300, height: 300})},
    '../browse/touch-menu-geometry': {touchMenuRect: () => undefined}});
  const panels = await screenModule(src('kit/overlays/TVPanelMotion.tsx'), {react, 'react-native': native, 'react-native-reanimated': animated,
    '../useMotion': motion, './native-dismissal': dismissal});
  const guide = await screenModule(src('screens/watch/TVMiniGuide.tsx'), {react, 'react-native': native, 'react-native-reanimated': animated,
    '../../../../../packages/i18n/src': i18n, '../../../../../packages/client-core/src/presentation/kit-model/index': {channelTint: () => '#123'},
    '../../shell/model': {shellLayout: () => ({left: 0, top: 0, width: 1920, height: 1080, scale: 1})}, '../../kit/controls': {Focusable},
    '../../kit/overlays/surface': surface, '../../kit/overlays/OverlayHost': host, '../../kit/Icon': {Icon: none}, '../../kit/artwork/Artwork': {Artwork: none},
    '../../platform/native-blur': {glassRecipe: () => ({nativeBlur: false}), BlurView: none}, '../../kit/theme': themed, '../../kit/useMotion': motion,
    '../../kit/overlays/TVPanelMotion': panels, '../../kit/overlays/native-dismissal': dismissal});
  return {Dialog: surface.Dialog, BottomSheet: sheet.BottomSheet, Popover: menu.Popover, Menu: menu.Menu, TVPanelTransition: panels.TVPanelTransition,
    useTVPanelModal: panels.useTVPanelModal, useTVPanelPresence: panels.useTVPanelPresence, NativeMiniGuide: guide.NativeMiniGuide,
    useOverlayLifecycle: host.useOverlayLifecycle, useNativeModalPresence: dismissal.useNativeModalPresence, FocusBoundary: surface.FocusBoundary, native};
}
