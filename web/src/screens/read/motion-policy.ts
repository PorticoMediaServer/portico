import type {ReaderMotionPolicy} from '@portico/reader-core';

/** Only the original app document's existing app preference. The canonical
 * renderer independently ORs this with its real system media query. */
export function webReaderMotion(document: Document | undefined): ReaderMotionPolicy {
  const root = document?.documentElement, body = document?.body;
  return Object.freeze({
    reduced() {return !root || root.dataset.reduceMotion === 'true' || body?.dataset.reduceMotion === 'true';},
    subscribe(changed: () => void) {
      if (!root || typeof MutationObserver !== 'function') return () => {};
      const observer = new MutationObserver(changed);
      observer.observe(root, {attributes: true, attributeFilter: ['data-reduce-motion']});
      if (body) observer.observe(body, {attributes: true, attributeFilter: ['data-reduce-motion']});
      return () => observer.disconnect();
    },
  });
}
