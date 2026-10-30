import React, {createContext, useContext, type ReactNode} from 'react';
import {ArtFrame, CardFrame, type CardFrameSurfaceProps, type CardFrameProps, type Interaction} from './primitives';

const SlotPaint = createContext<Interaction | undefined>(undefined);
/** The one real native host and its original actions/ref outlive waiting/art kind changes. */
export function TVSparseCardFrame({children, ...props}: CardFrameSurfaceProps & {children: ReactNode}) {
  return <CardFrame {...props}>{state => <SlotPaint.Provider value={state}>{children}</SlotPaint.Provider>}</CardFrame>;
}
/** Reuse each existing live card paint; it creates no second Pressable/ref/Menu owner. */
export function TVCardPaintFrame({children, overlay}: CardFrameProps) {
  const state = useContext(SlotPaint);
  if (!state) throw new Error('A TV card paint frame requires its original sparse slot.');
  return <>{children(state)}{overlay?.(state)}</>;
}

/** Unknown contents have only the declared slot art geometry and shared focus paint. */
export function TVPendingCardPaint({height}: {height: number}) {
  const state = useContext(SlotPaint);
  if (!state) throw new Error('A pending card requires its original sparse slot.');
  return <ArtFrame height={height} focused={state.focused} hovered={state.hovered} holding={state.holding}><></></ArtFrame>;
}
