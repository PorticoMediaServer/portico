import {useCallback, useEffect, useRef, useState} from 'react';
import {sameDockFence} from './model';
import {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withDelay, withTiming} from 'react-native-reanimated';
import {useMotion} from '../useMotion';
import type {DockRenderOccupant, DockSlotAdmission} from './types';

/** Retain only outgoing chrome while it leaves; its controls cannot dispatch to a new source. */
export function useDockPresence(wanted: DockRenderOccupant | null, tv: boolean) {
  const motion = useMotion();
  const [shown, setShown] = useState(wanted);
  const [slotAdmission, setSlotAdmission] = useState<DockSlotAdmission | null>(null);
  const previous = useRef(shown), epoch = useRef(0);
  previous.current = shown;
  // Position/playing facts update the chrome, but must not restart its entry.
  const intent = useRef({wanted, tv, motion});
  const oldIntent = intent.current.wanted;
  if (intent.current.tv !== tv || intent.current.motion !== motion ||
    !!oldIntent !== !!wanted || (oldIntent && wanted &&
      (!sameDockFence(oldIntent.playback.fence, wanted.playback.fence) || oldIntent.mode !== wanted.mode ||
        oldIntent.remote?.id !== wanted.remote?.id || oldIntent.remote?.sequence !== wanted.remote?.sequence))) intent.current = {wanted, tv, motion};
  const target = intent.current;
  const entering = useRef<(DockSlotAdmission & {version: number}) | null>(null);
  const opacity = useSharedValue(wanted ? 1 : 0);
  const finishExit = useCallback((version: number) => {if (epoch.current === version) setShown(null);}, []);
  const finishEnter = useCallback((version: number) => {
    const admission = entering.current;
    if (admission?.version === version && admission.isCurrent()) setSlotAdmission(admission);
  }, []);
  useEffect(() => {if (wanted) setShown(wanted);}, [wanted]);
  useEffect(() => {
    // Retire this run through the live counter, without invalidating a newer run.
    const ownedEpoch = epoch, animation = opacity;
    const version = ++ownedEpoch.current;
    const old = previous.current;
    const next = target.wanted;
    setSlotAdmission(null);
    entering.current = next ? {version, isCurrent: () => ownedEpoch.current === version && intent.current === target} : null;
    if (!next) {
      animation.value = withTiming(0, motion.timing('quick', 'in'), finished => {if (finished) runOnJS(finishExit)(version);});
    } else if (old && (!tv || sameDockFence(old.playback.fence, next.playback.fence) && old.mode === next.mode)) {
      // A touch handover changes the contents of the existing glass. Retarget
      // from its actual paint if an insertion/exit was interrupted; resetting
      // this ancestor would blank and move both old and new contents together.
      // The new slot still waits for this run's own settled ancestor.
      animation.value = withTiming(1, motion.timing('standard'), finished => {if (finished) runOnJS(finishEnter)(version);});
    } else {
      const returningPaused = !next.remote && !next.playback.playing && (next.mode === 'music' || next.mode === 'audiobook');
      animation.value = 0;
      animation.value = withDelay(returningPaused && motion.transforms ? tv ? 300 : 200 : 0, withTiming(1, motion.timing('standard'), finished => {if (finished) runOnJS(finishEnter)(version);}));
    }
    // A reduced-motion fade has no moving coordinate space to wait for.
    if (next && !motion.transforms) finishEnter(version);
    return () => {
      if (ownedEpoch.current !== version) return;
      ownedEpoch.current++;
      cancelAnimation(animation);
    };
  }, [target, tv, motion, opacity, finishExit, finishEnter]);
  const style = useAnimatedStyle(() => ({opacity: opacity.value, transform: tv ? [{translateX: motion.transforms ? 12 * (1 - opacity.value) : 0}] : [{translateY: motion.transforms ? 12 * (1 - opacity.value) : 0}]}));
  return {shown, style, slotAdmission: slotAdmission?.isCurrent() ? slotAdmission : null};
}
