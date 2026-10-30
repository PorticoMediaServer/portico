import {Keyboard, Platform, useTVEventHandler} from 'react-native';
import {dispatchRegisteredOverlayKey, hasRegisteredOverlayKeys} from './overlay-keys';

type MenuKey = {eventType?: string; eventKeyAction?: number};
let fallback: {run: () => void} | undefined;

/** The current shell supplies one fallback; stale teardown cannot retire its successor. */
export function registerTVMenuFallback(run: () => void): () => void {
  const registration = {run}; fallback = registration;
  return () => {if (fallback === registration) fallback = undefined;};
}

let focused: {owner: object; run: () => void} | undefined;

/** Focus primitives register only an enabled control's actual long-press action. */
export function registerFocusedMenu(owner: object, run: () => void): () => void {
  const registration = {owner, run};
  focused = registration;
  return () => {if (focused === registration) focused = undefined;};
}
export function dispatchMenuKey(event: MenuKey | undefined, os = Platform.OS, isTV = Platform.isTV === true): boolean {
  if (os !== 'android' || !isTV || event?.eventType !== 'menu' || (event.eventKeyAction !== undefined && event.eventKeyAction !== 1)) return false;
  if (Keyboard.isVisible?.()) {Keyboard.dismiss(); return true;}
  // An inert/retiring top overlay still owns input; never open a card menu behind it.
  if (hasRegisteredOverlayKeys()) {dispatchRegisteredOverlayKey('menu'); return true;}
  if (focused) {focused.run(); return true;}
  if (fallback) {fallback.run(); return true;}
  return false;
}
export function useMenuKey(): void {
  useTVEventHandler(event => {dispatchMenuKey(event);});
}
