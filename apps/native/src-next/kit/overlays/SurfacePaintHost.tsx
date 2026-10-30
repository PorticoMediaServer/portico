import React, {createContext, useCallback, useContext, useId, useLayoutEffect, useMemo, useState, useSyncExternalStore, type ReactElement, type ReactNode} from 'react';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {freezeOverlayPaint, type OverlayPaintNode, type OverlayPaintTypes} from './overlay-paint-model';
import {OverlayPaintProvider, OVERLAY_PAINT_HIDDEN, OVERLAY_PAINT_INERT, OVERLAY_PAINT_LIVE} from './OverlayPaintContext';

type Subscribe = (listener: () => void) => () => void;
export type OverlayPaintPermit = Readonly<{current: () => boolean; subscribe: Subscribe}>;
type Entry = Readonly<{id: string; node: OverlayPaintNode; allowed: OverlayPaintTypes; omitted?: OverlayPaintTypes; permit?: OverlayPaintPermit; open: boolean; exitMs: number; closing?: object; superseded?: boolean}>;
type Host = Readonly<{put: (entry: Entry) => void; retire: (id: string) => void}>;
const Context = createContext<Host | undefined>(undefined);
const noop = () => {};
const noSubscription: Subscribe = () => noop;
const allowed = (permit?: OverlayPaintPermit): boolean => {try {return permit?.current() === true;} catch {return false;}};
const openings = new WeakMap<object, string>();
let openingSequence = 0;
/** Presentation identity only: equal values reopened under a new original
 * capture must never inherit another opening's native surface or focus state. */
export function overlayPaintKey(opening: object): string {
  let key = openings.get(opening);
  if (!key) {key = `overlay-opening-${++openingSequence}`; openings.set(opening, key);}
  return key;
}

/** Original read-only page validity. Never constructs or rebinds a business opening. */
export function overlayPaintPermit(current: () => boolean, subscriptions: readonly Subscribe[], signals: readonly AbortSignal[] = []): OverlayPaintPermit {
  let failed = false;
  const read = () => {
    if (failed) return false;
    try {return current() === true;} catch {failed = true; return false;}
  };
  return {current: read, subscribe: listener => {
    if (!read()) return noop;
    const off: (() => void)[] = [], attached: AbortSignal[] = [];
    let disposed = false;
    const dispose = () => {
      if (disposed) return;
      disposed = true;
      // These are read-only paint watchers, not business controllers. A retired
      // watcher cannot block the remaining cleanup or regain paint permission.
      for (const release of off) {try {release();} catch {failed = true;}}
      for (const signal of attached) {try {signal.removeEventListener('abort', listener);} catch {failed = true;}}
    };
    try {
      for (const subscribe of subscriptions) off.push(subscribe(listener));
      for (const signal of signals) {signal.addEventListener('abort', listener); attached.push(signal);}
    } catch {
      failed = true; dispose(); listener();
    }
    return dispose;
  }};
}

function PaintEntry({entry, remove}: {entry: Entry; remove: (id: string, closing: object) => void}) {
  const [expired, setExpired] = useState(false);
  const watching = !entry.open && !entry.superseded;
  const valid = useSyncExternalStore(watching ? entry.permit?.subscribe ?? noSubscription : noSubscription,
    entry.open ? () => true : () => watching && allowed(entry.permit));
  useLayoutEffect(() => {
    if (entry.open || !entry.closing) {setExpired(false); return;}
    const closing = entry.closing;
    // Commit HIDDEN before unmounting: the old surface must suppress its opener
    // restoration before a successor takes focus, including failed captures.
    if (entry.superseded) {remove(entry.id, closing); return;}
    let live = true, frame: number | undefined;
    const check = () => {
      if (!live) return;
      if (!allowed(entry.permit)) {setExpired(true); return;}
      frame = requestAnimationFrame(check);
    };
    // Poll only the bounded outgoing paint, including validity horizons that have
    // no notification. There are no reads, commands or new opening constructors.
    check();
    const timer = setTimeout(() => {if (live) remove(entry.id, closing);}, entry.exitMs);
    return () => {live = false; clearTimeout(timer); if (frame !== undefined) cancelAnimationFrame(frame);};
  }, [entry, remove]);
  const visible = !entry.superseded && (entry.open || valid && !expired);
  return <OverlayPaintProvider returnCurrent={() => allowed(entry.permit)} mode={entry.open ? OVERLAY_PAINT_LIVE : visible ? OVERLAY_PAINT_INERT : OVERLAY_PAINT_HIDDEN}>{entry.node}</OverlayPaintProvider>;
}

/** The actual kit surface stays in one host. Its business publisher still
 * unmounts/disposes immediately; an exit contains audited inert display only. */
export function SurfacePaintHost({children}: {children: ReactNode}) {
  const [entries, setEntries] = useState<readonly Entry[]>([]);
  const put = useCallback((entry: Entry) => setEntries(current => {
    const at = current.findIndex(value => value.id === entry.id);
    // A fresh opening immediately hides old outgoing paint. Its hidden commit
    // disables return focus before removal; no old modal blocks the new opening.
    const next = current.map(value => !value.open && value.id !== entry.id ? {...value, superseded: true} : value);
    return at >= 0 && next.some(value => value.id === entry.id)
      ? next.map(value => value.id === entry.id ? entry : value) : [...next, entry];
  }), []);
  const retire = useCallback((id: string) => setEntries(current => {
    const entry = current.find(value => value.id === id);
    if (!entry?.open) return current;
    const paint = allowed(entry.permit) ? freezeOverlayPaint(entry.node, entry.allowed, [RetainedOverlay, ...(entry.omitted ?? [])]) : undefined;
    const closing = {};
    if (!paint) return current.map(value => value.id === id ? {...entry, open: false, closing, superseded: true} : value);
    const node = React.cloneElement(paint, {open: false, onOpenChange: noop, initialFocusRef: undefined});
    // Existing nested active surfaces remain; at most one outgoing surface lives.
    return current.map(value => value.id === id ? {...entry, node, open: false, closing} : value.open ? value : {...value, superseded: true});
  }), []);
  const remove = useCallback((id: string, closing: object) => setEntries(current => current.filter(value => value.id !== id || value.closing !== closing)), []);
  const host = useMemo<Host>(() => ({put, retire}), [put, retire]);
  return <Context.Provider value={host}>{children}{entries.map(entry => <PaintEntry key={entry.id} entry={entry} remove={remove}/>)}</Context.Provider>;
}

/** Caller supplies only a materialised kit surface and explicitly audited pure
 * UI types. No component body/controller is cached or called for outgoing paint. */
export function RetainedOverlay({children, permit, allowedTypes, omitTypes}: {children: ReactElement; permit?: OverlayPaintPermit; allowedTypes: OverlayPaintTypes; omitTypes?: OverlayPaintTypes}) {
  const host = useContext(Context), id = useId(), theme = useTheme(), motion = useMotion();
  const exitMs = motion.reduce ? 160 : theme.isTV ? 240 : 500;
  useLayoutEffect(() => {
    if (host) host.put({id, node: children as OverlayPaintNode, allowed: allowedTypes, omitted: omitTypes, permit, open: true, exitMs});
  }, [host, id, children, permit, allowedTypes, omitTypes, exitMs]);
  useLayoutEffect(() => () => {host?.retire(id);}, [host, id]);
  return host ? null : children;
}
