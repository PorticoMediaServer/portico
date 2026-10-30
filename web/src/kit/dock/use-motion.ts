import {useLayoutEffect, useRef, useState, type RefObject} from 'react';
import {easings} from '@design';
import {dockWordsOwner} from './dock-words';

export function useDockReducedMotion(ref: RefObject<HTMLElement | null>) {
  const [reduced, setReduced] = useState(false);
  useLayoutEffect(() => {
    const media = window.matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setReduced(media.matches || !!ref.current?.closest('[data-reduce-motion="true"]') || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true');
    const observer = new MutationObserver(update); observer.observe(document.documentElement, {subtree: true, attributes: true, attributeFilter: ['data-reduce-motion']});
    media.addEventListener('change', update); update();
    return () => { observer.disconnect(); media.removeEventListener('change', update); };
  }, [ref]); return reduced;
}

/** Only the identity changes on handover; the owner keeps its video surface mounted. */
export function useDockHandover(ref: RefObject<HTMLElement | null>, identity: string) {
  const reduced = useDockReducedMotion(ref), previous = useRef({identity, reduced}), animation = useRef<Animation | null>(null);
  useLayoutEffect(() => {
    if ((previous.current.identity === identity && previous.current.reduced === reduced) || !ref.current) return;
    const motionChange = previous.current.identity === identity;
    previous.current = {identity, reduced};
    const content = ref.current.querySelector<HTMLElement>('[data-dock-identity]'); if (!content) return;
    // The entry paint owns the words while it runs; it retargets from its own painted state.
    if (dockWordsOwner(content)) return;
    const running = animation.current?.playState === 'running';
    const current = getComputedStyle(content);
    // A motion-preference change starts from the real painted opacity and pose, even when
    // this hook has no running animation; it retargets 160ms linear opacity only.
    const opacity = motionChange || running ? current.opacity : '0';
    const transform = motionChange ? 'none' : running ? current.transform : 'translateY(6px)';
    animation.current?.cancel();
    animation.current = content.animate(
      motionChange || reduced ? [{opacity}, {opacity: 1}] : [{opacity, transform}, {opacity: 1, transform: 'none'}],
      {duration: motionChange || reduced ? 160 : 180, easing: motionChange || reduced ? 'linear' : easings.out, fill: 'both'});
  }, [identity, reduced, ref]);
  useLayoutEffect(() => () => animation.current?.cancel(), []);
}
