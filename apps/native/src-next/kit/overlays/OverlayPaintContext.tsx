import React from 'react';

/** How an overlay's already-painted surface should behave while it leaves. */
export type OverlayPaintMode = Readonly<{visible: boolean; interactive: boolean; root: boolean}>;

export const OVERLAY_PAINT_LIVE: OverlayPaintMode = Object.freeze({visible: true, interactive: true, root: true});
export const OVERLAY_PAINT_INERT: OverlayPaintMode = Object.freeze({visible: true, interactive: false, root: true});
export const OVERLAY_PAINT_HIDDEN: OverlayPaintMode = Object.freeze({visible: false, interactive: false, root: true});

export const OverlayPaintModeContext = React.createContext<OverlayPaintMode>(OVERLAY_PAINT_LIVE);
const ReturnCurrentContext = React.createContext<(() => boolean) | undefined>(undefined);
/** Original page validity, checked again at cleanup even when the whole shell
 * unmounts before a HIDDEN render can commit. Never a successor lookup. */
export function useOverlayPaintReturnCurrent(): (() => boolean) | undefined {return React.useContext(ReturnCurrentContext);}

export function useOverlayPaintMode(): OverlayPaintMode {
  return React.useContext(OverlayPaintModeContext);
}

/** Host integration point: publish one immutable mode for the painted surface. */
export function OverlayPaintProvider({mode, returnCurrent, children}: {mode: OverlayPaintMode; returnCurrent?: () => boolean; children?: React.ReactNode}) {
  const value = React.useMemo<OverlayPaintMode>(() => Object.freeze({visible: mode.visible, interactive: mode.interactive, root: mode.root}), [mode.visible, mode.interactive, mode.root]);
  return <ReturnCurrentContext.Provider value={returnCurrent}><OverlayPaintModeContext.Provider value={value}>{children}</OverlayPaintModeContext.Provider></ReturnCurrentContext.Provider>;
}

/** Marks everything below as a descendant: still visible and interactive as the
 * parent says, but never the root. Adds no native view of its own. */
export function OverlayPaintDescendants({children}: {children?: React.ReactNode}) {
  const parent = React.useContext(OverlayPaintModeContext);
  const value = React.useMemo<OverlayPaintMode>(() => Object.freeze({visible: parent.visible, interactive: parent.interactive, root: false}), [parent.visible, parent.interactive]);
  return <OverlayPaintModeContext.Provider value={value}>{children}</OverlayPaintModeContext.Provider>;
}
