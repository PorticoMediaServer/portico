import {Keyboard} from 'react-native';
import {dispatchRegisteredOverlayKey, hasRegisteredOverlayKeys} from '../../../../../native/src-next/platform/overlay-keys';

type MenuKey = {eventType?: string; eventKeyAction?: number};
let fallback: {run: () => void} | undefined;

/** The current shell supplies one fallback; stale teardown cannot retire its successor. */
export function registerTVMenuFallback(run: () => void): () => void {
  const registration = {run}; fallback = registration;
  return () => {if (fallback === registration) fallback = undefined;};
}

let focused: {owner: object; run: () => void} | undefined;
export function registerFocusedMenu(owner: object, run: () => void): () => void {
  const registration = {owner, run}; focused = registration;
  return () => {if (focused === registration) focused = undefined;};
}
/** Physical Menu, distinct from the Apple vocabulary's menu (Back). */
export function dispatchMenuKey(event: MenuKey | undefined): boolean {
  if (!['menu', 'context_menu'].includes(event?.eventType ?? '') || event?.eventKeyAction !== 1) return false;
  if (Keyboard.isVisible?.()) {Keyboard.dismiss(); return true;}
  // An inert/retiring top overlay still owns input; never act on content behind it.
  if (hasRegisteredOverlayKeys()) {dispatchRegisteredOverlayKey('menu'); return true;}
  if (focused) {focused.run(); return true;}
  if (fallback) {fallback.run(); return true;}
  return false;
}
/** Root's MenuKey listens to the SDK's raw events once. */
export function useMenuKey(): void {}
