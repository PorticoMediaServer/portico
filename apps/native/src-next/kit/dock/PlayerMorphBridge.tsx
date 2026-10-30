import React, {createContext, useCallback, useContext, useLayoutEffect, useState, type ReactNode} from 'react';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import type {PlayerMorphController} from './types';
import type {SharedValue} from 'react-native-reanimated';

type Binding = Readonly<{controller: PlayerMorphController; fence: PlaybackFence}>;
type Publish = (controller: PlayerMorphController, fence: PlaybackFence) => () => void;
const Published = createContext<Binding | undefined>(undefined);
const Publication = createContext<Publish | undefined>(undefined);
export type InitialPlayerPaint = Readonly<{key: object; fence: PlaybackFence; title: SharedValue<number>; page: SharedValue<number>; controls: SharedValue<number>; landed: boolean; entering: boolean; controlsAdmitted: boolean}>;
const InitialPaint = createContext<InitialPlayerPaint | undefined>(undefined);
const InitialPublication = createContext<((paint: InitialPlayerPaint) => () => void) | undefined>(undefined);

/** Pure presentation wiring, scoped by the shell's original authority key.
 * The persistent player alone publishes; no route owns playback or a second player. */
export function PlayerMorphProvider({children}: {children: ReactNode}) {
  const [binding, setBinding] = useState<Binding>();
  const [initial, setInitial] = useState<InitialPlayerPaint>();
  const publishInitial = useCallback((paint: InitialPlayerPaint) => {
    setInitial(paint);
    return () => setInitial(current => current === paint ? undefined : current);
  }, []);
  const publish = useCallback<Publish>((controller, fence) => {
    const entry = {controller, fence};
    setBinding(entry);
    return () => setBinding(current => current === entry ? undefined : current);
  }, []);
  return <InitialPublication.Provider value={publishInitial}><InitialPaint.Provider value={initial}><Publication.Provider value={publish}><Published.Provider value={binding}>{children}</Published.Provider></Publication.Provider></InitialPaint.Provider></InitialPublication.Provider>;
}

export function useInitialPlayerPublication(paint: InitialPlayerPaint | undefined) {
  const publish = useContext(InitialPublication);
  useLayoutEffect(() => {if (publish && paint) return publish(paint);}, [publish, paint]);
}
export function useInitialPlayerPagePaint(): InitialPlayerPaint | undefined {return useContext(InitialPaint);}
export function useInitialPlayerPaint(fence: PlaybackFence | undefined): InitialPlayerPaint | undefined {
  const paint = useContext(InitialPaint);
  return fence && paint && sameFence(paint.fence, fence) ? paint : undefined;
}

export function usePlayerMorphPublication(controller: PlayerMorphController, fence: PlaybackFence | undefined) {
  const publish = useContext(Publication);
  useLayoutEffect(() => {
    if (publish && fence) return publish(controller, fence);
  }, [controller, fence, publish]);
}

/** An older picture's gesture is never a fallback for a successor's chrome. */
export function usePublishedPlayerMorph(fence: PlaybackFence | undefined): PlayerMorphController | undefined {
  const binding = useContext(Published);
  return fence && binding && sameFence(binding.fence, fence) ? binding.controller : undefined;
}
