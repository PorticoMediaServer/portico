import {useLayoutEffect, useRef, type RefObject} from 'react';
import {easings, motion, reducedMotion} from '@design';
import {claimDockWords, dockWordsOwner, releaseDockWords} from './dock-words';
import {signalDockMotion} from './geometry-event';
import {useDockReducedMotion} from './use-motion';

/** The live preference at this element, read synchronously so the first paint is correct. */
function reducedNow(element: HTMLElement): boolean {
  return window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true
    || !!element.closest('[data-reduce-motion="true"]')
    || document.documentElement.dataset.reduceMotion === 'true'
    || document.body.dataset.reduceMotion === 'true';
}

/**
 * The authored entrance of the desktop collapsed bar (motion.html §3.6): the bar rises
 * 92 px and fades in over slow 320 on the `out` curve while its words follow 300 ms in.
 * Reduced motion is a 160 ms linear opacity paint with no rise and no delay.
 *
 * The entry claims the words wrapper while it paints them; a preference change retargets
 * both bar and words from their real painted values. Every completion is guarded by the
 * exact node, animation and owner it was started for, so a finished promise that resolves
 * before a takeover can never settle, cancel or release the successor paint. Paint only —
 * no player timer, media node or loading clock.
 */
export function useDockEntry(bar: RefObject<HTMLElement | null>, identity: string): void {
  const reduced = useDockReducedMotion(bar);
  const entered = useRef(false), paintedIdentity = useRef<string | undefined>(undefined), paintedReduced = useRef<boolean | undefined>(undefined);
  const wordsOwned = useRef(false), token = useRef(Symbol('dock-entry')), wordsNode = useRef<HTMLElement | null>(null);
  const barAnimation = useRef<Animation | null>(null), wordsAnimation = useRef<Animation | null>(null);
  const releaseClaim = (node: HTMLElement | null) => {
    if (node && wordsOwned.current) releaseDockWords(node, token.current);
    wordsOwned.current = false;
  };
  const stopBar = () => {barAnimation.current?.cancel(); barAnimation.current = null;};
  const stopWords = () => {
    const animation = wordsAnimation.current; wordsAnimation.current = null;
    animation?.cancel();
    releaseClaim(wordsNode.current);
  };
  /** Only the exact job that started this animation may settle it. */
  const settle = (element: HTMLElement, animation: Animation, transform: boolean, current: () => boolean, done?: () => void) => {
    const apply = () => {
      if (!current()) return;
      if (transform) element.style.transform = 'none';
      element.style.opacity = '1';
      animation.cancel();
      done?.();
    };
    void animation.finished.then(apply, () => {});
  };
  useLayoutEffect(() => {
    const element = bar.current;
    if (!element) return;
    const live = reducedNow(element);
    // A new occupant: stop this paint and hand the words to the identity handover.
    if (paintedIdentity.current !== undefined && paintedIdentity.current !== identity) {
      stopBar(); stopWords();
      element.style.transform = 'none'; element.style.opacity = '1';
      paintedIdentity.current = identity; paintedReduced.current = live;
      return;
    }
    paintedIdentity.current = identity;
    if (!entered.current) {
      entered.current = true; paintedReduced.current = live;
      const words = element.querySelector<HTMLElement>('[data-dock-identity]');
      wordsNode.current = words;
      const duration = live ? reducedMotion.durationMs : motion.slow, easing = live ? reducedMotion.easing : easings.out;
      const rise = live ? 0 : 92;
      const barPaint = element.animate(live
        ? [{opacity: 0}, {opacity: 1}]
        : [{transform: `translateY(${rise}px)`, opacity: 0}, {transform: 'none', opacity: 1}], {duration, easing, fill: 'both'});
      barAnimation.current = barPaint;
      settle(element, barPaint, true, () => barAnimation.current === barPaint);
      if (!live && words) {
        claimDockWords(words, token.current); wordsOwned.current = true;
        const ink = words.animate([{opacity: 0}, {opacity: 1}], {duration, delay: 300, easing, fill: 'both'});
        wordsAnimation.current = ink;
        settle(words, ink, false, () => wordsAnimation.current === ink && dockWordsOwner(words) === token.current, () => releaseClaim(words));
      }
      signalDockMotion(element, {kind: 'release', offsets: live ? [0, 0] : [rise, 0], opacity: [0, 1], duration});
      return;
    }
    // Preference changed after the entrance began: keep painted values, drop travel now,
    // and retarget 160 ms linear opacity without replaying the +300 ms words delay.
    if (live === paintedReduced.current) return;
    paintedReduced.current = live;
    const paintedBar = getComputedStyle(element).opacity;
    stopBar();
    element.style.transform = 'none';
    const barPaint = element.animate([{opacity: paintedBar}, {opacity: 1}], {duration: reducedMotion.durationMs, easing: reducedMotion.easing, fill: 'both'});
    barAnimation.current = barPaint;
    settle(element, barPaint, false, () => barAnimation.current === barPaint);
    const words = wordsNode.current;
    if (words && wordsOwned.current) {
      const paintedWords = getComputedStyle(words).opacity;
      const prior = wordsAnimation.current; wordsAnimation.current = null; prior?.cancel();
      const ink = words.animate([{opacity: paintedWords}, {opacity: 1}], {duration: reducedMotion.durationMs, easing: reducedMotion.easing, fill: 'both'});
      wordsAnimation.current = ink;
      settle(words, ink, false, () => wordsAnimation.current === ink && dockWordsOwner(words) === token.current, () => releaseClaim(words));
    }
  }, [bar, identity, reduced]);
  useLayoutEffect(() => () => {stopBar(); stopWords();}, []);
}
