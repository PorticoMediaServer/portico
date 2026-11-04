// The real TV refused-link page (NativeContentLinks' TVContentLinkUnavailable), with its real overlay lifecycle,
// native dismissal and `tv-page-modal` hold, on a tvFocusKit's fake engine (whose Modal is a window, D12-D15).
// Only the drawing is replaced: StateLayout is its Home button, the backdrop and design canvas draw nothing.
import * as registryModule from '../../src-next/kit/focus/registry.ts';
import * as pageModal from '../../src-next/kit/focus/tv-page-modal.ts';
import * as geometry from '../../src-next/kit/overlays/geometry.ts';
import * as overlayStack from '../../src-next/kit/overlays/overlay-stack.ts';
import {screenModule} from './screen-harness.mjs';

const src = path => new URL(`../../src-next/${path}`, import.meta.url);

/** `wait` is the dismissal fallback (LAYER_FOCUS_WAIT_MS in the app). */
export async function tvRefusedPage(kit, {wait = 60} = {}) {
  const {engine, React, e, focus, Stop, theme} = kit, react = {...React, default: React}, native = engine.native;
  const paint = await screenModule(src('kit/overlays/OverlayPaintContext.tsx'), {react});
  const back = await screenModule(src('kit/navigation/back.ts'), {react, 'react-native': native});
  const host = await screenModule(src('kit/overlays/OverlayHost.tsx'), {react, 'react-native': native,
    '../../platform/overlay-keys': {registerOverlayKeys: () => () => {}}, '../../platform/tv-menu': {captureTVMenuKey: () => () => {}},
    './OverlayPaintContext': paint, '../theme': {useTheme: () => theme}, '../focus': {...focus, requestNativeFocus: registryModule.requestNativeFocus},
    './geometry': geometry, './overlay-stack': overlayStack, '../navigation/back': back});
  const dismissal = await screenModule(src('kit/overlays/native-dismissal.ts'), {react, 'react-native': native, '../focus/layer-stack': {LAYER_FOCUS_WAIT_MS: wait}});
  const homes = [];
  const StateLayout = ({primaryAction}) => e(native.View, {style: {flexDirection: 'row', marginTop: 400}},
    e(Stop, {id: 'refused-home', focusId: primaryAction.id, w: 240, h: 80, onPress: () => {homes.push('press'); primaryAction.onPress();}}));
  const links = await screenModule(src('screens/share/NativeContentLinks.tsx'), {react, 'react-native': native,
    '../../../../../packages/i18n/src': {defaultI18n: {t: key => key}}, '../../kit/artwork': {StandardBackdrop: () => null},
    '../../kit/focus': {...focus, ...pageModal}, '../../kit/overlays/native-dismissal': dismissal, '../../kit/overlays/OverlayHost': host,
    '../../kit/overlays/OverlayPaintContext': paint, '../../kit/overlays/TVDesignCanvas': {TVDesignCanvas: ({children}) => children},
    '../../kit/states': {StateLayout, StateView: () => null}, '../../kit/theme': {useTheme: () => theme},
    '../../platform/content-links': {nativeContentLinks: {subscribe: () => () => {}, getSnapshot: () => undefined}}, './share-open': {openContentLink: async () => undefined}});
  /** The page as NativeContentLinks shows it: mounted while `open`, until its Home (or Back) is reported gone. */
  const Refused = ({onGone}) => {
    const [open, setOpen] = React.useState(true);
    return open ? e(links.TVContentLinkUnavailable, {title: 'No access', body: 'Ask the owner', onHome: () => {setOpen(false); onGone?.();}}) : null;
  };
  return {Refused, TVContentLinkUnavailable: links.TVContentLinkUnavailable, pageModal, homes};
}
