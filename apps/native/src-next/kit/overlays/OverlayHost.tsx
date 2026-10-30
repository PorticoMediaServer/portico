import React, {createContext, useCallback, useContext, useId, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {BackHandler, Platform, StyleSheet, View, type GestureResponderEvent} from 'react-native';
import {registerOverlayKeys} from '../../platform/overlay-keys';
import {captureTVMenuKey} from '../../platform/tv-menu';
import {OverlayPaintProvider, useOverlayPaintMode, useOverlayPaintReturnCurrent} from './OverlayPaintContext';
import {useTheme} from '../theme';
import {FocusProvider, requestNativeFocus, useOptionalFocusRegistry} from '../focus';
import {containsPoint, type Rect} from './geometry';
import {createOverlayStack} from './overlay-stack';
import type {FocusRef, FocusTarget, OverlayReason} from './types';

type Entry = {id: string; node: React.ReactNode; rect?: Rect; outside?: () => void};
type Portal = {put: (entry: Entry) => void; remove: (id: string) => void; frame: Rect};
const HostContext = createContext<Portal | null>(null);

/** Place inside the shell's providers and ThemeProvider. In-window surfaces never open a native modal. */
export function OverlaysProvider({children}: {children: React.ReactNode}) {
  const [entries, setEntries] = useState<Entry[]>([]);
  const root = useRef<View>(null);
  const [frame, setFrame] = useState<Rect>({x: 0, y: 0, width: 0, height: 0});
  const measure = () => root.current?.measureInWindow((x, y, width, height) => setFrame(current => current.x === x && current.y === y && current.width === width && current.height === height ? current : {x, y, width, height}));
  const latest = useRef(entries);
  latest.current = entries;
  const put = useCallback((entry: Entry) => setEntries(current => {
    const index = current.findIndex(value => value.id === entry.id);
    if (index < 0) return [...current, entry];
    return current.map((value, at) => at === index ? entry : value);
  }), []);
  const remove = useCallback((id: string) => setEntries(current => current.filter(entry => entry.id !== id)), []);
  const portal = useMemo(() => ({put, remove, frame}), [put, remove, frame]);
  const {layers} = useTheme();
  const outside = useCallback((event: GestureResponderEvent) => {
    const top = latest.current[latest.current.length - 1];
    if (top?.outside && top.rect && !containsPoint(top.rect, event.nativeEvent.pageX, event.nativeEvent.pageY)) top.outside();
    return false;
  }, []);
  return <FocusProvider><HostContext.Provider value={portal}><View ref={root} onLayout={measure} style={styles.root} onStartShouldSetResponderCapture={outside}>
    {children}
    <View pointerEvents="box-none" style={[StyleSheet.absoluteFill, {zIndex: layers.menu}]}>
      {entries.map(entry => <React.Fragment key={entry.id}>{entry.node}</React.Fragment>)}
    </View>
  </View></HostContext.Provider></FocusProvider>;
}
export function OverlayPortal({children, rect, onOutsidePress}: {children: React.ReactNode; rect?: Rect; onOutsidePress?: () => void}) {
  const host = useContext(HostContext), paint = useOverlayPaintMode(), returnCurrent = useOverlayPaintReturnCurrent();
  const id = useId();
  useLayoutEffect(() => {
    if (host) host.put({id, node: <OverlayPaintProvider mode={paint} returnCurrent={returnCurrent}>{children}</OverlayPaintProvider>, rect, outside: paint.interactive ? onOutsidePress : undefined});
  }, [host, id, children, rect, onOutsidePress, paint, returnCurrent]);
  useLayoutEffect(() => () => host?.remove(id), [host, id]);
  // Inline placement also supports a caller already mounting its surface at the shell root.
  return host ? null : <>{children}</>;
}

export function useOverlayFrame(): Rect {
  return useContext(HostContext)?.frame ?? {x: 0, y: 0, width: 0, height: 0};
}

export function focusTarget(target: FocusTarget | null | undefined) {
  requestNativeFocus(target);
}

const stack = createOverlayStack();
let keyUsers = 0;
let unregisterKeys: (() => void) | undefined;
function retainOverlayKeys(): () => void {
  if (keyUsers++ === 0) unregisterKeys = registerOverlayKeys(dispatchOverlayKey);
  let retained = true;
  return () => {
    if (!retained) return;
    retained = false;
    if (--keyUsers === 0) {unregisterKeys?.(); unregisterKeys = undefined;}
  };
}
/** Root's physical keyboard bridge can forward keys without making a non-modal surface modal. */
export function dispatchOverlayKey(key: 'escape' | 'tab' | 'menu', _shift = false): boolean {
  if (key === 'escape' || key === 'menu') return stack.dismiss(key === 'menu' ? 'back' : 'escape');
  // Native windows own Tab and Shift-Tab traversal, including modal containment.
  return false;
}
export function useOverlayLifecycle(open: boolean, modal: boolean, dismiss: (reason: OverlayReason) => void, initialFocusRef?: FocusRef, returnFocusRef?: FocusRef, focusDelay = 0, regionId?: string, restoreFocus = true) {
  const registry = useOptionalFocusRegistry(), paint = useOverlayPaintMode();
  const returnCurrent = useOverlayPaintReturnCurrent(), returnPermit = useRef(returnCurrent);
  returnPermit.current = returnCurrent;
  open = open && paint.visible;
  restoreFocus = restoreFocus && paint.visible && (paint.interactive || paint.root);
  const dismissRef = useRef(dismiss);
  dismissRef.current = dismiss;
  const initial = useRef(initialFocusRef);
  initial.current = initialFocusRef;
  const restore = useRef(returnFocusRef);
  restore.current = returnFocusRef;
  // A caller with a source-bound opener can own restoration after its own proof.
  const restoring = useRef(restoreFocus);
  restoring.current = restoreFocus;
  const delay = useRef(focusDelay);
  delay.current = focusDelay;
  useLayoutEffect(() => {
    if (!open) return;
    const opener = registry?.capture();
    const releaseReturn = regionId && restoring.current ? registry?.registerReturn(regionId, opener) : undefined;
    const releaseOpener = opener && restoring.current ? registry?.retain(opener) : undefined;
    const registration = {};
    const remove = stack.register(registration, reason => dismissRef.current(reason));
    const releaseKeys = retainOverlayKeys();
    const releaseMenu = captureTVMenuKey();
    // TV: an overlay opened without an initial target (a library Sort or Card size popover) enters its own region;
    // otherwise the remote's focus stayed on the opener and Down walked into the page behind (P2-74 E-tv-15).
    const timer = setTimeout(() => {
      if (!stack.isTop(registration)) return;
      const target = initial.current?.current;
      if (target) focusTarget(target); else if (regionId && (Platform as typeof Platform | undefined)?.isTV) registry?.focusRegion(regionId);
    }, delay.current);
    const back = BackHandler.addEventListener('hardwareBackPress', () => {
      if (!stack.isTop(registration)) return false;
      dismissRef.current('back');
      return true;
    });
    return () => {
      clearTimeout(timer);
      back.remove();
      const wasTop = remove();
      releaseKeys();
      releaseMenu();
      releaseReturn?.();
      releaseOpener?.();
      let originalCurrent = !returnPermit.current;
      try {if (returnPermit.current) originalCurrent = returnPermit.current();} catch {originalCurrent = false;}
      if (wasTop && restoring.current && originalCurrent) {
        if (restore.current?.current) focusTarget(restore.current.current);
        else registry?.restore(opener);
      }
    };
  }, [open, modal, registry, regionId]);
}
const styles = StyleSheet.create({root: {flex: 1}});
