import {useLayoutEffect} from 'react';

/** Keep the first visible content item in place while window chrome changes. */
export function useWindowScrollAnchor(): void {
  useLayoutEffect(() => {
    let anchor: {element: HTMLElement; top: number} | undefined, frame = 0, scrollFrame = 0, resizing = false;
    const remember = () => {
      if (resizing) return;
      const cards = document.querySelectorAll<HTMLElement>('#portico-content [data-layout-item]');
      // Prefer the actual visible item. A section can include heading/margins
      // above that item, which change independently across content size classes.
      const items = cards.length ? cards : document.querySelectorAll<HTMLElement>('#portico-content section[aria-labelledby]');
      const viewportHeight = window.innerHeight, viewportWidth = window.innerWidth;
      anchor = undefined;
      for (const item of items) {
        const bounds = item.getBoundingClientRect();
        if (bounds.bottom > 76 && bounds.top < viewportHeight && bounds.right > 0 && bounds.left < viewportWidth) {
          anchor = {element: item, top: bounds.top};
          return;
        }
      }
    };
    const scheduleRemember = () => {
      if (resizing || scrollFrame) return;
      scrollFrame = requestAnimationFrame(() => {
        scrollFrame = 0;
        remember();
      });
    };
    const resize = () => {
      resizing = true;
      cancelAnimationFrame(scrollFrame);
      scrollFrame = 0;
      cancelAnimationFrame(frame);
      // Width subscribers commit their React changes before this frame.
      frame = requestAnimationFrame(() => {
        frame = 0;
        if (anchor?.element.isConnected && window.scrollY > 0) {
          const delta = anchor.element.getBoundingClientRect().top - anchor.top;
          if (Math.abs(delta) > 1) window.scrollBy({top: delta, behavior: 'instant'});
        }
        resizing = false;
        remember();
      });
    };
    remember();
    window.addEventListener('scroll', scheduleRemember, {passive: true});
    window.addEventListener('resize', resize);
    return () => {
      cancelAnimationFrame(frame);
      cancelAnimationFrame(scrollFrame);
      window.removeEventListener('scroll', scheduleRemember);
      window.removeEventListener('resize', resize);
    };
  }, []);
}
