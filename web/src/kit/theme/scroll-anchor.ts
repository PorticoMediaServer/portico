import {useLayoutEffect} from 'react';

/** Keep the first visible content item in place while window chrome changes. */
export function useWindowScrollAnchor(): void {
  useLayoutEffect(() => {
    let anchor: {element: HTMLElement; top: number} | undefined, frame = 0, resizing = false;
    const remember = () => {
      if (resizing) return;
      const cards = document.querySelectorAll<HTMLElement>('#portico-content [data-layout-item]');
      // Prefer the actual visible item. A section can include heading/margins
      // above that item, which change independently across content size classes.
      const items = cards.length ? cards : document.querySelectorAll<HTMLElement>('#portico-content section[aria-labelledby]');
      const element = [...items].find(item => {
        const bounds = item.getBoundingClientRect();
        return bounds.bottom > 76 && bounds.top < window.innerHeight && bounds.right > 0 && bounds.left < window.innerWidth;
      });
      anchor = element ? {element, top: element.getBoundingClientRect().top} : undefined;
    };
    const resize = () => {
      resizing = true;
      cancelAnimationFrame(frame);
      // Width subscribers commit their React changes before this frame.
      frame = requestAnimationFrame(() => {
        if (anchor?.element.isConnected && window.scrollY > 0) {
          const delta = anchor.element.getBoundingClientRect().top - anchor.top;
          if (Math.abs(delta) > 1) window.scrollBy({top: delta, behavior: 'instant'});
        }
        resizing = false;
        remember();
      });
    };
    remember();
    window.addEventListener('scroll', remember, {passive: true});
    window.addEventListener('resize', resize);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener('scroll', remember);
      window.removeEventListener('resize', resize);
    };
  }, []);
}
