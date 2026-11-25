/** Shared document-level visibility observers for Home cards, rows, and hero.
 * Pools IntersectionObservers by (document, rootMargin, threshold) and shares
 * one `visibilitychange` listener per document instead of allocating hundreds. */
type VisibilityCallback = (intersecting: boolean) => void;
type PoolEntry = {
  live: boolean;
  observer: IntersectionObserver;
  entries: Map<Element, Set<VisibilityCallback>>;
};
type PageVisibilityEntry = {
  listeners: Set<() => void>;
  onChange: () => void;
};

const observerPools = new WeakMap<Document, Map<string, PoolEntry>>();
const pageVisibilityPools = new WeakMap<Document, PageVisibilityEntry>();

function optionsKey(options?: Readonly<{rootMargin?: string; threshold?: number | readonly number[]}>): string {
  const rootMargin = options?.rootMargin ?? '0px';
  const threshold = options?.threshold ?? 0;
  return `${rootMargin}|${Array.isArray(threshold) ? threshold.join(',') : threshold}`;
}

export function observeHomeIntersection(
  element: Element,
  callback: VisibilityCallback,
  options?: Readonly<{rootMargin?: string; threshold?: number | readonly number[]}>,
): () => void {
  const doc = element.ownerDocument;
  const Observer = doc.defaultView?.IntersectionObserver ?? globalThis.IntersectionObserver;
  if (!Observer) return () => {};
  const key = optionsKey(options);
  let docPools = observerPools.get(doc);
  if (!docPools) {
    docPools = new Map();
    observerPools.set(doc, docPools);
  }
  let pool = docPools.get(key);
  if (!pool) {
    const entries = new Map<Element, Set<VisibilityCallback>>();
    const created: PoolEntry = {
      live: true,
      entries,
      observer: new Observer(changes => {
        if (!created.live) return;
        const latest = new Map<Element, boolean>();
        for (const change of changes) latest.set(change.target, change.isIntersecting);
        for (const [target, intersecting] of latest) {
          const listeners = entries.get(target);
          if (!listeners) continue;
          for (const listener of [...listeners]) listener(intersecting);
        }
      }, options ? {
        ...(options.rootMargin !== undefined ? {rootMargin: options.rootMargin} : {}),
        ...(options.threshold !== undefined ? {threshold: Array.isArray(options.threshold) ? [...options.threshold] : options.threshold} : {}),
      } : undefined),
    };
    pool = created;
    docPools.set(key, pool);
  }
  const activePool = pool;
  let listeners = activePool.entries.get(element);
  if (!listeners) {
    listeners = new Set();
    activePool.entries.set(element, listeners);
    activePool.observer.observe(element);
  }
  listeners.add(callback);
  let live = true;
  return () => {
    if (!live) return;
    live = false;
    const current = activePool.entries.get(element);
    if (!current) return;
    current.delete(callback);
    if (!current.size) {
      activePool.entries.delete(element);
      activePool.observer.unobserve(element);
    }
    if (!activePool.entries.size) {
      activePool.live = false;
      activePool.observer.disconnect();
      const map = observerPools.get(doc);
      if (map?.get(key) === activePool) {
        map.delete(key);
        if (!map.size) observerPools.delete(doc);
      }
    }
  };
}

export function subscribePageVisibility(listener: () => void, doc: Document | undefined = globalThis.document): () => void {
  if (!doc) return () => {};
  let entry = pageVisibilityPools.get(doc);
  if (!entry) {
    const listeners = new Set<() => void>();
    const onChange = () => { for (const notify of [...listeners]) notify(); };
    entry = {listeners, onChange};
    pageVisibilityPools.set(doc, entry);
    doc.addEventListener('visibilitychange', onChange);
  }
  const active = entry;
  active.listeners.add(listener);
  return () => {
    active.listeners.delete(listener);
    if (!active.listeners.size) {
      doc.removeEventListener('visibilitychange', active.onChange);
      if (pageVisibilityPools.get(doc) === active) pageVisibilityPools.delete(doc);
    }
  };
}
