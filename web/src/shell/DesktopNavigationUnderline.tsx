import {useLayoutEffect, useRef, type RefObject} from 'react';
import {easings, motion, reducedMotion} from '@design';
import styles from './shell.module.css';

type Box = Readonly<{left: number; width: number; bottom: number}>;
type Painted = Readonly<{left: number; top: number; width: number; opacity: number}>;

/** One shell-owned underline for the real selected DesktopNavigation destination.
 * Pure decoration: it measures the selected link or More trigger in the owning
 * `<ul>`, never calls routes or reads session/library data. */
export function DesktopNavigationUnderline({list}: {list: RefObject<HTMLUListElement | null>}) {
  const underline = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => {
    const bar = underline.current, destinations = list.current;
    if (!bar || !destinations) return;
    let placed: Box | undefined, animation: Animation | undefined, disposed = false;
    const reduced = () => document.documentElement.dataset.reduceMotion === 'true' || matchMedia('(prefers-reduced-motion: reduce)').matches;
    const selected = () => destinations.querySelector<HTMLElement>('[aria-current="page"], button[data-active="true"]');
    const measure = (): Box | undefined => {
      const at = selected();
      if (!at || !at.isConnected || !destinations.isConnected) return undefined;
      const box = destinations.getBoundingClientRect(), target = at.getBoundingClientRect();
      const value = {left: target.left - box.left, width: target.width, bottom: box.bottom - target.bottom};
      return [value.left, value.width, value.bottom].every(Number.isFinite) ? value : undefined;
    };
    // The actually painted position, including any running FLIP transform.
    const painted = (): Painted => {
      const rect = bar.getBoundingClientRect(), opacity = Number.parseFloat(getComputedStyle(bar).opacity);
      return {left: rect.left, top: rect.top, width: rect.width, opacity: Number.isFinite(opacity) ? opacity : 1};
    };
    const settle = (box: Box) => {
      bar.style.left = `${box.left}px`; bar.style.width = `${box.width}px`; bar.style.bottom = `${box.bottom}px`;
      bar.style.transform = 'none'; bar.style.opacity = '1';
    };
    const stop = (): Painted | undefined => {const state = animation ? painted() : undefined; animation?.cancel(); animation = undefined; return state;};
    const move = (immediate: boolean) => {
      if (disposed) return;
      const next = measure();
      if (!next) {stop(); bar.style.opacity = '0'; placed = undefined; return;}
      const from = placed ? stop() ?? painted() : undefined;
      settle(next);
      placed = next;
      if (!from || immediate || !bar.animate) return;
      const final = bar.getBoundingClientRect();
      if (reduced()) {
        // No travel: a changed destination hands off with an actual opacity crossfade.
        if (from.left !== final.left || from.top !== final.top || from.width !== final.width)
          animation = bar.animate([{opacity: 0}, {opacity: 1}], {duration: reducedMotion.durationMs, easing: reducedMotion.easing});
        return;
      }
      animation = bar.animate([
        {transform: `translate(${from.left - final.left}px, ${from.top - final.top}px) scaleX(${final.width ? from.width / final.width : 1})`, opacity: from.opacity},
        {transform: 'none', opacity: 1}
      ], {duration: motion.slow, easing: easings.inOut});
    };
    const resize = new ResizeObserver(() => move(true));
    const observeTargets = () => {
      resize.disconnect();
      resize.observe(destinations);
      for (const child of destinations.children) resize.observe(child);
      for (const label of destinations.querySelectorAll('[aria-hidden="true"] span')) resize.observe(label);
    };
    observeTargets();
    const mutation = new MutationObserver(records => {
      if (records.some(record => record.type === 'childList')) observeTargets();
      move(false);
    });
    mutation.observe(destinations, {attributes: true, attributeFilter: ['aria-current', 'data-active'], childList: true, subtree: true});
    const media = matchMedia('(prefers-reduced-motion: reduce)');
    const onMedia = () => move(false);
    media.addEventListener('change', onMedia);
    const root = new MutationObserver(() => move(false));
    root.observe(document.documentElement, {attributes: true, attributeFilter: ['data-reduce-motion']});
    void document.fonts?.ready.then(() => move(true)).catch(() => {});
    move(true);
    return () => {disposed = true; resize.disconnect(); mutation.disconnect(); root.disconnect(); media.removeEventListener('change', onMedia); animation?.cancel();};
  }, [list]);
  return <li className={styles.underlineAnchor} aria-hidden="true"><span ref={underline} className={styles.navUnderline} /></li>;
}
